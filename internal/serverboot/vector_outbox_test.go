package serverboot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/metrics"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// fakeEmbedder returns a fixed-dimension vector, or an error when err is set.
type fakeEmbedder struct {
	dim int
	err error
}

func (fakeEmbedder) ID() string        { return "fake" }
func (fakeEmbedder) Model() string     { return "fake-model" }
func (e fakeEmbedder) Dimensions() int { return e.dim }
func (e fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if e.err != nil {
		return nil, e.err
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		v := make([]float32, e.dim)
		v[0] = 1
		out[i] = v
	}
	return out, nil
}

func enqueue(t *testing.T, st store.VectorOutbox, id, version string, at time.Time) {
	t.Helper()
	rec := store.ManifestRecord{TenantID: "default", ArtifactID: id, Version: version, ContentHash: "sha256:" + id, Type: "context", Description: "d"}
	p := store.VectorPending{TenantID: "default", ArtifactID: id, Version: version, Text: "embed " + id, EnqueuedAt: at, NextRetryAt: at}
	if err := st.PutManifestWithVectorPending(context.Background(), rec, p); err != nil {
		t.Fatalf("enqueue %s: %v", id, err)
	}
}

func TestVectorDrainWorker_DrainsPendingRows(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	enqueue(t, st, "a/x", "1.0.0", now)

	vec := vector.NewMemory(8)
	mreg := metrics.New()
	w := &vectorDrainWorker{outbox: st, vec: vec, embedder: fakeEmbedder{dim: 8}, interval: time.Second, batch: 50, metrics: mreg}

	w.runOnce(ctx, now)

	depth, _, _ := st.VectorOutboxStats(ctx)
	if depth != 0 {
		t.Fatalf("outbox depth = %d after drain, want 0", depth)
	}
	matches, err := vec.Query(ctx, "default", func() []float32 { v := make([]float32, 8); v[0] = 1; return v }(), 5)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(matches) == 0 {
		t.Error("drained vector not queryable in the backend")
	}
}

func TestVectorDrainWorker_RetryOnEmbedFailure(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	enqueue(t, st, "a/x", "1.0.0", now)

	w := &vectorDrainWorker{outbox: st, vec: vector.NewMemory(8), embedder: fakeEmbedder{dim: 8, err: errors.New("backend down")}, interval: time.Minute, batch: 50}
	w.runOnce(ctx, now)

	// Still pending, but hidden until the backoff elapses.
	if rows, _ := st.ListVectorPending(ctx, 10, now); len(rows) != 0 {
		t.Errorf("failed row eligible immediately; want hidden by backoff")
	}
	rows, _ := st.ListVectorPending(ctx, 10, now.Add(10*time.Minute))
	if len(rows) != 1 || rows[0].Attempts != 1 {
		t.Fatalf("rows after backoff = %+v, want one row with attempts=1", rows)
	}
}

func TestVectorDrainWorker_LaggingFiresOnce(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	enqueue(t, st, "a/x", "1.0.0", now)

	var fired int
	w := &vectorDrainWorker{
		outbox: st, vec: vector.NewMemory(8),
		embedder: fakeEmbedder{dim: 8, err: errors.New("down")},
		interval: time.Minute, batch: 50, lagDepth: 1,
		onLagging: func(depth int, age time.Duration) { fired++ },
	}
	w.runOnce(ctx, now)
	w.runOnce(ctx, now) // still lagging; must not re-fire
	if fired != 1 {
		t.Fatalf("onLagging fired %d times, want 1 (only on transition)", fired)
	}
}
