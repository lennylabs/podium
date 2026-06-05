package serverboot

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/embedding"
	"github.com/lennylabs/podium/pkg/metrics"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// vectorDrainWorker reconciles the §4.7.2 transactional vector outbox: it
// embeds each pending row's composed text and writes the vector to the external
// backend, retrying with exponential backoff on failure. After each pass it
// publishes the outbox depth to the §13.8 podium_vector_outbox_depth gauge and
// emits a vector.outbox_lagging audit event when depth or age first crosses the
// configured threshold.
type vectorDrainWorker struct {
	outbox    store.VectorOutbox
	vec       vector.Provider
	embedder  embedding.Provider
	interval  time.Duration
	batch     int
	lagDepth  int
	lagAge    time.Duration
	metrics   *metrics.Registry
	onLagging func(depth int, oldestAge time.Duration)

	wasLagging bool
}

// runOnce drains one batch of eligible rows and republishes the depth gauge.
func (w *vectorDrainWorker) runOnce(ctx context.Context, now time.Time) {
	rows, err := w.outbox.ListVectorPending(ctx, w.batch, now)
	if err != nil {
		log.Printf("vector outbox: list pending: %v", err)
		return
	}
	for _, p := range rows {
		if err := w.drainRow(ctx, p); err != nil {
			backoff := w.backoff(p.Attempts)
			_ = w.outbox.MarkVectorPendingRetry(ctx, p.TenantID, p.ArtifactID, p.Version, now.Add(backoff), err.Error())
			log.Printf("vector outbox: drain %s@%s failed (attempt %d): %v; retry in %s",
				p.ArtifactID, p.Version, p.Attempts+1, err, backoff)
			continue
		}
		_ = w.outbox.MarkVectorPendingDone(ctx, p.TenantID, p.ArtifactID, p.Version)
	}
	w.publishStats(ctx, now)
}

// drainRow embeds and writes one pending row to the external backend. A
// self-embedding backend takes the raw text; otherwise the configured embedder
// produces the vector first.
func (w *vectorDrainWorker) drainRow(ctx context.Context, p store.VectorPending) error {
	if w.embedder == nil && vector.SelfEmbeds(w.vec) {
		return w.vec.(vector.TextVectorizer).PutText(ctx, p.TenantID, p.ArtifactID, p.Version, p.Text)
	}
	if w.embedder == nil {
		return fmt.Errorf("no embedder configured for external vector backend")
	}
	vecs, err := w.embedder.Embed(ctx, []string{p.Text})
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}
	if len(vecs) != 1 {
		return fmt.Errorf("embed: expected 1 vector, got %d", len(vecs))
	}
	return w.vec.Put(ctx, p.TenantID, p.ArtifactID, p.Version, vecs[0])
}

// backoff returns the retry delay for a row that has already failed `attempts`
// times: the poll interval doubled per attempt, capped at one hour.
func (w *vectorDrainWorker) backoff(attempts int) time.Duration {
	d := w.interval
	for i := 0; i < attempts && d < time.Hour; i++ {
		d *= 2
	}
	if d > time.Hour {
		d = time.Hour
	}
	return d
}

// publishStats updates the depth gauge and fires the lagging callback on the
// transition into a lagging state.
func (w *vectorDrainWorker) publishStats(ctx context.Context, now time.Time) {
	depth, oldest, err := w.outbox.VectorOutboxStats(ctx)
	if err != nil {
		return
	}
	if w.metrics != nil {
		w.metrics.SetVectorOutboxDepth(int64(depth))
	}
	var age time.Duration
	if !oldest.IsZero() {
		age = now.Sub(oldest)
	}
	lagging := (w.lagDepth > 0 && depth >= w.lagDepth) || (w.lagAge > 0 && age >= w.lagAge)
	if lagging && !w.wasLagging && w.onLagging != nil {
		w.onLagging(depth, age)
	}
	w.wasLagging = lagging
}

// startVectorOutboxWorker launches the §4.7.2 drain worker when the store
// supports the outbox and a vector backend is configured. It mirrors the
// retention/anchor scheduler goroutine pattern: an immediate pass, then one per
// tick, running until the process exits.
func startVectorOutboxWorker(cfg *Config, st store.Store, vec vector.Provider, embedder embedding.Provider, mreg *metrics.Registry, auditSink audit.Sink, tenantID string) {
	ob, ok := st.(store.VectorOutbox)
	if !ok {
		return
	}
	if vec == nil {
		log.Printf("warning: vector outbox drain worker disabled (no vector backend opened); pending embeddings will queue")
		return
	}
	interval := time.Duration(cfg.vectorOutboxInterval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	w := &vectorDrainWorker{
		outbox:    ob,
		vec:       vec,
		embedder:  embedder,
		interval:  interval,
		batch:     cfg.vectorOutboxBatch,
		lagDepth:  cfg.vectorOutboxLagDepth,
		lagAge:    time.Duration(cfg.vectorOutboxLagAge) * time.Second,
		metrics:   mreg,
		onLagging: vectorOutboxLaggingCallback(auditSink, tenantID),
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		ctx := context.Background()
		w.runOnce(ctx, time.Now().UTC())
		for range t.C {
			w.runOnce(ctx, time.Now().UTC())
		}
	}()
	log.Printf("vector outbox: drain worker running (interval=%s, backend=%s, lag_depth=%d, lag_age=%ds)",
		interval, vec.ID(), cfg.vectorOutboxLagDepth, cfg.vectorOutboxLagAge)
}

// vectorOutboxLaggingCallback returns the §4.7.2 lagging notifier: it logs and,
// when an audit sink is configured, records a vector.outbox_lagging event with
// the observed depth and oldest-row age so operators can alert on a backed-up
// external backend.
func vectorOutboxLaggingCallback(sink audit.Sink, tenantID string) func(int, time.Duration) {
	return func(depth int, age time.Duration) {
		log.Printf("vector outbox lagging: depth=%d oldest_age=%s", depth, age)
		if sink == nil {
			return
		}
		_ = sink.Append(context.Background(), audit.Event{
			Type:   audit.EventType("vector.outbox_lagging"),
			Caller: "system",
			Target: tenantID,
			Context: map[string]string{
				"depth":              strconv.Itoa(depth),
				"oldest_age_seconds": strconv.Itoa(int(age.Seconds())),
			},
		})
	}
}
