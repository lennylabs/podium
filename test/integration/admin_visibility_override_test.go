package integration

import (
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

// avoSearchIDs returns the artifact IDs from a search response at the given
// full URL (so callers can append &as_admin=1).
func avoSearchIDs(t *testing.T, url string) []string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search status = %d, want 200 (%s)", resp.StatusCode, url)
	}
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

// Spec: §4.7.2 — "View any layer's contents for diagnostic purposes
// (override visibility; the override is itself audited)." Over the registry
// HTTP API, an admin caller passing as_admin=1 reads an artifact in a layer
// their identity cannot otherwise see; a normal read stays filtered, and a
// non-admin caller's override request is rejected with 403 auth.forbidden.
func TestAdminVisibilityOverride_HTTPReadPath(t *testing.T) {
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

	// A private layer visible only to bob, holding one artifact.
	rlvLayer(t, st, store.LayerConfig{TenantID: "default", ID: "private", Order: 1, Users: []string{"bob"}})
	rlvManifest(t, st, store.ManifestRecord{
		TenantID: "default", ArtifactID: "team/secret", Version: "1.0.0",
		ContentHash: "sha256:a", Type: "context", Layer: "private",
		Description: "secret context", Body: []byte("hidden body"),
	})
	// alice holds the admin role but is not in the private layer.
	if err := st.GrantAdmin(ctx, store.AdminGrant{UserID: "alice", OrgID: "default"}); err != nil {
		t.Fatalf("GrantAdmin: %v", err)
	}

	bootLayers := []layer.Layer{{ID: "private", Precedence: 1, Visibility: layer.Visibility{Users: []string{"bob"}}}}
	aliceSrv := httptest.NewServer(server.New(core.New(st, "default", bootLayers),
		server.WithIdentityResolver(func(*http.Request) layer.Identity { return rlvAlice })).Handler())
	defer aliceSrv.Close()
	carolSrv := httptest.NewServer(server.New(core.New(st, "default", bootLayers),
		server.WithIdentityResolver(func(*http.Request) layer.Identity {
			return layer.Identity{Sub: "carol", IsAuthenticated: true}
		})).Handler())
	defer carolSrv.Close()

	// Normal admin read: the private layer is invisible to alice.
	if got := rlvStatus(t, aliceSrv.URL+"/v1/load_artifact?id=team/secret"); got != http.StatusNotFound {
		t.Errorf("alice normal load = %d, want 404", got)
	}
	if rlvHas(avoSearchIDs(t, aliceSrv.URL+"/v1/search_artifacts?query="), "team/secret") {
		t.Errorf("alice normal search leaked team/secret")
	}

	// Override read: as_admin=1 bypasses visibility for the admin.
	if got := rlvStatus(t, aliceSrv.URL+"/v1/load_artifact?id=team/secret&as_admin=1"); got != http.StatusOK {
		t.Errorf("alice override load = %d, want 200", got)
	}
	if !rlvHas(avoSearchIDs(t, aliceSrv.URL+"/v1/search_artifacts?query=&as_admin=1"), "team/secret") {
		t.Errorf("alice override search missing team/secret")
	}

	// A non-admin caller cannot use the override.
	if got := rlvStatus(t, carolSrv.URL+"/v1/load_artifact?id=team/secret&as_admin=1"); got != http.StatusForbidden {
		t.Errorf("carol override load = %d, want 403", got)
	}
	if got := rlvStatus(t, carolSrv.URL+"/v1/search_artifacts?query=&as_admin=1"); got != http.StatusForbidden {
		t.Errorf("carol override search = %d, want 403", got)
	}
}
