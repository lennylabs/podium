package serverboot

import (
	"context"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// writtenOnlyIndex is a storage-only managed backend whose stored-vector
// read never finds the row, as an eventually consistent backend answers
// right after a write, and counts the reads.
type writtenOnlyIndex struct {
	*vector.Memory
	reads atomic.Int64
}

func (*writtenOnlyIndex) ID() string { return "pinecone" }

func (w *writtenOnlyIndex) StoredVector(context.Context, string, string, string) ([]float32, string, error) {
	w.reads.Add(1)
	return nil, "", vector.ErrNotFound
}

// laggingIndex is the self-embedding hostedIndex with a stored-vector read
// that answers no row while hidden is set, and counts the reads.
type laggingIndex struct {
	*hostedIndex
	hidden atomic.Bool
	reads  atomic.Int64
}

func (l *laggingIndex) StoredVector(ctx context.Context, tenant, id, version string) ([]float32, string, error) {
	l.reads.Add(1)
	if l.hidden.Load() {
		return nil, "", vector.ErrNotFound
	}
	return l.mem.StoredVector(ctx, tenant, id, version)
}

// captureLogs sends the standard logger to a buffer for the rest of the
// test. A test that calls it does not run in parallel, so the logger's
// output is restored before the parallel tests resume.
func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return buf
}

// laggingOutbox ingests the three fin artifacts through the outbox and
// returns the store, a hidden laggingIndex, the outbox overlap hook over a
// registry on that index, and a drain worker that runs the hook.
func laggingOutbox(t *testing.T) (*store.SQLite, *laggingIndex, *overlapOutbox, *vectorDrainWorker) {
	t.Helper()
	st := outboxIngest(t)
	idx := &laggingIndex{hostedIndex: newHostedIndex()}
	idx.hidden.Store(true)
	reg := core.New(st, "default", nil).WithVectorSearch(idx, nil).WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{Enabled: true})
	hook := newOverlapOutbox(reg)
	w := &vectorDrainWorker{outbox: st, vec: idx, interval: time.Second, batch: 10, onWritten: hook.afterPass}
	return st, idx, hook, w
}

// retryCount returns the number of computations the hook will retry.
func retryCount(o *overlapOutbox) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.retries)
}

// computations returns the number of computation entries of the default
// tenant.
func computations(t *testing.T, st store.Store) int {
	t.Helper()
	runs, err := st.ListOverlapComputations(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListOverlapComputations: %v", err)
	}
	return len(runs)
}

// Spec: §7.3.1 — on a managed storage-only backend the outbox-write
// computation queries with the vector the outbox row carries, so it reads no
// stored vector and computes even when the backend does not return the write
// yet.
func TestOverlapOutboxHook_QueriesWithTheWrittenVector(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := outboxIngest(t)
	idx := &writtenOnlyIndex{Memory: vector.NewMemory(4)}
	reg := core.New(st, "default", nil).WithVectorSearch(idx, wordEmbedder{}).WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{Enabled: true})
	w := &vectorDrainWorker{outbox: st, vec: idx, embedder: wordEmbedder{}, interval: time.Second, batch: 10, onWritten: overlapOutboxHook(reg)}
	w.runOnce(ctx, time.Now().UTC().Add(time.Minute))
	if n := computations(t, st); n != 3 || idx.reads.Load() != 0 {
		t.Fatalf("after one drain pass: %d computation entries and %d stored-vector reads, want 3 and 0", n, idx.reads.Load())
	}
	recs, err := st.ListOverlapRecords(ctx, "default")
	if err != nil {
		t.Fatalf("ListOverlapRecords: %v", err)
	}
	found := false
	for _, r := range recs {
		if r.Owner == "fin-invoices" && r.Peer == "fin-payables" && r.Similarity > 0.98 && r.ModelID == "word-table" {
			found = true
		}
	}
	if !found {
		t.Errorf("records %+v carry no fin-invoices record naming fin-payables under word-table", recs)
	}
}

// Spec: §7.3.1 — on a self-embedding backend the outbox-write computation
// reads the stored vector. A read the backend answers with no row is not
// logged as a failure and is retried on the outbox's next passes, even a pass
// that writes nothing, and the retry computes the artifact once the backend
// returns the vector.
func TestOverlapOutboxHook_RetriesAMissingStoredVector(t *testing.T) {
	logs := captureLogs(t)
	ctx := context.Background()
	st, idx, hook, w := laggingOutbox(t)
	now := time.Now().UTC().Add(time.Minute)

	w.runOnce(ctx, now)
	if n := computations(t, st); n != 0 || retryCount(hook) != 3 || idx.reads.Load() != 3 {
		t.Fatalf("after the write pass: %d entries, %d retries, %d reads; want 0, 3, and 3", n, retryCount(hook), idx.reads.Load())
	}
	w.runOnce(ctx, now)
	if retryCount(hook) != 3 || idx.reads.Load() != 6 {
		t.Fatalf("after the first retry: %d retries, %d reads; want 3 and 6", retryCount(hook), idx.reads.Load())
	}
	idx.hidden.Store(false)
	w.runOnce(ctx, now)
	if n := computations(t, st); n != 3 || retryCount(hook) != 0 {
		t.Errorf("after the vectors appeared: %d entries, %d retries; want 3 and 0", n, retryCount(hook))
	}
	if strings.Contains(logs.String(), "stored-vector read") {
		t.Errorf("a retried read was logged as a failure:\n%s", logs.String())
	}
}

