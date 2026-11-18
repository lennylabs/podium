package serverboot

import (
	"context"
	"log"
	"math/rand/v2"
	"time"

	"github.com/lennylabs/podium/pkg/embedding"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// buildSemanticOverlap returns the §7.3.1 overlap hook the reingest paths run
// after each cycle's writes, or nil when the check is off, no vector backend
// is open, or the backend is managed. A managed backend receives its vectors
// from the §4.7 outbox after the cycle, so the outbox computes each artifact
// when it writes the vector (overlapOutboxHook), and an ingest-time
// computation would find no vector. The startup ingest does not run the hook:
// it computes no overlap, so readiness never waits on it, and the startup
// pass (startOverlapSweep) computes those artifacts in the background. The
// hook reads the index through a registry of its own over the same store,
// vector backend, and embedder, which is the selection search uses, and
// scopes each call to the ingested layer's tenant.
//
// Spec: §7.3.1
func buildSemanticOverlap(st store.Store, vec vector.Provider, emb embedding.Provider, sem lint.SemanticConfig, managed bool) ingest.SemanticOverlapFunc {
	if !sem.Enabled {
		return nil
	}
	if vec == nil {
		log.Printf("warning: %s is on but no search index is configured; no overlap records are computed", lint.SemanticOverlapCode)
		return nil
	}
	if managed {
		return nil
	}
	reg := core.New(st, "", nil).WithVectorSearch(vec, emb).WithOverlapChecks(lint.Thresholds{}, sem)
	return func(ctx context.Context, tenantID, layerID string, stored []ingest.IngestedArtifact) []lint.Diagnostic {
		keys := make([]vector.RowKey, len(stored))
		for i, a := range stored {
			keys[i] = vector.RowKey{ArtifactID: a.ArtifactID, Version: a.Version}
		}
		return reg.IngestSemanticOverlap(core.ContextWithTenant(ctx, tenantID), layerID, keys)
	}
}

// overlapOutboxHook returns the callback the §4.7 outbox drain worker runs
// after each pass on a managed backend, or nil when the check is off. It
// computes the overlap records of the artifacts the pass wrote, so two
// artifacts of one cycle find each other once both vectors are written, and
// retries the computations of earlier passes whose stored vector the backend
// did not return yet (overlapOutbox). The drain has already marked every row
// of the pass done, so a failed computation never holds a row back: the
// computation logs it and leaves it to the sweep.
//
// Spec: §7.3.1
func overlapOutboxHook(reg *core.Registry) func(context.Context, []outboxWrite) {
	if !reg.SemanticOverlapEnabled() {
		return nil
	}
	return newOverlapOutbox(reg).afterPass
}

// startOverlapSweep launches the §7.3.1 startup pass and the periodic sweep
// in one background goroutine when the check is on, and returns at once, so
// readiness never waits on a computation. run calls it once, after the
// startup ingest of every bootstrap layer has completed, so one start runs
// one startup pass however many layers it ingested. The startup pass is one
// sweep pass that runs immediately, with no random delay, whether or not the
// periodic sweep is on; it computes the artifacts the startup ingest stored,
// and after a restart only those whose computation is missing or stale. When
// PODIUM_LINT_SEMANTIC_REFRESH_INTERVAL is not zero, a pass then runs every
// interval, offset on each replica by a random delay of up to a tenth of the
// interval (firstSweepDelay). The goroutine exits when ctx is cancelled.
//
// Spec: §7.3.1, §13.12
func startOverlapSweep(ctx context.Context, sem lint.SemanticConfig, st store.Store, reg *core.Registry) {
	if !sem.Enabled {
		return
	}
	var offset time.Duration
	if sem.RefreshInterval > 0 {
		offset = firstSweepDelay(sem.RefreshInterval, rand.Int64N)
		log.Printf("%s: startup pass running; sweep every %s (offset %s)", lint.SemanticOverlapCode, sem.RefreshInterval, offset.Round(time.Second))
	} else {
		log.Printf("%s: startup pass running; periodic sweep off", lint.SemanticOverlapCode)
	}
	go sweepLoop(ctx, sem.RefreshInterval, offset, func(ctx context.Context, label string) { runOverlapSweep(ctx, st, reg, label) })
}

// firstSweepDelay returns the random offset of a replica's periodic passes:
// from zero up to a tenth of the interval, drawn with randN (rand.Int64N in
// production). Replicas started together then spread their periodic passes
// instead of all computing at once. The bound derives from the configured
// interval, so PODIUM_LINT_SEMANTIC_REFRESH_INTERVAL is the one setting that
// tunes it, and it never exceeds the interval.
func firstSweepDelay(interval time.Duration, randN func(int64) int64) time.Duration {
	bound := max(interval/10, 1)
	return time.Duration(randN(int64(bound)))
}

// Labels of the passes sweepLoop runs, which the pass logs carry.
const (
	startupPassLabel  = "startup pass"
	periodicPassLabel = "sweep"
)

// sweepLoop runs the startup pass at once and then, when interval is not
// zero, a periodic pass after interval plus offset and once per interval
// after that. It returns when ctx is cancelled, or after the startup pass
// when interval is zero.
func sweepLoop(ctx context.Context, interval, offset time.Duration, pass func(ctx context.Context, label string)) {
	pass(ctx, startupPassLabel)
	if interval <= 0 {
		return
	}
	delay := time.NewTimer(interval + offset)
	defer delay.Stop()
	select {
	case <-ctx.Done():
		return
	case <-delay.C:
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		pass(ctx, periodicPassLabel)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// runOverlapSweep runs one sweep pass in every active tenant and logs, under
// label, the counts of a pass that computed, failed, or skipped anything. An
// artifact whose vector the outbox has not written is skipped and counted
// apart from the computed and failed ones. A cancelled ctx stops it before
// the next tenant.
//
// Spec: §7.3.1
func runOverlapSweep(ctx context.Context, st store.Store, reg *core.Registry, label string) {
	if ctx.Err() != nil {
		return
	}
	tenants, err := st.ListTenants(ctx)
	if err != nil {
		log.Printf("%s: %s: list tenants: %v", lint.SemanticOverlapCode, label, err)
		return
	}
	for _, t := range tenants {
		if ctx.Err() != nil {
			return
		}
		if !t.Active {
			continue
		}
		res, err := reg.RefreshOverlap(core.ContextWithTenant(ctx, t.ID))
		if err != nil {
			log.Printf("%s: %s of tenant %s: %v", lint.SemanticOverlapCode, label, t.ID, err)
			continue
		}
		switch {
		case res.Pending > 0:
			log.Printf("%s: %s of tenant %s computed %d, failed %d, skipped %d whose vectors the outbox has not written",
				lint.SemanticOverlapCode, label, t.ID, res.Computed, res.Failed, res.Pending)
		case res.Computed > 0 || res.Failed > 0:
			log.Printf("%s: %s of tenant %s computed %d, failed %d", lint.SemanticOverlapCode, label, t.ID, res.Computed, res.Failed)
		}
	}
}
