package core_test

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// rankNow is the frozen instant of the ranking tests; the §3.3 window is the
// 30 UTC days ending on its date.
var rankNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

// countingStore counts SumUsageLoads reads and fails them while failing is
// set, so a test observes how often the ranking reads the store.
type countingStore struct {
	*store.Memory
	reads   atomic.Int32
	failing atomic.Bool
}

func (s *countingStore) SumUsageLoads(ctx context.Context, tenantID, fromDay, toDay string) ([]store.UsageLoads, error) {
	s.reads.Add(1)
	if s.failing.Load() {
		return nil, errors.New("store unreachable")
	}
	return s.Memory.SumUsageLoads(ctx, tenantID, fromDay, toDay)
}

// rankingFixture is a registry over one public layer "L" with the §3.3
// ranking and a usage counter wired on a frozen clock.
type rankingFixture struct {
	st      *countingStore
	reg     *core.Registry
	counter *core.UsageCounter
	clock   *clock.Frozen
}

func newRankingFixture(t *testing.T, fsys fstest.MapFS) *rankingFixture {
	t.Helper()
	f := &rankingFixture{st: &countingStore{Memory: store.NewMemory()}, clock: clock.NewFrozen(rankNow)}
	ctx := context.Background()
	if err := f.st.CreateTenant(ctx, store.Tenant{ID: tenant}); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Ingest(ctx, f.st, ingest.Request{TenantID: tenant, LayerID: "L", Files: fsys}); err != nil {
		t.Fatal(err)
	}
	f.counter = core.NewUsageCounter(f.clock)
	f.reg = core.New(f.st, tenant, []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}, Precedence: 1}}).
		WithUsageCounts(f.counter).
		WithUsageRanking(core.NewUsageRanking(f.clock, time.Minute, 365))
	return f
}

// store adds rows dated daysAgo days before rankNow.
func (f *rankingFixture) store(t *testing.T, daysAgo int, counts ...store.UsageCount) {
	t.Helper()
	day := rankNow.AddDate(0, 0, -daysAgo).Format(store.UsageDayLayout)
	for i := range counts {
		counts[i].Day = day
		if counts[i].ClientKind == "" {
			counts[i].ClientKind = "mcp"
		}
		if counts[i].Harness == "" {
			counts[i].Harness = "none"
		}
	}
	if err := f.st.AddUsageCounts(context.Background(), tenant, counts); err != nil {
		t.Fatal(err)
	}
}

func (f *rankingFixture) search(t *testing.T, query string) []string {
	t.Helper()
	res, err := f.reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{Query: query})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	return ids(res.Results)
}

var tiedHelpers = fstest.MapFS{
	"tools/aaa/ARTIFACT.md": &fstest.MapFile{Data: []byte(agentManifest("shared helper"))},
	"tools/bbb/ARTIFACT.md": &fstest.MapFile{Data: []byte(agentManifest("shared helper"))},
}

// Spec: §3.3 — search results that tie on retrieval rank by their stored
// loads of the last 30 days under the serving layer. Syncs, search hits,
// loads recorded under another layer, and loads older than the window leave
// the order alone, and an empty-query listing keeps its order.
func TestSearchArtifacts_RerankByStoredLoads(t *testing.T) {
	t.Parallel()
	f := newRankingFixture(t, tiedHelpers)
	if got := f.search(t, "helper"); !reflect.DeepEqual(got, []string{"tools/aaa", "tools/bbb"}) {
		t.Fatalf("control order = %v, want the alphabetical tie", got)
	}
	f.store(t, 29, store.UsageCount{Layer: "L", ArtifactID: "tools/bbb", Loads: 2})
	f.store(t, 1,
		store.UsageCount{Layer: "L", ArtifactID: "tools/aaa", ClientKind: "sync", Syncs: 50, SearchHits: 50},
		store.UsageCount{Layer: "hidden", ArtifactID: "tools/aaa", Loads: 40})
	f.store(t, 30, store.UsageCount{Layer: "L", ArtifactID: "tools/aaa", Loads: 90})
	f.clock.Advance(time.Minute)
	if got := f.search(t, "helper"); !reflect.DeepEqual(got, []string{"tools/bbb", "tools/aaa"}) {
		t.Errorf("reranked order = %v, want tools/bbb first on its 2 loads in the window", got)
	}
	res, err := f.reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{})
	if err != nil || !reflect.DeepEqual(ids(res.Results), []string{"tools/aaa", "tools/bbb"}) {
		t.Errorf("empty-query listing = %v (%v), want the alphabetical listing", ids(res.Results), err)
	}
}

