package source

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"sync"

	"github.com/go-git/go-git/v5/plumbing/transport"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	sshagent "github.com/xanzy/ssh-agent"
	"golang.org/x/crypto/ssh"
)

// errAgentAuthReleased reports a client config requested after the snapshot
// that owns the auth returned. It reaches only a clone that boundedSnapshot
// already abandoned, so no caller reads it.
var errAgentAuthReleased = errors.New("the snapshot that owns this ssh auth has ended")

// snapshotSSHAuth returns the clone's auth for repo and a release function
// that closes every SSH agent connection the auth opened. For a remote that
// go-git does not clone over ssh it returns a nil auth, which leaves go-git's
// handling of that transport in place: the http transport, for one, reads the
// credential from the URL userinfo only when the auth is nil.
//
// Spec: §7.3.1
func snapshotSSHAuth(repo string) (transport.AuthMethod, func()) {
	ep, ok := sshEndpoint(repo)
	if !ok {
		return nil, func() {}
	}
	a := &agentAuth{user: ep.User, currentUser: user.Current}
	return a, a.release
}

// agentAuth is the ssh auth of one snapshot. It authenticates as go-git's
// default ssh auth does, with the SSH agent at SSH_AUTH_SOCK as the signer,
// and the snapshot owns the agent connection.
//
// go-git's default (ssh.NewSSHAgentAuth) opens an agent connection for each
// clone and never closes it, so every ssh ingest left a Unix socket and the
// agent client's reader goroutine behind, whether the clone succeeded,
// failed, or timed out. agentAuth opens the connection in ClientConfig, which
// the transport calls at the point where it would have built the default
// auth, so the clone fails at the same step and with the same message when
// SSH_AUTH_SOCK is unset or the agent is unreachable. The rest of the client
// config, the login name and the known_hosts host key check included, comes
// from go-git's own PublicKeysCallback, so it matches the default.
type agentAuth struct {
	// user is the login name of the remote URL, empty when it names none.
	user string
	// currentUser looks up the account the registry runs as, whose name is
	// the login name of a URL that names none. Tests substitute it.
	currentUser func() (*user.User, error)

	// mu guards conns and released. A clone that outlives its snapshot, which
	// boundedSnapshot allows at the deadline, can request a client config
	// while release runs.
	mu       sync.Mutex
	conns    []io.Closer
	released bool
}

// Name implements transport.AuthMethod with the name of go-git's default.
func (a *agentAuth) Name() string { return gitssh.PublicKeysCallbackName }

// String implements transport.AuthMethod in the format of go-git's default.
func (a *agentAuth) String() string {
	return fmt.Sprintf("user: %s, name: %s", a.user, a.Name())
}

// ClientConfig implements go-git's ssh.AuthMethod. It resolves the login name
// and opens an agent connection, in the order and with the error text of
// ssh.NewSSHAgentAuth, and returns the client config of go-git's
// PublicKeysCallback over that connection.
func (a *agentAuth) ClientConfig() (*ssh.ClientConfig, error) {
	login, err := sshLoginName(a.user, a.currentUser)
	if err != nil {
		return nil, err
	}
	signers, err := a.openAgent()
	if err != nil {
		return nil, fmt.Errorf("error creating SSH agent: %q", err)
	}
	return (&gitssh.PublicKeysCallback{User: login, Callback: signers}).ClientConfig()
}

// openAgent connects to the SSH agent and records the connection for
// release. The lock is held across the dial so that release cannot run
// between the dial and the record and miss the connection.
func (a *agentAuth) openAgent() (func() ([]ssh.Signer, error), error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.released {
		return nil, errAgentAuthReleased
	}
	ag, conn, err := sshagent.New()
	if err != nil {
		return nil, err
	}
	// The Windows Pageant agent has no connection to close.
	if conn != nil {
		a.conns = append(a.conns, conn)
	}
	return ag.Signers, nil
}

// release closes every agent connection the auth opened and refuses later
// requests for a client config. Closing the connection ends the agent
// client's reader goroutine.
func (a *agentAuth) release() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.released = true
	for _, c := range a.conns {
		_ = c.Close()
	}
	a.conns = nil
}

// sshLoginName returns the login name go-git's default ssh auth uses: the one
// the URL names, else the name of the account current returns, else $USER. It
// restates go-git's unexported username function, which ssh.NewSSHAgentAuth
// calls for a URL with no login name.
func sshLoginName(fromURL string, current func() (*user.User, error)) (string, error) {
	if fromURL != "" {
		return fromURL, nil
	}
	var name string
	if u, err := current(); err == nil {
		name = u.Username
	} else {
		name = os.Getenv("USER")
	}
	if name == "" {
		return "", errors.New("failed to get username")
	}
	return name, nil
}
