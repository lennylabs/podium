package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/embedding"
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

// failingIndex is the memory backend with a query that fails on demand. It
// records the row count of each query, so a test can follow a widening
// computation, and runs onQuery, when set, before each query, so a test can
// change the catalog while a computation is in flight.
type failingIndex struct {
	*vector.Memory
	fail    bool
	ks      []int
	onQuery func()
}

func (f *failingIndex) QueryModel(ctx context.Context, tenant string, vec []float32, k int, model string) ([]vector.Match, error) {
	f.ks = append(f.ks, k)
	if f.onQuery != nil {
		f.onQuery()
	}
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
	if ab.Owner != "a" || ab.OwnerVersion != "1.0.0" || ab.Peer != "b" || ab.PeerVersion != "2.0.0" || ac.Peer != "c" || ab.ModelID != "m1" || !ab.ComputedAt.Equal(overlapNow) {
		t.Errorf("records = %+v", recs)
	}
	if math.Abs(ab.Similarity-cosine(overlapVectors["alpha"], overlapVectors["bravo"])) > 1e-5 ||
		math.Abs(ac.Similarity-cosine(overlapVectors["alpha"], overlapVectors["charlie"])) > 1e-5 {
		t.Errorf("similarities = %v, %v; want the exact cosine", ab.Similarity, ac.Similarity)
	}
}

