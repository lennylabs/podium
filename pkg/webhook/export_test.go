package webhook

import (
	"context"
	"net"
	"net/http"
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
