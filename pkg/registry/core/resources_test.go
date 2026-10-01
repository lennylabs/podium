package core_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
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
	// The large body lives in object storage under its own hash, as ingest
	// leaves a resource above the cutoff, and admission reads it there.
	objects := objectstore.NewMemory()
	big := bytes.Repeat([]byte("b"), objectstore.InlineCutoff+1)
	bigSum := sha256.Sum256(big)
	bigKey := hex.EncodeToString(bigSum[:])
	if err := objects.Put(context.Background(), bigKey, big, "application/octet-stream"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	refs := []store.ResourceRef{
		{Path: "scripts/run.py", Inline: []byte("data")},
		{Path: "data/big.bin", ContentHash: "sha256:" + bigKey},
	}
	if err := st.PutManifest(context.Background(), storetest.Seal(t, store.ManifestRecord{
		TenantID: tenantID, ArtifactID: "finance/run", Version: "1.0.0",
		Type: "skill", Layer: "L", Resources: refs,
	}, objects, nil)); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	reg := core.New(st, tenantID, []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	}).WithAdmission(nil, objects, objectstore.DefaultReadTimeout)
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
