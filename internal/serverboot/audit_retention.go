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
// the moved chain head is re-anchored immediately (§8.6).
//
// The scheduler is best-effort: rewrite failures log a warning;
// the next tick retries.
func startRetentionScheduler(ctx context.Context, cfg *Config, sink *audit.FileSink, reAnchor func()) {
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
		// One immediate pass so a long-running operator doesn't have to wait for
		// the first tick after bumping the env var, then one pass per tick until
		// ctx is cancelled.
		for {
			runRetentionOnce(ctx, sink, policies, reAnchor)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
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
		// §8.6: dropping events rebuilt the hash chain, so any
		// prior anchor of the old head is stale. Re-anchor the new head.
		if reAnchor != nil {
			reAnchor()
		}
	}
}

// operationalEventTypes are the §8.1 operational audit events the §8.4
// "Audit events (metadata): 1 year" default applies to. defaultRetention
// Policies assigns each of them maxAge.
var operationalEventTypes = []audit.EventType{
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
	// admin.visibility_override is a §4.7.2 access-control accountability
	// event, retained on the same 1-year window as admin.granted and
	// visibility.denied.
	audit.EventAdminVisibilityOverride,
	audit.EventFreezeBreakGlass,
	audit.EventReadOnlyEntered,
	audit.EventReadOnlyExited,
}

// retainedIndefinitelyEventTypes are the integrity and GDPR-accountability
// events deliberately kept past the §8.4 metadata window. The §8.4 table
// frames its rows as "Defaults, configurable per deployment"; these types
// carry no default policy, so Enforce never drops them:
//
//   - The §8.6 transparency-anchor markers (audit.anchored / anchor_failed /
//     gap_detected) and the §8.4/§8.6 audit.retention_enforced boundary
//     marker record the chain state needed to reconcile an external anchor
//     of a superseded head; aging them out would erase the reconciliation
//     trail.
//   - user.erased is the §8.5 GDPR right-to-erasure accountability record;
//     it persists to prove the erasure was honored.
//
// An operator wanting a finite window for any of these adds an explicit
// Policy to the slice defaultRetentionPolicies returns.
var retainedIndefinitelyEventTypes = []audit.EventType{
	audit.EventUserErased,
	audit.EventAuditAnchored,
	audit.EventAuditAnchorFailed,
	audit.EventAuditGapDetected,
	audit.EventRetentionEnforced,
}

// defaultRetentionPolicies builds the §8.4 per-event-type policy table.
// Every operationalEventTypes entry gets maxAge (the 1-year "Audit events
// (metadata)" default); the retainedIndefinitelyEventTypes carry no policy
// and are kept indefinitely. The two sets together classify every
// audit.EventType (asserted by a unit test) so a newly added type is not
// silently dropped from retention. Deployers needing differentiated
// retention fork the returned slice.
func defaultRetentionPolicies(maxAge time.Duration) []audit.Policy {
	out := make([]audit.Policy, 0, len(operationalEventTypes))
	for _, t := range operationalEventTypes {
		out = append(out, audit.Policy{Type: t, MaxAge: maxAge})
	}
	return out
}
