package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

var (
	rlvAlice = layer.Identity{Sub: "alice", IsAuthenticated: true}
	rlvBob   = layer.Identity{Sub: "bob", IsAuthenticated: true}
)

func rlvLayer(t *testing.T, st store.Store, c store.LayerConfig) {
	t.Helper()
	if err := st.PutLayerConfig(context.Background(), c); err != nil {
		t.Fatalf("PutLayerConfig %q: %v", c.ID, err)
	}
}

func rlvManifest(t *testing.T, st store.Store, m store.ManifestRecord) {
	t.Helper()
	if err := st.PutManifest(context.Background(), m); err != nil {
		t.Fatalf("PutManifest %q: %v", m.ArtifactID, err)
	}
}

func rlvSearchIDs(t *testing.T, base string) []string {
	t.Helper()
	resp, err := http.Get(base + "/v1/search_artifacts?query=")
	if err != nil {
		t.Fatalf("GET search: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	ids := []string{}
	for _, r := range body.Results {
		ids = append(ids, r.ID)
	}
	return ids
}

func rlvStatus(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func rlvHas(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// Spec: §4.6 (F-4.6.1) — a runtime-registered (user-defined) layer registered
// after boot via POST /v1/layers enters its owner's effective view on the read
// path. The full server resolves the layer list per request from the SQLite
// store rather than from the static boot-time slice, so the owner sees the
// layer's artifacts and a different authenticated caller does not.
func TestRuntimeLayerVisibility_SQLiteReadPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	// Admin layer present at boot, with one artifact.
	rlvLayer(t, st, store.LayerConfig{TenantID: "default", ID: "org", Order: 1, Organization: true})
	rlvManifest(t, st, store.ManifestRecord{
		TenantID: "default", ArtifactID: "org/x", Version: "1.0.0",
		ContentHash: "sha256:a", Type: "context", Layer: "org",
	})

	// Boot-time slice carries only the admin layer (alice's layer does not yet
	// exist at construction).
	bootLayers := []layer.Layer{{ID: "org", Precedence: 1, Visibility: layer.Visibility{Organization: true}}}

	// alice registers a personal layer at runtime via POST /v1/layers.
	regEndpoint := server.NewLayerEndpoint(st, "default", server.NewModeTracker()).
		WithIdentityResolver(func(*http.Request) layer.Identity { return rlvAlice })
	regTS := httptest.NewServer(regEndpoint.Handler())
	defer regTS.Close()
	reqBody, _ := json.Marshal(map[string]any{
		"id": "alice-personal", "source_type": "local", "local_path": "/tmp/alice", "user_defined": true,
	})
	resp, err := http.Post(regTS.URL+"/v1/layers", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("register layer: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register layer status = %d, want 201", resp.StatusCode)
	}
	// A reingest would populate it; put the layer's artifact directly.
	rlvManifest(t, st, store.ManifestRecord{
		TenantID: "default", ArtifactID: "alice/y", Version: "1.0.0",
		ContentHash: "sha256:b", Type: "context", Layer: "alice-personal",
	})

	// Read servers over the same store, one per identity.
	aliceSrv := httptest.NewServer(server.New(core.New(st, "default", bootLayers),
		server.WithIdentityResolver(func(*http.Request) layer.Identity { return rlvAlice })).Handler())
	defer aliceSrv.Close()
	bobSrv := httptest.NewServer(server.New(core.New(st, "default", bootLayers),
		server.WithIdentityResolver(func(*http.Request) layer.Identity { return rlvBob })).Handler())
	defer bobSrv.Close()

	// alice sees the admin layer and her runtime-registered layer.
	aliceIDs := rlvSearchIDs(t, aliceSrv.URL)
	if !rlvHas(aliceIDs, "org/x") {
		t.Errorf("alice should see the admin org/x: %v", aliceIDs)
	}
	if !rlvHas(aliceIDs, "alice/y") {
		t.Errorf("alice should see her runtime-registered alice/y (F-4.6.1): %v", aliceIDs)
	}
	if got := rlvStatus(t, aliceSrv.URL+"/v1/load_artifact?id=alice/y"); got != http.StatusOK {
		t.Errorf("alice load alice/y = %d, want 200", got)
	}

	// bob sees only the admin layer.
	bobIDs := rlvSearchIDs(t, bobSrv.URL)
	if !rlvHas(bobIDs, "org/x") {
		t.Errorf("bob should see the admin org/x: %v", bobIDs)
	}
	if rlvHas(bobIDs, "alice/y") {
		t.Errorf("bob must not see alice's user-defined layer (§4.6): %v", bobIDs)
	}
	if got := rlvStatus(t, bobSrv.URL+"/v1/load_artifact?id=alice/y"); got != http.StatusNotFound {
		t.Errorf("bob load alice/y = %d, want 404", got)
	}
}
