package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// routedBootTenant is the tenant the fixture registry binds, which the
// resolver maps no organization to, so a handler that reports it observed an
// unrouted request.
const routedBootTenant = "poison-p"

// routedResolver maps org-a to tenant-a and resolves every other organization
// to no tenant, mirroring the server-boot resolver over provisioned tenants.
func routedResolver(_ context.Context, org string) (store.Tenant, bool) {
	if org == "org-a" {
		return store.Tenant{ID: "tenant-a"}, true
	}
	return store.Tenant{}, false
}

// headerVerifier is a stub §6.3.2 verifier: X-Test-Fail selects a
// verification error, otherwise X-Test-User and X-Test-Org name the caller.
func headerVerifier(r *http.Request) (layer.Identity, error) {
	if r.Header.Get("X-Test-Fail") != "" {
		return layer.Identity{}, identity.ErrUntrustedRuntime
	}
	return layer.Identity{
		Sub:             r.Header.Get("X-Test-User"),
		OrgID:           r.Header.Get("X-Test-Org"),
		IsAuthenticated: r.Header.Get("X-Test-User") != "",
	}, nil
}

// tenantProbe answers 200 with the tenant the registry resolves the request
// against, so a test reads which tenant the wrapper selected.
func tenantProbe(reg *core.Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(reg.TenantFor(r.Context())))
	})
}

// routedServer builds a Server over a registry bound to routedBootTenant with
// the given options.
func routedServer(opts ...server.Option) (*server.Server, *core.Registry) {
	reg := core.New(store.NewMemory(), routedBootTenant, nil)
	return server.New(reg, opts...), reg
}

// serveRouted sends one request with the given headers through h.
func serveRouted(h http.Handler, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v1/layers", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestTenantRouted_Routing covers every branch of the layer-route wrapper and
// its non-refusing posture-read variant.
// Spec: §6.3.1, §7.3.1, §7.3.4, §6.10
func TestTenantRouted_Routing(t *testing.T) {
	t.Parallel()
	known := map[string]string{"X-Test-User": "alice@acme.com", "X-Test-Org": "org-a"}
	unknown := map[string]string{"X-Test-User": "bob@acme.com", "X-Test-Org": "org-z"}
	failed := map[string]string{"X-Test-User": "alice@acme.com", "X-Test-Org": "org-a", "X-Test-Fail": "1"}
	anonymous := map[string]string{"X-Test-Org": "org-a"}

	cases := []struct {
		name       string
		opts       []server.Option
		noReject   bool
		headers    map[string]string
		wantStatus int
		wantTenant string
	}{
		{"resolved org is routed", []server.Option{server.WithIdentityVerifier(headerVerifier), server.WithTenantRouter(routedResolver, true)}, false, known, http.StatusOK, "tenant-a"},
		{"unresolved org is refused under a rejecting provider", []server.Option{server.WithIdentityVerifier(headerVerifier), server.WithTenantRouter(routedResolver, true)}, false, unknown, http.StatusUnauthorized, ""},
		{"unresolved org stays unrouted under a non-rejecting provider", []server.Option{server.WithIdentityVerifier(headerVerifier), server.WithTenantRouter(routedResolver, false)}, false, unknown, http.StatusOK, routedBootTenant},
		{"verification error stays unrouted", []server.Option{server.WithIdentityVerifier(headerVerifier), server.WithTenantRouter(routedResolver, true)}, false, failed, http.StatusOK, routedBootTenant},
		{"unauthenticated caller stays unrouted", []server.Option{server.WithIdentityVerifier(headerVerifier), server.WithTenantRouter(routedResolver, true)}, false, anonymous, http.StatusOK, routedBootTenant},
		{"no verifier stays unrouted", []server.Option{server.WithTenantRouter(routedResolver, true)}, false, known, http.StatusOK, routedBootTenant},
		{"no router passes through", []server.Option{server.WithIdentityVerifier(headerVerifier)}, false, known, http.StatusOK, routedBootTenant},
		{"no-reject routes a resolved org", []server.Option{server.WithIdentityVerifier(headerVerifier), server.WithTenantRouter(routedResolver, true)}, true, known, http.StatusOK, "tenant-a"},
		{"no-reject leaves an unresolved org unrouted", []server.Option{server.WithIdentityVerifier(headerVerifier), server.WithTenantRouter(routedResolver, true)}, true, unknown, http.StatusOK, routedBootTenant},
		{"no-reject with no router passes through", []server.Option{server.WithIdentityVerifier(headerVerifier)}, true, known, http.StatusOK, routedBootTenant},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, reg := routedServer(tc.opts...)
			h := srv.TenantRouted(tenantProbe(reg))
			if tc.noReject {
				h = srv.TenantRoutedNoReject(tenantProbe(reg))
			}
			rec := serveRouted(h, tc.headers)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				if got := rec.Body.String(); got != tc.wantTenant {
					t.Errorf("tenant = %q, want %q", got, tc.wantTenant)
				}
				return
			}
			assertTenantUnknown(t, rec, "org-z")
		})
	}
}

// TestTenantRouted_NilRouterReturnsNext pins that a single-tenant server
// returns the wrapped handler itself rather than a wrapper.
// Spec: §6.3.1
func TestTenantRouted_NilRouterReturnsNext(t *testing.T) {
	t.Parallel()
	srv, reg := routedServer(server.WithIdentityVerifier(headerVerifier))
	next := &probeHandler{reg: reg}
	for name, h := range map[string]http.Handler{
		"TenantRouted":         srv.TenantRouted(next),
		"TenantRoutedNoReject": srv.TenantRoutedNoReject(next),
	} {
		if h != http.Handler(next) {
			t.Fatalf("%s: handler %T, want the unwrapped next", name, h)
		}
		if rec := serveRouted(h, map[string]string{"X-Test-User": "alice@acme.com", "X-Test-Org": "org-a"}); rec.Body.String() != routedBootTenant {
			t.Errorf("%s: tenant = %q, want %q", name, rec.Body.String(), routedBootTenant)
		}
	}
}

// probeHandler is a comparable tenantProbe, so a test can check that a
// wrapper returned it unchanged.
type probeHandler struct{ reg *core.Registry }

func (p *probeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tenantProbe(p.reg).ServeHTTP(w, r)
}

// assertTenantUnknown checks the §6.10 auth.tenant_unknown envelope and its
// details.token_org_id member.
func assertTenantUnknown(t *testing.T, rec *httptest.ResponseRecorder, org string) {
	t.Helper()
	var env struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v (body %s)", err, rec.Body.String())
	}
	if env.Code != "auth.tenant_unknown" {
		t.Errorf("code = %q, want auth.tenant_unknown", env.Code)
	}
	if env.Details["token_org_id"] != org {
		t.Errorf("details.token_org_id = %v, want %q", env.Details["token_org_id"], org)
	}
}
