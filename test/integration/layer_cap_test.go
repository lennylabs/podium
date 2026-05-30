package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §7.3.1 / §1.4 (F-1.4.1) — the user-defined-layer cap is enforced
// against the file-backed SQLite store (the standalone backend), and the
// per-tenant quota configures the limit. This drives the real
// LayerEndpoint over HTTP against a persistent SQLite database so the
// owner count query runs through the SQL backend rather than the
// mutex-serialized in-memory store, and confirms the rejected layer is
// not persisted.
func TestLayerCap_SQLiteEnforcesTenantQuota(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "registry.db")
	st, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	// Per-tenant cap of 2 user-defined layers (configurable per tenant
	// per §4.4 "default user-layer cap").
	if err := st.CreateTenant(ctx, store.Tenant{
		ID:    "t",
		Quota: store.Quota{MaxUserLayers: 2},
	}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker())
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	register := func(id string) (int, string) {
		body, _ := json.Marshal(map[string]any{
			"id": id, "source_type": "local", "local_path": "/tmp/" + id,
			"user_defined": true, "owner": "alice@acme.com",
		})
		resp, err := http.Post(ts.URL+"/v1/layers", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("POST %s: %v", id, err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		var env struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(out, &env)
		return resp.StatusCode, env.Code
	}

	if status, code := register("personal-a"); status != http.StatusCreated {
		t.Fatalf("first layer: status %d code %q, want 201", status, code)
	}
	if status, code := register("personal-b"); status != http.StatusCreated {
		t.Fatalf("second layer: status %d code %q, want 201", status, code)
	}
	// Third exceeds the per-tenant cap of 2.
	status, code := register("personal-c")
	if status != http.StatusTooManyRequests {
		t.Fatalf("third layer: status %d, want 429", status)
	}
	if code != "quota.layer_count_exceeded" {
		t.Errorf("third layer: code %q, want quota.layer_count_exceeded", code)
	}

	// The rejected layer must not be persisted: exactly 2 user-defined
	// layers for the owner survive in the SQLite store.
	all, err := st.ListLayerConfigs(ctx, "t")
	if err != nil {
		t.Fatalf("ListLayerConfigs: %v", err)
	}
	owned := 0
	for _, l := range all {
		if l.UserDefined && l.Owner == "alice@acme.com" {
			owned++
		}
	}
	if owned != 2 {
		t.Errorf("persisted user-defined layers = %d, want 2 (rejected layer must not persist)", owned)
	}
}
