package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// usageNow is the frozen day every usage test records under: a Wednesday, so
// the week series below cuts its first period.
var usageNow = time.Date(2026, 11, 18, 15, 0, 0, 0, time.UTC)

// usageFixture is a registry over two layers, "team" (public) and "secret"
// (visible to bob only), with a skill in each, wired to a usage counter whose
// clock is frozen at usageNow.
type usageFixture struct {
	st      *store.Memory
	reg     *core.Registry
	counter *core.UsageCounter
	events  []core.AuditEvent
	mu      sync.Mutex
}

func newUsageFixture(t *testing.T) *usageFixture {
	t.Helper()
	f := &usageFixture{st: store.NewMemory()}
	ctx := context.Background()
	if err := f.st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []store.ManifestRecord{
		{TenantID: "t", ArtifactID: "ops/greet", Version: "1.0.0", ContentHash: "sha256:1", Type: "skill", Name: "greet", Description: "greet the user at session start", Layer: "team"},
		{TenantID: "t", ArtifactID: "ops/farewell", Version: "1.0.0", ContentHash: "sha256:2", Type: "skill", Name: "farewell", Description: "say goodbye at session end", Layer: "team"},
		{TenantID: "t", ArtifactID: "ops/hidden", Version: "1.0.0", ContentHash: "sha256:3", Type: "skill", Name: "hidden", Description: "a private session helper", Layer: "secret"},
	} {
		if err := f.st.PutManifest(ctx, storetest.Seal(t, m, nil, nil)); err != nil {
			t.Fatal(err)
		}
	}
	f.counter = core.NewUsageCounter(clock.NewFrozen(usageNow))
	f.reg = core.New(f.st, "t", []layer.Layer{
		{ID: "team", Visibility: layer.Visibility{Public: true}, Precedence: 1},
		{ID: "secret", Visibility: layer.Visibility{Users: []string{"bob"}}, Precedence: 2},
	}).WithUsageCounts(f.counter).WithAudit(func(_ context.Context, e core.AuditEvent) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.events = append(f.events, e)
	})
	return f
}

var (
	usageAlice = layer.Identity{Sub: "alice", IsAuthenticated: true}
	usageBob   = layer.Identity{Sub: "bob", IsAuthenticated: true}
)

func (f *usageFixture) rows(t *testing.T) []store.UsageCount {
	t.Helper()
	if err := f.counter.Flush(context.Background(), f.st); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	rows, err := f.st.ListUsageCounts(context.Background(), "t", "2026-01-01", "2026-12-31")
	if err != nil {
		t.Fatal(err)
	}
	for i := range rows {
		rows[i].TenantID = ""
	}
	return rows
}

func clientCtx(kind, harness string) context.Context {
	return core.ContextWithUsageClient(context.Background(), kind, harness)
}

// Spec: §8.7 — a resolved load counts one load under the request's client
// kind, harness, and serving layer, or one sync for a sync client; a request
// with no client value counts under none; an unresolved load counts nothing.
func TestUsageCounts_Loads(t *testing.T) {
	t.Parallel()
	f := newUsageFixture(t)
	for _, c := range []struct {
		ctx context.Context
		id  string
	}{
		{clientCtx("mcp", "claude-code"), "ops/greet"},
		{clientCtx("mcp", "claude-code"), "ops/greet"},
		{clientCtx("sync", "cursor"), "ops/greet"},
		{context.Background(), "ops/farewell"},
		{clientCtx("mcp", "claude-code"), "ops/missing"},
		{clientCtx("mcp", "claude-code"), "ops/hidden"},
	} {
		_, _ = f.reg.LoadArtifact(c.ctx, usageAlice, c.id, core.LoadArtifactOptions{})
	}
	want := []store.UsageCount{
		{Day: "2026-11-18", Layer: "team", ArtifactID: "ops/farewell", ClientKind: "none", Harness: "none", Loads: 1},
		{Day: "2026-11-18", Layer: "team", ArtifactID: "ops/greet", ClientKind: "mcp", Harness: "claude-code", Loads: 2},
		{Day: "2026-11-18", Layer: "team", ArtifactID: "ops/greet", ClientKind: "sync", Harness: "cursor", Syncs: 1},
	}
	if got := f.rows(t); !reflect.DeepEqual(got, want) {
		t.Errorf("rows:\n got %+v\nwant %+v", got, want)
	}
}

