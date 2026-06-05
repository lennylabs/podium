package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// newClassHarness builds a layer endpoint whose admin authorizer denies every
// caller (emulating an identity-provider deployment with a non-admin caller)
// and whose identity resolver returns the given identity. It exercises the
// §7.3.1 server-side registration-class resolution.
func newClassHarness(t *testing.T, id layer.Identity) (string, store.Store, func()) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithAdminAuth(func(*http.Request) error { return server.ErrAdminRequired }).
		WithIdentityResolver(func(*http.Request) layer.Identity { return id })
	ts := httptest.NewServer(endpoint.Handler())
	return ts.URL, st, ts.Close
}

// spec: §7.3.1 / §14.9 — the documented `podium layer register` invocation
// carries no --user-defined flag. An authenticated non-admin caller registers
// a personal (user-defined) layer rather than being rejected with
// auth.forbidden. The owner is the attested identity and visibility is the
// implicit users:[<registrant>].
func TestLayerEndpoint_NonAdminPlainRegisterBecomesUserDefined(t *testing.T) {
	t.Parallel()
	base, st, cleanup := newClassHarness(t, layer.Identity{Sub: "alice@acme.com", IsAuthenticated: true})
	defer cleanup()

	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "my-experiments", "source_type": "git",
		"repo": "git@github.com:alice/podium-experiments.git", "ref": "main",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", resp.StatusCode, body)
	}
	cfg, err := st.GetLayerConfig(context.Background(), "t", "my-experiments")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if !cfg.UserDefined {
		t.Errorf("layer not marked user-defined: %+v", cfg)
	}
	if cfg.Owner != "alice@acme.com" {
		t.Errorf("Owner = %q, want alice@acme.com", cfg.Owner)
	}
	if len(cfg.Users) != 1 || cfg.Users[0] != "alice@acme.com" {
		t.Errorf("Users = %v, want [alice@acme.com]", cfg.Users)
	}
}

// spec: §7.3.1 — an anonymous caller attempting an admin-defined registration
// (no --user-defined, not an admin) is rejected with auth.forbidden rather
// than silently downgraded to a user-defined layer.
func TestLayerEndpoint_AnonymousPlainRegisterForbidden(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newClassHarness(t, layer.Identity{IsPublic: true})
	defer cleanup()

	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "team-shared", "source_type": "local", "local_path": "/x",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", resp.StatusCode, body)
	}
}

// spec: §4.6 / §14.9 — a user-defined registration with no resolvable owner
// (no attested identity and no body owner) would create a layer with no
// visibility entries, visible to no one. The handler rejects it rather than
// persisting an orphaned, unreachable row.
func TestLayerEndpoint_UserDefinedNoOwnerRejected(t *testing.T) {
	t.Parallel()
	// Default harness: anonymous identity, no admin gating.
	base, st, cleanup := newLayerHarness(t)
	defer cleanup()

	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "orphan", "source_type": "local", "local_path": "/x",
		"user_defined": true,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", resp.StatusCode, body)
	}
	if _, err := st.GetLayerConfig(context.Background(), "t", "orphan"); err == nil {
		t.Errorf("orphaned user-defined layer was persisted despite rejection")
	}
}

// spec: §13.10/§13.11 — a no-identity standalone deployment has no
// authenticated callers; the local operator supplies the owner via the
// request body (--owner), which the handler honors because identity-derived
// owner is unavailable and visibility is bypassed in that mode.
func TestLayerEndpoint_UserDefinedBodyOwnerHonoredWithoutIdentity(t *testing.T) {
	t.Parallel()
	base, st, cleanup := newLayerHarness(t)
	defer cleanup()

	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "alice-personal", "source_type": "local", "local_path": "/x",
		"user_defined": true, "owner": "alice@acme.com",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", resp.StatusCode, body)
	}
	cfg, err := st.GetLayerConfig(context.Background(), "t", "alice-personal")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if len(cfg.Users) != 1 || cfg.Users[0] != "alice@acme.com" {
		t.Errorf("Users = %v, want [alice@acme.com]", cfg.Users)
	}
}

// spec: §4.6 — an authenticated admin's plain register (no --user-defined)
// stays admin-defined and honors the request-body visibility.
func TestLayerEndpoint_AdminPlainRegisterStaysAdminDefined(t *testing.T) {
	t.Parallel()
	// Default harness: no-op admin authorizer => caller is treated as admin.
	base, st, cleanup := newLayerHarness(t)
	defer cleanup()

	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "org-defaults", "source_type": "git",
		"repo": "git@github.com:acme/defaults.git", "ref": "main",
		"organization": true,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", resp.StatusCode, body)
	}
	cfg, err := st.GetLayerConfig(context.Background(), "t", "org-defaults")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if cfg.UserDefined {
		t.Errorf("admin register marked user-defined: %+v", cfg)
	}
	if !cfg.Organization {
		t.Errorf("organization visibility dropped: %+v", cfg)
	}
}
