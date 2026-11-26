package source_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/lennylabs/podium/pkg/layer/source"
)

// testAgent is an in-process SSH agent on a Unix socket that holds one key
// and counts the connections it accepted and the ones a client has not yet
// closed.
type testAgent struct {
	addr string
	key  ssh.PublicKey

	// mu guards accepted and open, which the accept loop and the goroutine
	// serving each connection update.
	mu       sync.Mutex
	accepted int
	open     int
}

// startTestAgent starts a testAgent that serves until the test ends.
func startTestAgent(t *testing.T) *testAgent {
	t.Helper()
	// A unix socket path is limited to about 100 bytes, so the socket goes in
	// a short directory of its own rather than under t.TempDir().
	dir, err := os.MkdirTemp("", "pga")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	ln, err := net.Listen("unix", filepath.Join(dir, "agent.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: key}); err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	ag := &testAgent{addr: ln.Addr().String(), key: signer.PublicKey()}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			ag.mu.Lock()
			ag.accepted++
			ag.open++
			ag.mu.Unlock()
			go func() {
				// ServeAgent returns when the client closes its end.
				_ = agent.ServeAgent(keyring, c)
				_ = c.Close()
				ag.mu.Lock()
				ag.open--
				ag.mu.Unlock()
			}()
		}
	}()
	return ag
}

// acceptedCount returns how many connections the agent has accepted.
func (a *testAgent) acceptedCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.accepted
}

// wantAllClosed fails the test unless the agent accepted a connection after
// acceptedBefore and every connection it accepted is closed within two
// seconds. The bound is far below the test's lifetime, so a connection that
// only the test's cleanup would close fails the check.
func (a *testAgent) wantAllClosed(t *testing.T, acceptedBefore int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		a.mu.Lock()
		accepted, open := a.accepted, a.open
		a.mu.Unlock()
		if accepted <= acceptedBefore {
			t.Fatalf("the snapshot opened no agent connection (accepted %d)", accepted)
		}
		if open == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d agent connections are still open after the snapshot returned", open, accepted)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// sshGitServer is an in-process SSH server that serves git-upload-pack for one
// repository with go-git's server implementation. It records the login name of
// each connection it authenticated.
type sshGitServer struct {
	addr string

	// mu guards logins, which the connection goroutines append to.
	mu     sync.Mutex
	logins []string
}

// sshServerOptions configures startSSHGitServer.
type sshServerOptions struct {
	// authorized is the one public key the server admits. A nil key admits
	// none, so authentication fails.
	authorized ssh.PublicKey
	// knownHosts, when set, is the known_hosts file the server records its
	// host key in. When empty the client does not know the host key.
	knownHosts string
}

// startSSHGitServer starts an sshGitServer that serves repo.
func startSSHGitServer(t *testing.T, repo storer.Storer, opts sshServerOptions) *sshGitServer {
	t.Helper()
	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatal(err)
	}
	srv := &sshGitServer{}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if opts.authorized == nil || !bytes.Equal(key.Marshal(), opts.authorized.Marshal()) {
				return nil, errors.New("key not authorized")
			}
			srv.mu.Lock()
			srv.logins = append(srv.logins, meta.User())
			srv.mu.Unlock()
			return nil, nil
		},
	}
	cfg.AddHostKey(signer)
	upload := server.NewServer(fixedLoader{repo})
	srv.addr = stallingListener(t, func(c net.Conn) { go serveSSHConn(c, cfg, upload) })
	if opts.knownHosts != "" {
		line := knownhosts.Line([]string{knownhosts.Normalize(srv.addr)}, signer.PublicKey())
		if err := os.WriteFile(opts.knownHosts, []byte(line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return srv
}

// loginNames returns the login names the server authenticated.
func (s *sshGitServer) loginNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.logins...)
}

// fixedLoader is a go-git server Loader that serves one storer for every path.
type fixedLoader struct{ s storer.Storer }

// Load implements server.Loader.
func (l fixedLoader) Load(*transport.Endpoint) (storer.Storer, error) { return l.s, nil }

// serveSSHConn runs the SSH handshake on c and serves each session channel.
func serveSSHConn(c net.Conn, cfg *ssh.ServerConfig, upload transport.Transport) {
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		ch, chReqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go serveSSHSession(ch, chReqs, upload)
	}
}

// serveSSHSession answers the session's exec request with git-upload-pack and
// refuses every other request.
func serveSSHSession(ch ssh.Channel, reqs <-chan *ssh.Request, upload transport.Transport) {
	defer func() { _ = ch.Close() }()
	for req := range reqs {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		_ = req.Reply(true, nil)
		status := uint32(0)
		if err := uploadPack(ch, upload); err != nil {
			status = 1
		}
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
		return
	}
}

// uploadPack runs one git-upload-pack exchange over rw: it advertises the
// references, reads the client's request, and sends the pack.
func uploadPack(rw io.ReadWriter, upload transport.Transport) error {
	sess, err := upload.NewUploadPackSession(&transport.Endpoint{Protocol: "ssh", Path: "/acme/layer.git"}, nil)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	refs, err := sess.AdvertisedReferences()
	if err != nil {
		return err
	}
	if err := refs.Encode(rw); err != nil {
		return err
	}
	req := packp.NewUploadPackRequest()
	if err := req.Decode(rw); err != nil {
		return err
	}
	resp, err := sess.UploadPack(context.Background(), req)
	if err != nil {
		return err
	}
	return resp.Encode(rw)
}

