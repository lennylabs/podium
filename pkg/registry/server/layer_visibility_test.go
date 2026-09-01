package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

func putJSON(t *testing.T, base, path string, body any) (*http.Response, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	defer resp.Body.Close()
	out := new(bytes.Buffer)
	_, _ = out.ReadFrom(resp.Body)
	return resp, out.Bytes()
}

// Spec: §4.6 — a user-defined layer's implicit users:[owner] visibility
// "cannot be widened." An update that tries to add public/organization/
// groups is ignored for a user-defined layer.
func TestLayerEndpoint_UpdateCannotWidenUserDefined(t *testing.T) {
	t.Parallel()
	base, st, cleanup := newLayerHarness(t)
	defer cleanup()
	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "personal", "source_type": "local", "local_path": "/tmp/p",
		"user_defined": true, "owner": "alice",
	})
	resp, body := putJSON(t, base, "/v1/layers/update?id=personal", map[string]any{
		"public": true, "organization": true, "groups": []string{"team-a"}, "users": []string{"bob"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d: %s", resp.StatusCode, body)
	}
	cfg, err := st.GetLayerConfig(context.Background(), "t", "personal")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if cfg.Public || cfg.Organization || len(cfg.Groups) != 0 {
		t.Errorf("user-defined layer was widened: %+v", cfg)
	}
	if len(cfg.Users) != 1 || cfg.Users[0] != "alice" {
		t.Errorf("Users = %v, want [alice] (unchanged)", cfg.Users)
	}
}

// Spec: §4.6 — the owner of a user-defined layer is the authenticated
// registrant; a caller cannot register a layer owned by an arbitrary
// subject. The identity-derived owner overrides the request body.
func TestLayerEndpoint_UserDefinedOwnerFromIdentity(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) {
			return layer.Identity{Sub: "alice", IsAuthenticated: true}, nil
		})
	ts := httptest.NewServer(endpoint.Handler())
	defer ts.Close()

	resp, body := mustPost(t, ts.URL, "/v1/layers", map[string]any{
		"id": "personal", "source_type": "local", "local_path": "/tmp/p",
		"user_defined": true, "owner": "bob", // attempt to spoof another owner
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	var got server.LayerRegisterResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Layer.Owner != "alice" {
		t.Errorf("Owner = %q, want alice (from identity, not the body's bob)", got.Layer.Owner)
	}
	if len(got.Layer.Users) != 1 || got.Layer.Users[0] != "alice" {
		t.Errorf("Users = %v, want [alice]", got.Layer.Users)
	}
}

// Spec: §7.3.1 — the layer-write authorization rule. Mutating an
// admin-defined layer is authorized to a tenant admin alone. A user-defined
// layer is authorized to its stored owner or to a tenant admin, so a caller
// whom the admin arm denies and who resolves no subject is refused as well.
func TestLayerEndpoint_UpdateAdminGating(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	// Seed both an admin-defined and a user-defined layer with a no-op
	// admin authorizer so setup is unobstructed.
	seed := server.NewLayerEndpoint(st, "t", server.NewModeTracker())
	seedTS := httptest.NewServer(seed.Handler())
	mustPost(t, seedTS.URL, "/v1/layers", map[string]any{"id": "team", "source_type": "local", "local_path": "/x"})
	mustPost(t, seedTS.URL, "/v1/layers", map[string]any{
		"id": "personal", "source_type": "local", "local_path": "/p", "user_defined": true, "owner": "alice",
	})
	seedTS.Close()

	// A second endpoint that denies admin authorization.
	denied := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithAdminAuth(func(*http.Request) error { return server.ErrAdminRequired })
	ts := httptest.NewServer(denied.Handler())
	defer ts.Close()

	// Admin-defined layer update is rejected without admin auth.
	resp, _ := putJSON(t, ts.URL, "/v1/layers/update?id=team", map[string]any{"ref": "release"})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("admin-defined update status = %d, want 403", resp.StatusCode)
	}
	// The user-defined layer is owned by alice; this endpoint's default
	// identity resolver resolves no subject, so the owner arm fails and the
	// denying admin arm refuses the update.
	resp2, body2 := putJSON(t, ts.URL, "/v1/layers/update?id=personal", map[string]any{"local_path": "/p2"})
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("user-defined update status = %d, want 403: %s", resp2.StatusCode, body2)
	}
	if code := errCode(t, body2); code != "auth.forbidden" {
		t.Errorf("user-defined update code = %q, want auth.forbidden", code)
	}
}
