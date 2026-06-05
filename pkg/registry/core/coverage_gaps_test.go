package core

// White-box coverage for undertested helpers in this package. These tests
// reach the unexported functions directly, so they live in package core
// rather than core_test. They construct the in-memory store and synthetic
// embedders the rest of the suite uses; no network, Docker, or live backend
// is involved.

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sort"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// --- shared white-box fakes ---------------------------------------------

// gapEmbedder is a deterministic local embedder. It returns one fixed
// vector per input text unless err is set, in which case Embed fails. count
// controls how many vectors it returns so a caller can force the
// "expected 1 vector" guard in upsertVector.
type gapEmbedder struct {
	dim   int
	err   error
	count int // 0 means one vector per text (the normal contract)
	calls int
}

func (*gapEmbedder) ID() string        { return "fake" }
func (*gapEmbedder) Model() string     { return "fake-model" }
func (e *gapEmbedder) Dimensions() int { return e.dim }
func (e *gapEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	e.calls++
	if e.err != nil {
		return nil, e.err
	}
	n := len(texts)
	if e.count != 0 {
		n = e.count
	}
	out := make([][]float32, n)
	for i := range out {
		out[i] = make([]float32, e.dim)
		if e.dim > 0 {
			out[i][0] = 1
		}
	}
	return out, nil
}

// plainVec is a vector backend that is neither model-versioned nor
// self-embedding. It records the rows written through Put so upsertVector's
// precomputed-vector branch (the non-ModelVersioned, non-self-embed path)
// can be asserted. vector.Memory implements ModelVersioned, so it cannot
// exercise that branch.
type plainVec struct {
	puts map[string][]float32
	err  error
}

func newPlainVec() *plainVec { return &plainVec{puts: map[string][]float32{}} }

func (*plainVec) ID() string      { return "plain" }
func (*plainVec) Dimensions() int { return 0 }
func (p *plainVec) Put(_ context.Context, _, id, ver string, vec []float32) error {
	if p.err != nil {
		return p.err
	}
	p.puts[id+"@"+ver] = vec
	return nil
}
func (*plainVec) Query(context.Context, string, []float32, int) ([]vector.Match, error) {
	return nil, nil
}
func (*plainVec) Delete(context.Context, string, string, string) error { return nil }
func (*plainVec) Close() error                                         { return nil }

// gapSelfEmbed is a §13.12 self-embedding backend for the white-box suite.
// It stores raw text and answers via the TextVectorizer methods; the
// precomputed-vector Put/Query paths fail loudly so a test catches the
// registry taking the wrong branch.
type gapSelfEmbed struct {
	texts map[string]string
	err   error
}

func newGapSelfEmbed() *gapSelfEmbed { return &gapSelfEmbed{texts: map[string]string{}} }

func (*gapSelfEmbed) ID() string       { return "qdrant" }
func (*gapSelfEmbed) Dimensions() int  { return 0 }
func (*gapSelfEmbed) SelfEmbeds() bool { return true }
func (s *gapSelfEmbed) PutText(_ context.Context, _, id, ver, text string) error {
	if s.err != nil {
		return s.err
	}
	s.texts[id+"@"+ver] = text
	return nil
}
func (*gapSelfEmbed) QueryText(context.Context, string, string, int) ([]vector.Match, error) {
	return nil, nil
}
func (*gapSelfEmbed) Put(context.Context, string, string, string, []float32) error {
	panic("Put called on a self-embedding backend; expected PutText")
}
func (*gapSelfEmbed) Query(context.Context, string, []float32, int) ([]vector.Match, error) {
	panic("Query called on a self-embedding backend; expected QueryText")
}
func (*gapSelfEmbed) Delete(context.Context, string, string, string) error { return nil }
func (*gapSelfEmbed) Close() error                                         { return nil }

var (
	_ vector.Provider       = (*plainVec)(nil)
	_ vector.Provider       = (*gapSelfEmbed)(nil)
	_ vector.TextVectorizer = (*gapSelfEmbed)(nil)
)

