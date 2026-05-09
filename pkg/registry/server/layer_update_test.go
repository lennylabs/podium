package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §7.3.1 — `podium layer update` issues PUT
// /v1/layers/update?id=ID. The endpoint applies a partial patch:
// non-zero fields replace the prior LayerConfig values, zero
// fields are preserved.
// Phase: 10
func TestLayerUpdate_PartialPatch(t *testing.T) {
	testharness.RequirePhase(t, 10)
	t.Parallel()
	const tenantID = "t"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenantID}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutLayerConfig(context.Background(), store.LayerConfig{
		TenantID:   tenantID,
		ID:         "team",
		SourceType: "git",
		Repo:       "git@example/team.git",
		Ref:        "main",
		Public:     false,
		Users:      []string{"alice"},
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, tenantID, server.NewModeTracker())
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]any{
		"ref":          "release-26",
		"organization": true,
	})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/layers/update?id=team", bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got, err := st.GetLayerConfig(context.Background(), tenantID, "team")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if got.Ref != "release-26" {
		t.Errorf("Ref = %q, want release-26", got.Ref)
	}
	if !got.Organization {
		t.Errorf("Organization = false, want true")
	}
	// Untouched fields stay the same.
	if got.SourceType != "git" {
		t.Errorf("SourceType = %q, want git (immutable)", got.SourceType)
	}
	if len(got.Users) != 1 || got.Users[0] != "alice" {
		t.Errorf("Users = %v, want [alice] preserved", got.Users)
	}
}

// Spec: §7.3.1 — updating an unknown layer returns
// registry.not_found.
// Phase: 10
func TestLayerUpdate_NotFound(t *testing.T) {
	testharness.RequirePhase(t, 10)
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker())
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]any{"ref": "main"})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/layers/update?id=missing", bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
