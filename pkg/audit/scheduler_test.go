package audit_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/sign"
)

// recordingSigner counts Sign invocations; Verify is unused here.
type recordingSigner struct {
	signs    atomic.Int64
	failOnce atomic.Bool
}

func (r *recordingSigner) ID() string               { return "recording" }
func (r *recordingSigner) Verify(_, _ string) error { return nil }
func (r *recordingSigner) Sign(contentHash string) (string, error) {
	r.signs.Add(1)
	if r.failOnce.CompareAndSwap(true, false) {
		return "", errors.New("transient")
	}
	return "rec:" + contentHash, nil
}

// Spec: §8.6 — Scheduler.Run calls Anchor on startup and again
// every Interval. ctx cancel exits cleanly.
func TestScheduler_AnchorsOnStartupAndPeriodically(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink, err := audit.NewFileSink(filepath.Join(dir, "audit.log"))
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	if err := sink.Append(context.Background(), audit.Event{
		Type: audit.EventArtifactLoaded, Caller: "u",
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	signer := &recordingSigner{}
	sched := &audit.Scheduler{
		Sink:     sink,
		Signer:   signer,
		Interval: 50 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()
	// Allow a few ticks.
	time.Sleep(180 * time.Millisecond)
	cancel()
	<-done
	if got := signer.signs.Load(); got < 2 {
		t.Errorf("Sign called %d times, want >= 2 (startup + at least one tick)", got)
	}
}

// Spec: §8.6 — failures don't terminate the scheduler; the next
// tick retries and OnFailure observes the error.
func TestScheduler_FailureDoesNotStop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink, _ := audit.NewFileSink(filepath.Join(dir, "audit.log"))
	_ = sink.Append(context.Background(), audit.Event{
		Type: audit.EventArtifactLoaded, Caller: "u",
	})
	signer := &recordingSigner{}
	signer.failOnce.Store(true)
	failed := atomic.Int64{}
	sched := &audit.Scheduler{
		Sink:      sink,
		Signer:    signer,
		Interval:  30 * time.Millisecond,
		OnFailure: func(error) { failed.Add(1) },
	}
	ctx, cancel := context.WithCancel(context.Background())
	go sched.Run(ctx)
	time.Sleep(120 * time.Millisecond)
	cancel()
	if failed.Load() == 0 {
		t.Errorf("OnFailure was not invoked despite first-call failure")
	}
	if signer.signs.Load() < 2 {
		t.Errorf("scheduler stopped after first failure (signs = %d)", signer.signs.Load())
	}
}

// Spec: §8.6 — when Sink or Signer is nil, Run returns immediately.
func TestScheduler_NoOpWithoutSinkOrSigner(t *testing.T) {
	t.Parallel()
	sched := &audit.Scheduler{}
	if err := sched.Run(context.Background()); err != nil {
		t.Errorf("Run: %v, want nil", err)
	}
	sched.Sink, _ = audit.NewFileSink(filepath.Join(t.TempDir(), "x"))
	if err := sched.Run(context.Background()); err != nil {
		t.Errorf("Run with sink only: %v, want nil", err)
	}
}

// quiet unused-import linter when sign isn't reached in some build paths.
var _ sign.Provider = (*recordingSigner)(nil)
