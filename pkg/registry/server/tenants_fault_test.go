package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// bootFaultTenantServer boots a multi-tenant server backed by a FaultStore so a
// test can fail one store method and exercise the registry.unavailable error
// path. The operator is granted on the inner store, and a target tenant exists
// for the update/deactivate scenarios. FailMethod on the returned FaultStore
// fails one method while the rest of the request path succeeds.
func bootFaultTenantServer(t *testing.T) (*httptest.Server, *storetest.FaultStore, string) {
	t.Helper()
	mem := store.NewMemory()
	if err := mem.CreateTenant(context.Background(), store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("seed default: %v", err)
	}
	if err := mem.GrantOperator(context.Background(), operatorCaller.Sub); err != nil {
		t.Fatalf("grant operator: %v", err)
	}
	target := core.OrgIDForName("acme.com")
	if err := mem.CreateTenant(context.Background(), store.Tenant{ID: target, Name: "acme.com"}); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	fault := storetest.NewFaultStore(mem)
	srv := server.New(core.New(fault, "default", nil),
		server.WithIdentityResolver(func(*http.Request) layer.Identity { return operatorCaller }),
		server.WithTenantRouter(func(context.Context, string) (string, bool) { return "default", true }, true),
	)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, fault, target
}

// Spec: §6.10 — a metadata-store fault surfaces as registry.unavailable (or
// auth.forbidden when the operator check itself faults), exercising the
// store-error branches of the tenant-management path.
func TestTenants_StoreFaults(t *testing.T) {
	// The operator check faulting yields auth.forbidden (the non-ErrForbidden
	// path of requireOperator, mapped to 403).
	t.Run("operator check faults", func(t *testing.T) {
		ts, fault, _ := bootFaultTenantServer(t)
		fault.FailMethod("IsOperator")
		code, body := tenantHTTP(t, http.MethodGet, ts.URL+"/v1/admin/tenants", "")
		if code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", code)
		}
		if c := tenantErrCode(t, body); c != "auth.forbidden" {
			t.Errorf("code = %q, want auth.forbidden", c)
		}
	})

	// Each data-method fault, with the operator check succeeding, yields a 500
	// registry.unavailable.
	cases := []struct {
		name, failMethod, method, path, body string
	}{
		{"list faults", "ListTenants", http.MethodGet, "/v1/admin/tenants", ""},
		{"create read faults", "GetTenant", http.MethodPost, "/v1/admin/tenants", `{"name":"globex.com"}`},
		{"create write faults", "CreateTenant", http.MethodPost, "/v1/admin/tenants", `{"name":"newco.com"}`},
		{"update read faults", "GetTenant", http.MethodPatch, "", `{"active":false}`},
		{"update write faults", "UpdateTenant", http.MethodPatch, "", `{"active":false}`},
		{"deactivate faults", "DeactivateTenant", http.MethodDelete, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, fault, target := bootFaultTenantServer(t)
			fault.FailMethod(tc.failMethod)
			path := tc.path
			if path == "" {
				path = "/v1/admin/tenants/" + target
			}
			code, body := tenantHTTP(t, tc.method, ts.URL+path, tc.body)
			if code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500", code)
			}
			if c := tenantErrCode(t, body); c != "registry.unavailable" {
				t.Errorf("code = %q, want registry.unavailable", c)
			}
		})
	}
}
