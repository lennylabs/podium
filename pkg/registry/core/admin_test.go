package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7.2 / §6.10 — non-admin callers fail admin-only
// operations with auth.forbidden.
// Matrix: §6.10 (auth.forbidden)
func TestAdminAuthorize_NonAdminRejected(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	reg := core.New(st, "t", nil)
	id := layer.Identity{Sub: "joan", IsAuthenticated: true}
	err := reg.AdminAuthorize(context.Background(), id)
	if !errors.Is(err, core.ErrForbidden) {
		t.Errorf("got %v, want ErrForbidden", err)
	}
}

// Spec: §4.7.2 — admin-granted callers pass.
func TestAdminAuthorize_AdminAllowed(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	if err := st.GrantAdmin(context.Background(), store.AdminGrant{
		UserID: "joan", OrgID: "t",
	}); err != nil {
		t.Fatalf("GrantAdmin: %v", err)
	}
	reg := core.New(st, "t", nil)
	id := layer.Identity{Sub: "joan", IsAuthenticated: true}
	if err := reg.AdminAuthorize(context.Background(), id); err != nil {
		t.Errorf("AdminAuthorize: %v", err)
	}
}

// Spec: §13.10 — public-mode callers cannot be admins; admin ops
// return auth.forbidden.
func TestAdminAuthorize_PublicModeRejected(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	reg := core.New(st, "t", nil)
	err := reg.AdminAuthorize(context.Background(), layer.Identity{IsPublic: true})
	if !errors.Is(err, core.ErrForbidden) {
		t.Errorf("got %v, want ErrForbidden", err)
	}
}
