package serverboot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// Spec: §7.3.1, §13.12 — a registry started with the check on and no
// embedding source refuses to start with config.invalid naming
// PODIUM_LINT_SEMANTIC_OVERLAP; an embedding provider or a self-embedding
// backend is an embedding source, and the check off needs neither.
func TestSemanticOverlapGuard(t *testing.T) {
	t.Parallel()
	on := lint.SemanticConfig{Enabled: true}
	cases := []struct {
		name   string
		cfg    Config
		refuse bool
	}{
		{"off", Config{}, false},
		{"on with no provider", Config{semanticOverlap: on}, true},
		{"on with provider none", Config{semanticOverlap: on, embeddingProvider: "none"}, true},
		{"on with a provider", Config{semanticOverlap: on, embeddingProvider: "openai"}, false},
		{"on with a self-embedding backend", Config{semanticOverlap: on, vectorBackend: "pinecone", vectorInferenceModel: "llama-text-embed-v2"}, false},
	}
	for _, c := range cases {
		err := c.cfg.semanticOverlapGuard()
		if (err != nil) != c.refuse {
			t.Errorf("%s: err = %v, want refusal %v", c.name, err, c.refuse)
		}
		if err != nil && (!strings.HasPrefix(err.Error(), "config.invalid:") || !strings.Contains(err.Error(), "PODIUM_LINT_SEMANTIC_OVERLAP")) {
			t.Errorf("%s: message %q does not carry config.invalid and the variable", c.name, err)
		}
	}
}

// Spec: §7.3.1 — the refusal names the configured PODIUM_EMBEDDING_PROVIDER
// value, so a deployment that set none is not told the variable is empty.
func TestSemanticOverlapGuard_NamesTheProviderValue(t *testing.T) {
	t.Parallel()
	on := lint.SemanticConfig{Enabled: true}
	none := (&Config{semanticOverlap: on, embeddingProvider: "none"}).semanticOverlapGuard()
	empty := (&Config{semanticOverlap: on}).semanticOverlapGuard()
	if none == nil || !strings.Contains(none.Error(), `PODIUM_EMBEDDING_PROVIDER is "none"`) || strings.Contains(none.Error(), "is empty") {
		t.Errorf("refusal with none = %v, want it to name \"none\"", none)
	}
	if empty == nil || !strings.Contains(empty.Error(), `PODIUM_EMBEDDING_PROVIDER is ""`) {
		t.Errorf("refusal with an empty provider = %v, want it to name the empty value", empty)
	}
}

// Spec: §13.12 — LoadConfig reads the PODIUM_LINT_SEMANTIC_* variables, and
// validate applies the startup refusal.
func TestLoadConfig_SemanticOverlap(t *testing.T) {
	t.Setenv("PODIUM_LINT_SEMANTIC_OVERLAP", "on")
	t.Setenv("PODIUM_LINT_SEMANTIC_REFRESH_INTERVAL", "2h")
	t.Setenv("PODIUM_EMBEDDING_PROVIDER", "")
	cfg := LoadConfig()
	if !cfg.semanticOverlap.Enabled || cfg.semanticOverlap.RefreshInterval != 2*time.Hour {
		t.Fatalf("semanticOverlap = %+v", cfg.semanticOverlap)
	}
	cfg.embeddingProvider = ""
	cfg.vectorBackend = ""
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "PODIUM_LINT_SEMANTIC_OVERLAP") {
		t.Errorf("validate = %v, want the overlap refusal", err)
	}
}

// hostedIndex is a self-embedding managed backend fake over the memory
// backend: it embeds text with a word table and answers QueryText by cosine
// distance, which is what a managed index created with the cosine metric
// reports.
type hostedIndex struct {
	mem   *vector.Memory
	words map[string][]float32
}

func newHostedIndex() *hostedIndex {
	return &hostedIndex{mem: vector.NewMemory(4), words: map[string][]float32{
		"invoices": {1, 0, 0, 0},
		"payables": {0.99, 0.12, 0, 0},
	}}
}

func (h *hostedIndex) embed(text string) []float32 {
	for w, v := range h.words {
		if strings.Contains(text, w) {
			return v
		}
	}
	return []float32{0, 0, 0, 1}
}

