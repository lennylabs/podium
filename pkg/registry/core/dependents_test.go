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
		TenantID: "t", ArtifactID: "parent", Version: "1.0.0",
		ContentHash: "sha256:p", Type: "agent", Layer: "public-layer",
	})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "t", ArtifactID: "child", Version: "1.0.0",
		ContentHash: "sha256:c", Type: "agent", Layer: "public-layer",
		ExtendsPin: "parent@1.0.0",
	})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "t", ArtifactID: "secret-child", Version: "1.0.0",
		ContentHash: "sha256:s", Type: "agent", Layer: "secret-layer",
		ExtendsPin: "parent@1.0.0",
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

// depFixture seeds a Memory store with manifests and edges and returns a
// registry over the given layers. Each manifest's TenantID is set to "t".
func depFixture(t *testing.T, layers []layer.Layer, recs []store.ManifestRecord, edges []store.DependencyEdge) *core.Registry {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, rec := range recs {
		rec.TenantID = "t"
		if rec.ContentHash == "" {
			rec.ContentHash = "sha256:" + rec.ArtifactID + "@" + rec.Version
		}
		if rec.Type == "" {
			rec.Type = "agent"
		}
		if err := st.PutManifest(ctx, rec); err != nil {
			t.Fatalf("PutManifest(%s@%s): %v", rec.ArtifactID, rec.Version, err)
		}
	}
	for _, e := range edges {
		if err := st.PutDependency(ctx, "t", e); err != nil {
			t.Fatalf("PutDependency(%s->%s): %v", e.From, e.To, err)
		}
	}
	return core.New(st, "t", layers)
}

// depLayers is a public "open" layer and a "hidden" layer only bob reads.
var depLayers = []layer.Layer{
	{ID: "open", Visibility: layer.Visibility{Public: true}, Precedence: 2},
	{ID: "hidden", Visibility: layer.Visibility{Users: []string{"bob"}}, Precedence: 1},
}

var (
	depAlice = layer.Identity{Sub: "alice", IsAuthenticated: true}
	depBob   = layer.Identity{Sub: "bob", IsAuthenticated: true}
)

func depQuery(t *testing.T, reg *core.Registry, id layer.Identity, target string) []core.DependentsEdge {
	t.Helper()
	edges, err := reg.DependentsOf(context.Background(), id, target)
	if err != nil {
		t.Fatalf("DependentsOf(%s): %v", target, err)
	}
	return edges
}

// Spec: §4.7.3 visibility — a query against a target the caller cannot see
// returns no edges, even when the dependent itself is visible.
func TestDependentsOf_InvisibleTargetYieldsNoEdges(t *testing.T) {
	t.Parallel()
	reg := depFixture(t, depLayers, []store.ManifestRecord{
		{ArtifactID: "acme/hidden-parent", Version: "1.0.0", Layer: "hidden"},
		{ArtifactID: "acme/child", Version: "1.0.0", Layer: "open", ExtendsPin: "acme/hidden-parent@1.0.0"},
	}, []store.DependencyEdge{{From: "acme/child", To: "acme/hidden-parent", Kind: "extends"}})
	if got := depQuery(t, reg, depAlice, "acme/hidden-parent"); len(got) != 0 {
		t.Errorf("alice sees %v, want no edges for an invisible target", got)
	}
	if got := depQuery(t, reg, depBob, "acme/hidden-parent"); len(got) != 1 {
		t.Errorf("bob sees %v, want the one edge", got)
	}
}

// Spec: §4.7.3 visibility — an invisible target is indistinguishable from a
// target that does not exist and from one nothing depends on.
func TestDependentsOf_InvisibleTargetMatchesNonexistentTarget(t *testing.T) {
	t.Parallel()
	reg := depFixture(t, depLayers, []store.ManifestRecord{
		{ArtifactID: "acme/hidden-parent", Version: "1.0.0", Layer: "hidden"},
		{ArtifactID: "acme/child", Version: "1.0.0", Layer: "open", ExtendsPin: "acme/hidden-parent@1.0.0"},
		{ArtifactID: "acme/lonely", Version: "1.0.0", Layer: "open"},
	}, []store.DependencyEdge{{From: "acme/child", To: "acme/hidden-parent", Kind: "extends"}})
	invisible := depQuery(t, reg, depAlice, "acme/hidden-parent")
	missing := depQuery(t, reg, depAlice, "acme/does-not-exist")
	lonely := depQuery(t, reg, depAlice, "acme/lonely")
	if !reflect.DeepEqual(invisible, missing) || !reflect.DeepEqual(invisible, lonely) {
		t.Errorf("invisible=%#v missing=%#v lonely=%#v, want identical results", invisible, missing, lonely)
	}
}

