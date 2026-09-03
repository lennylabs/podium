package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) { return id, nil })
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

// decodeErrorEnvelope reads the §6.10 error envelope from a refusal body.
func decodeErrorEnvelope(t *testing.T, body []byte) (code, message, constraint string) {
	t.Helper()
	var env struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v: %s", err, body)
	}
	c, _ := env.Details["constraint"].(string)
	return env.Code, env.Message, c
}

// Spec: §7.3.1 — a caller the admin arm does not admit that asserts owner,
// public, organization, groups, or users on a registration is refused with
// 403 auth.forbidden carrying details.constraint "admin_only_fields", and
// nothing is stored.
func TestLayerRegister_AdminOnlyFieldsRefused(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		body   map[string]any
		fields []string
	}{
		{"public", map[string]any{"public": true}, []string{"public"}},
		{"organization", map[string]any{"organization": true}, []string{"organization"}},
		{"groups", map[string]any{"groups": []string{"acme-finance"}}, []string{"groups"}},
		{"users", map[string]any{"users": []string{"bob@acme.com"}}, []string{"users"}},
		{"owner", map[string]any{"owner": "bob@acme.com"}, []string{"owner"}},
		{"several", map[string]any{
			"public": true, "organization": true,
			"groups": []string{"acme-finance"}, "users": []string{"bob@acme.com"},
			"owner": "bob@acme.com",
		}, []string{"groups", "organization", "owner", "public", "users"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, st, cleanup := newClassHarness(t, layer.Identity{Sub: "alice@acme.com", IsAuthenticated: true})
			defer cleanup()

			body := map[string]any{
				"id": "refused", "source_type": "git",
				"repo": "git@github.com:alice/x.git", "ref": "main",
			}
			for k, v := range tc.body {
				body[k] = v
			}
			resp, out := mustPost(t, base, "/v1/layers", body)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403: %s", resp.StatusCode, out)
			}
			code, msg, constraint := decodeErrorEnvelope(t, out)
			if code != "auth.forbidden" {
				t.Errorf("code = %q, want auth.forbidden", code)
			}
			if constraint != "admin_only_fields" {
				t.Errorf("details.constraint = %q, want admin_only_fields", constraint)
			}
			for _, f := range tc.fields {
				if !strings.Contains(msg, f) {
					t.Errorf("message %q does not name %q", msg, f)
				}
			}
			if _, err := st.GetLayerConfig(context.Background(), "t", "refused"); err == nil {
				t.Errorf("refused registration was persisted")
			}
		})
	}
}

// Spec: §7.3.1 — a field is asserted by its value. A false boolean, an empty
// array, an empty owner, and an owner naming the caller's own subject assert
// nothing, so the registration is admitted.
func TestLayerRegister_AdminOnlyFieldsAssertNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body map[string]any
	}{
		{"bare", map[string]any{}},
		{"user-defined-empty-owner", map[string]any{"user_defined": true, "owner": ""}},
		{"public-false", map[string]any{"public": false}},
		{"organization-false", map[string]any{"organization": false}},
		{"empty-groups", map[string]any{"groups": []string{}}},
		{"empty-users", map[string]any{"users": []string{}}},
		{"own-subject-owner", map[string]any{"user_defined": true, "owner": "alice@acme.com"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, st, cleanup := newClassHarness(t, layer.Identity{Sub: "alice@acme.com", IsAuthenticated: true})
			defer cleanup()

			body := map[string]any{
				"id": "admitted", "source_type": "git",
				"repo": "git@github.com:alice/x.git", "ref": "main",
			}
			for k, v := range tc.body {
				body[k] = v
			}
			resp, out := mustPost(t, base, "/v1/layers", body)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("status = %d, want 201: %s", resp.StatusCode, out)
			}
			cfg, err := st.GetLayerConfig(context.Background(), "t", "admitted")
			if err != nil {
				t.Fatalf("GetLayerConfig: %v", err)
			}
			if tc.name == "own-subject-owner" && cfg.Owner != "alice@acme.com" {
				t.Errorf("Owner = %q, want alice@acme.com", cfg.Owner)
			}
		})
	}
}

// Spec: §7.3.1 — the refusal keys on the caller's §4.7.2 admin arm. A caller
// the arm admits has every admin-only field read on the admin-defined arm.
func TestLayerRegister_AdminArmAdmitsEveryField(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) {
			return layer.Identity{Sub: "alice@acme.com", IsAuthenticated: true}, nil
		})
	ts := httptest.NewServer(endpoint.Handler())
	defer ts.Close()

	resp, out := mustPost(t, ts.URL, "/v1/layers", map[string]any{
		"id": "org-defaults", "source_type": "git",
		"repo": "git@github.com:acme/defaults.git", "ref": "main",
		"owner": "bob@acme.com", "public": true, "organization": true,
		"groups": []string{"acme-finance"}, "users": []string{"bob@acme.com"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", resp.StatusCode, out)
	}
	cfg, err := st.GetLayerConfig(context.Background(), "t", "org-defaults")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if !cfg.Public {
		t.Errorf("Public = false, want true: %+v", cfg)
	}
	if cfg.Owner != "bob@acme.com" {
		t.Errorf("Owner = %q, want bob@acme.com", cfg.Owner)
	}
	if len(cfg.Groups) != 1 || cfg.Groups[0] != "acme-finance" {
		t.Errorf("Groups = %v, want [acme-finance]", cfg.Groups)
	}
}

// Spec: §7.3.1 — the rule keys on the admin arm rather than on the resolved
// class, so it reaches a re-registration of a stored layer the caller owns on
// the same terms, and the stored configuration is unchanged.
func TestLayerRegister_AdminOnlyFieldsOnReRegistration(t *testing.T) {
	t.Parallel()
	base, st, cleanup := newClassHarness(t, layer.Identity{Sub: "alice@acme.com", IsAuthenticated: true})
	defer cleanup()

	resp, out := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "alice-personal", "source_type": "git",
		"repo": "git@github.com:alice/x.git", "ref": "main",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed status = %d, want 201: %s", resp.StatusCode, out)
	}
	before, err := st.GetLayerConfig(context.Background(), "t", "alice-personal")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}

	resp, out = mustPost(t, base, "/v1/layers", map[string]any{
		"id": "alice-personal", "source_type": "git",
		"repo": "git@github.com:alice/x.git", "ref": "main",
		"public": true,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", resp.StatusCode, out)
	}
	code, msg, constraint := decodeErrorEnvelope(t, out)
	if code != "auth.forbidden" || constraint != "admin_only_fields" {
		t.Errorf("code = %q, constraint = %q, want auth.forbidden / admin_only_fields", code, constraint)
	}
	if !strings.Contains(msg, "public") {
		t.Errorf("message %q does not name public", msg)
	}
	after, err := st.GetLayerConfig(context.Background(), "t", "alice-personal")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if after.Public != before.Public || after.Owner != before.Owner {
		t.Errorf("stored layer changed: before %+v after %+v", before, after)
	}
}
