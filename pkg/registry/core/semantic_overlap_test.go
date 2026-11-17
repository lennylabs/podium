package core

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// overlapNow is the frozen time the overlap tests stamp records with.
var overlapNow = time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)

// overlapEmbedder maps a projection to the vector of the first table key the
// text contains, so a test fixes each artifact's position by a word in its
// description.
type overlapEmbedder struct {
	table map[string][]float32
	model string
}

func (overlapEmbedder) ID() string      { return "overlap" }
func (e overlapEmbedder) Model() string { return e.model }
func (overlapEmbedder) Dimensions() int { return 4 }
func (e overlapEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = []float32{0, 0, 0, 1}
		for k, v := range e.table {
			if strings.Contains(text, k) {
				out[i] = v
			}
		}
	}
	return out, nil
}

// overlapVectors positions the words the fixtures use. alpha and bravo are
// close (cosine about 0.99), alpha and charlie are far (about 0.45).
var overlapVectors = map[string][]float32{
	"alpha":   {1, 0, 0, 0},
	"bravo":   {0.99, 0.12, 0, 0},
	"charlie": {0.45, 0.89, 0, 0},
	"delta":   {0.98, 0, 0.2, 0},
}

// failingIndex is the memory backend with a query that fails on demand.
type failingIndex struct {
	*vector.Memory
	fail bool
}

func (f *failingIndex) QueryModel(ctx context.Context, tenant string, vec []float32, k int, model string) ([]vector.Match, error) {
	if f.fail {
		return nil, errors.New("index unreachable")
	}
	return f.Memory.QueryModel(ctx, tenant, vec, k, model)
}

type overlapFixture struct {
	t   *testing.T
	st  *store.Memory
	idx *failingIndex
	reg *Registry
	ctx context.Context
}

func newOverlapFixture(t *testing.T, layers []layer.Layer) *overlapFixture {
	t.Helper()
	st := store.NewMemory()
	ctx := ContextWithTenant(context.Background(), "t")
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	idx := &failingIndex{Memory: vector.NewMemory(4)}
	reg := New(st, "t", layers).
		WithVectorSearch(idx, overlapEmbedder{table: overlapVectors, model: "m1"}).
		WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{Enabled: true})
	reg.overlap.clock = clock.NewFrozen(overlapNow)
	return &overlapFixture{t: t, st: st, idx: idx, reg: reg, ctx: ctx}
}

// put stores a manifest and, unless embed is false, its vector.
func (f *overlapFixture) put(id, ver, lay, desc, extraFrontmatter string, embed bool) store.ManifestRecord {
	f.t.Helper()
	m := store.ManifestRecord{
		TenantID: "t", ArtifactID: id, Version: ver, ContentHash: "sha256:" + id + ver,
		Type: "agent", Description: desc, Layer: lay, IngestedAt: overlapNow,
		Frontmatter: []byte("---\ntype: agent\nversion: " + ver + "\ndescription: " + desc + "\n" + extraFrontmatter + "---\n"),
	}
	if err := f.st.PutManifest(f.ctx, m); err != nil {
		f.t.Fatalf("PutManifest %s: %v", id, err)
	}
	if embed {
		if err := f.reg.embedAndUpsert(f.ctx, m); err != nil {
			f.t.Fatalf("embed %s: %v", id, err)
		}
	}
	return m
}

func (f *overlapFixture) records() []store.OverlapRecord {
	f.t.Helper()
	recs, err := f.st.ListOverlapRecords(f.ctx, "t")
	if err != nil {
		f.t.Fatalf("ListOverlapRecords: %v", err)
	}
	return recs
}

func publicLayer(id string) layer.Layer {
	return layer.Layer{ID: id, Visibility: layer.Visibility{Public: true}}
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i] * b[i])
		na += float64(a[i] * a[i])
		nb += float64(b[i] * b[i])
	}
	return dot / math.Sqrt(na*nb)
}

// Spec: §7.3.1 — computing an artifact stores one record for each nearest
// row that is the current served version of another artifact, whatever its
// similarity, with both versions, the cosine similarity, the model, and the
// time. The artifact itself and a non-served version are never peers.
func TestComputeOverlap_StoresEveryServedNeighbour(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	f.put("a", "1.0.0", "L", "alpha", "", true)
	f.put("b", "1.0.0", "L", "bravo", "", true)
	f.put("b", "2.0.0", "L", "bravo", "", true)
	f.put("c", "1.0.0", "L", "charlie", "", true)
	res, err := f.reg.ComputeOverlap(f.ctx, []vector.RowKey{{ArtifactID: "a", Version: "1.0.0"}, {ArtifactID: "b", Version: "1.0.0"}})
	if err != nil || res != (OverlapComputeResult{Computed: 1}) {
		t.Fatalf("ComputeOverlap = %+v, %v; want a computed and b@1.0.0 skipped as not served", res, err)
	}
	recs := f.records()
	if len(recs) != 2 {
		t.Fatalf("records = %+v, want (a, b@2.0.0) and (a, c)", recs)
	}
	ab, ac := recs[0], recs[1]
	if ab.ArtifactB != "b" || ab.VersionB != "2.0.0" || ac.ArtifactB != "c" || ab.ModelID != "m1" || !ab.ComputedAt.Equal(overlapNow) {
		t.Errorf("records = %+v", recs)
	}
	if math.Abs(ab.Similarity-cosine(overlapVectors["alpha"], overlapVectors["bravo"])) > 1e-5 ||
		math.Abs(ac.Similarity-cosine(overlapVectors["alpha"], overlapVectors["charlie"])) > 1e-5 {
		t.Errorf("similarities = %v, %v; want the exact cosine", ab.Similarity, ac.Similarity)
	}
}

