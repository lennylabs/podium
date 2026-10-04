package core_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/core"
)

// Spec: §6.3.1 — TenantFromContext reports the tenant ContextWithTenant
// attached and reports false for a context carrying none, including one
// carrying the empty string, so a caller can tell a routed request from an
// unrouted one.
func TestTenantFromContext(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		ctx    context.Context
		want   string
		wantOK bool
	}{
		{"unset", context.Background(), "", false},
		{"empty", core.ContextWithTenant(context.Background(), ""), "", false},
		{"routed", core.ContextWithTenant(context.Background(), "acme"), "acme", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := core.TenantFromContext(tc.ctx)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("TenantFromContext = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
