package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

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

// Spec: §3.5 — counts are per distinct artifact, not per (artifact,
// version) pair. An artifact with three ingested versions counts once,
// and its type / sensitivity reflect the §4.7.6 `latest` version.
// spec:
func TestPreviewScope_CountsDistinctArtifactsNotVersions(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// One artifact, three versions; the most recently ingested (v3, "high")
	// is what `latest` resolves to, so the tally must reflect "high".
	for i, v := range []struct {
		ver, sens, hash string
	}{
		{"1.0.0", "low", "sha256:a1"},
		{"2.0.0", "medium", "sha256:a2"},
		{"3.0.0", "high", "sha256:a3"},
	} {
		_ = st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: "t", ArtifactID: "multi", Version: v.ver,
			ContentHash: v.hash, Type: "skill", Sensitivity: v.sens, Layer: "L",
			IngestedAt: base.Add(time.Duration(i) * time.Hour),
		})
	}
	// A second, single-version artifact so the count is unambiguous.
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "t", ArtifactID: "solo", Version: "1.0.0",
		ContentHash: "sha256:s", Type: "agent", Sensitivity: "low", Layer: "L",
		IngestedAt: base,
	})
	reg := core.New(st, "t", []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}}})

	preview, err := reg.PreviewScope(context.Background(), publicID)
	if err != nil {
		t.Fatalf("PreviewScope: %v", err)
	}
	if preview.ArtifactCount != 2 {
		t.Errorf("ArtifactCount = %d, want 2 (distinct artifacts, not 4 versions)", preview.ArtifactCount)
	}
	if preview.ByType["skill"] != 1 {
		t.Errorf("ByType[skill] = %d, want 1 (multi counted once)", preview.ByType["skill"])
	}
	if preview.ByType["agent"] != 1 {
		t.Errorf("ByType[agent] = %d, want 1", preview.ByType["agent"])
	}
	// "multi" resolves latest to v3 (high); "solo" is low. No medium bucket.
	if preview.BySensitivity["high"] != 1 {
		t.Errorf("BySensitivity[high] = %d, want 1 (latest version of multi)", preview.BySensitivity["high"])
	}
	if preview.BySensitivity["medium"] != 0 {
		t.Errorf("BySensitivity[medium] = %d, want 0 (intermediate version must not count)", preview.BySensitivity["medium"])
	}
	if preview.BySensitivity["low"] != 1 {
		t.Errorf("BySensitivity[low] = %d, want 1 (solo)", preview.BySensitivity["low"])
	}
}

// Spec: §3.5 / §4.6 — `layers` is the ordered composition (lowest
// precedence first) of every layer the identity sees, including a visible
// layer that currently holds no artifacts. The order is deterministic
// across calls.
// spec:
func TestPreviewScope_LayersOrderedIncludingEmpty(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	// Artifacts live only in "mid" and "high"; "low" is visible but empty.
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "t", ArtifactID: "x", Version: "1.0.0",
		ContentHash: "sha256:x", Type: "skill", Layer: "mid",
	})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "t", ArtifactID: "y", Version: "1.0.0",
		ContentHash: "sha256:y", Type: "skill", Layer: "high",
	})
	// Layers supplied out of precedence order to prove the result is sorted.
	reg := core.New(st, "t", []layer.Layer{
		{ID: "high", Precedence: 3, Visibility: layer.Visibility{Public: true}},
		{ID: "low", Precedence: 1, Visibility: layer.Visibility{Public: true}},
		{ID: "mid", Precedence: 2, Visibility: layer.Visibility{Public: true}},
	})

	preview, err := reg.PreviewScope(context.Background(), publicID)
	if err != nil {
		t.Fatalf("PreviewScope: %v", err)
	}
	want := []string{"low", "mid", "high"}
	if !reflect.DeepEqual(preview.Layers, want) {
		t.Errorf("Layers = %v, want %v (precedence order, empty 'low' included)", preview.Layers, want)
	}
	// Determinism: a second call yields the identical ordering.
	again, err := reg.PreviewScope(context.Background(), publicID)
	if err != nil {
		t.Fatalf("PreviewScope (again): %v", err)
	}
	if !reflect.DeepEqual(again.Layers, want) {
		t.Errorf("Layers (second call) = %v, want %v (must be deterministic)", again.Layers, want)
	}
}

