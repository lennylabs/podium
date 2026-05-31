package serverboot

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
)

// startRetentionScheduler runs §8.4 audit-event retention enforcement
// on a cadence. The default policy applies a single per-type MaxAge to
// every event the registry emits; deployers wanting fine-grained
// per-event-type retention fork defaultRetentionPolicies.
//
// reAnchor, when non-nil, is invoked after a pass that drops events so
// the moved chain head is re-anchored immediately (§8.6, F-8.4.8).
//
// The scheduler is best-effort: rewrite failures log a warning;
// the next tick retries.
func startRetentionScheduler(cfg *Config, sink *audit.FileSink, reAnchor func()) {
	if sink == nil {
		log.Printf("warning: audit retention disabled (no sink)")
		return
	}
	maxAge := time.Duration(cfg.auditRetentionMaxAgeDays) * 24 * time.Hour
	if maxAge <= 0 {
		log.Printf("warning: audit retention disabled (max age <= 0)")
		return
	}
	interval := time.Duration(cfg.auditRetentionInterval) * time.Second
	if interval <= 0 {
		return
	}
	policies := defaultRetentionPolicies(maxAge)
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		ctx := context.Background()
		// One immediate pass so a long-running operator doesn't
		// have to wait for the first tick after bumping the env
		// var to see retention applied.
		runRetentionOnce(ctx, sink, policies, reAnchor)
		for range t.C {
			runRetentionOnce(ctx, sink, policies, reAnchor)
		}
	}()
	log.Printf("audit retention scheduler running (interval=%ds, max age=%dd)",
		cfg.auditRetentionInterval, cfg.auditRetentionMaxAgeDays)
}

func runRetentionOnce(ctx context.Context, sink *audit.FileSink, policies []audit.Policy, reAnchor func()) {
	// §8.4 query-text window (placeholder at 7d, drop at 30d) runs in the
	// same pass so the query field ages out independently of the event
	// metadata kept under the per-type policies.
	dropped, err := audit.Enforce(ctx, sink, time.Now(), policies, audit.DefaultQueryRetention())
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("audit retention failure: %v", err)
		return
	}
	if dropped > 0 {
		log.Printf("audit retention dropped %d event(s)", dropped)
		// §8.6/F-8.4.8: dropping events rebuilt the hash chain, so any
		// prior anchor of the old head is stale. Re-anchor the new head.
		if reAnchor != nil {
			reAnchor()
		}
	}
}

// defaultRetentionPolicies builds a per-event-type policy table
// that applies maxAge to every event the registry emits. Deployers
// needing differentiated retention fork the slice.
func defaultRetentionPolicies(maxAge time.Duration) []audit.Policy {
	types := []audit.EventType{
		audit.EventDomainLoaded,
		audit.EventDomainsSearched,
		audit.EventArtifactsSearched,
		audit.EventArtifactLoaded,
		audit.EventArtifactPublished,
		audit.EventArtifactDeprecated,
		audit.EventArtifactSigned,
		audit.EventDomainPublished,
		audit.EventLayerIngested,
		audit.EventLayerHistoryRewritten,
		audit.EventLayerConfigChanged,
		audit.EventLayerUserRegistered,
		audit.EventAdminGranted,
		audit.EventVisibilityDenied,
		audit.EventFreezeBreakGlass,
		audit.EventReadOnlyEntered,
		audit.EventReadOnlyExited,
	}
	out := make([]audit.Policy, 0, len(types))
	for _, t := range types {
		out = append(out, audit.Policy{Type: t, MaxAge: maxAge})
	}
	return out
}
