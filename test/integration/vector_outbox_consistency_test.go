package integration

// Transactional-outbox consistency under a transient backend failure, closing
// G-VEC-6 (TEST-GAPS.md). For a non-collocated vector backend the registry
// commits the manifest and a vector_pending row in one transaction, and a
// background worker drives the vector write with exponential-backoff retry.
// This test exercises that path in-process with a deterministic fault-injecting
// vector.Provider that fails a transient number of writes and then succeeds,
// and asserts the outbox retries to a consistent state with no lost or
// duplicated vectors.
//
// The drain worker that the server runs (internal/serverboot.vectorDrainWorker)
// is unexported, so this test drives the public store.VectorOutbox SPI through
// the same contract the worker implements: list eligible rows, attempt the
// backend write, mark done on success or retry with backoff on failure. The
// consistency is then observed through the registry's search surface
// (core.SearchArtifacts over an in-process core.Registry wired with the same
// fault provider), so the assertion covers the registry path rather than only
// the raw provider.
//
// A live-backend variant of this scenario runs under the Release lane: the same
// outbox flow against a managed vector backend with an injected transient error
// gates on PODIUM_LIVE_EXTERNAL=1 plus that backend's credentials. This
// in-process test is the deterministic core; it runs on every lane with no
// credentials.

import (
	"context"
	"io/fs"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/pkg/embedding"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// flakyVector wraps an in-memory vector backend and fails the first failPuts
// calls to Put with vector.ErrUnreachable before delegating to the wrapped
// store. It records every (artifact, version) that lands so the test can assert
// the vector was written exactly once with no duplication. It is concurrency-
// safe because the drain contract and the registry both call it.
type flakyVector struct {
	mu       sync.Mutex
	inner    *vector.Memory
	failPuts int      // remaining Puts to fail before succeeding
	putCalls int      // total Put attempts (including the failed ones)
	landed   []string // "id@version" for each Put that reached the backend
}

func newFlakyVector(dim, failPuts int) *flakyVector {
	return &flakyVector{inner: vector.NewMemory(dim), failPuts: failPuts}
}

func (f *flakyVector) ID() string      { return "flaky-memory" }
func (f *flakyVector) Dimensions() int { return f.inner.Dimensions() }
func (f *flakyVector) Close() error    { return f.inner.Close() }
func (f *flakyVector) Delete(ctx context.Context, tenantID, id, ver string) error {
	return f.inner.Delete(ctx, tenantID, id, ver)
}

func (f *flakyVector) Put(ctx context.Context, tenantID, id, ver string, vec []float32) error {
	f.mu.Lock()
	f.putCalls++
	if f.failPuts > 0 {
		f.failPuts--
		f.mu.Unlock()
		return vector.ErrUnreachable
	}
	f.mu.Unlock()
	if err := f.inner.Put(ctx, tenantID, id, ver, vec); err != nil {
		return err
	}
	f.mu.Lock()
	f.landed = append(f.landed, id+"@"+ver)
	f.mu.Unlock()
	return nil
}

func (f *flakyVector) Query(ctx context.Context, tenantID string, vec []float32, topK int) ([]vector.Match, error) {
	return f.inner.Query(ctx, tenantID, vec, topK)
}

// landedCount returns how many vectors reached the backend.
func (f *flakyVector) landedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.landed)
}

// putAttempts returns the total number of Put calls, including failed ones.
func (f *flakyVector) putAttempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.putCalls
}

// outboxEmbedder embeds the composed outbox text deterministically so the drain
// loop produces a vector the backend can store and the registry can query
// against. It mirrors bagEmbedder; a separate type avoids depending on
// bagEmbedder's dimension.
type outboxEmbedder struct{ dim int }

func (outboxEmbedder) ID() string        { return "outbox" }
func (outboxEmbedder) Model() string     { return "outbox" }
func (e outboxEmbedder) Dimensions() int { return e.dim }
func (e outboxEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return bagEmbedder{dim: e.dim}.Embed(ctx, texts)
}

