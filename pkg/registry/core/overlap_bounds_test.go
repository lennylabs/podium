package core

import (
	"context"
	"fmt"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// limitedIndex is a storage-only managed backend fake that reports maxRows as
// its per-query limit (0 reports none) and fails every query of more than
// failAbove rows, as a backend that enforces its limit does. It records the
// row count of each query.
type limitedIndex struct {
	managedIndex
	maxRows, failAbove int
	ks                 []int
}

func (l *limitedIndex) MaxQueryRows() int { return l.maxRows }

func (l *limitedIndex) Query(ctx context.Context, tenant string, vec []float32, k int) ([]vector.Match, error) {
	l.ks = append(l.ks, k)
	if k > l.failAbove {
		return nil, fmt.Errorf("topK %d exceeds the backend limit of %d", k, l.failAbove)
	}
	return l.managedIndex.Query(ctx, tenant, vec, k)
}

// selfRowsThen returns n rows of earlier versions of a, nearest first,
// followed by tail.
func selfRowsThen(n int, tail ...vector.Match) []vector.Match {
	out := make([]vector.Match, 0, n+len(tail))
	for i := 0; i < n; i++ {
		out = append(out, vector.Match{ArtifactID: "a", Version: fmt.Sprintf("0.0.%d", i)})
	}
	return append(out, tail...)
}

// limitedFixture wires idx as the registry's index with
// PODIUM_LINT_SEMANTIC_NEIGHBORS set to neighbors and stores served a@1.0.0,
// b@1.0.0, and c@1.0.0.
func limitedFixture(t *testing.T, idx *limitedIndex, neighbors int) *overlapFixture {
	t.Helper()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	f.reg.WithVectorSearch(idx, overlapEmbedder{model: "m1"}).
		WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{Enabled: true, Neighbors: neighbors})
	for _, id := range []string{"a", "b", "c"} {
		f.put(id, "1.0.0", "L", id, "", false)
	}
	return f
}

// Spec: §7.3.1 — the widening asks no more rows than the backend accepts in
// one query. With two neighbours, a backend that accepts 8 rows is asked 3,
// 6, and then 8 rows, never 12, and the computation succeeds with the peers
// those rows hold.
func TestComputeOverlap_WideningClampedToBackendLimit(t *testing.T) {
	t.Parallel()
	idx := &limitedIndex{
		managedIndex: newManagedIndex(selfRowsThen(7, vector.Match{ArtifactID: "b", Version: "1.0.0", Distance: 0.1},
			vector.Match{ArtifactID: "c", Version: "1.0.0", Distance: 0.2})),
		maxRows: 8, failAbove: 8,
	}
	f := limitedFixture(t, idx, 2)
	res, err := f.reg.ComputeOverlap(f.ctx, []vector.RowKey{{ArtifactID: "a", Version: "1.0.0"}})
	if err != nil || res != (OverlapComputeResult{Computed: 1}) || ownedSummary(f.records()) != "a>b" {
		t.Fatalf("ComputeOverlap = %+v, %v; records %s; want a computed with its one peer within 8 rows",
			res, err, ownedSummary(f.records()))
	}
	if got := fmt.Sprint(idx.ks); got != "[3 6 8]" {
		t.Errorf("index queries asked for %s rows, want [3 6 8], clamped at the backend's limit of 8", got)
	}
	if limit := vector.MaxQueryRowsOf(idx); limit != 8 || vector.MaxQueryRowsOf(managedIndex{}) != 0 {
		t.Errorf("MaxQueryRowsOf = %d and %d for a backend without the capability, want 8 and 0",
			limit, vector.MaxQueryRowsOf(managedIndex{}))
	}
}

