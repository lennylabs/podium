package serverboot

// White-box integration test for the §4.7.2 transactional vector outbox under a
// bulk-ingest burst, closing G-VEC-9 (TEST-GAPS.md). The unit tests in
// vector_outbox_test.go assert the publishStats lagging transition fires once on
// a single queued row with lagDepth=1, and test/integration/
// vector_outbox_consistency_test.go drives the SPI to a consistent state with no
// lost or duplicated vectors. Neither ingests a burst large enough to cross a
// realistic PODIUM_VECTOR_OUTBOX_LAG_DEPTH and observe the vector.outbox_lagging
// audit event end to end, then drains to consistency and asserts semantic search
// ranks the expected artifact first.
//
// This test runs the real vectorDrainWorker (the production drain + publishStats
// + lag callback) and the real vectorOutboxLaggingCallback wired to a FileSink
// audit sink, so the lag signal is observed through the recorded audit event
// rather than a stub. It ingests 320 artifacts through the outbox path, fails
// every backend write on the first drain pass so the depth stays above the lag
// threshold (the signal fires), then recovers the backend and drains to depth 0,
// and finally asserts a core.Registry wired with the drained backend ranks the
// target artifact first with no degraded flag.
//
// It uses the in-memory store + backend so it is deterministic and runs on the
// PR lane with no external infrastructure; the outbox lag machinery is
// backend-agnostic, so the in-process backend exercises the same code path a
// managed backend would.

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// burstBagEmbedder projects text into a dim-element bag-of-words vector so the
// target description and a matching query land near each other under cosine
// distance, making the drained semantic search deterministic.
type burstBagEmbedder struct{ dim int }

func (burstBagEmbedder) ID() string        { return "burst-bag" }
func (burstBagEmbedder) Model() string     { return "burst-bag" }
func (e burstBagEmbedder) Dimensions() int { return e.dim }
func (e burstBagEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, e.dim)
		for _, f := range strings.Fields(strings.ToLower(t)) {
			h := 0
			for _, r := range f {
				h = h*31 + int(r)
			}
			if h < 0 {
				h = -h
			}
			v[h%e.dim]++
		}
		out[i] = v
	}
	return out, nil
}

// recoverableVector wraps an in-memory backend and fails every Put while broken
// is set, so a drain pass during the outage leaves the outbox depth unchanged
// (the lag signal can then fire). Clearing broken lets the backend accept
// writes. It records landed (id@version) so the consistency assertion can check
// no duplication.
type recoverableVector struct {
	mu     sync.Mutex
	inner  *vector.Memory
	broken bool
	landed map[string]int
}

func newRecoverableVector(dim int) *recoverableVector {
	return &recoverableVector{inner: vector.NewMemory(dim), landed: map[string]int{}}
}

func (r *recoverableVector) ID() string      { return "recoverable-memory" }
func (r *recoverableVector) Dimensions() int { return r.inner.Dimensions() }
func (r *recoverableVector) Close() error    { return r.inner.Close() }
func (r *recoverableVector) Delete(ctx context.Context, t, id, v string) error {
	return r.inner.Delete(ctx, t, id, v)
}
func (r *recoverableVector) Query(ctx context.Context, t string, v []float32, k int) ([]vector.Match, error) {
	return r.inner.Query(ctx, t, v, k)
}
func (r *recoverableVector) Put(ctx context.Context, t, id, ver string, v []float32) error {
	r.mu.Lock()
	if r.broken {
		r.mu.Unlock()
		return vector.ErrUnreachable
	}
	r.mu.Unlock()
	if err := r.inner.Put(ctx, t, id, ver, v); err != nil {
		return err
	}
	r.mu.Lock()
	r.landed[id+"@"+ver]++
	r.mu.Unlock()
	return nil
}
func (r *recoverableVector) setBroken(b bool) {
	r.mu.Lock()
	r.broken = b
	r.mu.Unlock()
}
func (r *recoverableVector) maxLanded() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := 0
	for _, n := range r.landed {
		if n > m {
			m = n
		}
	}
	return m
}

// readAuditEvents returns the decoded event "type" fields and the full file
// contents for a FileSink log path.
func readAuditEventTypes(t *testing.T, path string) (string, int) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	body := string(b)
	count := strings.Count(body, `"vector.outbox_lagging"`)
	return body, count
}