// Spec: §3.5 — an artifact that omits the optional sensitivity field falls
// into the documented `low` bucket; the response never carries an
// empty-string key.
// spec:
func TestPreviewScope_EmptySensitivityBucketsAsLow(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "t", ArtifactID: "unset", Version: "1.0.0",
		ContentHash: "sha256:u", Type: "skill", Layer: "L", // no Sensitivity
	})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "t", ArtifactID: "high", Version: "1.0.0",
		ContentHash: "sha256:h", Type: "skill", Sensitivity: "high", Layer: "L",
	})
	reg := core.New(st, "t", []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}}})

	preview, err := reg.PreviewScope(context.Background(), publicID)
	if err != nil {
		t.Fatalf("PreviewScope: %v", err)
	}
	if _, ok := preview.BySensitivity[""]; ok {
		t.Errorf("by_sensitivity carries an empty-string bucket: %v", preview.BySensitivity)
	}
	if preview.BySensitivity["low"] != 1 {
		t.Errorf("BySensitivity[low] = %d, want 1 (unset sensitivity floors to low)", preview.BySensitivity["low"])
	}
	if preview.BySensitivity["high"] != 1 {
		t.Errorf("BySensitivity[high] = %d, want 1", preview.BySensitivity["high"])
	}
}

// Spec: §3.5 — the endpoint is gated by tenant config expose_scope_preview
// (default true). A tenant with the flag set false yields
// ErrScopePreviewDisabled; an unset flag and a missing tenant both stay
// enabled.
// spec:
func TestPreviewScope_TenantGate(t *testing.T) {
	t.Parallel()
	ptr := func(b bool) *bool { return &b }
	mk := func(t *testing.T, flag *bool, createTenant bool) *core.Registry {
		t.Helper()
		st := store.NewMemory()
		if createTenant {
			if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t", ExposeScopePreview: flag}); err != nil {
				t.Fatalf("CreateTenant: %v", err)
			}
		}
		_ = st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: "t", ArtifactID: "a", Version: "1.0.0",
			ContentHash: "sha256:a", Type: "skill", Layer: "L",
		})
		return core.New(st, "t", []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}}})
	}

	t.Run("disabled returns ErrScopePreviewDisabled", func(t *testing.T) {
		t.Parallel()
		_, err := mk(t, ptr(false), true).PreviewScope(context.Background(), publicID)
		if !errors.Is(err, core.ErrScopePreviewDisabled) {
			t.Fatalf("err = %v, want ErrScopePreviewDisabled", err)
		}
	})
	t.Run("explicit true is enabled", func(t *testing.T) {
		t.Parallel()
		p, err := mk(t, ptr(true), true).PreviewScope(context.Background(), publicID)
		if err != nil {
			t.Fatalf("PreviewScope: %v", err)
		}
		if p.ArtifactCount != 1 {
			t.Errorf("ArtifactCount = %d, want 1", p.ArtifactCount)
		}
	})
	t.Run("unset flag defaults enabled", func(t *testing.T) {
		t.Parallel()
		if _, err := mk(t, nil, true).PreviewScope(context.Background(), publicID); err != nil {
			t.Fatalf("PreviewScope: %v", err)
		}
	})
	t.Run("missing tenant defaults enabled", func(t *testing.T) {
		t.Parallel()
		if _, err := mk(t, nil, false).PreviewScope(context.Background(), publicID); err != nil {
			t.Fatalf("PreviewScope: %v", err)
		}
	})
}