// layerStorer returns the storer of a repository with one artifact file.
func layerStorer(t *testing.T) storer.Storer {
	t.Helper()
	url, _ := repoFactory(t, map[string]string{
		"company-glossary/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\n---\n\nbody\n",
	})
	repo, err := git.PlainOpen(strings.TrimPrefix(url, "file://"))
	if err != nil {
		t.Fatal(err)
	}
	return repo.Storer
}

// sshSnapshot runs a snapshot of the master branch of repo under a one-second
// FetchTimeout. The zero PriorRef selects a full clone, which the force-push
// check treats as a first ingest, because go-git's upload-pack server ignores
// the depth of a shallow clone and the client then finds no ref to resolve.
func sshSnapshot(repo string) (*source.Snapshot, error) {
	return source.Git{FetchTimeout: time.Second}.Snapshot(context.Background(), source.LayerConfig{
		Repo: repo, Ref: "master", PriorRef: "0000000000000000000000000000000000000000",
	})
}

// Spec: §7.3.1 — an ssh snapshot authenticates with the key the SSH agent at
// SSH_AUTH_SOCK holds, as the login name the URL names, and checks the host
// key against known_hosts. It closes its agent connection before it returns,
// whether the clone succeeds, fails, or reaches the PODIUM_GIT_FETCH_TIMEOUT
// deadline (§13.12), so repeated ssh ingests hold no agent socket. The cases
// set process environment variables, so they do not run in parallel.
func TestGit_SnapshotClosesSSHAgentConnection(t *testing.T) {
	ag, knownHosts := sshAgentEnv(t)
	repo := layerStorer(t)

	t.Run("success", func(t *testing.T) {
		srv := startSSHGitServer(t, repo, sshServerOptions{authorized: ag.key, knownHosts: knownHosts})
		before := ag.acceptedCount()
		snap, err := sshSnapshot("ssh://alice@" + srv.addr + "/acme/layer.git")
		if err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
		body, err := fs.ReadFile(snap.Files, "company-glossary/ARTIFACT.md")
		if err != nil || !strings.Contains(string(body), "type: agent") {
			t.Fatalf("ReadFile = %q, %v; want the artifact", body, err)
		}
		if got := srv.loginNames(); len(got) != 1 || got[0] != "alice" {
			t.Errorf("server authenticated %v, want the URL's login name alice", got)
		}
		ag.wantAllClosed(t, before)
	})

	t.Run("success with no login name in the URL", func(t *testing.T) {
		cur, err := user.Current()
		if err != nil || cur.Username == "" {
			t.Skipf("no current user name: %v", err)
		}
		srv := startSSHGitServer(t, repo, sshServerOptions{authorized: ag.key, knownHosts: knownHosts})
		before := ag.acceptedCount()
		if _, err := sshSnapshot("ssh://" + srv.addr + "/acme/layer.git"); err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
		if got := srv.loginNames(); len(got) != 1 || got[0] != cur.Username {
			t.Errorf("server authenticated %v, want the current user %s", got, cur.Username)
		}
		ag.wantAllClosed(t, before)
	})

	t.Run("authentication refused", func(t *testing.T) {
		srv := startSSHGitServer(t, repo, sshServerOptions{knownHosts: knownHosts})
		before := ag.acceptedCount()
		_, err := sshSnapshot("ssh://alice@" + srv.addr + "/acme/layer.git")
		if !errors.Is(err, source.ErrSourceUnreachable) || !strings.Contains(err.Error(), "unable to authenticate") {
			t.Fatalf("Snapshot = %v, want ErrSourceUnreachable for refused authentication", err)
		}
		ag.wantAllClosed(t, before)
	})

	t.Run("unknown host key", func(t *testing.T) {
		if err := os.WriteFile(knownHosts, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		srv := startSSHGitServer(t, repo, sshServerOptions{authorized: ag.key})
		before := ag.acceptedCount()
		_, err := sshSnapshot("ssh://alice@" + srv.addr + "/acme/layer.git")
		if !errors.Is(err, source.ErrSourceUnreachable) || !strings.Contains(err.Error(), "knownhosts: key is unknown") {
			t.Fatalf("Snapshot = %v, want ErrSourceUnreachable for an unknown host key", err)
		}
		if got := srv.loginNames(); len(got) != 0 {
			t.Errorf("server authenticated %v, want no authentication against an unknown host", got)
		}
		ag.wantAllClosed(t, before)
	})

	t.Run("timeout", func(t *testing.T) {
		addr := authStallingSSHServer(t, knownHosts)
		before := ag.acceptedCount()
		wantDeadlineFailure(t, "ssh://git@"+addr+"/acme/layer.git")
		ag.wantAllClosed(t, before)
	})
}

// Spec: §7.3.1 (Source unreachable) — an ssh snapshot with SSH_AUTH_SOCK unset
// or naming an unreachable agent fails as ingest.source_unreachable with the
// message go-git's default ssh auth reports for the same environment.
func TestGit_SnapshotSSHWithoutAgent(t *testing.T) {
	addr := stallingListener(t, silent)
	repo := "ssh://alice@" + addr + "/acme/layer.git"
	for _, tc := range []struct {
		name, sock string
	}{
		{name: "SSH_AUTH_SOCK unset"},
		{name: "agent unreachable", sock: filepath.Join(t.TempDir(), "absent.sock")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SSH_AUTH_SOCK", tc.sock)
			_, want := gitssh.NewSSHAgentAuth("alice")
			if want == nil {
				t.Fatal("go-git's default auth succeeded, want a failure to compare with")
			}
			_, err := sshSnapshot(repo)
			if !errors.Is(err, source.ErrSourceUnreachable) || !strings.Contains(err.Error(), want.Error()) {
				t.Fatalf("Snapshot = %v, want ErrSourceUnreachable carrying %q", err, want)
			}
		})
	}
}
