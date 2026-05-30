package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.6 + PODIUM_DEFAULT_LAYER_VISIBILITY — when an admin-
// defined layer arrives at register time without explicit
// visibility, the configured default takes effect.
func TestLayerRegister_DefaultVisibilityPublic(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithDefaultVisibility("public")
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]any{
		"id":          "team-shared",
		"source_type": "local",
		"local_path":  "/tmp/x",
	})
	resp, err := http.Post(ts.URL+"/v1/layers", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d: %s", resp.StatusCode, buf)
	}
	cfg, err := st.GetLayerConfig(context.Background(), "t", "team-shared")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if !cfg.Public {
		t.Errorf("Public = false, want true (default visibility = public)")
	}
}

// Spec: §4.6 — when an admin-defined layer carries explicit
// visibility, the default does not override it.
func TestLayerRegister_ExplicitVisibilityWins(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithDefaultVisibility("public")
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]any{
		"id":           "team-shared",
		"source_type":  "local",
		"local_path":   "/tmp/x",
		"organization": true,
	})
	resp, err := http.Post(ts.URL+"/v1/layers", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	cfg, _ := st.GetLayerConfig(context.Background(), "t", "team-shared")
	if cfg.Public {
		t.Errorf("Public = true; explicit organization should win over default")
	}
	if !cfg.Organization {
		t.Errorf("Organization = false; explicit value lost")
	}
}

// Spec: §13.10 line 175 (F-13.12.15) — PODIUM_DEFAULT_LAYER_VISIBILITY=users
// scopes an admin-defined layer registered without explicit visibility to the
// authenticated registrant (users: [<registrant>]).
func TestLayerRegister_DefaultVisibilityUsers(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithDefaultVisibility("users").
		WithIdentityResolver(func(*http.Request) layer.Identity {
			return layer.Identity{IsAuthenticated: true, Sub: "alice@acme.com"}
		})
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]any{
		"id": "team-shared", "source_type": "local", "local_path": "/tmp/x",
	})
	resp, err := http.Post(ts.URL+"/v1/layers", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	cfg, _ := st.GetLayerConfig(context.Background(), "t", "team-shared")
	if cfg.Public || cfg.Organization {
		t.Errorf("layer = {public=%v org=%v}, want neither for the users default", cfg.Public, cfg.Organization)
	}
	if len(cfg.Users) != 1 || cfg.Users[0] != "alice@acme.com" {
		t.Errorf("Users = %v, want [alice@acme.com]", cfg.Users)
	}
}

// Spec: §13.10 (F-13.12.15) — with the users default but no authenticated
// registrant (anonymous standalone), the layer falls back to private (no
// visibility filters), the safe fallback for the users selection.
func TestLayerRegister_DefaultVisibilityUsersAnonymousStaysPrivate(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithDefaultVisibility("users") // default identity resolver is anonymous
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]any{
		"id": "team-shared", "source_type": "local", "local_path": "/tmp/x",
	})
	resp, err := http.Post(ts.URL+"/v1/layers", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	cfg, _ := st.GetLayerConfig(context.Background(), "t", "team-shared")
	if cfg.Public || cfg.Organization || len(cfg.Users) != 0 || len(cfg.Groups) != 0 {
		t.Errorf("layer = %+v, want no visibility filters (private)", cfg)
	}
}