// Spec: §8.1, §8.7 — a search with a query counts one hit for each of its
// first 10 results and none for a later result; an empty query counts
// nothing; the artifacts.searched event carries every returned artifact and
// its layer in context.results, in result order, and a search with no result
// carries no results member.
func TestUsageCounts_SearchHits(t *testing.T) {
	t.Parallel()
	f := newUsageFixture(t)
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("bulk/a%02d", i)
		if err := f.st.PutManifest(ctx, store.ManifestRecord{TenantID: "t", ArtifactID: id, Version: "1.0.0", ContentHash: "sha256:b" + id, Type: "skill", Description: "zebra helper", Layer: "team"}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := f.reg.SearchArtifacts(clientCtx("sdk", ""), usageAlice, core.SearchArtifactsOptions{Query: "zebra", TopK: 12})
	if err != nil || len(res.Results) != 12 {
		t.Fatalf("SearchArtifacts: %v, %d results", err, len(res.Results))
	}
	if _, err := f.reg.SearchArtifacts(ctx, usageAlice, core.SearchArtifactsOptions{TopK: 50}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reg.SearchArtifacts(ctx, usageAlice, core.SearchArtifactsOptions{Query: "nothingmatches"}); err != nil {
		t.Fatal(err)
	}
	rows := f.rows(t)
	if len(rows) != 10 {
		t.Fatalf("search hit rows = %d, want 10: %+v", len(rows), rows)
	}
	counted := map[string]bool{}
	for _, r := range rows {
		if r.SearchHits != 1 || r.ClientKind != "sdk" || r.Harness != "none" || r.Layer != "team" {
			t.Errorf("row %+v, want one sdk search hit in team", r)
		}
		counted[r.ArtifactID] = true
	}
	for _, d := range res.Results[10:] {
		if counted[d.ID] {
			t.Errorf("result %s beyond the tenth counted a hit", d.ID)
		}
	}
	var searched []core.AuditEvent
	for _, e := range f.events {
		if e.Type == "artifacts.searched" {
			searched = append(searched, e)
		}
	}
	if len(searched) != 3 {
		t.Fatalf("artifacts.searched events = %d, want 3", len(searched))
	}
	var refs []struct {
		ArtifactID string `json:"artifact_id"`
		Layer      string `json:"layer"`
	}
	if err := json.Unmarshal([]byte(searched[0].Context["results"]), &refs); err != nil || len(refs) != 12 {
		t.Fatalf("results = %q (%v), want 12 entries", searched[0].Context["results"], err)
	}
	for i, d := range res.Results {
		if refs[i].ArtifactID != d.ID || refs[i].Layer != "team" {
			t.Errorf("results[%d] = %+v, want %s in team", i, refs[i], d.ID)
		}
	}
	if _, ok := searched[2].Context["results"]; ok {
		t.Errorf("a search with no result carries results: %q", searched[2].Context["results"])
	}
}

// failingUsageStore fails AddUsageCounts while failing is set.
type failingUsageStore struct {
	*store.Memory
	failing bool
}

func (s *failingUsageStore) AddUsageCounts(ctx context.Context, tenant string, counts []store.UsageCount) error {
	if s.failing {
		return errors.New("store unreachable")
	}
	return s.Memory.AddUsageCounts(ctx, tenant, counts)
}

// Spec: §8.7 — a flush that fails keeps its counts, merged with the counts
// recorded since, and the next flush that succeeds adds them all once.
func TestUsageCounter_FailedFlushKeepsCounts(t *testing.T) {
	t.Parallel()
	f := newUsageFixture(t)
	fs := &failingUsageStore{Memory: f.st, failing: true}
	ctx := clientCtx("mcp", "claude-code")
	_, _ = f.reg.LoadArtifact(ctx, usageAlice, "ops/greet", core.LoadArtifactOptions{})
	if err := f.counter.Flush(context.Background(), fs); err == nil {
		t.Fatal("Flush to a failing store returned nil")
	}
	_, _ = f.reg.LoadArtifact(ctx, usageAlice, "ops/greet", core.LoadArtifactOptions{})
	fs.failing = false
	if err := f.counter.Flush(context.Background(), fs); err != nil {
		t.Fatal(err)
	}
	if err := f.counter.Flush(context.Background(), fs); err != nil {
		t.Fatal(err)
	}
	rows, _ := f.st.ListUsageCounts(context.Background(), "t", "2026-11-18", "2026-11-18")
	if len(rows) != 1 || rows[0].Loads != 2 {
		t.Errorf("rows after a failed then a good flush = %+v, want one row with 2 loads", rows)
	}
}

// blockingUsageStore holds its first AddUsageCounts until release is closed
// and then fails it, as an add does when the shutdown cancels its context;
// later adds reach the store.
type blockingUsageStore struct {
	*store.Memory
	entered, release chan struct{}
	once             sync.Once
}

func (s *blockingUsageStore) AddUsageCounts(ctx context.Context, tenant string, counts []store.UsageCount) error {
	first := false
	s.once.Do(func() { first = true })
	if first {
		close(s.entered)
		<-s.release
		return context.Canceled
	}
	return s.Memory.AddUsageCounts(ctx, tenant, counts)
}

// Spec: §8.7 — a flush that starts while another is in progress waits for
// it, so the shutdown flush adds the counts that an interrupted periodic
// flush puts back when its add fails, rather than finding the buffer empty.
func TestUsageCounter_FlushWaitsForFlushInProgress(t *testing.T) {
	t.Parallel()
	f := newUsageFixture(t)
	bs := &blockingUsageStore{Memory: f.st, entered: make(chan struct{}), release: make(chan struct{})}
	_, _ = f.reg.LoadArtifact(clientCtx("mcp", "claude-code"), usageAlice, "ops/greet", core.LoadArtifactOptions{})
	periodic := make(chan error, 1)
	go func() { periodic <- f.counter.Flush(context.Background(), bs) }()
	<-bs.entered
	shutdown := make(chan error, 1)
	go func() { shutdown <- f.counter.Flush(context.Background(), bs) }()
	select {
	case err := <-shutdown:
		t.Fatalf("the second flush returned (%v) while the first was still adding", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(bs.release)
	if err := <-periodic; err == nil {
		t.Error("the interrupted flush returned nil")
	}
	if err := <-shutdown; err != nil {
		t.Fatal(err)
	}
	if rows := f.rows(t); len(rows) != 1 || rows[0].Loads != 1 {
		t.Errorf("rows = %+v, want the interrupted flush's load added by the second flush", rows)
	}
}

// Spec: §8.7 — counts flushed by two replicas that share a store add up.
func TestUsageCounter_TwoReplicasAdd(t *testing.T) {
	t.Parallel()
	f := newUsageFixture(t)
	other := core.NewUsageCounter(clock.NewFrozen(usageNow))
	second := core.New(f.st, "t", []layer.Layer{{ID: "team", Visibility: layer.Visibility{Public: true}, Precedence: 1}}).WithUsageCounts(other)
	ctx := clientCtx("mcp", "claude-code")
	_, _ = f.reg.LoadArtifact(ctx, usageAlice, "ops/greet", core.LoadArtifactOptions{})
	_, _ = second.LoadArtifact(ctx, usageAlice, "ops/greet", core.LoadArtifactOptions{})
	if err := other.Flush(context.Background(), f.st); err != nil {
		t.Fatal(err)
	}
	if rows := f.rows(t); len(rows) != 1 || rows[0].Loads != 2 {
		t.Errorf("rows = %+v, want one row with 2 loads", rows)
	}
}

// Spec: §8.7 — a nil counter records nothing and flushes without error.
func TestUsageCounter_Nil(t *testing.T) {
	t.Parallel()
	var u *core.UsageCounter
	if err := u.Flush(context.Background(), store.NewMemory()); err != nil {
		t.Errorf("nil Flush: %v", err)
	}
	reg := core.New(store.NewMemory(), "t", nil)
	_, _ = reg.LoadArtifact(context.Background(), publicID, "x", core.LoadArtifactOptions{})
}
