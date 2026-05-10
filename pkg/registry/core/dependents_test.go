package core_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7.3 / §4.7.5 — DependentsOf returns the reverse-dependency
// edges for the artifact; invisible callers' artifacts are filtered.
func TestDependentsOf_FiltersByVisibility(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	// public-layer child extends the public parent.
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "t", ArtifactID: "child", Version: "1.0.0",
		ContentHash: "sha256:c", Type: "agent", Layer: "public-layer",
	})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "t", ArtifactID: "secret-child", Version: "1.0.0",
		ContentHash: "sha256:s", Type: "agent", Layer: "secret-layer",
	})
	_ = st.PutDependency(context.Background(), "t", store.DependencyEdge{
		From: "child", To: "parent", Kind: "extends",
	})
	_ = st.PutDependency(context.Background(), "t", store.DependencyEdge{
		From: "secret-child", To: "parent", Kind: "extends",
	})

	reg := core.New(st, "t", []layer.Layer{
		{ID: "public-layer", Visibility: layer.Visibility{Public: true}, Precedence: 1},
		{ID: "secret-layer", Visibility: layer.Visibility{Users: []string{"only-this"}}, Precedence: 2},
	})

	edges, err := reg.DependentsOf(context.Background(), layer.Identity{
		Sub: "joan", IsAuthenticated: true,
	}, "parent")
	if err != nil {
		t.Fatalf("DependentsOf: %v", err)
	}
	if len(edges) != 1 || edges[0].From != "child" {
		t.Errorf("got %v, want only the public child", edges)
	}
}

// Spec: §3.5 — PreviewScope returns aggregated counts only.
func TestPreviewScope_Aggregates(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "t", ArtifactID: "a", Version: "1.0.0",
		ContentHash: "sha256:a", Type: "skill", Sensitivity: "low", Layer: "L",
	})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "t", ArtifactID: "b", Version: "1.0.0",
		ContentHash: "sha256:b", Type: "agent", Sensitivity: "medium", Layer: "L",
	})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "t", ArtifactID: "c", Version: "1.0.0",
		ContentHash: "sha256:c", Type: "skill", Sensitivity: "low", Layer: "L",
	})
	reg := core.New(st, "t", []layer.Layer{
		{ID: "L", Visibility: layer.Visibility{Public: true}},
	})
	preview, err := reg.PreviewScope(context.Background(), publicID)
	if err != nil {
		t.Fatalf("PreviewScope: %v", err)
	}
	if preview.ArtifactCount != 3 {
		t.Errorf("ArtifactCount = %d, want 3", preview.ArtifactCount)
	}
	if preview.ByType["skill"] != 2 {
		t.Errorf("ByType[skill] = %d, want 2", preview.ByType["skill"])
	}
	if preview.BySensitivity["medium"] != 1 {
		t.Errorf("BySensitivity[medium] = %d, want 1", preview.BySensitivity["medium"])
	}
}