// Spec: §7.3.1 — a failed index query leaves the artifact's records in
// place, as do an artifact with no stored vector and one whose stored vector
// carries another model; each counts as a failure.
func TestComputeOverlap_FailureKeepsRecords(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	f.put("a", "1.0.0", "L", "alpha", "", true)
	f.put("b", "1.0.0", "L", "bravo", "", true)
	f.put("n", "1.0.0", "L", "novector", "", false)
	key := []vector.RowKey{{ArtifactID: "a", Version: "1.0.0"}}
	if _, err := f.reg.ComputeOverlap(f.ctx, key); err != nil {
		t.Fatalf("ComputeOverlap: %v", err)
	}
	before := f.records()
	f.idx.fail = true
	res, _ := f.reg.ComputeOverlap(f.ctx, key)
	f.idx.fail = false
	noVec, _ := f.reg.ComputeOverlap(f.ctx, []vector.RowKey{{ArtifactID: "n", Version: "1.0.0"}})
	if err := f.idx.PutModel(f.ctx, "t", "a", "1.0.0", overlapVectors["alpha"], "m0"); err != nil {
		t.Fatalf("PutModel: %v", err)
	}
	stale, _ := f.reg.ComputeOverlap(f.ctx, key)
	if res.Failed != 1 || noVec.Failed != 1 || stale.Failed != 1 || len(f.records()) != len(before) || len(before) != 1 {
		t.Errorf("failures = %+v %+v %+v, records %d before and %d after", res, noVec, stale, len(before), len(f.records()))
	}
}

// managedIndex is a storage-only managed backend fake: it returns stored
// vectors and reports a distance, and scores nothing itself.
type managedIndex struct {
	matches []vector.Match
}

func (managedIndex) ID() string      { return "pinecone" }
func (managedIndex) Dimensions() int { return 4 }
func (managedIndex) Put(context.Context, string, string, string, []float32) error {
	return nil
}
func (m managedIndex) Query(context.Context, string, []float32, int) ([]vector.Match, error) {
	return m.matches, nil
}
func (managedIndex) Delete(context.Context, string, string, string) error { return nil }
func (managedIndex) Close() error                                         { return nil }
func (managedIndex) StoredVector(context.Context, string, string, string) ([]float32, string, error) {
	return []float32{1, 0, 0, 0}, "", nil
}

// hostedIndex is a self-embedding backend fake that records the text it is
// queried with.
type hostedIndex struct {
	managedIndex
	queried []string
}

func (*hostedIndex) SelfEmbeds() bool    { return true }
func (*hostedIndex) HostedModel() string { return "hosted-1" }
func (*hostedIndex) PutText(context.Context, string, string, string, string) error {
	return nil
}
func (h *hostedIndex) QueryText(_ context.Context, _, text string, _ int) ([]vector.Match, error) {
	h.queried = append(h.queried, text)
	return h.matches, nil
}

// Spec: §7.3.1 — a managed backend's similarity is one minus the distance it
// reports, a storage-only managed backend is queried with the stored vector,
// and a self-embedding backend is queried with the projection text and stamps
// records with its hosted model.
func TestComputeOverlap_ManagedAndSelfEmbedding(t *testing.T) {
	t.Parallel()
	matches := []vector.Match{{ArtifactID: "a", Version: "1.0.0", Distance: 0}, {ArtifactID: "b", Version: "1.0.0", Distance: 0.12}}
	for name, tc := range map[string]struct {
		idx   vector.Provider
		emb   bool
		model string
	}{
		"storage-only": {idx: managedIndex{matches: matches}, emb: true, model: "m1"},
		"self-embed":   {idx: &hostedIndex{managedIndex: managedIndex{matches: matches}}, model: "hosted-1"},
	} {
		f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
		f.put("a", "1.0.0", "L", "alpha", "", false)
		f.put("b", "1.0.0", "L", "bravo", "", false)
		var emb overlapEmbedder
		if tc.emb {
			emb = overlapEmbedder{model: "m1"}
			f.reg.WithVectorSearch(tc.idx, emb)
		} else {
			f.reg.WithVectorSearch(tc.idx, nil)
		}
		if _, err := f.reg.ComputeOverlap(f.ctx, []vector.RowKey{{ArtifactID: "a", Version: "1.0.0"}}); err != nil {
			t.Fatalf("%s: ComputeOverlap: %v", name, err)
		}
		recs := f.records()
		if len(recs) != 1 || math.Abs(recs[0].Similarity-0.88) > 1e-6 || recs[0].ModelID != tc.model {
			t.Errorf("%s: records = %+v, want (a, b) at 0.88 under %s", name, recs, tc.model)
		}
		if h, ok := tc.idx.(*hostedIndex); ok && (len(h.queried) != 1 || !strings.Contains(h.queried[0], "alpha")) {
			t.Errorf("%s: QueryText calls = %q, want the projection text", name, h.queried)
		}
	}
}

