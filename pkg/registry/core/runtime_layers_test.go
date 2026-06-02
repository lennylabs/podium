package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// seedRuntimeLayers builds a store with one admin layer present at boot and
// one user-defined layer (owned by alice) registered at runtime. The
// user-defined layer is persisted only as a store.LayerConfig; it is never
// added to the boot-time slice handed to core.New, mirroring a POST /v1/layers
// registration that happens after the registry is constructed (F-4.6.1).
func seedRuntimeLayers(t *testing.T) *core.Registry {
	t.Helper()
	st := store.NewMemory()
	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: tenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	mustPutLayer(t, st, store.LayerConfig{
		TenantID: tenant, ID: "org", Order: 1, Organization: true,
	})
	mustPutLayer(t, st, store.LayerConfig{
		TenantID: tenant, ID: "alice-personal", Order: 10,
		UserDefined: true, Owner: "alice", Users: []string{"alice"},
	})
	mustPutManifest(t, st, store.ManifestRecord{
		TenantID: tenant, ArtifactID: "org-x", Version: "1.0.0",
		ContentHash: "sha256:a", Type: "context", Layer: "org",
	})
	mustPutManifest(t, st, store.ManifestRecord{
		TenantID: tenant, ArtifactID: "alice-y", Version: "1.0.0",
		ContentHash: "sha256:b", Type: "context", Layer: "alice-personal",
	})
	// Boot-time slice carries only the admin layer.
	return core.New(st, tenant, []layer.Layer{
		{ID: "org", Visibility: layer.Visibility{Organization: true}, Precedence: 1},
	})
}

func mustPutLayer(t *testing.T, st store.Store, c store.LayerConfig) {
	t.Helper()
	if err := st.PutLayerConfig(context.Background(), c); err != nil {
		t.Fatalf("PutLayerConfig %q: %v", c.ID, err)
	}
}

func mustPutManifest(t *testing.T, st store.Store, m store.ManifestRecord) {
	t.Helper()
	if err := st.PutManifest(context.Background(), m); err != nil {
		t.Fatalf("PutManifest %q: %v", m.ArtifactID, err)
	}
}

func searchIDs(t *testing.T, reg *core.Registry, id layer.Identity) []string {
	t.Helper()
	res, err := reg.SearchArtifacts(context.Background(), id, core.SearchArtifactsOptions{})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	ids := []string{}
	for _, r := range res.Results {
		ids = append(ids, r.ID)
	}
	return ids
}

// Spec: §4.6 — "User-defined layers: registered at runtime by individual
// users via the CLI/API (§7.3.1)" and "Resolution of layers 1 and 2 happens at
// the registry on every load_domain, search_domains, search_artifacts, and
// load_artifact call." A runtime-registered layer persisted after boot must
// enter its owner's effective view (F-4.6.1). Fails before the fix because the
// read path filtered against a static boot-time slice that never saw it.
func TestRuntimeLayer_UserDefinedEntersOwnersView(t *testing.T) {
	t.Parallel()
	reg := seedRuntimeLayers(t)
	alice := layer.Identity{Sub: "alice", IsAuthenticated: true}

	ids := searchIDs(t, reg, alice)
	if !contains(ids, "org-x") {
		t.Errorf("alice should see the admin org artifact: %v", ids)
	}
	if !contains(ids, "alice-y") {
		t.Errorf("alice should see her runtime-registered layer's artifact (F-4.6.1): %v", ids)
	}
	if _, err := reg.LoadArtifact(context.Background(), alice, "alice-y", core.LoadArtifactOptions{}); err != nil {
		t.Errorf("LoadArtifact alice-y as owner: %v", err)
	}
}

// Spec: §4.6 — a user-defined layer has implicit visibility users:[<registrant>]
// and "is visible only to the user who registered it". A different authenticated
// caller must not see it even though it is now in the resolved layer list.
func TestRuntimeLayer_UserDefinedHiddenFromOthers(t *testing.T) {
	t.Parallel()
	reg := seedRuntimeLayers(t)
	bob := layer.Identity{Sub: "bob", IsAuthenticated: true}

	ids := searchIDs(t, reg, bob)
	if !contains(ids, "org-x") {
		t.Errorf("bob should still see the admin org artifact: %v", ids)
	}
	if contains(ids, "alice-y") {
		t.Errorf("bob must not see alice's user-defined layer (§4.6): %v", ids)
	}
	if _, err := reg.LoadArtifact(context.Background(), bob, "alice-y", core.LoadArtifactOptions{}); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("LoadArtifact alice-y as bob: got %v, want ErrNotFound", err)
	}
}

// Spec: §4.6 composition order — "User-defined layers belonging to the caller"
// sit above "Admin-defined layers" in precedence. The §4.7.5 resolved-layer
// composition recorded on every read reflects that order (admin first, lowest
// precedence; the caller's user-defined layer last, highest).
func TestRuntimeLayer_CompositionOrderUserAboveAdmin(t *testing.T) {
	t.Parallel()
	reg := seedRuntimeLayers(t)
	rec := &recorder{}
	reg = reg.WithAudit(rec.emit)
	alice := layer.Identity{Sub: "alice", IsAuthenticated: true}

	if _, err := reg.SearchArtifacts(context.Background(), alice, core.SearchArtifactsOptions{}); err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	events := rec.snapshot()
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	got := events[0].ResolvedLayers
	want := []string{"org", "alice-personal"}
	if len(got) != len(want) {
		t.Fatalf("ResolvedLayers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ResolvedLayers = %v, want %v (admin below user-defined)", got, want)
		}
	}
}
