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

// buildSemanticOverlap returns the §7.3.1 overlap hook the ingest paths run
// after each cycle's writes, or nil when the check is off, no vector backend
// is open, or the backend is managed. A managed backend receives its vectors
// from the §4.7 outbox after the cycle, so the outbox computes each artifact
// when it writes the vector (overlapOutboxHook), and an ingest-time
// computation would find no vector. The bootstrap ingest runs before the
// serving registry is built, so the hook reads the index through a registry of
// its own over the same store, vector backend, and embedder, which is the
// selection search uses, and scopes each call to the ingested layer's tenant.
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
// after it writes an artifact's vector to a managed backend, or nil when the
// check is off. It computes the artifact's overlap records then, so two
// artifacts of one cycle find each other once both vectors are written. A
// failure is logged by the computation and left to the sweep.
//
// Spec: §7.3.1
func overlapOutboxHook(reg *core.Registry) func(context.Context, store.VectorPending) {
	if !reg.SemanticOverlapEnabled() {
		return nil
	}
	return func(ctx context.Context, p store.VectorPending) {
		key := []vector.RowKey{{ArtifactID: p.ArtifactID, Version: p.Version}}
		if _, err := reg.ComputeOverlap(core.ContextWithTenant(ctx, p.TenantID), key); err != nil {
			log.Printf("%s: %s@%s after the outbox write: %v", lint.SemanticOverlapCode, p.ArtifactID, p.Version, err)
		}
	}
}

// overlapSweepJitterCap bounds the random delay before a replica's first
// sweep, so replicas started together do not all compute at once.
const overlapSweepJitterCap = time.Minute

// startOverlapSweep launches the §7.3.1 periodic sweep when the check is on
// and PODIUM_LINT_SEMANTIC_REFRESH_INTERVAL is not zero. Every replica runs
// it: the first pass follows a random delay of up to a minute, and then one
// pass runs per interval until ctx is cancelled.
//
// Spec: §7.3.1, §13.12
func startOverlapSweep(ctx context.Context, sem lint.SemanticConfig, st store.Store, reg *core.Registry) {
	if !sem.Enabled || sem.RefreshInterval <= 0 {
		return
	}
	jitter := time.Duration(rand.Int64N(int64(min(sem.RefreshInterval, overlapSweepJitterCap))))
	go sweepLoop(ctx, jitter, sem.RefreshInterval, func(ctx context.Context) { runOverlapSweep(ctx, st, reg) })
	log.Printf("%s: sweep running (interval=%s)", lint.SemanticOverlapCode, sem.RefreshInterval)
}

// sweepLoop runs pass after first and then once per interval, returning when
// ctx is cancelled.
func sweepLoop(ctx context.Context, first, interval time.Duration, pass func(context.Context)) {
	delay := time.NewTimer(first)
	defer delay.Stop()
	select {
	case <-ctx.Done():
		return
	case <-delay.C:
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// runOverlapSweep runs one sweep pass in every active tenant and logs the
// counts of a pass that computed or failed anything.
//
// Spec: §7.3.1
func runOverlapSweep(ctx context.Context, st store.Store, reg *core.Registry) {
	tenants, err := st.ListTenants(ctx)
	if err != nil {
		log.Printf("%s: sweep: list tenants: %v", lint.SemanticOverlapCode, err)
		return
	}
	for _, t := range tenants {
		if !t.Active || ctx.Err() != nil {
			continue
		}
		res, err := reg.RefreshOverlap(core.ContextWithTenant(ctx, t.ID))
		if err != nil {
			log.Printf("%s: sweep of tenant %s: %v", lint.SemanticOverlapCode, t.ID, err)
			continue
		}
		if res.Computed > 0 || res.Failed > 0 {
			log.Printf("%s: sweep of tenant %s computed %d, failed %d", lint.SemanticOverlapCode, t.ID, res.Computed, res.Failed)
		}
	}
}
