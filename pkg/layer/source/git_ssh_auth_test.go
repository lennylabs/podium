package source

import (
	"errors"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

// Spec: §7.3.1 — the snapshot supplies its own agent-backed auth for every
// ssh remote, ssh:// and scp-like, carrying the URL's login name, and leaves
// the auth nil for http, https, git://, and file remotes so that go-git's
// handling of those transports, the http credential in the URL included,
// stays in place.
func TestSnapshotSSHAuth_CoversSSHRemotes(t *testing.T) {
	t.Parallel()
	for repo, wantUser := range map[string]string{
		"ssh://alice@git.acme.com/acme/layer.git": "alice",
		"git@git.acme.com:acme/layer.git":         "git",
		"ssh://git.acme.com/acme/layer.git":       "",
	} {
		auth, release := snapshotSSHAuth(repo)
		a, ok := auth.(*agentAuth)
		if !ok {
			t.Fatalf("%s: auth = %T, want *agentAuth", repo, auth)
		}
		if a.user != wantUser || a.currentUser == nil {
			t.Errorf("%s: user = %q, account lookup set %t; want %q and set", repo, a.user, a.currentUser != nil, wantUser)
		}
		if a.Name() != gitssh.PublicKeysCallbackName {
			t.Errorf("%s: Name = %q, want go-git's %q", repo, a.Name(), gitssh.PublicKeysCallbackName)
		}
		login, err := sshLoginName(wantUser, user.Current)
		if err != nil {
			login = wantUser
		}
		if want := "user: " + login + ", name: " + gitssh.PublicKeysCallbackName; a.String() != want {
			t.Errorf("%s: String = %q, want %q", repo, a.String(), want)
		}
		release()
	}
	for _, repo := range []string{"https://alice:s3cr3t@git.acme.com/acme/layer.git",
		"http://git.acme.com/acme/layer.git", "git://git.acme.com/acme/layer.git",
		"file:///srv/acme/layer", "://not a url"} {
		auth, release := snapshotSSHAuth(repo)
		release()
		if auth != nil {
			t.Errorf("%s: auth = %T, want nil", repo, auth)
		}
	}
}

// countingAgent listens on a Unix socket, accepts connections without serving
// them, and reports on closed each time a client closes one.
func countingAgent(t *testing.T) (addr string, accepted <-chan struct{}, closed <-chan struct{}) {
	t.Helper()
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
	acc := make(chan struct{}, 16)
	cls := make(chan struct{}, 16)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			acc <- struct{}{}
			go func() {
				// Read returns when the client closes its end.
				_, _ = c.Read(make([]byte, 1))
				_ = c.Close()
				cls <- struct{}{}
			}()
		}
	}()
	return ln.Addr().String(), acc, cls
}

// wantSignals fails the test unless n signals arrive on ch within two seconds.
func wantSignals(t *testing.T, ch <-chan struct{}, n int, what string) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for i := 0; i < n; i++ {
		select {
		case <-ch:
		case <-timeout:
			t.Fatalf("%s: got %d of %d", what, i, n)
		}
	}
}

// Spec: §7.3.1 — the auth builds the client config of go-git's default ssh
// auth for the URL's login name over a fresh agent connection per request,
// release closes every connection it opened, and a request after release
// opens none. Release runs when the snapshot returns, so a clone that
// outlives its snapshot's deadline cannot open an agent connection that
// nothing would close.
func TestAgentAuth_ReleaseClosesEveryConnection(t *testing.T) {
	addr, accepted, closed := countingAgent(t)
	t.Setenv("SSH_AUTH_SOCK", addr)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHosts, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_KNOWN_HOSTS", knownHosts)

	auth, release := snapshotSSHAuth("ssh://alice@git.acme.com/acme/layer.git")
	a := auth.(*agentAuth)
	for i := 0; i < 2; i++ {
		cfg, err := a.ClientConfig()
		if err != nil {
			t.Fatalf("ClientConfig: %v", err)
		}
		if cfg.User != "alice" || len(cfg.Auth) != 1 || cfg.HostKeyCallback == nil {
			t.Fatalf("ClientConfig = user %q, %d auth methods, host key callback set %t; want alice, 1, true",
				cfg.User, len(cfg.Auth), cfg.HostKeyCallback != nil)
		}
	}
	wantSignals(t, accepted, 2, "agent connections opened")
	select {
	case <-closed:
		t.Fatal("an agent connection closed before release")
	default:
	}

	release()
	wantSignals(t, closed, 2, "agent connections closed by release")

	_, err := a.ClientConfig()
	// ClientConfig quotes the agent error as go-git's default does, so the
	// sentinel is matched by its text.
	if err == nil || !strings.Contains(err.Error(), errAgentAuthReleased.Error()) {
		t.Fatalf("ClientConfig after release = %v, want %v", err, errAgentAuthReleased)
	}
	select {
	case <-accepted:
		t.Fatal("ClientConfig after release opened an agent connection")
	case <-time.After(50 * time.Millisecond):
	}
	// A second release is harmless.
	release()
}

// Spec: §7.3.1 — the login name of a URL that names none is the name of the
// account the registry runs as, else $USER, as go-git's default ssh auth
// resolves it. With neither, the clone fails with go-git's message before
// any agent connection opens.
func TestSSHLoginName(t *testing.T) {
	bob := func() (*user.User, error) { return &user.User{Username: "bob"}, nil }
	noAccount := func() (*user.User, error) { return nil, errors.New("no account") }

	if got, err := sshLoginName("alice", noAccount); err != nil || got != "alice" {
		t.Errorf("sshLoginName(alice) = %q, %v; want alice", got, err)
	}
	if got, err := sshLoginName("", bob); err != nil || got != "bob" {
		t.Errorf("sshLoginName with an account = %q, %v; want bob", got, err)
	}
	t.Setenv("USER", "carol")
	if got, err := sshLoginName("", noAccount); err != nil || got != "carol" {
		t.Errorf("sshLoginName with $USER = %q, %v; want carol", got, err)
	}
	t.Setenv("USER", "")
	if _, err := sshLoginName("", noAccount); err == nil || err.Error() != "failed to get username" {
		t.Errorf("sshLoginName with no name = %v, want go-git's failed to get username", err)
	}

	addr, accepted, _ := countingAgent(t)
	t.Setenv("SSH_AUTH_SOCK", addr)
	auth, release := snapshotSSHAuth("ssh://git.acme.com/acme/layer.git")
	defer release()
	a := auth.(*agentAuth)
	a.currentUser = noAccount
	if got, want := a.String(), "user: , name: "+gitssh.PublicKeysCallbackName; got != want {
		t.Errorf("String with no name = %q, want %q", got, want)
	}
	if _, err := a.ClientConfig(); err == nil || err.Error() != "failed to get username" {
		t.Errorf("ClientConfig with no name = %v, want go-git's failed to get username", err)
	}
	select {
	case <-accepted:
		t.Error("ClientConfig opened an agent connection with no login name")
	case <-time.After(50 * time.Millisecond):
	}
}
