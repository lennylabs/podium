package source_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/lennylabs/podium/pkg/layer/source"
)

// sshClientEnv points go-git's ssh transport at an in-process agent that
// holds one key and at an empty known_hosts file, and returns that file's
// path. go-git refuses to dial an ssh remote without SSH_AUTH_SOCK and reads
// SSH_KNOWN_HOSTS for host keys, so both are needed to reach the remote.
func sshClientEnv(t *testing.T) string {
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
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _ = agent.ServeAgent(keyring, c) }()
		}
	}()
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHosts, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_AUTH_SOCK", ln.Addr().String())
	t.Setenv("SSH_KNOWN_HOSTS", knownHosts)
	return knownHosts
}

// stallingListener accepts TCP connections, runs onConn on each in the accept
// loop, and keeps every connection open until the test ends. It returns the
// listener address.
func stallingListener(t *testing.T, onConn func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	held := make(chan net.Conn, 64)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			onConn(c)
			held <- c
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		for {
			select {
			case c := <-held:
				_ = c.Close()
			default:
				return
			}
		}
	})
	return ln.Addr().String()
}

// silent sends nothing on an accepted connection.
func silent(net.Conn) {}

// bannerOnly sends an SSH version banner and then nothing, so the client
// stalls waiting for the server's key exchange.
func bannerOnly(c net.Conn) { _, _ = c.Write([]byte("SSH-2.0-podium-stall\r\n")) }

// authStallingSSHServer is an in-process SSH server that completes the key
// exchange and then never answers the client's public-key authentication. It
// records its host key in knownHosts so the client reaches authentication.
func authStallingSSHServer(t *testing.T, knownHosts string) string {
	t.Helper()
	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			<-stop
			return nil, errors.New("refused")
		},
	}
	cfg.AddHostKey(signer)
	addr := stallingListener(t, func(c net.Conn) {
		go func() { _, _, _, _ = ssh.NewServerConn(c, cfg) }()
	})
	// Cleanups run last-in first-out, so the callbacks return before the
	// listener's cleanup closes the connections.
	t.Cleanup(func() { close(stop) })
	line := knownhosts.Line([]string{knownhosts.Normalize(addr)}, signer.PublicKey())
	if err := os.WriteFile(knownHosts, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return addr
}

