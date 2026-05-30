package core_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §7.2 — LoadArtifact returns the bundled-resource refs persisted
// on the manifest record so the HTTP layer can serve them (the data
// plane reads res.Resources, not a construction-time cache).
func TestLoadArtifact_ReturnsResourceRefs(t *testing.T) {
	t.Parallel()
	const tenantID = "t"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenantID}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	refs := []store.ResourceRef{
		{Path: "scripts/run.py", ContentHash: "sha256:aaaa", Size: 4, Inline: []byte("data")},
		{Path: "data/big.bin", ContentHash: "sha256:bbbb", Size: 9_000_000},
	}
	if err := st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenantID, ArtifactID: "finance/run", Version: "1.0.0",
		ContentHash: "sha256:c", Type: "skill", Layer: "L", Resources: refs,
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	reg := core.New(st, tenantID, []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	got, err := reg.LoadArtifact(context.Background(), layer.Identity{IsPublic: true}, "finance/run", core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if len(got.Resources) != 2 {
		t.Fatalf("Resources len = %d, want 2", len(got.Resources))
	}
	if got.Resources[0].Path != "scripts/run.py" || string(got.Resources[0].Inline) != "data" {
		t.Errorf("inline ref not surfaced: %+v", got.Resources[0])
	}
}

// Spec: §7.2 / §4.4 — the /objects data-plane route re-checks visibility
// by content hash. ResolveResourceOwner returns a visible owner for a
// caller who can see an artifact bundling the bytes, and false for a
// caller who cannot, or for an unknown hash.
func TestResolveResourceOwner_VisibilityReCheck(t *testing.T) {
	t.Parallel()
	const tenantID = "t"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenantID}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenantID, ArtifactID: "team/secret", Version: "1.0.0",
		ContentHash: "sha256:m", Type: "context", Layer: "private",
		Resources: []store.ResourceRef{{Path: "data/big.bin", ContentHash: "sha256:deadbeef", Size: 9_000_000}},
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	reg := core.New(st, tenantID, []layer.Layer{
		{ID: "private", Precedence: 1, Visibility: layer.Visibility{Users: []string{"alice"}}},
	})

	alice := layer.Identity{Sub: "alice", IsAuthenticated: true}
	bob := layer.Identity{Sub: "bob", IsAuthenticated: true}

	if owner, ok := reg.ResolveResourceOwner(context.Background(), alice, "deadbeef"); !ok || owner != "team/secret" {
		t.Errorf("alice should own deadbeef: owner=%q ok=%v", owner, ok)
	}
	if _, ok := reg.ResolveResourceOwner(context.Background(), bob, "deadbeef"); ok {
		t.Errorf("bob cannot see the private layer; resolution must fail")
	}
	if _, ok := reg.ResolveResourceOwner(context.Background(), alice, "0000"); ok {
		t.Errorf("unknown content hash must not resolve to an owner")
	}
}
