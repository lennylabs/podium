package webhook

import (
	"context"
	"net"
	"net/http"
	"time"
)

// WorkerCheckRedirect exposes the worker's selected CheckRedirect hook to
// the external test package so the policy-aware-versus-NoRedirect selection
// is covered without driving a live TLS redirect.
func WorkerCheckRedirect(w *Worker) func(*http.Request, []*http.Request) error {
	return w.checkRedirect()
}

// SetResolver injects a host resolver into a URLPolicy for tests so the
// SSRF policy can be exercised without DNS. It is exported only to the
// external webhook_test package through this _test.go file.
func SetResolver(p *URLPolicy, resolve func(ctx context.Context, host string) ([]net.IP, error)) {
	p.resolveHost = resolve
}

// DefaultResolveHost exposes the production resolver to tests so the
// default DNS path is covered against a host that resolves without an
// external network (localhost).
func DefaultResolveHost(ctx context.Context, host string) ([]net.IP, error) {
	return defaultResolveHost(ctx, host)
}

// SetTimerFactory injects a debounce-window timer factory so a test drives the
// trailing window through Worker.Flush instead of through wall-clock expiry.
// The factory receives the window duration and the fire callback and returns a
// Stop()-able handle; a test returns a no-op timer so the window fires only
// when the test calls Flush.
func SetTimerFactory(w *Worker, factory func(d time.Duration, fn func()) func() bool) {
	w.newTimer = func(d time.Duration, fn func()) stopper {
		return stopFunc(factory(d, fn))
	}
}

// stopFunc adapts a plain stop closure to the internal stopper interface so a
// test timer factory needs no knowledge of unexported types.
type stopFunc func() bool

func (s stopFunc) Stop() bool { return s() }