// Spec: §13.12 — PODIUM_LINT_SEMANTIC_NEIGHBORS is the number of peers a
// computation finds and stores records for. The artifact's own row is among
// its nearest rows, so a query of exactly that many rows would store one
// record fewer, and a count of 1 would store none.
func TestComputeOverlap_NeighborCountIsRecordsStored(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	f.reg.WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{Enabled: true, Neighbors: 1})
	f.put("a", "1.0.0", "L", "alpha", "", true)
	f.put("b", "1.0.0", "L", "bravo", "", true)
	f.put("c", "1.0.0", "L", "charlie", "", true)
	res, err := f.reg.ComputeOverlap(f.ctx, []vector.RowKey{{ArtifactID: "a", Version: "1.0.0"}})
	if err != nil || res != (OverlapComputeResult{Computed: 1}) {
		t.Fatalf("ComputeOverlap = %+v, %v", res, err)
	}
	if recs := f.records(); len(recs) != 1 || recs[0].Owner != "a" || recs[0].Peer != "b" {
		t.Fatalf("records = %+v, want exactly one record, a's nearest peer b", recs)
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

// managedRows is a managed backend fake that reports a distance and scores
// nothing itself. A query returns the first k of its matches, which it holds
// nearest first. It returns no stored vector.
type managedRows struct {
	matches []vector.Match
}

func (m managedRows) firstK(k int) []vector.Match { return m.matches[:min(k, len(m.matches))] }

func (managedRows) ID() string      { return "pinecone" }
func (managedRows) Dimensions() int { return 4 }
func (managedRows) Put(context.Context, string, string, string, []float32) error {
	return nil
}
func (m managedRows) Query(_ context.Context, _ string, _ []float32, k int) ([]vector.Match, error) {
	return m.firstK(k), nil
}
func (managedRows) Delete(context.Context, string, string, string) error { return nil }
func (managedRows) Close() error                                         { return nil }

// managedIndex is a storage-only managed backend fake: managedRows that also
// returns stored vectors.
type managedIndex struct {
	managedRows
}

func newManagedIndex(matches []vector.Match) managedIndex {
	return managedIndex{managedRows{matches: matches}}
}

func (managedIndex) StoredVector(context.Context, string, string, string) ([]float32, string, error) {
	return []float32{1, 0, 0, 0}, "", nil
}

// hostedIndex is a self-embedding backend fake that cannot return a stored
// vector, so the overlap computation queries it with the projection text. It
// records the text and the row count of each text query.
type hostedIndex struct {
	managedRows
	queried []string
	ks      []int
}

func (*hostedIndex) SelfEmbeds() bool    { return true }
func (*hostedIndex) HostedModel() string { return "hosted-1" }
func (*hostedIndex) PutText(context.Context, string, string, string, string) error {
	return nil
}
func (h *hostedIndex) QueryText(_ context.Context, _, text string, k int) ([]vector.Match, error) {
	h.queried = append(h.queried, text)
	h.ks = append(h.ks, k)
	return h.firstK(k), nil
}

// Spec: §7.3.1 — a managed backend's similarity is one minus the distance it
// reports, a storage-only managed backend is queried with the stored vector,
// and a self-embedding backend that cannot return a stored vector is queried
// with the projection text and stamps records with its hosted model.
func TestComputeOverlap_ManagedAndSelfEmbedding(t *testing.T) {
	t.Parallel()
	matches := []vector.Match{{ArtifactID: "a", Version: "1.0.0", Distance: 0}, {ArtifactID: "b", Version: "1.0.0", Distance: 0.12}}
	for name, tc := range map[string]struct {
		idx   vector.Provider
		emb   bool
		model string
	}{
		"storage-only": {idx: newManagedIndex(matches), emb: true, model: "m1"},
		"self-embed":   {idx: &hostedIndex{managedRows: managedRows{matches: matches}}, model: "hosted-1"},
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

// joinedKeys renders a set's members in order, so a case compares a whole set
// at once.
func joinedKeys(set map[string]bool) string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// ownedSummary renders records as owner>peer in store order.
func ownedSummary(recs []store.OverlapRecord) string {
	parts := make([]string, 0, len(recs))
	for _, r := range recs {
		parts = append(parts, r.Owner+">"+r.Peer)
	}
	return strings.Join(parts, " ")
}

// Spec: §7.3.1 — the sweep computes a served artifact whose computation entry
// is missing or was for another version or another model, and one that owns a
// record naming a version that is no longer served, its own or its peer's. It
// leaves a current artifact alone, including one whose computation found no
// peer, and deletes rather than computes an artifact that is no longer served.
func TestStaleOverlapArtifacts(t *testing.T) {
	t.Parallel()
	served := map[string]store.ManifestRecord{}
	for _, id := range []string{"b", "cur", "lonely", "missing", "oldmodel", "oldver", "peergone", "peerold"} {
		served[id] = store.ManifestRecord{ArtifactID: id, Version: "2"}
	}
	run := func(id, version, model string) store.OverlapComputation {
		return store.OverlapComputation{ArtifactID: id, Version: version, ModelID: model}
	}
	rec := func(owner, ov, peer, pv string) store.OverlapRecord {
		return store.OverlapRecord{Owner: owner, OwnerVersion: ov, Peer: peer, PeerVersion: pv, ModelID: "m1"}
	}
	runs := []store.OverlapComputation{
		run("b", "2", "m1"), run("cur", "2", "m1"), run("lonely", "2", "m1"), run("oldmodel", "2", "m0"),
		run("oldver", "1", "m1"), run("peergone", "2", "m1"), run("peerold", "2", "m1"), run("gone", "1", "m1"),
	}
	records := []store.OverlapRecord{
		rec("cur", "2", "b", "2"),
		rec("peergone", "2", "gone", "1"),
		rec("peerold", "2", "b", "1"),
		rec("gone", "1", "b", "2"),
		rec("orphan", "1", "b", "2"),
	}
	got := joinedKeys(staleOverlapArtifacts(served, records, runs, "m1"))
	gone := strings.Join(unservedOverlapArtifacts(served, records, runs), ",")
	if got != "missing,oldmodel,oldver,peergone,peerold" || gone != "gone,orphan" {
		t.Errorf("stale = %s, want missing,oldmodel,oldver,peergone,peerold; unserved = %s, want gone,orphan", got, gone)
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

// Spec: §7.3.1 — an artifact whose computation found no peer is current: the
// sweep records the computation and does not compute it again, until the
// configured model changes.
func TestRefreshOverlap_NoPeerArtifactIsCurrent(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	f.put("solo", "1.0.0", "L", "alpha", "", true)
	first, err := f.reg.RefreshOverlap(f.ctx)
	second, _ := f.reg.RefreshOverlap(f.ctx)
	runs, _ := f.st.ListOverlapComputations(f.ctx, "t")
	if err != nil || first.Computed != 1 || second.Computed != 0 || len(f.records()) != 0 ||
		len(runs) != 1 || runs[0].ArtifactID != "solo" || runs[0].Version != "1.0.0" || runs[0].ModelID != "m1" {
		t.Fatalf("refresh = %+v then %+v (%v), records %d, entries %+v; want 1 then 0, no record, and solo@1.0.0 under m1",
			first, second, err, len(f.records()), runs)
	}
	f.reg.WithVectorSearch(f.idx, overlapEmbedder{table: overlapVectors, model: "m2"})
	if third, _ := f.reg.RefreshOverlap(f.ctx); third.Computed+third.Failed != 1 {
		t.Errorf("after a model change: refresh = %+v, want solo attempted again", third)
	}
}

// Spec: §7.3.1 — the sweep deletes the records and the computation entry of
// an artifact that is no longer served, and computes the served artifact that
// owns a record naming it, which replaces that record; a current artifact is
// left alone.
func TestRefreshOverlap_DeletesUnservedArtifacts(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	f.put("a", "1.0.0", "L", "alpha", "", true)
	f.put("b", "1.0.0", "L", "bravo", "", true)
	if _, err := f.reg.RefreshOverlap(f.ctx); err != nil {
		t.Fatalf("RefreshOverlap: %v", err)
	}
	// gone was computed while it was served, and a's computation found it.
	f.writeOwned("gone", "1.0.0", overlapNow, map[string]float64{"a": 0.9})
	f.writeOwned("a", "1.0.0", overlapNow, map[string]float64{"b": 0.99, "gone": 0.9})
	res, err := f.reg.RefreshOverlap(f.ctx)
	runs, _ := f.st.ListOverlapComputations(f.ctx, "t")
	entries := make([]string, 0, len(runs))
	for _, c := range runs {
		entries = append(entries, c.ArtifactID)
	}
	if err != nil || res != (OverlapComputeResult{Computed: 1}) || ownedSummary(f.records()) != "a>b b>a" || strings.Join(entries, ",") != "a,b" {
		t.Errorf("refresh = %+v (%v), records %q, entries %v; want a recomputed, records a>b b>a, entries a,b",
			res, err, ownedSummary(f.records()), entries)
	}
}

// Spec: §7.3.1 — an artifact with more earlier versions than
// PODIUM_LINT_SEMANTIC_NEIGHBORS still finds its peers. The index keeps a row
// for every version, and the rows of an artifact's own earlier versions lie
// nearest, so the computation widens its query past them and keeps the
// served row of each peer.
func TestComputeOverlap_WidensPastEarlierVersions(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	var latest string
	for i := 0; i <= 10; i++ {
		latest = fmt.Sprintf("1.0.%d", i)
		f.put("a/alpha", latest, "L", "alpha", "", true)
		f.put("b/bravo", latest, "L", "bravo", "", true)
	}
	res, err := f.reg.RebuildOverlap(f.ctx)
	if err != nil || res != (OverlapComputeResult{Computed: 2}) {
		t.Fatalf("RebuildOverlap = %+v, %v; want both computed", res, err)
	}
	recs := f.records()
	want := cosine(overlapVectors["alpha"], overlapVectors["bravo"])
	if len(recs) != 2 || ownedSummary(recs) != "a/alpha>b/bravo b/bravo>a/alpha" ||
		recs[0].PeerVersion != latest || recs[1].PeerVersion != latest || math.Abs(recs[0].Similarity-want) > 1e-5 {
		t.Fatalf("records = %+v, want each artifact to own one record naming the other at %s with similarity %.3f", recs, latest, want)
	}
	if got := fmt.Sprint(f.idx.ks); got != "[11 22 44 11 22 44]" {
		t.Errorf("index queries asked for %s rows, want [11 22 44] per artifact, the last returning every row", got)
	}
	r, err := f.reg.CatalogOverlap(f.ctx, layer.Identity{IsPublic: true}, OverlapQuery{})
	if err != nil || peerIDs(semanticOf(r, "a/alpha")) != "b/bravo" || peerIDs(semanticOf(r, "b/bravo")) != "a/alpha" {
		t.Errorf("report = %+v, %v; want the pair reported once from each side", r, err)
	}
}

// Spec: §7.3.1 — the computation widens its query up to 50 times one more
// than PODIUM_LINT_SEMANTIC_NEIGHBORS rows and no further. An artifact that
// finds no peer within them stores no record and is recorded as computed.
func TestComputeOverlap_WideningIsBounded(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	f.reg.WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{Enabled: true, Neighbors: 1})
	var latest string
	for i := 0; i < 120; i++ {
		latest = fmt.Sprintf("1.0.%d", i)
		f.put("a", latest, "L", "alpha", "", true)
	}
	f.put("c", "1.0.0", "L", "charlie", "", true)
	res, err := f.reg.ComputeOverlap(f.ctx, []vector.RowKey{{ArtifactID: "a", Version: latest}})
	runs, _ := f.st.ListOverlapComputations(f.ctx, "t")
	if err != nil || res != (OverlapComputeResult{Computed: 1}) || len(f.records()) != 0 || len(runs) != 1 || runs[0].Version != latest {
		t.Fatalf("ComputeOverlap = %+v, %v; records %d, entries %+v; want a computed with no record", res, err, len(f.records()), runs)
	}
	if got := fmt.Sprint(f.idx.ks); got != "[2 4 8 16 32 64 100]" {
		t.Errorf("index queries asked for %s rows, want doubling from 2 up to the cap of 100", got)
	}
}

// Spec: §7.3.1 — the widening applies on a storage-only managed backend that
// is not model-versioned and on a self-embedding backend that cannot return a
// stored vector, which is queried with the projection text each time.
func TestComputeOverlap_WidensOnManagedBackends(t *testing.T) {
	t.Parallel()
	matches := []vector.Match{
		{ArtifactID: "a", Version: "1.0.0", Distance: 0},
		{ArtifactID: "a", Version: "0.9.0", Distance: 0},
		{ArtifactID: "a", Version: "0.8.0", Distance: 0},
		{ArtifactID: "b", Version: "1.0.0", Distance: 0.12},
	}
	hosted := &hostedIndex{managedRows: managedRows{matches: matches}}
	for name, tc := range map[string]struct {
		idx vector.Provider
		emb embedding.Provider
	}{
		"storage-only": {idx: newManagedIndex(matches), emb: overlapEmbedder{model: "m1"}},
		"self-embed":   {idx: hosted},
	} {
		f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
		f.reg.WithVectorSearch(tc.idx, tc.emb).WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{Enabled: true, Neighbors: 1})
		f.put("a", "1.0.0", "L", "alpha", "", false)
		f.put("b", "1.0.0", "L", "bravo", "", false)
		if _, err := f.reg.ComputeOverlap(f.ctx, []vector.RowKey{{ArtifactID: "a", Version: "1.0.0"}}); err != nil {
			t.Fatalf("%s: ComputeOverlap: %v", name, err)
		}
		if recs := f.records(); ownedSummary(recs) != "a>b" || math.Abs(recs[0].Similarity-0.88) > 1e-6 {
			t.Errorf("%s: records = %+v, want a>b at 0.88", name, recs)
		}
	}
	if got := fmt.Sprint(hosted.ks); got != "[2 4]" {
		t.Errorf("self-embedding queries asked for %s rows, want [2 4]", got)
	}
}

// unitAt returns the unit vector at deg degrees in the first two dimensions.
func unitAt(deg float64) []float32 {
	rad := deg * math.Pi / 180
	return []float32{float32(math.Cos(rad)), float32(math.Sin(rad)), 0, 0}
}

// Spec: §7.3.1 — a record is owned by the artifact whose computation stored
// it and keyed by owner and peer, so computing A replaces only A's records. A
// pair that only its other side found survives A's recomputation, and the
// stored records do not depend on the order of computation. With one
// neighbour and unit vectors at 0 (a), 8 (b), -5 (c), and 20 (d) degrees, a
// finds c, b and c find a, and d finds b, so the pairs are (a, b), (a, c),
// and (b, d), and (a, b) exists only because b owns it.
func TestComputeOverlap_OwnershipKeepsPeersRecords(t *testing.T) {
	t.Parallel()
	circle := map[string][]float32{"zerodeg": unitAt(0), "eightdeg": unitAt(8), "minusfivedeg": unitAt(-5), "twentydeg": unitAt(20)}
	build := func() *overlapFixture {
		f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
		f.reg.WithVectorSearch(f.idx, overlapEmbedder{table: circle, model: "m1"}).
			WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{Enabled: true, Neighbors: 1})
		f.reg.overlap.clock = clock.NewFrozen(overlapNow)
		for id, word := range map[string]string{"a": "zerodeg", "b": "eightdeg", "c": "minusfivedeg", "d": "twentydeg"} {
			f.put(id, "1.0.0", "L", word, "", true)
		}
		return f
	}
	keys := func(ids ...string) []vector.RowKey {
		out := make([]vector.RowKey, len(ids))
		for i, id := range ids {
			out[i] = vector.RowKey{ArtifactID: id, Version: "1.0.0"}
		}
		return out
	}
	forward, backward := build(), build()
	if _, err := forward.reg.ComputeOverlap(forward.ctx, keys("a", "b", "c", "d")); err != nil {
		t.Fatalf("ComputeOverlap: %v", err)
	}
	if _, err := backward.reg.ComputeOverlap(backward.ctx, keys("d", "c", "b", "a")); err != nil {
		t.Fatalf("ComputeOverlap: %v", err)
	}
	const want = "a>c b>a c>a d>b"
	if got, rev := ownedSummary(forward.records()), ownedSummary(backward.records()); got != want || rev != want {
		t.Fatalf("records = %q forward and %q backward, want %q in both orders", got, rev, want)
	}
	if _, err := forward.reg.ComputeOverlap(forward.ctx, keys("a")); err != nil {
		t.Fatalf("recompute a: %v", err)
	}
	r, err := forward.reg.CatalogOverlap(forward.ctx, layer.Identity{IsPublic: true}, OverlapQuery{})
	if err != nil {
		t.Fatalf("CatalogOverlap: %v", err)
	}
	got := fmt.Sprintf("a:%s b:%s c:%s d:%s", peerIDs(semanticOf(r, "a")), peerIDs(semanticOf(r, "b")), peerIDs(semanticOf(r, "c")), peerIDs(semanticOf(r, "d")))
	if ownedSummary(forward.records()) != want || got != "a:c,b b:a,d c:a d:b" {
		t.Errorf("after recomputing a: records %q, report %s; want %q and a:c,b b:a,d c:a d:b", ownedSummary(forward.records()), got, want)
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
	if got := ownedSummary(f.records()); got != "a>b b>a" {
		t.Errorf("a full pass stored %q, want a>b b>a", got)
	}
}

// faultyOverlapStore is the memory store with overlap reads and the deletion
// of removed artifacts that fail on demand.
type faultyOverlapStore struct {
	*store.Memory
	failRecords, failRuns, failDelete bool
}

func (s *faultyOverlapStore) ListOverlapRecords(ctx context.Context, tenant string) ([]store.OverlapRecord, error) {
	if s.failRecords {
		return nil, errors.New("records unreadable")
	}
	return s.Memory.ListOverlapRecords(ctx, tenant)
}

func (s *faultyOverlapStore) ListOverlapComputations(ctx context.Context, tenant string) ([]store.OverlapComputation, error) {
	if s.failRuns {
		return nil, errors.New("entries unreadable")
	}
	return s.Memory.ListOverlapComputations(ctx, tenant)
}

func (s *faultyOverlapStore) DeleteOverlapArtifacts(ctx context.Context, tenant string, ids []string) error {
	if s.failDelete {
		return errors.New("delete refused")
	}
	return s.Memory.DeleteOverlapArtifacts(ctx, tenant, ids)
}

// Spec: §7.3.1 — a sweep pass that cannot read the stored records or the
// computation entries fails without computing. One that cannot delete the
// records of a removed artifact logs the failure and still computes the stale
// artifacts.
func TestRefreshOverlap_StoreFailures(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	f.put("a", "1.0.0", "L", "alpha", "", true)
	f.put("b", "1.0.0", "L", "bravo", "", true)
	f.writeOwned("gone", "1.0.0", overlapNow, map[string]float64{"a": 0.9})
	fs := &faultyOverlapStore{Memory: f.st}
	reg := New(fs, "t", []layer.Layer{publicLayer("L")}).
		WithVectorSearch(f.idx, overlapEmbedder{table: overlapVectors, model: "m1"}).
		WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{Enabled: true})
	fs.failRecords = true
	_, errRecords := reg.RefreshOverlap(f.ctx)
	fs.failRecords, fs.failRuns = false, true
	_, errRuns := reg.RefreshOverlap(f.ctx)
	fs.failRuns, fs.failDelete = false, true
	res, errDelete := reg.RefreshOverlap(f.ctx)
	if errRecords == nil || errRuns == nil || errDelete != nil || res != (OverlapComputeResult{Computed: 2}) {
		t.Errorf("refresh errors = %v, %v, %v and result %+v; want two read failures, then a and b computed despite the failed delete",
			errRecords, errRuns, errDelete, res)
	}
}