// gapFailingStore overrides the two reads these tests force into their error
// path: GetTenant (a non-not-found failure must surface as ErrUnavailable in
// PreviewScope) and DependencyInDegree (a failure must be swallowed by
// dependencyRanking). Everything else delegates to the embedded Memory.
type gapFailingStore struct {
	store.Store
	tenantErr   error
	inDegreeErr error
}

func (s gapFailingStore) GetTenant(ctx context.Context, id string) (store.Tenant, error) {
	if s.tenantErr != nil {
		return store.Tenant{}, s.tenantErr
	}
	return s.Store.GetTenant(ctx, id)
}

func (s gapFailingStore) DependencyInDegree(ctx context.Context, tenantID string) (map[string]int, error) {
	if s.inDegreeErr != nil {
		return nil, s.inDegreeErr
	}
	return s.Store.DependencyInDegree(ctx, tenantID)
}

func gapMemStore(t *testing.T) *store.Memory {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	return st
}

// --- composeEmbeddingText (reembed.go) ----------------------------------

// Spec: §4.7 "Artifact embeddings" — the projection joins name, description,
// when_to_use, and tags with newlines. Empty components are dropped so the
// input never carries stray separators, and the prose body is excluded.
func TestComposeEmbeddingText_Projection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		rec  store.ManifestRecord
		want string
	}{
		{
			name: "name and description only",
			rec:  store.ManifestRecord{Name: "Pay Invoice", Description: "settles an AP invoice", Body: []byte("ignored prose")},
			want: "Pay Invoice\nsettles an AP invoice",
		},
		{
			name: "when_to_use joined with newlines",
			rec:  store.ManifestRecord{Name: "n", Description: "d", WhenToUse: []string{"first", "second"}},
			want: "n\nd\nfirst\nsecond",
		},
		{
			name: "tags joined with spaces",
			rec:  store.ManifestRecord{Name: "n", Description: "d", Tags: []string{"finance", "ap"}},
			want: "n\nd\nfinance ap",
		},
		{
			name: "all components present",
			rec:  store.ManifestRecord{Name: "n", Description: "d", WhenToUse: []string{"w"}, Tags: []string{"t"}},
			want: "n\nd\nw\nt",
		},
		{
			name: "empty name skipped, no leading separator",
			rec:  store.ManifestRecord{Description: "only desc"},
			want: "only desc",
		},
		{
			name: "interior empty when_to_use entries dropped",
			rec:  store.ManifestRecord{Name: "n", Description: "d", WhenToUse: []string{"", "kept", ""}},
			want: "n\nd\nkept",
		},
		{
			name: "fully empty record yields empty string",
			rec:  store.ManifestRecord{Body: []byte("body is never embedded")},
			want: "",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := composeEmbeddingText(c.rec); got != c.want {
				t.Errorf("composeEmbeddingText() = %q, want %q", got, c.want)
			}
		})
	}
}

// --- max1 / logf (core.go BM25 helpers) ---------------------------------