// Spec: §7.3.1 — a failed query after the first one ends the widening, and
// the computation keeps and stores the peers the earlier queries found. A
// failed first query still fails the computation and stores nothing.
func TestComputeOverlap_LaterQueryFailureKeepsPeers(t *testing.T) {
	t.Parallel()
	matches := []vector.Match{
		{ArtifactID: "a", Version: "0.9.0"},
		{ArtifactID: "b", Version: "1.0.0", Distance: 0.1},
		{ArtifactID: "a", Version: "0.8.0"},
		{ArtifactID: "a", Version: "0.7.0"},
		{ArtifactID: "c", Version: "1.0.0", Distance: 0.3},
	}
	idx := &limitedIndex{managedIndex: newManagedIndex(matches), failAbove: 3}
	f := limitedFixture(t, idx, 2)
	key := []vector.RowKey{{ArtifactID: "a", Version: "1.0.0"}}
	res, err := f.reg.ComputeOverlap(f.ctx, key)
	runs, _ := f.st.ListOverlapComputations(f.ctx, "t")
	if err != nil || res != (OverlapComputeResult{Computed: 1}) || ownedSummary(f.records()) != "a>b" || len(runs) != 1 {
		t.Fatalf("ComputeOverlap = %+v, %v; records %s, entries %+v; want b kept from the first query and a recorded as computed",
			res, err, ownedSummary(f.records()), runs)
	}
	if got := fmt.Sprint(idx.ks); got != "[3 6]" {
		t.Errorf("index queries asked for %s rows, want [3 6], the second failing", got)
	}
	idx.failAbove = 0
	failed, _ := f.reg.ComputeOverlap(f.ctx, key)
	if failed != (OverlapComputeResult{Failed: 1}) || ownedSummary(f.records()) != "a>b" {
		t.Errorf("a failed first query = %+v with records %s, want one failure and the records kept", failed, ownedSummary(f.records()))
	}
}

// Spec: §7.3.1 — a computation stores nothing when its artifact's version is
// no longer the current served version by the time it would store. Here a
// newer version is ingested while the computation queries the index.
func TestComputeOverlap_SupersededVersionStoresNothing(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	f.put("a", "1.0.0", "L", "alpha", "", true)
	f.put("b", "1.0.0", "L", "bravo", "", true)
	f.idx.onQuery = func() {
		f.idx.onQuery = nil
		f.put("a", "2.0.0", "L", "alpha", "", false)
	}
	res, err := f.reg.ComputeOverlap(f.ctx, []vector.RowKey{{ArtifactID: "a", Version: "1.0.0"}})
	runs, _ := f.st.ListOverlapComputations(f.ctx, "t")
	if err != nil || res != (OverlapComputeResult{}) || len(f.records()) != 0 || len(runs) != 0 {
		t.Errorf("ComputeOverlap = %+v, %v; records %+v, entries %+v; want nothing stored and no failure counted",
			res, err, f.records(), runs)
	}
}

// Spec: §7.3.1 — a sweep computation whose version was superseded during the
// pass does not overwrite the newer version's computation. Version 2.0.0 of a
// is ingested and computed while the sweep computes a@1.0.0 from the served
// set it read at its start.
func TestRefreshOverlap_KeepsNewerComputation(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	f.put("a", "1.0.0", "L", "alpha", "", true)
	f.put("b", "1.0.0", "L", "bravo", "", true)
	var nested error
	f.idx.onQuery = func() {
		f.idx.onQuery = nil
		f.put("a", "2.0.0", "L", "alpha", "", true)
		_, nested = f.reg.ComputeOverlap(f.ctx, []vector.RowKey{{ArtifactID: "a", Version: "2.0.0"}})
	}
	res, err := f.reg.RefreshOverlap(f.ctx)
	runs, _ := f.st.ListOverlapComputations(f.ctx, "t")
	versions := map[string]string{}
	for _, c := range runs {
		versions[c.ArtifactID] = c.Version
	}
	owned := ""
	for _, r := range f.records() {
		if r.Owner == "a" {
			owned += r.OwnerVersion + ">" + r.Peer + " "
		}
	}
	if err != nil || nested != nil || res.OverlapComputeResult != (OverlapComputeResult{Computed: 1}) || versions["a"] != "2.0.0" || owned != "2.0.0>b " {
		t.Errorf("RefreshOverlap = %+v, %v (nested %v); entries %v, a owns %q; want a's 2.0.0 computation kept and b computed",
			res, err, nested, versions, owned)
	}
}

// hookedOverlapStore is the memory store with a callback that runs once at
// the start of ListOverlapRecords, before the records are read.
type hookedOverlapStore struct {
	*store.Memory
	beforeRecords func()
}

func (s *hookedOverlapStore) ListOverlapRecords(ctx context.Context, tenant string) ([]store.OverlapRecord, error) {
	if hook := s.beforeRecords; hook != nil {
		s.beforeRecords = nil
		hook()
	}
	return s.Memory.ListOverlapRecords(ctx, tenant)
}