// Spec: §3.3 — a replica's unflushed loads count at once: a load raises its
// artifact in search and in the load_domain notable order before any flush,
// and a load that does not resolve counts nothing.
func TestUsageRanking_UnflushedLoadsCountAtOnce(t *testing.T) {
	t.Parallel()
	f := newRankingFixture(t, fstest.MapFS{
		"g1/ARTIFACT.md": &fstest.MapFile{Data: []byte(agentManifest("shared helper one"))},
		"g2/ARTIFACT.md": &fstest.MapFile{Data: []byte(agentManifest("shared helper two"))},
		"g3/ARTIFACT.md": &fstest.MapFile{Data: []byte(agentManifest("shared helper three"))},
	})
	dom, err := f.reg.LoadDomain(context.Background(), publicID, "", core.LoadDomainOptions{})
	if err != nil || len(dom.Notable) == 0 || dom.Notable[0].ID != "g1" {
		t.Fatalf("control notable = %v (%v), want g1 first", notableIDs(dom.Notable), err)
	}
	for range 2 {
		if _, err := f.reg.LoadArtifact(context.Background(), publicID, "g3", core.LoadArtifactOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.reg.LoadArtifact(context.Background(), publicID, "g9", core.LoadArtifactOptions{}); err == nil {
		t.Fatal("a load of an unknown artifact resolved")
	}
	dom, err = f.reg.LoadDomain(context.Background(), publicID, "", core.LoadDomainOptions{})
	if err != nil || len(dom.Notable) == 0 || dom.Notable[0].ID != "g3" {
		t.Errorf("notable after unflushed loads = %v (%v), want g3 first", notableIDs(dom.Notable), err)
	}
	if got := f.search(t, "helper"); len(got) == 0 || got[0] != "g3" {
		t.Errorf("search after unflushed loads = %v, want g3 first", got)
	}
}

// Spec: §3.3 — a replica reads the stored counts again only once its last
// read is a flush interval old, and a failed read keeps the last counts and
// waits another interval before the next attempt.
func TestUsageRanking_RefreshesOncePerIntervalAndKeepsCountsOnFailure(t *testing.T) {
	t.Parallel()
	f := newRankingFixture(t, tiedHelpers)
	f.store(t, 0, store.UsageCount{Layer: "L", ArtifactID: "tools/bbb", Loads: 1})
	if got := f.search(t, "helper"); got[0] != "tools/bbb" || f.st.reads.Load() != 1 {
		t.Fatalf("first search = %v after %d reads, want tools/bbb first after one read", got, f.st.reads.Load())
	}
	f.store(t, 0, store.UsageCount{Layer: "L", ArtifactID: "tools/aaa", Loads: 5})
	f.clock.Advance(59 * time.Second)
	if got := f.search(t, "helper"); got[0] != "tools/bbb" || f.st.reads.Load() != 1 {
		t.Errorf("search within the interval = %v after %d reads, want the cached order and no new read", got, f.st.reads.Load())
	}
	f.clock.Advance(time.Second)
	if got := f.search(t, "helper"); got[0] != "tools/aaa" || f.st.reads.Load() != 2 {
		t.Errorf("search after the interval = %v after %d reads, want tools/aaa first after a second read", got, f.st.reads.Load())
	}
	f.st.failing.Store(true)
	f.clock.Advance(time.Minute)
	if got := f.search(t, "helper"); got[0] != "tools/aaa" || f.st.reads.Load() != 3 {
		t.Errorf("search on a failed read = %v after %d reads, want the last counts kept", got, f.st.reads.Load())
	}
	if got := f.search(t, "helper"); got[0] != "tools/aaa" || f.st.reads.Load() != 3 {
		t.Errorf("search after a failed read = %v after %d reads, want no retry within the interval", got, f.st.reads.Load())
	}
}

func ids(ds []core.ArtifactDescriptor) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.ID
	}
	return out
}

func notableIDs(ds []core.ArtifactDescriptor) []string { return ids(ds) }
