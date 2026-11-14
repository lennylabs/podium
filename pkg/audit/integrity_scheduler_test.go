package audit_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// Spec: §8.6 — the anchor scheduler records an
// audit.anchor_failed event when Anchor fails, so the failure operators
// are told to monitor lands in the audit log (and any SIEM mirror) rather
// than only in process logs.
func TestScheduler_RecordsAnchorFailedEvent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, err := audit.OpenFile(path, audit.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
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

// Spec: §8.6 — the verification scheduler re-verifies the hash
// chain on a cadence. On a detected gap it records an audit.gap_detected
// event (best-effort, for SIEM mirroring) and invokes OnGap so an
// out-of-band edit that breaks the chain is detected and alerted at
// runtime.
func TestVerifyScheduler_DetectsGapAndAlerts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, err := audit.OpenFile(path, audit.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
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
	tampered := strings.Replace(string(raw), `"identity":"alice"`, `"identity":"mallory"`, 1)
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

// Spec: §8.6 — a clean chain produces no gap event and no alert,
// so verification does not raise false positives on an intact log.
func TestVerifyScheduler_CleanChainNoAlert(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, _ := audit.OpenFile(path, audit.OpenOptions{})
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

// Spec: §8.6 — nil sink disables verification; Run returns
// immediately without panicking.
func TestVerifyScheduler_NilSinkNoOp(t *testing.T) {
	t.Parallel()
	sched := &audit.VerifyScheduler{}
	if err := sched.Run(context.Background()); err != nil {
		t.Errorf("Run with nil sink: %v, want nil", err)
	}
}

// countingObserver records the Dropped calls a stream reports.
type countingObserver struct{ dropped atomic.Int64 }

func (o *countingObserver) AddQueued(string, int)      {}
func (o *countingObserver) Dropped(_, _ string, n int) { o.dropped.Add(int64(n)) }

// lineRecorder is an HTTP destination's receiver that decodes each NDJSON
// line it receives.
type lineRecorder struct {
	mu      sync.Mutex
	records []map[string]any
}

func (r *lineRecorder) ServeHTTP(_ http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, line := range strings.Split(string(body), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err == nil {
			r.records = append(r.records, rec)
		}
	}
}

// hashOfType returns the hash of the first received record of typ, or "".
func (r *lineRecorder) hashOfType(typ audit.EventType) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range r.records {
		if rec["type"] == string(typ) {
			h, _ := rec["hash"].(string)
			return h
		}
	}
	return ""
}

// lastLineHash returns the hash of the file's last line.
func lastLineHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	var rec struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		t.Fatalf("decode last line: %v", err)
	}
	return rec.Hash
}

// openReceivingStream opens a stream over a file and an HTTP receiver and
// appends three records.
func openReceivingStream(t *testing.T) (*audit.Stream, string, *lineRecorder, *countingObserver) {
	t.Helper()
	rcv := &lineRecorder{}
	srv := httptest.NewServer(rcv)
	t.Cleanup(srv.Close)
	path := filepath.Join(t.TempDir(), "audit.log")
	obs := &countingObserver{}
	s, err := audit.Open(context.Background(), path+","+srv.URL, audit.OpenOptions{Observer: obs})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	for i := 0; i < 3; i++ {
		if err := s.Append(context.Background(), audit.Event{
			Type: audit.EventArtifactLoaded, Caller: "alice", Target: fmt.Sprintf("skill/%d", i),
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	return s, path, rcv, obs
}

// Spec: §8.6 — audit.gap_detected and audit.anchor_failed go through the
// stream, so they reach every non-file destination with the hash the
// file's line holds. A scheduler that wrote through the file alone would
// leave the receiver without them.
func TestVerifyScheduler_GapDetectedReachesEveryDestination(t *testing.T) {
	t.Parallel()
	t.Run("gap_detected", func(t *testing.T) {
		t.Parallel()
		s, path, rcv, obs := openReceivingStream(t)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// Drop the middle line, so the third record's prev_hash names no
		// earlier hash.
		lines := strings.SplitAfter(string(raw), "\n")
		if err := os.WriteFile(path, []byte(lines[0]+lines[2]), 0o644); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		sched := &audit.VerifyScheduler{Sink: s, Interval: time.Hour, OnGap: func(error) { cancel() }}
		_ = sched.Run(ctx)
		if err := s.Close(context.Background()); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if got, want := rcv.hashOfType(audit.EventAuditGapDetected), lastLineHash(t, path); got == "" || got != want {
			t.Errorf("receiver gap_detected hash = %q, want the file's last line %q", got, want)
		}
		if n := obs.dropped.Load(); n != 0 {
			t.Errorf("dropped = %d, want 0", n)
		}
	})
	t.Run("anchor_failed", func(t *testing.T) {
		t.Parallel()
		s, path, rcv, obs := openReceivingStream(t)
		ctx, cancel := context.WithCancel(context.Background())
		sched := &audit.Scheduler{Sink: s, Signer: alwaysFailSigner{}, Interval: time.Hour, OnFailure: func(error) { cancel() }}
		_ = sched.Run(ctx)
		if err := s.Close(context.Background()); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if got, want := rcv.hashOfType(audit.EventAuditAnchorFailed), lastLineHash(t, path); got == "" || got != want {
			t.Errorf("receiver anchor_failed hash = %q, want the file's last line %q", got, want)
		}
		if n := obs.dropped.Load(); n != 0 {
			t.Errorf("dropped = %d, want 0", n)
		}
	})
}