// Spec: §7.3.1 — the sweep deletes only artifacts that are no longer served,
// so an artifact ingested and computed while a pass runs keeps its records
// and its computation entry.
func TestRefreshOverlap_KeepsArtifactComputedMidPass(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	f.put("a", "1.0.0", "L", "alpha", "", true)
	hs := &hookedOverlapStore{Memory: f.st}
	reg := New(hs, "t", []layer.Layer{publicLayer("L")}).
		WithVectorSearch(f.idx, overlapEmbedder{table: overlapVectors, model: "m1"}).
		WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{Enabled: true})
	var computeErr error
	hs.beforeRecords = func() {
		f.put("x", "1.0.0", "L", "bravo", "", true)
		_, computeErr = reg.ComputeOverlap(f.ctx, []vector.RowKey{{ArtifactID: "x", Version: "1.0.0"}})
	}
	if _, err := reg.RefreshOverlap(f.ctx); err != nil || computeErr != nil {
		t.Fatalf("RefreshOverlap: %v (mid-pass computation: %v)", err, computeErr)
	}
	runs, _ := f.st.ListOverlapComputations(f.ctx, "t")
	entries := ""
	for _, c := range runs {
		entries += c.ArtifactID + "@" + c.Version + " "
	}
	if summary := ownedSummary(f.records()); entries != "a@1.0.0 x@1.0.0 " || summary != "a>x x>a" {
		t.Errorf("after the pass: entries %q, records %s; want x's records and entry kept", entries, summary)
	}
}

// Spec: §7.3.1 — a store refusal of a superseded computation counts as
// neither computed nor failed.
func TestComputeOverlap_StoreRefusalIsNotAFailure(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	f.put("a", "1.0.0", "L", "alpha", "", true)
	f.put("b", "1.0.0", "L", "bravo", "", true)
	newer := store.OverlapComputation{ArtifactID: "a", Version: "9.0.0", ModelID: "m1", ComputedAt: overlapNow.Add(1)}
	if err := f.st.ReplaceOverlapRecords(f.ctx, "t", newer, nil); err != nil {
		t.Fatalf("ReplaceOverlapRecords: %v", err)
	}
	res, err := f.reg.ComputeOverlap(f.ctx, []vector.RowKey{{ArtifactID: "a", Version: "1.0.0"}})
	runs, _ := f.st.ListOverlapComputations(f.ctx, "t")
	if err != nil || res != (OverlapComputeResult{}) || len(runs) != 1 || runs[0].Version != "9.0.0" || len(f.records()) != 0 {
		t.Errorf("ComputeOverlap = %+v, %v; entries %+v; want the refusal skipped and the newer entry kept", res, err, runs)
	}
}

// listingCountStore is the memory store counting manifest listings and
// failing every listing after the first failAfter, when failAfter is set.
type listingCountStore struct {
	*store.Memory
	listings, failAfter int
}

func (s *listingCountStore) ListManifests(ctx context.Context, tenant string) ([]store.ManifestRecord, error) {
	s.listings++
	if s.failAfter > 0 && s.listings > s.failAfter {
		return nil, fmt.Errorf("store unreachable")
	}
	return s.Memory.ListManifests(ctx, tenant)
}

// Spec: §7.3.1 — a batch confirms its computations' served versions once per
// chunk of overlapConfirmChunk computations before storing them, and a chunk
// whose confirmation cannot read the served set stores nothing and counts
// each of its computations as failed.
func TestRebuildOverlap_ConfirmsPerChunk(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	n := overlapConfirmChunk + 1
	for i := 0; i < n; i++ {
		f.put(fmt.Sprintf("a%02d", i), "1.0.0", "L", "alpha", "", true)
	}
	cs := &listingCountStore{Memory: f.st}
	reg := New(cs, "t", []layer.Layer{publicLayer("L")}).
		WithVectorSearch(f.idx, overlapEmbedder{table: overlapVectors, model: "m1"}).
		WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{Enabled: true})
	res, err := reg.RebuildOverlap(f.ctx)
	if err != nil || res != (OverlapComputeResult{Computed: n}) || cs.listings != 3 {
		t.Fatalf("RebuildOverlap = %+v, %v with %d listings; want %d computed with one listing to select and one per chunk", res, err, cs.listings, n)
	}
	cs.listings, cs.failAfter = 0, 1
	res, err = reg.RebuildOverlap(f.ctx)
	if err != nil || res != (OverlapComputeResult{Failed: n}) {
		t.Errorf("RebuildOverlap with unreadable confirmations = %+v, %v; want every computation failed", res, err)
	}
}
