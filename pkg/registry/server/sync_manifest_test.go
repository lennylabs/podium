package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

type syncManifestBody struct {
	Artifacts []core.EffectiveArtifact `json:"artifacts"`
}

// Spec: §2.2, §7.5 (F-2.2.2) — GET /v1/sync/manifest returns the caller's
// effective view as a flat artifact list, visibility-filtered server-side,
// so podium sync server-source can enumerate every artifact in one request.
func TestSyncManifest_ListsEffectiveView(t *testing.T) {
	t.Parallel()
	ts, _ := newBatchFixture(t)
	resp, err := http.Get(ts.URL + "/v1/sync/manifest")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body syncManifestBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Artifacts) != 2 {
		t.Fatalf("artifacts = %d, want 2: %+v", len(body.Artifacts), body.Artifacts)
	}
	for _, a := range body.Artifacts {
		if a.ID == "" || a.Type == "" || a.Layer == "" {
			t.Errorf("artifact missing fields: %+v", a)
		}
	}
}

// Spec: §6.10 — a non-GET method on /v1/sync/manifest is rejected.
func TestSyncManifest_RejectsNonGET(t *testing.T) {
	t.Parallel()
	ts, _ := newBatchFixture(t)
	resp, err := http.Post(ts.URL+"/v1/sync/manifest", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

// Spec: §4.6 — /v1/sync/manifest applies per-layer visibility: an
// anonymous-public caller does not see a private layer's artifacts.
func TestSyncManifest_AppliesVisibility(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "default"})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "default", ArtifactID: "public-x", Version: "1.0.0",
		ContentHash: "sha256:a", Type: "context", Layer: "public",
	})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "default", ArtifactID: "private-y", Version: "1.0.0",
		ContentHash: "sha256:b", Type: "context", Layer: "private",
	})
	reg := core.New(st, "default", []layer.Layer{
		{ID: "public", Precedence: 1, Visibility: layer.Visibility{Public: true}},
		{ID: "private", Precedence: 2, Visibility: layer.Visibility{Users: []string{"u"}}},
	})
	// The default server resolver marks callers IsPublic (sees everything);
	// install a resolver returning an authenticated outsider so per-layer
	// visibility filtering applies through the endpoint.
	srv := server.New(reg, server.WithIdentityResolver(func(*http.Request) layer.Identity {
		return layer.Identity{Sub: "joan", IsAuthenticated: true}
	}))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/v1/sync/manifest")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var body syncManifestBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Artifacts) != 1 || body.Artifacts[0].ID != "public-x" {
		t.Errorf("anonymous view = %+v, want only public-x", body.Artifacts)
	}
}