func (*hostedIndex) ID() string          { return "pinecone" }
func (*hostedIndex) Dimensions() int     { return 0 }
func (*hostedIndex) Close() error        { return nil }
func (*hostedIndex) SelfEmbeds() bool    { return true }
func (*hostedIndex) HostedModel() string { return "hosted-1" }
func (h *hostedIndex) Put(ctx context.Context, t, id, ver string, v []float32) error {
	return h.mem.Put(ctx, t, id, ver, v)
}
func (h *hostedIndex) Query(ctx context.Context, t string, v []float32, k int) ([]vector.Match, error) {
	return h.mem.Query(ctx, t, v, k)
}
func (h *hostedIndex) Delete(ctx context.Context, t, id, ver string) error {
	return h.mem.Delete(ctx, t, id, ver)
}
func (h *hostedIndex) PutText(ctx context.Context, t, id, ver, text string) error {
	return h.mem.Put(ctx, t, id, ver, h.embed(text))
}
func (h *hostedIndex) QueryText(ctx context.Context, t, text string, k int) ([]vector.Match, error) {
	return h.mem.Query(ctx, t, h.embed(text), k)
}

// Spec: §7.3.1 — on a managed backend the ingest cycle computes nothing, and
// the computation runs when the outbox writes each vector, so two artifacts of
// one cycle find each other once both vectors are written. The self-embedding
// backend is queried with the projection text, and the report then names the
// two artifacts as peers.
func TestSemanticOverlap_OutboxWriteComputes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "reg.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	dir := t.TempDir()
	for id, desc := range map[string]string{"fin-invoices": "reconciles invoices", "fin-payables": "reconciles payables"} {
		if err := os.MkdirAll(filepath.Join(dir, id), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\ntype: agent\nversion: 1.0.0\ndescription: " + desc + "\nsensitivity: low\n---\n\nbody\n"
		if err := os.WriteFile(filepath.Join(dir, id, "ARTIFACT.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx := newHostedIndex()
	sem := lint.SemanticConfig{Enabled: true}
	if hook := buildSemanticOverlap(st, idx, nil, sem, true); hook != nil {
		t.Fatal("a managed backend got an ingest-time overlap hook")
	}
	lc := store.LayerConfig{TenantID: "default", ID: "team", SourceType: "local", LocalPath: dir, Public: true}
	if err := st.PutLayerConfig(ctx, lc); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	if _, err := ingest.SourceIngestWithOptions(ctx, st, source.Local{}, lc, ingest.SourceIngestOptions{UseVectorOutbox: true}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	reg := core.New(st, "default", nil).WithVectorSearch(idx, nil).WithOverlapChecks(lint.Thresholds{}, sem)
	w := &vectorDrainWorker{outbox: st, vec: idx, interval: time.Second, batch: 10, onWritten: overlapOutboxHook(reg)}
	w.runOnce(ctx, time.Now().UTC().Add(time.Minute))
	recs, err := st.ListOverlapRecords(ctx, "default")
	if err != nil || len(recs) != 2 || recs[0].Owner != "fin-invoices" || recs[1].Owner != "fin-payables" ||
		recs[0].ModelID != "hosted-1" || recs[0].Similarity < 0.98 {
		t.Fatalf("records after the outbox drain = %+v, %v; want each artifact to own one record naming the other", recs, err)
	}
	report, err := reg.CatalogOverlap(ctx, layer.Identity{IsPublic: true}, core.OverlapQuery{})
	if err != nil {
		t.Fatalf("CatalogOverlap: %v", err)
	}
	named := 0
	for _, d := range report.Diagnostics {
		if d.Code == lint.SemanticOverlapCode {
			named++
		}
	}
	if named != 2 {
		t.Errorf("report carries %d semantic diagnostics, want one per artifact: %+v", named, report.Diagnostics)
	}
	if overlapOutboxHook(core.New(st, "default", nil)) != nil {
		t.Error("the outbox hook is installed with the check off")
	}
}

// Spec: §7.3.1 — the ingest hook is absent with the check off or no search
// index, and on a collocated backend it computes and reports within the
// cycle.
func TestBuildSemanticOverlap(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if buildSemanticOverlap(st, vector.NewMemory(4), fakeEmbedder{dim: 4}, lint.SemanticConfig{}, false) != nil {
		t.Error("hook built with the check off")
	}
	if buildSemanticOverlap(st, nil, nil, lint.SemanticConfig{Enabled: true}, false) != nil {
		t.Error("hook built with no search index")
	}
	hook := buildSemanticOverlap(st, vector.NewMemory(4), fakeEmbedder{dim: 4}, lint.SemanticConfig{Enabled: true}, false)
	if hook == nil {
		t.Fatal("no hook on a collocated backend")
	}
	if diags := hook(context.Background(), "default", "team", []ingest.IngestedArtifact{{ArtifactID: "missing", Version: "1"}}); len(diags) != 0 {
		t.Errorf("hook reported %+v for an artifact that is not stored", diags)
	}
}

// Spec: §7.3.1 — a sweep pass runs in every active tenant and computes the
// artifacts with missing records; an inactive tenant is skipped.
func TestRunOverlapSweep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := store.NewMemory()
	vec := vector.NewMemory(8)
	reg := core.New(st, "", nil).WithVectorSearch(vec, fakeEmbedder{dim: 8}).WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{Enabled: true})
	for _, tenant := range []string{"acme", "globex"} {
		if err := st.CreateTenant(ctx, store.Tenant{ID: tenant}); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"a", "b"} {
			rec := store.ManifestRecord{TenantID: tenant, ArtifactID: id, Version: "1", ContentHash: "sha256:" + id, Type: "agent", Description: "d " + id}
			if err := st.PutManifest(ctx, rec); err != nil {
				t.Fatal(err)
			}
			if err := reg.ReembedOne(core.ContextWithTenant(ctx, tenant), id, "1"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := st.DeactivateTenant(ctx, "globex"); err != nil {
		t.Fatal(err)
	}
	runOverlapSweep(ctx, st, reg, periodicPassLabel)
	acme, _ := st.ListOverlapRecords(ctx, "acme")
	globex, _ := st.ListOverlapRecords(ctx, "globex")
	if len(acme) != 2 || len(globex) != 0 {
		t.Errorf("records acme=%d globex=%d, want 2 (one owned by each artifact) and 0 (globex inactive)", len(acme), len(globex))
	}
	// A registry with the check off logs the refusal per tenant and stores
	// nothing.
	runOverlapSweep(ctx, st, core.New(st, "", nil), periodicPassLabel)
	// A cancelled context stops the pass before it lists the tenants.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	runOverlapSweep(cancelled, st, reg, startupPassLabel)
}

// Spec: §7.3.1, §13.12 — the startup pass and the sweep start only with the
// check on, and the goroutine exits when the context ends.
func TestStartOverlapSweep_StopsWithContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reg := core.New(store.NewMemory(), "", nil)
	startOverlapSweep(ctx, lint.SemanticConfig{}, store.NewMemory(), reg)
	startOverlapSweep(ctx, lint.SemanticConfig{Enabled: true}, store.NewMemory(), reg)
	startOverlapSweep(ctx, lint.SemanticConfig{Enabled: true, RefreshInterval: time.Minute}, store.NewMemory(), reg)
}

// Spec: §7.3.1 — the loop runs the startup pass at once, then a periodic pass
// after the interval plus the offset and once per interval after that, and
// stops when the context ends, whether it is waiting for the first periodic
// pass or between passes. With the periodic sweep off it runs the startup
// pass alone and returns. It runs a goroutine and is -race clean.
func TestSweepLoop(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	passes := make(chan string, 8)
	done := make(chan struct{})
	go func() {
		sweepLoop(ctx, time.Millisecond, time.Millisecond, func(_ context.Context, label string) {
			select {
			case passes <- label:
			default:
			}
		})
		close(done)
	}()
	for i, want := range []string{startupPassLabel, periodicPassLabel, periodicPassLabel} {
		select {
		case got := <-passes:
			if got != want {
				t.Errorf("pass %d label = %q, want %q", i+1, got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("pass %d did not run", i+1)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sweepLoop did not return after cancellation")
	}

	var labels []string
	sweepLoop(context.Background(), 0, 0, func(_ context.Context, label string) { labels = append(labels, label) })
	if len(labels) != 1 || labels[0] != startupPassLabel {
		t.Errorf("with the periodic sweep off the loop ran %v, want the startup pass alone", labels)
	}

	waiting, stop := context.WithCancel(context.Background())
	returned := make(chan struct{})
	go func() {
		sweepLoop(waiting, time.Hour, 0, func(context.Context, string) {})
		close(returned)
	}()
	stop()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("sweepLoop did not return when cancelled before its first periodic pass")
	}
}

// gatedTenants is a store whose tenant listing, the first call of every pass,
// waits for release and counts each call, so a test can hold a pass in flight
// and count the passes. released is closed once.
type gatedTenants struct {
	store.Store
	release chan struct{}
	calls   atomic.Int64
}

func (g *gatedTenants) ListTenants(ctx context.Context) ([]store.Tenant, error) {
	g.calls.Add(1)
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return g.Store.ListTenants(ctx)
}

// sweepFixture stores two embedded artifacts in tenant acme and returns the
// store, a gated wrapper of it, and a registry over the gated store.
func sweepFixture(t *testing.T) (*store.Memory, *gatedTenants, *core.Registry) {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	gated := &gatedTenants{Store: st, release: make(chan struct{})}
	reg := core.New(gated, "", nil).WithVectorSearch(vector.NewMemory(8), fakeEmbedder{dim: 8}).WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{Enabled: true})
	if err := st.CreateTenant(ctx, store.Tenant{ID: "acme"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		rec := store.ManifestRecord{TenantID: "acme", ArtifactID: id, Version: "1", ContentHash: "sha256:" + id, Type: "agent", Description: "d " + id}
		if err := st.PutManifest(ctx, rec); err != nil {
			t.Fatal(err)
		}
		if err := reg.ReembedOne(core.ContextWithTenant(ctx, "acme"), id, "1"); err != nil {
			t.Fatal(err)
		}
	}
	return st, gated, reg
}

// waitComputations waits until tenant acme holds n computation entries.
func waitComputations(t *testing.T, st store.Store, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runs, _ := st.ListOverlapComputations(context.Background(), "acme"); len(runs) == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("tenant acme did not reach %d computation entries", n)
}

// Spec: §7.3.1, §13.12 — startOverlapSweep returns while the startup pass is
// still in flight, so readiness never waits on it. The startup pass runs at
// once with the periodic sweep on and with it off, computes every artifact
// whose computation is missing, and runs once: a periodic pass waits a whole
// interval. The goroutine exits on cancellation while the pass is held.
func TestStartOverlapSweep_StartupPassInBackground(t *testing.T) {
	t.Parallel()
	for _, interval := range []time.Duration{0, time.Hour} {
		st, gated, reg := sweepFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		returned := make(chan struct{})
		go func() {
			startOverlapSweep(ctx, lint.SemanticConfig{Enabled: true, RefreshInterval: interval}, gated, reg)
			close(returned)
		}()
		select {
		case <-returned:
		case <-time.After(5 * time.Second):
			t.Fatalf("interval %s: startOverlapSweep blocked on the startup pass", interval)
		}
		if runs, _ := st.ListOverlapComputations(ctx, "acme"); len(runs) != 0 {
			t.Errorf("interval %s: %d computations before the held pass was released", interval, len(runs))
		}
		close(gated.release)
		waitComputations(t, st, 2)
		time.Sleep(20 * time.Millisecond)
		if n := gated.calls.Load(); n != 1 {
			t.Errorf("interval %s: %d passes ran, want the startup pass alone", interval, n)
		}
		cancel()
	}

	_, held, reg := sweepFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	startOverlapSweep(ctx, lint.SemanticConfig{Enabled: true, RefreshInterval: time.Hour}, held, reg)
	deadline := time.Now().Add(5 * time.Second)
	for held.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if held.calls.Load() != 1 {
		t.Errorf("the held startup pass listed tenants %d times, want 1", held.calls.Load())
	}
}

// listingStore counts the full-tenant manifest listings a registry issues and
// fails them on demand, so a test can assert how often the served set is read.
type listingStore struct {
	store.Store
	listings atomic.Int64
	fail     atomic.Bool
}

func (s *listingStore) ListManifests(ctx context.Context, tenantID string) ([]store.ManifestRecord, error) {
	s.listings.Add(1)
	if s.fail.Load() {
		return nil, errors.New("store unreachable")
	}
	return s.Store.ListManifests(ctx, tenantID)
}

// outboxCycle ingests three artifacts through the §4.7.2 outbox into a fresh
// SQLite store and returns the store, the self-embedding index, and the
// registry over a listing-counting wrapper of the store.
func outboxCycle(t *testing.T) (*store.SQLite, *hostedIndex, *listingStore, *core.Registry) {
	t.Helper()
	st := outboxIngest(t)
	idx := newHostedIndex()
	counted := &listingStore{Store: st}
	reg := core.New(counted, "default", nil).WithVectorSearch(idx, nil).WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{Enabled: true})
	return st, idx, counted, reg
}

// outboxIngest ingests fin-invoices, fin-payables, and fin-ledger at 1.0.0
// through the §4.7.2 outbox into a fresh SQLite store, so each has a pending
// outbox row and no vector, and returns the store.
func outboxIngest(t *testing.T) *store.SQLite {
	t.Helper()
	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "reg.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	dir := t.TempDir()
	for id, desc := range map[string]string{"fin-invoices": "reconciles invoices", "fin-payables": "reconciles payables", "fin-ledger": "closes the ledger"} {
		if err := os.MkdirAll(filepath.Join(dir, id), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\ntype: agent\nversion: 1.0.0\ndescription: " + desc + "\nsensitivity: low\n---\n\nbody\n"
		if err := os.WriteFile(filepath.Join(dir, id, "ARTIFACT.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lc := store.LayerConfig{TenantID: "default", ID: "team", SourceType: "local", LocalPath: dir, Public: true}
	if err := st.PutLayerConfig(ctx, lc); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	if _, err := ingest.SourceIngestWithOptions(ctx, st, source.Local{}, lc, ingest.SourceIngestOptions{UseVectorOutbox: true}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	return st
}

// Spec: §7.3.1 — the outbox drain computes every artifact one pass wrote and
// reads the tenant's served set once to select the computations and once to
// confirm their versions before storing, rather than once per row.
func TestOverlapOutboxHook_OneListingPerPass(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, idx, counted, reg := outboxCycle(t)
	w := &vectorDrainWorker{outbox: st, vec: idx, interval: time.Second, batch: 10, onWritten: overlapOutboxHook(reg)}
	w.runOnce(ctx, time.Now().UTC().Add(time.Minute))
	runs, err := st.ListOverlapComputations(ctx, "default")
	if err != nil || len(runs) != 3 || counted.listings.Load() != 2 {
		t.Errorf("after one drain pass: %d computation entries (%v) and %d served-set listings, want 3 and 2", len(runs), err, counted.listings.Load())
	}
}

// Spec: §7.3.1 — a failed computation after the outbox write does not hold
// back the drain: every written row is already marked done, and the sweep
// computes the artifacts later.
func TestOverlapOutboxHook_FailureDoesNotBlockTheDrain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, idx, counted, reg := outboxCycle(t)
	counted.fail.Store(true)
	w := &vectorDrainWorker{outbox: st, vec: idx, interval: time.Second, batch: 10, onWritten: overlapOutboxHook(reg)}
	w.runOnce(ctx, time.Now().UTC().Add(time.Minute))
	depth, _, err := st.VectorOutboxStats(ctx)
	runs, _ := st.ListOverlapComputations(ctx, "default")
	if err != nil || depth != 0 || len(runs) != 0 {
		t.Errorf("outbox depth %d (%v) and %d computation entries after a failed computation, want 0 and 0", depth, err, len(runs))
	}
}

// Spec: §7.3.1, §13.12 — the first sweep waits a random delay of up to a
// tenth of PODIUM_LINT_SEMANTIC_REFRESH_INTERVAL, so the bound follows the
// configured interval and never exceeds it.
func TestFirstSweepDelay(t *testing.T) {
	t.Parallel()
	var bounds []int64
	top := func(n int64) int64 { bounds = append(bounds, n); return n - 1 }
	day := firstSweepDelay(24*time.Hour, top)
	minute := firstSweepDelay(time.Minute, top)
	tiny := firstSweepDelay(time.Nanosecond, top)
	if day != 144*time.Minute-1 || minute != 6*time.Second-1 || tiny != 0 {
		t.Errorf("delays = %s, %s, %s; want just under 144m, just under 6s, and 0", day, minute, tiny)
	}
	if bounds[0] != int64(144*time.Minute) || bounds[1] != int64(6*time.Second) || bounds[2] != 1 {
		t.Errorf("bounds = %v, want a tenth of each interval and at least 1", bounds)
	}
}
