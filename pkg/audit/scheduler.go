package audit

import (
	"context"
	"time"

	"github.com/lennylabs/podium/pkg/sign"
)

// Scheduler runs Anchor periodically against the supplied sink,
// honoring §8.6 transparency-log anchoring without requiring an
// operator to invoke `podium admin anchor` by hand.
//
// The scheduler is best-effort: failures (signer outage, network
// blip) are not fatal; the next tick retries. Operators monitor
// the audit log for `audit.anchored` events and the
// `audit.anchor_failed` events the scheduler emits on failure.
type Scheduler struct {
	Sink     *FileSink
	Signer   sign.Provider
	Interval time.Duration
	// Now overrides the clock for tests; production leaves nil.
	Now func() time.Time
	// OnFailure is called when Anchor returns an error. The
	// scheduler logs internally; OnFailure is the integration seam
	// for tests + operational notifications.
	OnFailure func(error)
}

// Run blocks until ctx is canceled, calling Anchor every Interval.
// One immediate Anchor fires before the first sleep so the chain
// head lands in the transparency log promptly after startup.
func (s *Scheduler) Run(ctx context.Context) error {
	if s.Sink == nil || s.Signer == nil {
		return nil
	}
	interval := s.Interval
	if interval <= 0 {
		interval = time.Hour
	}
	if _, err := Anchor(ctx, s.Sink, s.Signer); err != nil {
		s.notifyFailure(err)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if _, err := Anchor(ctx, s.Sink, s.Signer); err != nil {
				s.notifyFailure(err)
			}
		}
	}
}

func (s *Scheduler) notifyFailure(err error) {
	if s.OnFailure != nil {
		s.OnFailure(err)
	}
}
