package server

import (
	"context"
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

// ReadOnlyProbe runs the §13.2.1 health probe against the metadata
// store. After Failures consecutive failures, it flips the
// ModeTracker into read_only; the first successful probe after a
// flip restores ready mode.
//
// The probe checks that the store can answer GetTenant("default")
// (or any deterministic tenant id supplied by the operator). Any
// non-nil error counts as a failure. Operators monitor the
// `registry.read_only_entered` / `registry.read_only_exited`
// audit events the tracker emits on transition.
type ReadOnlyProbe struct {
	Store      store.Store
	Tracker    *ModeTracker
	TenantID   string
	Interval   time.Duration
	Failures   int
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
	t := time.NewTicker(interval)
	defer t.Stop()
	consecutive := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			ctxOnce, cancel := context.WithTimeout(ctx, interval)
			_, err := p.Store.GetTenant(ctxOnce, tenant)
			cancel()
			if err != nil {
				consecutive++
				if consecutive >= p.Failures && p.Tracker.Get() != ModeReadOnly {
					p.Tracker.Set(ModeReadOnly)
					if p.OnEnter != nil {
						p.OnEnter()
					}
				}
				continue
			}
			if consecutive > 0 || p.Tracker.Get() == ModeReadOnly {
				if p.Tracker.Get() == ModeReadOnly {
					p.Tracker.Set(ModeReady)
					if p.OnExit != nil {
						p.OnExit()
					}
				}
				consecutive = 0
			}
		}
	}
}
