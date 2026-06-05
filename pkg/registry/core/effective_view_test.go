package core_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §2.2, §7.5 — EffectiveView enumerates the caller's whole
// effective view (every visible artifact, latest version per id, sorted by
// id) so podium sync server-source can materialize it. Unlike
// SearchArtifacts it applies no top-K cap.
func TestEffectiveView_ListsLatestPerIDSorted(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: tenant})
	// Two versions of one id, plus a second id, in a public layer.
	for _, v := range []string{"1.0.0", "2.0.0"} {
		_ = st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: tenant, ArtifactID: "b/glossary", Version: v,
			ContentHash: "sha256:" + v, Type: "context", Layer: "L",
		})
	}
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenant, ArtifactID: "a/policy", Version: "1.0.0",
		ContentHash: "sha256:p", Type: "context", Layer: "L",
	})
	reg := core.New(st, tenant, []layer.Layer{
		{ID: "L", Visibility: layer.Visibility{Public: true}, Precedence: 1},
	})

	view, err := reg.EffectiveView(context.Background(), publicID)
	if err != nil {
		t.Fatalf("EffectiveView: %v", err)
	}
	if len(view) != 2 {
		t.Fatalf("EffectiveView returned %d artifacts, want 2 (deduped to latest): %+v", len(view), view)
	}
	// Sorted by id: a/policy before b/glossary.
	if view[0].ID != "a/policy" || view[1].ID != "b/glossary" {
		t.Errorf("order = [%s, %s], want [a/policy, b/glossary]", view[0].ID, view[1].ID)
	}
	if view[1].Version != "2.0.0" {
		t.Errorf("b/glossary version = %q, want 2.0.0 (latest)", view[1].Version)
	}
	if view[0].Layer != "L" {
		t.Errorf("a/policy layer = %q, want L", view[0].Layer)
	}
}

// Spec: §4.6, §2.2 — EffectiveView applies per-layer visibility: a public
// caller never sees an artifact in a layer it cannot read.
func TestEffectiveView_AppliesVisibility(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: tenant})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenant, ArtifactID: "public-x", Version: "1.0.0",
		ContentHash: "sha256:a", Type: "context", Layer: "public",
	})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenant, ArtifactID: "private-y", Version: "1.0.0",
		ContentHash: "sha256:b", Type: "context", Layer: "private",
	})
	reg := core.New(st, tenant, []layer.Layer{
		{ID: "public", Visibility: layer.Visibility{Public: true}, Precedence: 1},
		{ID: "private", Visibility: layer.Visibility{Users: []string{"specific-user"}}, Precedence: 2},
	})

	// An authenticated caller that matches no private-layer grant sees only
	// the public layer. (A bare IsPublic caller bypasses layer filtering by
	// design, so visibility is exercised with a real identity here.)
	outsider := layer.Identity{Sub: "joan", IsAuthenticated: true}
	view, err := reg.EffectiveView(context.Background(), outsider)
	if err != nil {
		t.Fatalf("EffectiveView: %v", err)
	}
	if len(view) != 1 || view[0].ID != "public-x" {
		t.Fatalf("outsider view = %+v, want only public-x", view)
	}

	// The authorized user sees both layers.
	authed := layer.Identity{Sub: "specific-user", IsAuthenticated: true}
	view, err = reg.EffectiveView(context.Background(), authed)
	if err != nil {
		t.Fatalf("EffectiveView(authed): %v", err)
	}
	if len(view) != 2 {
		t.Errorf("authorized caller view has %d artifacts, want 2: %+v", len(view), view)
	}
}

// Spec: §2.2 — an empty registry yields an empty view, not an error.
func TestEffectiveView_EmptyRegistry(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: tenant})
	reg := core.New(st, tenant, []layer.Layer{
		{ID: "L", Visibility: layer.Visibility{Public: true}, Precedence: 1},
	})
	view, err := reg.EffectiveView(context.Background(), publicID)
	if err != nil {
		t.Fatalf("EffectiveView: %v", err)
	}
	if len(view) != 0 {
		t.Errorf("empty registry view = %+v, want empty", view)
	}
}
