package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
)

// Spec: §8.6 (F-8.6.1) — "Detection of gaps is automated and alerted."
// The VerifyScheduler re-verifies the persisted hash chain on a cadence;
// an out-of-band edit that breaks the chain is detected at runtime,
// recorded as an audit.gap_detected event for SIEM mirroring, and surfaced
// through the alert seam. This drives the scheduler over a real FileSink
// the way serverboot wires it.
func TestAuditGapDetection_SchedulerDetectsOutOfBandEdit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		if err := sink.Append(ctx, audit.Event{
			Type: audit.EventArtifactLoaded, Caller: "alice@acme.com", Target: "skill/x",
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	// A clean chain verifies cleanly.
	if err := sink.Verify(ctx); err != nil {
		t.Fatalf("clean chain failed verification: %v", err)
	}

	// Tamper an interior event body out of band (the threat the §8.6
	// guarantee targets): rewrite a caller without recomputing its hash.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	tampered := strings.Replace(string(raw), `"identity":"alice@acme.com"`, `"identity":"mallory@evil.com"`, 1)
	if tampered == string(raw) {
		t.Fatal("test setup: nothing tampered")
	}
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write tampered: %v", err)
	}

	alerts := atomic.Int64{}
	sched := &audit.VerifyScheduler{
		Sink:     sink,
		Interval: time.Hour, // only the immediate startup pass runs in the window
		OnGap:    func(error) { alerts.Add(1) },
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = sched.Run(runCtx) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("verify scheduler did not exit within 2s of cancel")
	}

	if alerts.Load() == 0 {
		t.Errorf("gap was not alerted through the OnGap seam")
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if !strings.Contains(string(out), `"type":"audit.gap_detected"`) {
		t.Errorf("audit log missing the audit.gap_detected record:\n%s", out)
	}
}
