package audit_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
)

// alwaysFailSigner makes every Anchor attempt fail at the Sign step so
// the scheduler's failure path runs deterministically.
type alwaysFailSigner struct{}

func (alwaysFailSigner) ID() string { return "always-fail" }
func (alwaysFailSigner) Sign(_ context.Context, _ string) (string, error) {
	return "", errors.New("signer outage")
}
func (alwaysFailSigner) Verify(_ context.Context, _, _ string) error { return nil }

// Spec: §8.6 (F-8.6.2) — the anchor scheduler records an
// audit.anchor_failed event when Anchor fails, so the failure operators
// are told to monitor lands in the audit log (and any SIEM mirror) rather
// than only in process logs.
func TestScheduler_RecordsAnchorFailedEvent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	// A non-empty chain so Anchor reaches the failing Sign step.
	if err := sink.Append(context.Background(), audit.Event{
		Type: audit.EventArtifactLoaded, Caller: "alice",
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	alerted := atomic.Int64{}
	sched := &audit.Scheduler{
		Sink:      sink,
		Signer:    alwaysFailSigner{},
		Interval:  time.Hour, // only the immediate startup attempt should run
		OnFailure: func(error) { alerted.Add(1) },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = sched.Run(ctx) }()
	// Give the immediate startup Anchor time to fail and record the event.
	time.Sleep(80 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not exit within 2s of cancel")
	}

	if alerted.Load() == 0 {
		t.Errorf("OnFailure alert seam was never invoked")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if !strings.Contains(string(data), `"type":"audit.anchor_failed"`) {
		t.Errorf("audit log missing audit.anchor_failed event: %s", data)
	}
	if !strings.Contains(string(data), "signer outage") {
		t.Errorf("audit.anchor_failed event missing the error detail: %s", data)
	}
}

// Spec: §8.6 (F-8.6.1) — the verification scheduler re-verifies the hash
// chain on a cadence. On a detected gap it records an audit.gap_detected
// event (best-effort, for SIEM mirroring) and invokes OnGap so an
// out-of-band edit that breaks the chain is detected and alerted at
// runtime.
func TestVerifyScheduler_DetectsGapAndAlerts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := sink.Append(context.Background(), audit.Event{
			Type: audit.EventArtifactLoaded, Caller: "alice", Target: "skill/x",
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	// Tamper an event body out of band: change the recorded caller without
	// recomputing its hash so the self-hash check fails on verification.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	tampered := strings.Replace(string(raw), `"caller":"alice"`, `"caller":"mallory"`, 1)
	if tampered == string(raw) {
		t.Fatal("test setup: expected to tamper a caller field")
	}
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write tampered: %v", err)
	}

	alerted := atomic.Int64{}
	sched := &audit.VerifyScheduler{
		Sink:     sink,
		Interval: time.Hour, // only the immediate startup pass should run
		OnGap:    func(error) { alerted.Add(1) },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = sched.Run(ctx) }()
	time.Sleep(80 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("verify scheduler did not exit within 2s of cancel")
	}

	if alerted.Load() == 0 {
		t.Errorf("OnGap alert seam was never invoked for a broken chain")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if !strings.Contains(string(data), `"type":"audit.gap_detected"`) {
		t.Errorf("audit log missing audit.gap_detected event: %s", data)
	}
}

// Spec: §8.6 (F-8.6.1) — a clean chain produces no gap event and no alert,
// so verification does not raise false positives on an intact log.
func TestVerifyScheduler_CleanChainNoAlert(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, _ := audit.NewFileSink(path)
	for i := 0; i < 3; i++ {
		_ = sink.Append(context.Background(), audit.Event{
			Type: audit.EventArtifactLoaded, Caller: "alice",
		})
	}
	alerted := atomic.Int64{}
	sched := &audit.VerifyScheduler{
		Sink:     sink,
		Interval: time.Hour,
		OnGap:    func(error) { alerted.Add(1) },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = sched.Run(ctx) }()
	time.Sleep(60 * time.Millisecond)
	cancel()
	<-done

	if alerted.Load() != 0 {
		t.Errorf("OnGap fired %d times on a clean chain, want 0", alerted.Load())
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "audit.gap_detected") {
		t.Errorf("clean chain wrote a spurious gap_detected event: %s", data)
	}
}

// Spec: §8.6 (F-8.6.1) — nil sink disables verification; Run returns
// immediately without panicking.
func TestVerifyScheduler_NilSinkNoOp(t *testing.T) {
	t.Parallel()
	sched := &audit.VerifyScheduler{}
	if err := sched.Run(context.Background()); err != nil {
		t.Errorf("Run with nil sink: %v, want nil", err)
	}
}