// Spec: §7.3.1 — the sweep computes an artifact no record names, one whose
// records carry another model, and one whose records name a version that is
// no longer served, and leaves a current artifact alone.
func TestStaleOverlapArtifacts(t *testing.T) {
	t.Parallel()
	served := map[string]store.ManifestRecord{}
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		served[id] = store.ManifestRecord{ArtifactID: id, Version: "2"}
	}
	records := []store.OverlapRecord{
		{ArtifactA: "a", VersionA: "2", ArtifactB: "b", VersionB: "2", ModelID: "m1"},  // current
		{ArtifactA: "c", VersionA: "2", ArtifactB: "x", VersionB: "1", ModelID: "m1"},  // x removed
		{ArtifactA: "d", VersionA: "2", ArtifactB: "e", VersionB: "2", ModelID: "old"}, // other model
		{ArtifactA: "a", VersionA: "2", ArtifactB: "e", VersionB: "1", ModelID: "m1"},  // e@1 not served
	}
	got := staleOverlapArtifacts(served, records, "m1")
	want := map[string]bool{"a": true, "c": true, "d": true, "e": true, "f": true}
	if len(got) != len(want) {
		t.Fatalf("stale = %v, want %v", got, want)
	}
	for id := range want {
		if !got[id] {
			t.Errorf("stale = %v, missing %s", got, id)
		}
	}
}

// Spec: §7.3.1 — a sweep pass computes the stale artifacts and leaves current
// ones alone, and the rebuild computes every served artifact and reports the
// counts; neither runs with the check off.
func TestRefreshAndRebuildOverlap(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	f.put("a", "1.0.0", "L", "alpha", "", true)
	f.put("b", "1.0.0", "L", "bravo", "", true)
	first, err := f.reg.RefreshOverlap(f.ctx)
	second, _ := f.reg.RefreshOverlap(f.ctx)
	if err != nil || first.Computed != 2 || second.Computed != 0 {
		t.Fatalf("refresh = %+v then %+v (%v), want 2 then 0", first, second, err)
	}
	f.put("b", "2.0.0", "L", "bravo", "", true)
	third, _ := f.reg.RefreshOverlap(f.ctx)
	if third.Computed != 2 {
		t.Errorf("after b@2.0.0: refresh = %+v, want a and b recomputed", third)
	}
	f.put("n", "1.0.0", "L", "novector", "", false)
	rebuilt, err := f.reg.RebuildOverlap(f.ctx)
	if err != nil || rebuilt != (OverlapComputeResult{Computed: 2, Failed: 1}) {
		t.Errorf("rebuild = %+v, %v; want 2 computed and 1 failed", rebuilt, err)
	}
	off := New(f.st, "t", nil).WithVectorSearch(f.idx, overlapEmbedder{model: "m1"})
	if _, err := off.RebuildOverlap(f.ctx); !errors.Is(err, errOverlapUnavailable) {
		t.Errorf("rebuild with the check off = %v, want errOverlapUnavailable", err)
	}
	noIndex := New(f.st, "t", nil).WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{Enabled: true})
	if _, err := noIndex.RefreshOverlap(f.ctx); !errors.Is(err, errOverlapUnavailable) {
		t.Errorf("refresh with no index = %v, want errOverlapUnavailable", err)
	}
}

// Spec: §7.3.1 — a full re-embed computes every artifact it re-embeds; a
// scoped pass does not.
func TestReembed_FullPassComputesOverlap(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	f.put("a", "1.0.0", "L", "alpha", "", false)
	f.put("b", "1.0.0", "L", "bravo", "", false)
	if _, err := f.reg.Reembed(f.ctx, ReembedOptions{OnlyIfMissing: true}); err != nil {
		t.Fatalf("scoped reembed: %v", err)
	}
	if n := len(f.records()); n != 0 {
		t.Fatalf("a scoped pass stored %d records, want 0", n)
	}
	if _, err := f.reg.Reembed(f.ctx, ReembedOptions{}); err != nil {
		t.Fatalf("full reembed: %v", err)
	}
	if n := len(f.records()); n != 1 {
		t.Errorf("a full pass stored %d records, want 1", n)
	}
}