// drainOutboxOnce mirrors internal/serverboot.vectorDrainWorker.runOnce against
// the public SPI: it lists eligible rows at `now`, embeds and writes each to the
// backend, marks done on success, and on failure marks a retry at now+backoff.
// backoffFor doubles the base interval per prior attempt (the worker's policy)
// so a deterministic clock advance re-exposes the row. It returns the number of
// rows attempted.
func drainOutboxOnce(t *testing.T, ctx context.Context, ob store.VectorOutbox, vec vector.Provider, emb embedding.Provider, now time.Time, base time.Duration) int {
	t.Helper()
	rows, err := ob.ListVectorPending(ctx, 50, now)
	if err != nil {
		t.Fatalf("ListVectorPending: %v", err)
	}
	for _, p := range rows {
		err := drainOneRow(ctx, vec, emb, p)
		if err != nil {
			backoff := backoffFor(base, p.Attempts)
			if e := ob.MarkVectorPendingRetry(ctx, p.TenantID, p.ArtifactID, p.Version, now.Add(backoff), err.Error()); e != nil {
				t.Fatalf("MarkVectorPendingRetry: %v", e)
			}
			continue
		}
		if e := ob.MarkVectorPendingDone(ctx, p.TenantID, p.ArtifactID, p.Version); e != nil {
			t.Fatalf("MarkVectorPendingDone: %v", e)
		}
	}
	return len(rows)
}

// drainOneRow embeds the pending text and writes the vector, mirroring the
// worker's storage-only branch (the flaky backend does not self-embed).
func drainOneRow(ctx context.Context, vec vector.Provider, emb embedding.Provider, p store.VectorPending) error {
	vecs, err := emb.Embed(ctx, []string{p.Text})
	if err != nil {
		return err
	}
	return vec.Put(ctx, p.TenantID, p.ArtifactID, p.Version, vecs[0])
}

// backoffFor returns base doubled `attempts` times, capped at one hour, the same
// policy as the server's drain worker.
func backoffFor(base time.Duration, attempts int) time.Duration {
	d := base
	for i := 0; i < attempts && d < time.Hour; i++ {
		d *= 2
	}
	if d > time.Hour {
		d = time.Hour
	}
	return d
}

