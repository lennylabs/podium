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
		s.notifyFailure(ctx, err)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if _, err := Anchor(ctx, s.Sink, s.Signer); err != nil {
				s.notifyFailure(ctx, err)
			}
		}
	}
}

// notifyFailure records an audit.anchor_failed event (best-effort) so the
// failure the operator is told to monitor lands in the audit log and any
// SIEM mirror, then invokes the OnFailure seam. A sink write error here
// must not mask the original anchor error, so the append result is
// ignored deliberately. spec: §8.6 (F-8.6.2).
func (s *Scheduler) notifyFailure(ctx context.Context, err error) {
	_ = s.Sink.Append(ctx, Event{
		Type:    EventAuditAnchorFailed,
		Caller:  "system:anchor",
		Context: map[string]string{"error": err.Error()},
	})
	if s.OnFailure != nil {
		s.OnFailure(err)
	}
}

// VerifyScheduler periodically re-verifies the audit hash chain, honoring
// §8.6 "Detection of gaps is automated and alerted." It mirrors the anchor
// and retention schedulers: one immediate pass on start, then every
// Interval until ctx is canceled.
//
// On a detected gap (Verify returns ErrChainBroken) the scheduler records
// an audit.gap_detected event best-effort, so SIEM mirroring (the §8.6
// operational backstop) surfaces the break even when OnGap is unconfigured,
// then invokes OnGap for in-process alerting. spec: §8.6 (F-8.6.1).
type VerifyScheduler struct {
	Sink     *FileSink
	Interval time.Duration
	// OnGap is called when Verify detects a chain gap. The scheduler
	// records the gap in the audit log regardless; OnGap is the
	// integration seam for tests and operational notifications.
	OnGap func(error)
}

// Run blocks until ctx is canceled, verifying the chain every Interval.
func (s *VerifyScheduler) Run(ctx context.Context) error {
	if s.Sink == nil {
		return nil
	}
	interval := s.Interval
	if interval <= 0 {
		interval = time.Hour
	}
	s.verifyOnce(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			s.verifyOnce(ctx)
		}
	}
}

func (s *VerifyScheduler) verifyOnce(ctx context.Context) {
	err := s.Sink.Verify(ctx)
	if err == nil {
		return
	}
	// Record the gap so it is tamper-evident in the log and mirrored to
	// SIEM. The gap_detected event chains off the (broken) head; the
	// underlying break persists across passes until an operator repairs
	// the log, so each pass re-alerts while the gap remains.
	_ = s.Sink.Append(ctx, Event{
		Type:    EventAuditGapDetected,
		Caller:  "system:verify",
		Context: map[string]string{"error": err.Error()},
	})
	if s.OnGap != nil {
		s.OnGap(err)
	}
}