// max1 floors its argument at 1 so a BM25 denominator never reaches zero.
func TestMax1_FloorsAtOne(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want int }{
		{-5, 1},
		{0, 1},
		{1, 1},
		{2, 2},
		{1000, 1000},
	}
	for _, c := range cases {
		if got := max1(c.in); got != c.want {
			t.Errorf("max1(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// logf returns 0 for non-positive input (guarding the BM25 IDF term) and the
// natural log otherwise.
func TestLogf_NonPositiveIsZeroElseNaturalLog(t *testing.T) {
	t.Parallel()
	if got := logf(0); got != 0 {
		t.Errorf("logf(0) = %v, want 0", got)
	}
	if got := logf(-3); got != 0 {
		t.Errorf("logf(-3) = %v, want 0", got)
	}
	for _, x := range []float64{1, math.E, 10, 1234.5} {
		if got, want := logf(x), math.Log(x); math.Abs(got-want) > 1e-12 {
			t.Errorf("logf(%v) = %v, want %v", x, got, want)
		}
	}
}

// --- visibilityReason (admin.go) ----------------------------------------

// Spec: §4.6 / §4.7.2 — visibilityReason returns a stable one-liner per
// switch arm of the visibility outcome. Each case below selects one arm.
func TestVisibilityReason_Arms(t *testing.T) {
	t.Parallel()
	authed := layer.Identity{Sub: "alice", IsAuthenticated: true}
	anon := layer.Identity{IsPublic: true}
	cases := []struct {
		name    string
		l       layer.Layer
		id      layer.Identity
		visible bool
		want    string
	}{
		{
			name:    "public layer",
			l:       layer.Layer{Visibility: layer.Visibility{Public: true}},
			id:      anon,
			visible: true,
			want:    "layer.public=true",
		},
		{
			name:    "anonymous caller against a non-public layer",
			l:       layer.Layer{Visibility: layer.Visibility{Organization: true}},
			id:      anon,
			visible: false,
			want:    "caller is anonymous; layer requires authentication",
		},
		{
			name:    "organization layer with an authenticated identity",
			l:       layer.Layer{Visibility: layer.Visibility{Organization: true}},
			id:      authed,
			visible: true,
			want:    "layer.organization=true and identity is authenticated",
		},
		{
			name:    "visible via users or groups",
			l:       layer.Layer{Visibility: layer.Visibility{Users: []string{"alice"}}},
			id:      authed,
			visible: true,
			want:    "user matches layer.users or layer.groups",
		},
		{
			name:    "authenticated but not in users or groups",
			l:       layer.Layer{Visibility: layer.Visibility{Users: []string{"bob"}}},
			id:      authed,
			visible: false,
			want:    "user is not in layer.users or layer.groups",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := visibilityReason(c.l, c.id, c.visible); got != c.want {
				t.Errorf("visibilityReason() = %q, want %q", got, c.want)
			}
		})
	}
}

// ShowEffective wires resolveLayers through visibilityReason for an
// end-to-end admin diagnostic. The reasons must match the per-layer outcome.
func TestShowEffective_ReasonsPerLayer(t *testing.T) {
	t.Parallel()
	st := gapMemStore(t)
	reg := New(st, "t", []layer.Layer{
		{ID: "open", Precedence: 1, Visibility: layer.Visibility{Public: true}},
		{ID: "team", Precedence: 2, Visibility: layer.Visibility{Users: []string{"alice"}}},
	})
	got, err := reg.ShowEffective(context.Background(), layer.Identity{Sub: "alice", IsAuthenticated: true})
	if err != nil {
		t.Fatalf("ShowEffective: %v", err)
	}
	byID := map[string]EffectiveLayer{}
	for _, e := range got {
		byID[e.LayerID] = e
	}
	if e := byID["open"]; !e.Visible || e.Reason != "layer.public=true" {
		t.Errorf("open layer = %+v, want visible with layer.public reason", e)
	}
	if e := byID["team"]; !e.Visible || e.Reason != "user matches layer.users or layer.groups" {
		t.Errorf("team layer = %+v, want visible via users", e)
	}
}

// --- dependencyRanking (dependents.go) ----------------------------------

// Spec: §4.7.3 — dependencyRanking orders candidate IDs by descending
// reverse-dependency in-degree, breaking ties by ascending ID. Artifacts
// with no dependents are omitted.
func TestDependencyRanking_OrdersByInDegreeTiesById(t *testing.T) {
	t.Parallel()
	st := gapMemStore(t)
	// in-degree: high=2, mid-a=1, mid-b=1, zero=0 (no edges).
	edges := []store.DependencyEdge{
		{From: "c1", To: "high", Kind: "extends"},
		{From: "c2", To: "high", Kind: "extends"},
		{From: "c1", To: "mid-a", Kind: "extends"},
		{From: "c1", To: "mid-b", Kind: "extends"},
	}
	for _, e := range edges {
		if err := st.PutDependency(context.Background(), "t", e); err != nil {
			t.Fatalf("PutDependency: %v", err)
		}
	}
	reg := New(st, "t", nil)
	got := reg.dependencyRanking(context.Background(), nil)
	// high first (degree 2), then the degree-1 pair in ascending-ID order.
	want := []string{"high", "mid-a", "mid-b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ranking = %v, want %v (degree desc, ties by id)", got, want)
	}
}

// The allowed set restricts the ranking to candidate IDs the caller may see;
// an in-degree entry outside the set is dropped before sorting.
func TestDependencyRanking_RespectsAllowedSet(t *testing.T) {
	t.Parallel()
	st := gapMemStore(t)
	for _, e := range []store.DependencyEdge{
		{From: "c1", To: "visible", Kind: "extends"},
		{From: "c1", To: "hidden", Kind: "extends"},
	} {
		if err := st.PutDependency(context.Background(), "t", e); err != nil {
			t.Fatalf("PutDependency: %v", err)
		}
	}
	reg := New(st, "t", nil)
	got := reg.dependencyRanking(context.Background(), map[string]bool{"visible": true})
	if !reflect.DeepEqual(got, []string{"visible"}) {
		t.Errorf("ranking = %v, want only [visible] (hidden excluded by allowed set)", got)
	}
}

// No edges means an empty in-degree map, so the ranking is the cheap nil skip.
func TestDependencyRanking_NoDependentsReturnsNil(t *testing.T) {
	t.Parallel()
	reg := New(gapMemStore(t), "t", nil)
	if got := reg.dependencyRanking(context.Background(), nil); got != nil {
		t.Errorf("ranking = %v, want nil when nothing has dependents", got)
	}
}

// Every candidate has dependents, but none is in the allowed set: the post-
// filter set is empty, so the ranking is nil rather than an empty slice.
func TestDependencyRanking_AllowedExcludesEverythingReturnsNil(t *testing.T) {
	t.Parallel()
	st := gapMemStore(t)
	if err := st.PutDependency(context.Background(), "t", store.DependencyEdge{
		From: "c1", To: "only", Kind: "extends",
	}); err != nil {
		t.Fatalf("PutDependency: %v", err)
	}
	reg := New(st, "t", nil)
	if got := reg.dependencyRanking(context.Background(), map[string]bool{"other": true}); got != nil {
		t.Errorf("ranking = %v, want nil when allowed excludes every candidate", got)
	}
}

// Spec: §4.7.3 — the in-degree lookup is best-effort. A store failure must
// not fail the search; dependencyRanking swallows it and returns nil.
func TestDependencyRanking_StoreErrorReturnsNil(t *testing.T) {
	t.Parallel()
	st := gapFailingStore{Store: gapMemStore(t), inDegreeErr: errors.New("simulated db outage")}
	reg := New(st, "t", nil)
	if got := reg.dependencyRanking(context.Background(), nil); got != nil {
		t.Errorf("ranking = %v, want nil on store error", got)
	}
}

// --- resolveImports (core.go) -------------------------------------------

// Spec: §4.5.2 / §12 — resolveImports expands include/exclude globs against
// the visible artifact-ID snapshot. The cached path (a Registry built with
// New) and the uncached fallback (importCache == nil) return identical
// results.
func TestResolveImports_CachedAndUncachedAgree(t *testing.T) {
	t.Parallel()
	ids := []string{"finance/ap/pay", "finance/ar/bill", "finance/ap/secret", "ops/runbook"}
	include := []string{"finance/**"}
	exclude := []string{"finance/ap/secret"}
	want := []string{"finance/ap/pay", "finance/ar/bill"}

	cached := New(store.NewMemory(), "t", nil) // New wires importCache.
	if got := cached.resolveImports(include, exclude, ids); !reflect.DeepEqual(got, want) {
		t.Errorf("cached resolveImports = %v, want %v", got, want)
	}

	// A Registry built without New has a nil importCache and takes the
	// uncached branch. The result must match the cached path.
	uncached := &Registry{store: store.NewMemory(), tenantID: "t"}
	if uncached.importCache != nil {
		t.Fatal("expected nil importCache on a bare Registry")
	}
	if got := uncached.resolveImports(include, exclude, ids); !reflect.DeepEqual(got, want) {
		t.Errorf("uncached resolveImports = %v, want %v", got, want)
	}
}

// An empty include set yields no imports on both paths.
func TestResolveImports_EmptyIncludeNoImports(t *testing.T) {
	t.Parallel()
	ids := []string{"a/x", "b/y"}
	cached := New(store.NewMemory(), "t", nil)
	if got := cached.resolveImports(nil, []string{"a/**"}, ids); len(got) != 0 {
		t.Errorf("cached empty include = %v, want none", got)
	}
	uncached := &Registry{store: store.NewMemory(), tenantID: "t"}
	if got := uncached.resolveImports(nil, []string{"a/**"}, ids); len(got) != 0 {
		t.Errorf("uncached empty include = %v, want none", got)
	}
}

// --- upsertVector (core.go) ---------------------------------------------

// Spec: §4.7 — an empty embedding text is a no-op: no backend call, no error.
func TestUpsertVector_EmptyTextIsNoOp(t *testing.T) {
	t.Parallel()
	pv := newPlainVec()
	e := &gapEmbedder{dim: 8}
	reg := New(store.NewMemory(), "t", nil).WithVectorSearch(pv, e)
	if err := reg.upsertVector(context.Background(), "t", "a", "1.0.0", ""); err != nil {
		t.Fatalf("upsertVector: %v", err)
	}
	if e.calls != 0 {
		t.Errorf("embedder called %d times for empty text, want 0", e.calls)
	}
	if len(pv.puts) != 0 {
		t.Errorf("vector written for empty text: %v", pv.puts)
	}
}

// Spec: §13.12 — with a self-embedding backend and a nil embedder, the text
// is sent verbatim through PutText and no local embedding runs.
func TestUpsertVector_SelfEmbedRoutesText(t *testing.T) {
	t.Parallel()
	se := newGapSelfEmbed()
	reg := New(store.NewMemory(), "t", nil).WithVectorSearch(se, nil)
	if err := reg.upsertVector(context.Background(), "t", "a", "1.0.0", "raw text"); err != nil {
		t.Fatalf("upsertVector: %v", err)
	}
	if se.texts["a@1.0.0"] != "raw text" {
		t.Errorf("PutText stored %q, want %q", se.texts["a@1.0.0"], "raw text")
	}
}

// A self-embed PutText failure is wrapped and returned.
func TestUpsertVector_SelfEmbedErrorPropagates(t *testing.T) {
	t.Parallel()
	se := newGapSelfEmbed()
	se.err = errors.New("inference down")
	reg := New(store.NewMemory(), "t", nil).WithVectorSearch(se, nil)
	if err := reg.upsertVector(context.Background(), "t", "a", "1.0.0", "x"); err == nil {
		t.Error("expected error when PutText fails")
	}
}

// Spec: §4.7 — a precomputed-vector backend that is not model-versioned takes
// the plain Put branch; the locally embedded vector lands under the row key.
func TestUpsertVector_PrecomputedPutBranch(t *testing.T) {
	t.Parallel()
	pv := newPlainVec()
	e := &gapEmbedder{dim: 8}
	reg := New(store.NewMemory(), "t", nil).WithVectorSearch(pv, e)
	if err := reg.upsertVector(context.Background(), "t", "a", "1.0.0", "text"); err != nil {
		t.Fatalf("upsertVector: %v", err)
	}
	if _, ok := pv.puts["a@1.0.0"]; !ok {
		t.Errorf("Put did not store the row; have %v", pv.puts)
	}
	if e.calls != 1 {
		t.Errorf("embedder calls = %d, want 1", e.calls)
	}
}

// Spec: §4.7 — a model-versioned backend (vector.Memory) takes the PutModel
// branch and tags the row with the embedding model.
func TestUpsertVector_ModelVersionedPutBranch(t *testing.T) {
	t.Parallel()
	v := vector.NewMemory(8)
	e := &gapEmbedder{dim: 8}
	reg := New(store.NewMemory(), "t", nil).WithVectorSearch(v, e)
	if err := reg.upsertVector(context.Background(), "t", "a", "1.0.0", "text"); err != nil {
		t.Fatalf("upsertVector: %v", err)
	}
	probe := make([]float32, 8)
	probe[0] = 1
	// The model-versioned query restricted to the embedder's model must find
	// the row, confirming PutModel tagged it.
	matches, err := v.QueryModel(context.Background(), "t", probe, 5, e.Model())
	if err != nil {
		t.Fatalf("QueryModel: %v", err)
	}
	found := false
	for _, m := range matches {
		if m.ArtifactID == "a" && m.Version == "1.0.0" {
			found = true
		}
	}
	if !found {
		t.Errorf("row not found under model %q; matches=%v", e.Model(), matches)
	}
}

// An embed failure on the local path is wrapped and returned.
func TestUpsertVector_EmbedErrorPropagates(t *testing.T) {
	t.Parallel()
	e := &gapEmbedder{dim: 8, err: errors.New("embedder unavailable")}
	reg := New(store.NewMemory(), "t", nil).WithVectorSearch(newPlainVec(), e)
	if err := reg.upsertVector(context.Background(), "t", "a", "1.0.0", "text"); err == nil {
		t.Error("expected error when embedder fails")
	}
}

// A backend contract violation (more than one vector for one text) is
// rejected before any write.
func TestUpsertVector_WrongVectorCountRejected(t *testing.T) {
	t.Parallel()
	e := &gapEmbedder{dim: 8, count: 2} // returns 2 vectors for 1 text
	pv := newPlainVec()
	reg := New(store.NewMemory(), "t", nil).WithVectorSearch(pv, e)
	if err := reg.upsertVector(context.Background(), "t", "a", "1.0.0", "text"); err == nil {
		t.Error("expected error when embedder returns the wrong vector count")
	}
	if len(pv.puts) != 0 {
		t.Errorf("vector written despite count mismatch: %v", pv.puts)
	}
}

// --- PreviewScope (dependents.go) ---------------------------------------

// Spec: §3.5 / §6.10 — a store failure on the tenant gate lookup surfaces as
// ErrUnavailable, which the HTTP layer maps to registry.unavailable rather
// than reporting the preview as disabled.
func TestPreviewScope_TenantLookupFailureMapsToUnavailable(t *testing.T) {
	t.Parallel()
	st := gapFailingStore{Store: gapMemStore(t), tenantErr: errors.New("simulated db outage")}
	reg := New(st, "t", []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}}})
	_, err := reg.PreviewScope(context.Background(), layer.Identity{IsPublic: true})
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("got %v, want ErrUnavailable", err)
	}
}