// Spec: §4.7 (Registry as a Service — "Dual-write semantics for external vector
// backends": the transactional outbox commits the manifest and a vector_pending
// row in one transaction, a background worker drains the backend with
// exponential-backoff retry, and ingest never blocks on the external service so
// the vector lands without loss or duplication once the backend recovers.)
func TestVectorOutbox_RetriesTransientFailureToConsistentState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const dim = 32

	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "acme"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	ob, ok := store.Store(st).(store.VectorOutbox)
	if !ok {
		t.Fatal("memory store does not implement store.VectorOutbox")
	}

	// The backend fails its first two writes (transient outage), then recovers.
	failPuts := 2
	vec := newFlakyVector(dim, failPuts)
	emb := outboxEmbedder{dim: dim}

	// Ingest through the §4.7.2 outbox: the manifest and the vector_pending row
	// commit together and ingest does NOT write the vector inline. The artifact
	// is BM25-discoverable immediately; its vector lands when the worker drains.
	embedOne := func(c context.Context, text string) ([]float32, error) {
		vs, e := emb.Embed(c, []string{text})
		if e != nil {
			return nil, e
		}
		return vs[0], nil
	}
	res, err := ingest.Ingest(ctx, st, ingest.Request{
		TenantID: "acme",
		LayerID:  "L",
		Files:    vectorOutboxFixture(),
		Embedder: embedOne,
		VectorPut: func(c context.Context, tid, id, ver string, v []float32) error {
			return vec.Put(c, tid, id, ver, v)
		},
		DomainVectorPut: func(c context.Context, tid, path string, v []float32) error {
			return vec.Put(c, tid, path, core.DomainVectorVersion, v)
		},
		UseVectorOutbox: true,
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted == 0 {
		t.Fatalf("ingest accepted 0 artifacts: %+v", res)
	}

	// The outbox path must not have written the vector inline at ingest.
	if got := vec.putAttempts(); got != 0 {
		t.Fatalf("vector Put attempted %d times at ingest; the outbox path must defer all writes", got)
	}
	depth0, _, err := ob.VectorOutboxStats(ctx)
	if err != nil {
		t.Fatalf("VectorOutboxStats: %v", err)
	}
	if depth0 == 0 {
		t.Fatal("outbox depth is 0 after an outbox ingest; expected a queued vector_pending row")
	}

	// Drain with a deterministic clock. The first failPuts passes fail the write
	// (the row stays queued, hidden by backoff); advancing past each backoff
	// re-exposes it until the backend recovers and the vector lands.
	//
	// Anchor the clock to the same wall-clock origin ingest used to stamp the
	// row's NextRetryAt (commitManifest stamps time.Now().UTC()). A fixed start
	// date would make the row ineligible whenever the real UTC hour is later than
	// the pinned date plus its per-pass advances, which is a wall-clock-dependent
	// flake. The +1h advance and backoff math below remain fully deterministic;
	// only the origin tracks real time so the row is eligible from the first pass.
	base := time.Second
	now := time.Now().UTC()
	const maxPasses = 12
	drained := false
	for pass := 0; pass < maxPasses; pass++ {
		drainOutboxOnce(t, ctx, ob, vec, emb, now, base)
		depth, _, err := ob.VectorOutboxStats(ctx)
		if err != nil {
			t.Fatalf("VectorOutboxStats: %v", err)
		}
		if depth == 0 {
			drained = true
			break
		}
		// Advance the clock past the row's current backoff so the next pass sees
		// it. The longest single backoff here is base<<failPuts, well under the
		// jump below, so the row is always eligible on the following pass.
		now = now.Add(time.Hour)
	}
	if !drained {
		t.Fatalf("outbox did not drain to depth 0 within %d passes", maxPasses)
	}

	// Consistency: the backend recovered, so it took more than `failPuts` Put
	// attempts, but the vector landed exactly once (no duplication, no loss).
	if got := vec.putAttempts(); got <= failPuts {
		t.Errorf("Put attempts = %d, want > %d (the transient failures plus one success)", got, failPuts)
	}
	if got := vec.landedCount(); got != 1 {
		t.Fatalf("vectors landed = %d, want exactly 1 (no lost or duplicated vectors)", got)
	}

	// A second drain after the outbox is empty must be a no-op: no rows to
	// attempt and no additional vector written.
	landedBefore := vec.landedCount()
	if n := drainOutboxOnce(t, ctx, ob, vec, emb, now.Add(time.Hour), base); n != 0 {
		t.Errorf("drain after empty outbox attempted %d rows, want 0", n)
	}
	if vec.landedCount() != landedBefore {
		t.Errorf("a no-op drain wrote another vector: landed %d, was %d", vec.landedCount(), landedBefore)
	}

	// Observe the consistent state through the registry surface: with the vector
	// landed and a configured backend, search is not degraded and the artifact
	// is retrievable by a semantic query that matches its description.
	reg := core.New(st, "acme", []layer.Layer{
		{ID: "L", Visibility: layer.Visibility{Public: true}, Precedence: 1},
	}).WithVectorSearch(vec, emb)
	id := layer.Identity{IsPublic: true}

	sr, err := reg.SearchArtifacts(ctx, id, core.SearchArtifactsOptions{Query: "invoice reconciliation", TopK: 10})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if sr.Degraded {
		t.Errorf("search Degraded = true after the vector landed; vector backend and embedder are configured")
	}
	if len(sr.Results) == 0 || sr.Results[0].ID != "finance/reconcile" {
		t.Fatalf("search results = %+v, want finance/reconcile first", sr.Results)
	}
}

// vectorOutboxFixture returns one context artifact whose description drives both
// its outbox embedding text (composeEmbeddingText projects frontmatter only)
// and the semantic query match.
func vectorOutboxFixture() fs.FS {
	const art = "---\ntype: context\nversion: 1.0.0\ndescription: invoice reconciliation matching vendor payments against purchase orders\n---\n\ninvoice reconciliation body.\n"
	return fstest.MapFS{
		"finance/reconcile/ARTIFACT.md": &fstest.MapFile{Data: []byte(art)},
	}
}