// wantDeadlineFailure runs a snapshot of repo under a 300ms FetchTimeout and
// fails the test unless it fails as ingest.source_unreachable naming the
// setting, shortly after the deadline.
func wantDeadlineFailure(t *testing.T, repo string) {
	t.Helper()
	start := time.Now()
	_, err := source.Git{FetchTimeout: 300 * time.Millisecond}.Snapshot(context.Background(), source.LayerConfig{
		Repo: repo, Ref: "main",
	})
	elapsed := time.Since(start)
	if !errors.Is(err, source.ErrSourceUnreachable) || !strings.Contains(err.Error(), "PODIUM_GIT_FETCH_TIMEOUT (300ms)") {
		t.Fatalf("Snapshot(%s) = %v, want ErrSourceUnreachable naming PODIUM_GIT_FETCH_TIMEOUT (300ms)", repo, err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("Snapshot(%s) returned after %v, want shortly after the 300ms deadline", repo, elapsed)
	}
}

// Spec: §7.3.1 (Source unreachable), §13.12 — the PODIUM_GIT_FETCH_TIMEOUT
// deadline bounds an ssh remote's connection setup as well as its transfer:
// a remote that never sends its banner, one that stops after the banner, and
// one that stalls in authentication each fail the snapshot as
// ingest.source_unreachable at the deadline. The cases set process
// environment variables, so they do not run in parallel.
func TestGit_SnapshotDeadlineSSH(t *testing.T) {
	knownHosts := sshClientEnv(t)
	t.Run("no banner", func(t *testing.T) {
		wantDeadlineFailure(t, "ssh://git@"+stallingListener(t, silent)+"/acme/layer.git")
	})
	t.Run("banner then silence", func(t *testing.T) {
		wantDeadlineFailure(t, "ssh://git@"+stallingListener(t, bannerOnly)+"/acme/layer.git")
	})
	t.Run("authentication stall", func(t *testing.T) {
		wantDeadlineFailure(t, "ssh://git@"+authStallingSSHServer(t, knownHosts)+"/acme/layer.git")
	})
}

// Spec: §7.3.1 (Source unreachable), §13.12 — a git:// remote that accepts the
// connection and never answers fails the snapshot at the deadline.
func TestGit_SnapshotDeadlineGitProtocol(t *testing.T) {
	t.Parallel()
	wantDeadlineFailure(t, "git://"+stallingListener(t, silent)+"/acme/layer.git")
}

// Spec: §7.3.1, §13.12 — a snapshot that times out leaves no goroutine of its
// own running, on an ssh remote that never sends its banner and on a git://
// remote that never answers, however many times it times out.
//
// Two goroutines per ssh clone are ignored because they belong to the SSH
// agent connection rather than to the remote: go-git's default ssh auth
// (NewSSHAgentAuth) opens an agent client whose connection it never closes,
// on every ssh clone whether it succeeds, fails, or times out, and the
// in-process agent serves that connection.
func TestGit_SnapshotDeadlineLeavesNoGoroutines(t *testing.T) {
	sshClientEnv(t)
	sshRepo := "ssh://git@" + stallingListener(t, silent) + "/acme/layer.git"
	gitRepo := "git://" + stallingListener(t, silent) + "/acme/layer.git"
	baseline := goleak.IgnoreCurrent()
	for i := 0; i < 5; i++ {
		wantDeadlineFailure(t, sshRepo)
		wantDeadlineFailure(t, gitRepo)
	}
	goleak.VerifyNone(t, baseline,
		goleak.IgnoreAnyFunction("golang.org/x/crypto/ssh/agent.ServeAgent"),
		goleak.IgnoreAnyFunction("golang.org/x/crypto/ssh/agent.(*pipeline).readLoop"))
}

// Spec: §7.3.1 (Source unreachable), §13.12 — every snapshot that a stalled
// remote holds to its deadline names the setting, whichever transport stalls
// and however the deadline lines up with the transport's own failure. Rounds
// of 20 concurrent snapshots with timeouts from 200ms to 580ms run against an
// ssh remote with no banner, one that stops after its banner, one that stalls
// in authentication, a silent git:// remote, and a silent http remote.
func TestGit_SnapshotDeadlineStress(t *testing.T) {
	knownHosts := sshClientEnv(t)
	repos := []string{
		"ssh://git@" + stallingListener(t, silent) + "/acme/layer.git",
		"ssh://git@" + stallingListener(t, bannerOnly) + "/acme/layer.git",
		"ssh://git@" + authStallingSSHServer(t, knownHosts) + "/acme/layer.git",
		"git://" + stallingListener(t, silent) + "/acme/layer.git",
		stalledRemote(t, hangingHandshake),
	}
	const rounds, concurrent = 3, 20
	for round := 0; round < rounds; round++ {
		errs := make(chan string, concurrent)
		var wg sync.WaitGroup
		for i := 0; i < concurrent; i++ {
			repo := repos[i%len(repos)]
			timeout := 200*time.Millisecond + time.Duration(i)*20*time.Millisecond
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := source.Git{FetchTimeout: timeout}.Snapshot(context.Background(), source.LayerConfig{Repo: repo, Ref: "main"})
				want := fmt.Sprintf("PODIUM_GIT_FETCH_TIMEOUT (%s)", timeout)
				if !errors.Is(err, source.ErrSourceUnreachable) || !strings.Contains(err.Error(), want) {
					errs <- fmt.Sprintf("%s with %s: %v", repo, timeout, err)
				}
			}()
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			t.Errorf("round %d: snapshot did not name the setting: %s", round, e)
		}
	}
}
