package server

import (
	"context"
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

// DefaultRecoverySuccesses is the §13.2.1 recovery threshold: the
// registry flips back to ready "after three consecutive probe
// successes once the primary is reachable again". Used when a probe
// leaves Recoveries unset (<= 0).
const DefaultRecoverySuccesses = 3

// ReadOnlyProbe runs the §13.2.1 health probe against the metadata
// store. After Failures consecutive failures, it flips the
// ModeTracker into read_only; after Recoveries consecutive successes
// it flips back to ready.
//
// The probe checks that the store can answer GetTenant("default")
// (or any deterministic tenant id supplied by the operator). Any
// non-nil error counts as a failure. Operators monitor the
// `registry.read_only_entered` / `registry.read_only_exited`
// audit events the tracker emits on transition.
type ReadOnlyProbe struct {
	Store    store.Store
	Tracker  *ModeTracker
	TenantID string
	Interval time.Duration
	Failures int
	// Recoveries is the §13.2.1 number of consecutive probe successes
	// required to flip back to ready. Defaults to DefaultRecoverySuccesses
	// when unset (<= 0) so an intermittently reachable primary does not flap
	// the registry in and out of read-only on a single lucky probe.
	Recoveries int
	OnEnter    func()
	OnExit     func()
}

// Run blocks until ctx is canceled, ticking every Interval and
// transitioning between modes per the failure threshold.
func (p *ReadOnlyProbe) Run(ctx context.Context) error {
	if p.Store == nil || p.Tracker == nil || p.Failures <= 0 {
		return nil
	}
	interval := p.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	tenant := p.TenantID
	if tenant == "" {
		tenant = "default"
	}
	recoveries := p.Recoveries
	if recoveries <= 0 {
		recoveries = DefaultRecoverySuccesses
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	// Separate counters so a single success during an outage does not reset
	// the §13.2.1 recovery requirement to ready, and a single failure during
	// recovery does not count toward a re-entry.
	consecutiveFail := 0
	consecutiveOK := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			ctxOnce, cancel := context.WithTimeout(ctx, interval)
			_, err := p.Store.GetTenant(ctxOnce, tenant)
			cancel()
			if err != nil {
				consecutiveOK = 0
				consecutiveFail++
				if consecutiveFail >= p.Failures && p.Tracker.Get() != ModeReadOnly {
					p.Tracker.Set(ModeReadOnly)
					if p.OnEnter != nil {
						p.OnEnter()
					}
				}
				continue
			}
			consecutiveFail = 0
			// §13.2.1: only flip back to ready after `recoveries` consecutive
			// successes once the primary is reachable again.
			if p.Tracker.Get() != ModeReadOnly {
				consecutiveOK = 0
				continue
			}
			consecutiveOK++
			if consecutiveOK >= recoveries {
				p.Tracker.Set(ModeReady)
				if p.OnExit != nil {
					p.OnExit()
				}
				consecutiveOK = 0
			}
		}
	}
}
