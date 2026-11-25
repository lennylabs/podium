package source

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"golang.org/x/net/proxy"
)

// deadlineDialScheme is the proxy URL scheme that routes the TCP dial of an
// ssh clone through deadlineDialer. It names no real proxy, and no other
// caller of golang.org/x/net/proxy uses it.
const deadlineDialScheme = "podium-git-deadline"

// The ssh transport of go-git dials under context.Background() and then runs
// the SSH handshake with no deadline, before it consults the clone's context,
// so a remote that accepts the connection and never sends its banner, or that
// stalls in key exchange or authentication, would hold the snapshot past
// FetchTimeout. The transport's one per-clone hook into that dial is the
// proxy URL, which it resolves through golang.org/x/net/proxy. The scheme is
// registered here, at package initialization, because that registry is a
// plain map with no lock: registering it later could race a concurrent
// lookup.
//
// Spec: §7.3.1, §13.12
func init() {
	proxy.RegisterDialerType(deadlineDialScheme, newDeadlineDialer)
}

// deadlineDials maps the token in a deadline proxy URL to the context of the
// snapshot that issued it. A snapshot removes its token when it returns.
var (
	deadlineDials     sync.Map // token string -> context.Context
	deadlineDialToken atomic.Uint64
)

// snapshotProxyOptions returns the clone's proxy options for repo. For an
// ssh remote, ssh:// or scp-like, it registers ctx under a fresh token and
// returns a deadline proxy URL naming it, with a release function that
// unregisters the token. For every other remote it returns empty options:
// the http transport honours the clone context on every request, and an
// empty value leaves go-git's own proxy handling in place.
func snapshotProxyOptions(ctx context.Context, repo string) (transport.ProxyOptions, func()) {
	ep, err := transport.NewEndpoint(repo)
	if err != nil || ep.Protocol != "ssh" {
		return transport.ProxyOptions{}, func() {}
	}
	token := strconv.FormatUint(deadlineDialToken.Add(1), 10)
	deadlineDials.Store(token, ctx)
	return transport.ProxyOptions{URL: deadlineDialScheme + "://" + token},
		func() { deadlineDials.Delete(token) }
}

// newDeadlineDialer is the golang.org/x/net/proxy constructor for the
// deadline scheme. A token that is no longer registered belongs to a snapshot
// that already returned, so the dial is refused.
func newDeadlineDialer(u *url.URL, _ proxy.Dialer) (proxy.Dialer, error) {
	v, ok := deadlineDials.Load(u.Host)
	if !ok {
		return nil, fmt.Errorf("git fetch deadline: no snapshot is waiting on this dial")
	}
	return deadlineDialer{ctx: v.(context.Context)}, nil
}

// deadlineDialer dials an ssh remote and ties the connection to the snapshot
// context, so the handshake and every later read or write on it end at the
// snapshot's deadline, and the connection is closed when the snapshot ends.
// It dials through golang.org/x/net/proxy.Dial, which go-git's ssh transport
// itself uses when no proxy is set, so ALL_PROXY and NO_PROXY apply as
// before.
type deadlineDialer struct {
	ctx context.Context
}

// Dial implements proxy.Dialer.
func (d deadlineDialer) Dial(network, addr string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, addr)
}

// DialContext implements proxy.ContextDialer. go-git passes a context with no
// deadline, so the dial runs under the snapshot context instead.
func (d deadlineDialer) DialContext(_ context.Context, network, addr string) (net.Conn, error) {
	conn, err := proxy.Dial(d.ctx, network, addr)
	if err != nil {
		return nil, err
	}
	if deadline, ok := d.ctx.Deadline(); ok {
		// SetDeadline fails only on a closed connection, which a dial that
		// just succeeded does not return, so no test reaches this branch.
		if err := conn.SetDeadline(deadline); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	// The snapshot cancels its context when it returns, so the connection
	// never outlives the snapshot, whatever go-git does with it.
	context.AfterFunc(d.ctx, func() { _ = conn.Close() })
	return conn, nil
}
