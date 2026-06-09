package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// bootTenantCLIServer boots a multi-tenant server whose caller is a granted
// operator, so the admin tenant CLI reaches the §7.3.3 endpoints.
func bootTenantCLIServer(t *testing.T) (*httptest.Server, store.Store) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("seed default: %v", err)
	}
	op := layer.Identity{Sub: "operator@acme.com", OrgID: "acme.com", IsAuthenticated: true}
	if err := st.GrantOperator(context.Background(), op.Sub); err != nil {
		t.Fatalf("grant operator: %v", err)
	}
	srv := server.New(core.New(st, "default", nil),
		server.WithIdentityResolver(func(*http.Request) layer.Identity { return op }),
		server.WithTenantRouter(func(context.Context, string) (string, bool) { return "default", true }, true),
	)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st
}

// Spec: §13 / §7.3.3 — the admin tenant CLI drives create, update (partial),
// list, deactivate, and reactivate end to end against the operator API.
func TestAdminTenantCLI_Lifecycle(t *testing.T) {
	ts, st := bootTenantCLIServer(t)

	if rc := adminTenantCreate([]string{"globex.com", "--storage-bytes", "1000", "--max-user-layers", "5", "--registry", ts.URL}); rc != 0 {
		t.Fatalf("create rc = %d", rc)
	}
	gid := core.OrgIDForName("globex.com")
	tn, err := st.GetTenant(context.Background(), gid)
	if err != nil {
		t.Fatalf("created tenant missing: %v", err)
	}
	if tn.Quota.StorageBytes != 1000 || tn.Quota.MaxUserLayers != 5 || !tn.Active {
		t.Errorf("created tenant = %+v", tn)
	}

	// update only --storage-bytes preserves --max-user-layers (partial PATCH).
	if rc := adminTenantUpdate([]string{gid, "--storage-bytes", "2000", "--registry", ts.URL}); rc != 0 {
		t.Fatalf("update rc = %d", rc)
	}
	tn, _ = st.GetTenant(context.Background(), gid)
	if tn.Quota.StorageBytes != 2000 || tn.Quota.MaxUserLayers != 5 {
		t.Errorf("update did not merge: %+v", tn.Quota)
	}

	if rc := adminTenantList([]string{"--registry", ts.URL}); rc != 0 {
		t.Errorf("list rc = %d", rc)
	}
	if rc := adminTenantList([]string{"--json", "--registry", ts.URL}); rc != 0 {
		t.Errorf("list --json rc = %d", rc)
	}

	if rc := adminTenantDeactivate([]string{gid, "--registry", ts.URL}); rc != 0 {
		t.Fatalf("deactivate rc = %d", rc)
	}
	if tn, _ = st.GetTenant(context.Background(), gid); tn.Active {
		t.Errorf("deactivate did not flip active")
	}

	// reactivate via update --active true.
	if rc := adminTenantUpdate([]string{gid, "--active", "true", "--registry", ts.URL}); rc != 0 {
		t.Fatalf("reactivate rc = %d", rc)
	}
	if tn, _ = st.GetTenant(context.Background(), gid); !tn.Active {
		t.Errorf("reactivate did not restore active")
	}
}

// Missing required args surface as an argument error (exit 2) before any HTTP.
func TestAdminTenantCLI_MissingArgs(t *testing.T) {
	if rc := adminTenantCreate([]string{"--registry", "http://localhost"}); rc != 2 {
		t.Errorf("create without name rc = %d, want 2", rc)
	}
	if rc := adminTenantUpdate([]string{"some-id", "--registry", "http://localhost"}); rc != 2 {
		t.Errorf("update with no fields rc = %d, want 2", rc)
	}
	if rc := adminTenantDeactivate([]string{"--registry", "http://localhost"}); rc != 2 {
		t.Errorf("deactivate without id rc = %d, want 2", rc)
	}
}

// Spec: §13 — `podium admin tenant` dispatches its subcommands and prints help
// for an unknown or empty subcommand.
func TestAdminTenantCmd_Dispatch(t *testing.T) {
	if rc := adminTenantCmd(nil); rc != 2 {
		t.Errorf("empty rc = %d, want 2", rc)
	}
	if rc := adminTenantCmd([]string{"bogus"}); rc != 2 {
		t.Errorf("unknown subcommand rc = %d, want 2", rc)
	}
	if rc := adminTenantCmd([]string{"--help"}); rc != 0 {
		t.Errorf("--help rc = %d, want 0", rc)
	}
}