// Spec: §4.7.3 visibility, §4.6 — a same-ID overlay records the self-edge
// {X -> X}. Both endpoints are visible by ID to a caller who reads only the
// overlaying layer, so the edge is dropped by the pinned-parent test.
func TestDependentsOf_SameIDOverlayHidesLowerLayer(t *testing.T) {
	t.Parallel()
	reg := depFixture(t, depLayers, []store.ManifestRecord{
		{ArtifactID: "team/overlay", Version: "1.0.0", Layer: "hidden"},
		{ArtifactID: "team/overlay", Version: "2.0.0", Layer: "open", ExtendsPin: "team/overlay@1.0.0"},
	}, []store.DependencyEdge{{From: "team/overlay", To: "team/overlay", Kind: "extends"}})
	if got := depQuery(t, reg, depAlice, "team/overlay"); len(got) != 0 {
		t.Errorf("alice sees %v, want no self-edge for an unreadable lower layer", got)
	}
}

// Spec: §4.7.3 visibility, §4.6 — a caller who reads both layers of a
// same-ID overlay sees the self-edge. The asymmetry with the single-layer
// caller is the rule, and it requires the pin lookup.
func TestDependentsOf_SameIDOverlayVisibleToBoth(t *testing.T) {
	t.Parallel()
	reg := depFixture(t, depLayers, []store.ManifestRecord{
		{ArtifactID: "team/overlay", Version: "1.0.0", Layer: "hidden"},
		{ArtifactID: "team/overlay", Version: "2.0.0", Layer: "open", ExtendsPin: "team/overlay@1.0.0"},
	}, []store.DependencyEdge{{From: "team/overlay", To: "team/overlay", Kind: "extends"}})
	got := depQuery(t, reg, depBob, "team/overlay")
	want := []core.DependentsEdge{{From: "team/overlay", To: "team/overlay", Kind: "extends"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bob sees %v, want %v", got, want)
	}
}

// Spec: §4.7.3 visibility — the endpoint rule is edge-kind-agnostic: a
// delegates_to edge to an invisible target is dropped, and one to a visible
// target is kept without any pin.
func TestDependentsOf_DelegatesToInvisibleTargetDropped(t *testing.T) {
	t.Parallel()
	reg := depFixture(t, depLayers, []store.ManifestRecord{
		{ArtifactID: "acme/hidden-worker", Version: "1.0.0", Layer: "hidden"},
		{ArtifactID: "acme/open-worker", Version: "1.0.0", Layer: "open"},
		{ArtifactID: "acme/lead", Version: "1.0.0", Layer: "open"},
	}, []store.DependencyEdge{
		{From: "acme/lead", To: "acme/hidden-worker", Kind: "delegates_to"},
		{From: "acme/lead", To: "acme/open-worker", Kind: "delegates_to"},
	})
	if got := depQuery(t, reg, depAlice, "acme/hidden-worker"); len(got) != 0 {
		t.Errorf("alice sees %v, want no edge to an invisible delegate", got)
	}
	if got := depQuery(t, reg, depAlice, "acme/open-worker"); len(got) != 1 {
		t.Errorf("alice sees %v, want the visible delegates_to edge", got)
	}
}

// Spec: §4.7.3 visibility — an extends edge whose visible child records all
// carry an empty ExtendsPin names no parent record to test, so it is
// dropped rather than admitted by the ID test.
func TestDependentsOf_UnpinnedExtendsEdgeDropped(t *testing.T) {
	t.Parallel()
	reg := depFixture(t, depLayers, []store.ManifestRecord{
		{ArtifactID: "acme/hidden-parent", Version: "1.0.0", Layer: "open"},
		{
			ArtifactID: "acme/child", Version: "1.0.0", Layer: "open",
			Frontmatter: []byte("type: agent\nextends: acme/hidden-parent\n"),
		},
	}, []store.DependencyEdge{{From: "acme/child", To: "acme/hidden-parent", Kind: "extends"}})
	if got := depQuery(t, reg, depBob, "acme/hidden-parent"); len(got) != 0 {
		t.Errorf("bob sees %v, want the unpinned extends edge dropped", got)
	}
}

// Spec: §4.7.3 — a pin naming a different parent than the edge target does
// not admit the edge.
func TestDependentsOf_PinToOtherParentDropped(t *testing.T) {
	t.Parallel()
	reg := depFixture(t, depLayers, []store.ManifestRecord{
		{ArtifactID: "acme/a", Version: "1.0.0", Layer: "open"},
		{ArtifactID: "acme/b", Version: "1.0.0", Layer: "open"},
		{ArtifactID: "acme/child", Version: "1.0.0", Layer: "open", ExtendsPin: "acme/a@1.0.0"},
	}, []store.DependencyEdge{{From: "acme/child", To: "acme/b", Kind: "extends"}})
	if got := depQuery(t, reg, depBob, "acme/b"); len(got) != 0 {
		t.Errorf("got %v, want no edge when the pin names another parent", got)
	}
}

// Spec: §4.7.3 — a store failure while resolving the caller's visible set
// propagates rather than yielding an unfiltered or empty list.
func TestDependentsOf_StoreErrorPropagates(t *testing.T) {
	t.Parallel()
	reg := core.New(failingStore{Store: store.NewMemory()}, "t", depLayers)
	if _, err := reg.DependentsOf(context.Background(), depBob, "acme/x"); !errors.Is(err, core.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}