// TestVectorOutbox_LagSignalUnderBurstThenDrains closes G-VEC-9. It ingests a
// burst of 320 artifacts through the §4.7.2 outbox, fails the first drain pass so
// the depth stays above the configured lag threshold and the real
// vector.outbox_lagging audit event is recorded, then recovers the backend and
// drains to depth 0, and asserts the drained backend ranks the target artifact
// first through the registry with no degraded flag.
//
// Spec: §4.7.2 ("Operators monitor outbox depth via a Prometheus gauge; a
// vector.outbox_lagging event fires when depth or oldest-row age exceeds an
// operator-configured threshold"; the outbox drains to consistency once the
// backend recovers with no lost or duplicated vectors).
func TestVectorOutbox_LagSignalUnderBurstThenDrains(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const dim = 32
	const burst = 320
	const lagDepth = 100 // 320 queued rows cross this on the first failed pass

	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	ob, ok := store.Store(st).(store.VectorOutbox)
	if !ok {
		t.Fatal("memory store does not implement store.VectorOutbox")
	}

	emb := burstBagEmbedder{dim: dim}
	vec := newRecoverableVector(dim)

	// Ingest the burst through the outbox: each manifest commits with a
	// vector_pending row and no inline vector write. One artifact carries the
	// discriminating "invoice reconciliation" description that the consistency
	// query matches; the rest are filler so the queue depth crosses the lag
	// threshold.
	embedOne := func(c context.Context, text string) ([]float32, error) {
		vs, e := emb.Embed(c, []string{text})
		if e != nil {
			return nil, e
		}
		return vs[0], nil
	}
	res, err := ingest.Ingest(ctx, st, ingest.Request{
		TenantID:  "default",
		LayerID:   "L",
		Files:     burstFixture(burst),
		Embedder:  embedOne,
		VectorPut: func(c context.Context, tid, id, ver string, v []float32) error { return vec.Put(c, tid, id, ver, v) },
		DomainVectorPut: func(c context.Context, tid, p string, v []float32) error {
			return vec.Put(c, tid, p, core.DomainVectorVersion, v)
		},
		UseVectorOutbox: true,
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted < burst {
		t.Fatalf("ingest accepted %d artifacts, want >= %d", res.Accepted, burst)
	}
	depth0, _, err := ob.VectorOutboxStats(ctx)
	if err != nil {
		t.Fatalf("VectorOutboxStats: %v", err)
	}
	if depth0 < lagDepth {
		t.Fatalf("outbox depth after the burst = %d, want >= %d so the lag threshold is crossable", depth0, lagDepth)
	}
	if got := vec.maxLanded(); got != 0 {
		t.Fatalf("vector landed %d times at ingest; the outbox path defers all writes", got)
	}

	// Real audit sink + the production lagging callback, so the signal is the
	// recorded vector.outbox_lagging event rather than a stub.
	logPath := t.TempDir() + "/audit.log"
	sink, err := audit.NewFileSink(logPath)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	w := &vectorDrainWorker{
		outbox:    ob,
		vec:       vec,
		embedder:  emb,
		interval:  time.Second,
		batch:     500, // a single pass covers the whole burst
		lagDepth:  lagDepth,
		onLagging: vectorOutboxLaggingCallback(sink, "default"),
	}

	// First pass with the backend broken: every write fails, the depth stays at
	// the burst size, and publishStats observes depth >= lagDepth and fires the
	// lag event exactly once on the transition.
	vec.setBroken(true)
	now := time.Now().UTC()
	w.runOnce(ctx, now)

	depthAfterFail, _, _ := ob.VectorOutboxStats(ctx)
	if depthAfterFail < lagDepth {
		t.Fatalf("depth after the failed pass = %d, want still >= %d (writes must have failed)", depthAfterFail, lagDepth)
	}
	body, lagEvents := readAuditEventTypes(t, logPath)
	if lagEvents != 1 {
		t.Fatalf("vector.outbox_lagging events recorded = %d, want 1\naudit log:\n%s", lagEvents, body)
	}
	// The recorded event carries the observed depth so operators can alert on it.
	if !strings.Contains(body, fmt.Sprintf(`"depth":"%d"`, depthAfterFail)) {
		t.Errorf("lagging event missing depth=%d in context\naudit log:\n%s", depthAfterFail, body)
	}

	// Recover the backend and drain to consistency. The failed rows are hidden by
	// backoff, so advance the clock past each backoff until the outbox empties.
	vec.setBroken(false)
	const maxPasses = 16
	drained := false
	for pass := 0; pass < maxPasses; pass++ {
		now = now.Add(time.Hour) // past any backoff
		w.runOnce(ctx, now)
		depth, _, _ := ob.VectorOutboxStats(ctx)
		if depth == 0 {
			drained = true
			break
		}
	}
	if !drained {
		t.Fatalf("outbox did not drain to 0 within %d passes", maxPasses)
	}
	// Consistency: every vector landed exactly once (no duplication) despite the
	// retried failures.
	if got := vec.maxLanded(); got != 1 {
		t.Fatalf("a vector landed %d times, want exactly 1 (no duplication across retries)", got)
	}
	// The lag event must not re-fire after the depth recovers below the
	// threshold; it stays a single transition event.
	if _, lagEvents := readAuditEventTypes(t, logPath); lagEvents != 1 {
		t.Errorf("vector.outbox_lagging fired %d times across the run, want 1 (transition only)", lagEvents)
	}

	// Observe consistency through the registry: with every vector landed and a
	// configured backend, search is not degraded and the discriminating artifact
	// ranks first for its matching query.
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Visibility: layer.Visibility{Public: true}, Precedence: 1},
	}).WithVectorSearch(vec, emb)
	sr, err := reg.SearchArtifacts(ctx, layer.Identity{IsPublic: true}, core.SearchArtifactsOptions{
		Query: "invoice reconciliation vendor payments", TopK: 10,
	})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if sr.Degraded {
		t.Errorf("search Degraded = true after the outbox drained; the backend and embedder are configured")
	}
	if len(sr.Results) == 0 || sr.Results[0].ID != "finance/reconcile" {
		t.Fatalf("search results = %+v, want finance/reconcile first", sr.Results)
	}
}

// burstFixture returns an fs.FS with one discriminating target artifact plus
// n-1 filler context artifacts, so an outbox ingest queues n vector_pending
// rows. The target's description matches the consistency query.
func burstFixture(n int) fs.FS {
	m := fstest.MapFS{
		"finance/reconcile/ARTIFACT.md": &fstest.MapFile{Data: []byte("---\ntype: context\nversion: 1.0.0\ndescription: invoice reconciliation matching vendor payments against purchase orders\n---\n\ninvoice reconciliation body.\n")},
	}
	for i := 0; i < n-1; i++ {
		path := fmt.Sprintf("filler/item-%03d/ARTIFACT.md", i)
		data := fmt.Sprintf("---\ntype: context\nversion: 1.0.0\ndescription: filler artifact number %d unrelated topic\n---\n\nfiller %d body.\n", i, i)
		m[path] = &fstest.MapFile{Data: []byte(data)}
	}
	return m
}