// Spec: §3.5 / §4.6 — with no layer config (a filesystem-source registry or a
// bare core), `layers` is derived from the distinct non-empty layers present
// among visible records and sorted for a stable response.
func TestPreviewScope_NoLayerConfigDerivesLayersFromRecords(t *testing.T) {
	t.Parallel()
	st := gapMemStore(t)
	// Records carry layer labels but the Registry is built with no layer list.
	for _, m := range []store.ManifestRecord{
		{TenantID: "t", ArtifactID: "a", Version: "1.0.0", ContentHash: "sha256:a", Type: "skill", Layer: "zeta"},
		{TenantID: "t", ArtifactID: "b", Version: "1.0.0", ContentHash: "sha256:b", Type: "agent", Layer: "alpha"},
		{TenantID: "t", ArtifactID: "c", Version: "1.0.0", ContentHash: "sha256:c", Type: "skill", Layer: ""},
	} {
		if err := st.PutManifest(context.Background(), m); err != nil {
			t.Fatalf("PutManifest: %v", err)
		}
	}
	reg := New(st, "t", nil) // no layers configured

	preview, err := reg.PreviewScope(context.Background(), layer.Identity{IsPublic: true})
	if err != nil {
		t.Fatalf("PreviewScope: %v", err)
	}
	if preview.ArtifactCount != 3 {
		t.Errorf("ArtifactCount = %d, want 3", preview.ArtifactCount)
	}
	// Distinct non-empty layers, sorted; the empty-layer record contributes
	// no key.
	want := []string{"alpha", "zeta"}
	got := append([]string(nil), preview.Layers...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Layers = %v, want %v (distinct non-empty, sorted)", preview.Layers, want)
	}
}