// Spec: §7.3.1 — a stored vector the backend still does not return after the
// outbox's next five passes is dropped, logged once, and left to the sweep;
// a later pass reads nothing for it.
func TestOverlapOutboxHook_DropsAMissAfterFivePasses(t *testing.T) {
	logs := captureLogs(t)
	ctx := context.Background()
	st, idx, hook, w := laggingOutbox(t)
	now := time.Now().UTC().Add(time.Minute)

	w.runOnce(ctx, now)
	for pass := 1; pass < overlapReadRetryPasses; pass++ {
		w.runOnce(ctx, now)
	}
	if retryCount(hook) != 3 {
		t.Fatalf("after %d retry passes: %d retries, want 3", overlapReadRetryPasses-1, retryCount(hook))
	}
	w.runOnce(ctx, now)
	reads := idx.reads.Load()
	if retryCount(hook) != 0 || reads != int64(3*(1+overlapReadRetryPasses)) {
		t.Fatalf("after %d retry passes: %d retries and %d reads, want 0 and %d", overlapReadRetryPasses, retryCount(hook), reads, 3*(1+overlapReadRetryPasses))
	}
	w.runOnce(ctx, now)
	if idx.reads.Load() != reads || computations(t, st) != 0 {
		t.Errorf("a pass after the last retry read %d vectors and stored %d entries, want 0 and 0", idx.reads.Load()-reads, computations(t, st))
	}
	if n := strings.Count(logs.String(), "found no row after 5 outbox passes; the sweep computes it"); n != 3 {
		t.Errorf("the log carries %d dropped retries, want 3:\n%s", n, logs.String())
	}
}

// Spec: §7.3.1 — an outbox write of an artifact replaces the retry of its
// earlier version with a fresh one, and a retry whose version is no longer
// served is dropped.
func TestOverlapOutboxHook_NewerVersionReplacesRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, _, hook, _ := laggingOutbox(t)
	hook.retries[overlapRetryKey{tenant: "default", artifactID: "fin-invoices"}] = overlapRetry{version: "0.9.0", passesLeft: 2}
	hook.retries[overlapRetryKey{tenant: "default", artifactID: "fin-ledger"}] = overlapRetry{version: "0.5.0", passesLeft: 2}
	hook.afterPass(ctx, []outboxWrite{{row: store.VectorPending{TenantID: "default", ArtifactID: "fin-invoices", Version: "1.0.0"}}})
	want := map[overlapRetryKey]overlapRetry{
		{tenant: "default", artifactID: "fin-invoices"}: {version: "1.0.0", passesLeft: overlapReadRetryPasses},
	}
	if len(hook.retries) != len(want) || hook.retries[overlapRetryKey{tenant: "default", artifactID: "fin-invoices"}] != want[overlapRetryKey{tenant: "default", artifactID: "fin-invoices"}] {
		t.Errorf("retries = %+v, want only fin-invoices@1.0.0 with %d passes", hook.retries, overlapReadRetryPasses)
	}
}

// Spec: §7.3.1 — passes that run the hook at once keep the retry set
// consistent and race-free under -race: it holds one retry per artifact.
func TestOverlapOutboxHook_ConcurrentPasses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, _, hook, _ := laggingOutbox(t)
	written := []outboxWrite{
		{row: store.VectorPending{TenantID: "default", ArtifactID: "fin-invoices", Version: "1.0.0"}},
		{row: store.VectorPending{TenantID: "default", ArtifactID: "fin-payables", Version: "1.0.0"}},
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				hook.afterPass(ctx, written)
			} else {
				hook.afterPass(ctx, nil)
			}
		}(i)
	}
	wg.Wait()
	if n := retryCount(hook); n != 2 {
		t.Errorf("retries after concurrent passes = %d, want 2", n)
	}
}

// Spec: §7.3.1 — the startup pass and every sweep pass skip an artifact
// whose vector the outbox has not written, counting it as neither computed
// nor failed, and compute it once the outbox has written it.
func TestRunOverlapSweep_SkipsUnwrittenVectors(t *testing.T) {
	logs := captureLogs(t)
	ctx := context.Background()
	st := outboxIngest(t)
	idx := &laggingIndex{hostedIndex: newHostedIndex()}
	reg := core.New(st, "default", nil).WithVectorSearch(idx, nil).WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{Enabled: true})

	runOverlapSweep(ctx, st, reg, startupPassLabel)
	runOverlapSweep(ctx, st, reg, periodicPassLabel)
	if n := computations(t, st); n != 0 || idx.reads.Load() != 0 {
		t.Fatalf("passes over unwritten vectors stored %d entries and read %d vectors, want 0 and 0", n, idx.reads.Load())
	}
	out := logs.String()
	for _, label := range []string{startupPassLabel, periodicPassLabel} {
		want := "lint.semantic_overlap: " + label + " of tenant default computed 0, failed 0, skipped 3 whose vectors the outbox has not written"
		if !strings.Contains(out, want) {
			t.Errorf("the log does not carry %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "stored-vector read") {
		t.Errorf("a pass logged a stored-vector failure:\n%s", out)
	}

	w := &vectorDrainWorker{outbox: st, vec: idx, interval: time.Second, batch: 10}
	w.runOnce(ctx, time.Now().UTC().Add(time.Minute))
	runOverlapSweep(ctx, st, reg, periodicPassLabel)
	if n := computations(t, st); n != 3 {
		t.Errorf("a pass after the outbox wrote the vectors stored %d entries, want 3", n)
	}
	if !strings.Contains(logs.String(), "lint.semantic_overlap: sweep of tenant default computed 3, failed 0\n") {
		t.Errorf("the pass after the write did not log three computations:\n%s", logs.String())
	}
}
