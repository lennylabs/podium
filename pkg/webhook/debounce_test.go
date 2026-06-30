package webhook_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/webhook"
)

// manualTimer is a debounce-window timer that never fires on its own. A test
// injects it through webhook.SetTimerFactory so the trailing window fires only
// when the test calls Worker.Flush, making the batch delivery deterministic.
type manualTimer struct {
	mu       sync.Mutex
	duration time.Duration
	stopped  bool
}

// timerRecorder records the durations the buffer armed and the fire callbacks
// it scheduled, so a test can assert the window length and invoke a callback
// directly without depending on wall-clock timing.
type timerRecorder struct {
	mu        sync.Mutex
	durations []time.Duration
	fires     []func()
}

// newManualTimerFactory returns a SetTimerFactory factory plus the recorder it
// writes to.
func newManualTimerFactory() (func(d time.Duration, fn func()) func() bool, *timerRecorder) {
	rec := &timerRecorder{}
	factory := func(d time.Duration, fn func()) func() bool {
		rec.mu.Lock()
		rec.durations = append(rec.durations, d)
		rec.fires = append(rec.fires, fn)
		rec.mu.Unlock()
		mt := &manualTimer{duration: d}
		return func() bool {
			mt.mu.Lock()
			defer mt.mu.Unlock()
			already := mt.stopped
			mt.stopped = true
			return !already
		}
	}
	return factory, rec
}

func (r *timerRecorder) armed() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Duration(nil), r.durations...)
}

// debounceWorker builds a worker wired to a receiver server with a manual
// debounce timer and a pinned clock, returning the worker and the timer
// recorder.
func debounceWorker(t *testing.T, rs *receiverServer, now time.Time) (*webhook.Worker, *timerRecorder) {
	t.Helper()
	factory, rec := newManualTimerFactory()
	w := &webhook.Worker{
		Store:      webhook.NewMemoryStore(),
		HTTPClient: rs.srv.Client(),
		Backoff:    []time.Duration{},
		Now:        func() time.Time { return now },
	}
	webhook.SetTimerFactory(w, factory)
	return w, rec
}

// Spec: §7.3.2 — a debounced receiver holds a burst of matched events in a
// trailing window and, on expiry, delivers them as one batch. The window opens
// on the first event and arms a timer for receiver.Debounce.
func TestDebounce_CoalescesBurstIntoOneBatch(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "k")
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	w, rec := debounceWorker(t, rs, now)
	recv := webhook.Receiver{
		ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "k",
		EventFilter: []string{"layer.ingested"}, Debounce: 5 * time.Second,
	}
	ctx := context.Background()
	w.Enqueue(ctx, recv, "layer.ingested", "tr-1", nil, map[string]any{"layer": "team-shared"})
	w.Enqueue(ctx, recv, "layer.ingested", "tr-2", nil, map[string]any{"layer": "platform"})
	w.Enqueue(ctx, recv, "layer.ingested", "tr-3", nil, map[string]any{"layer": "finance"})

	// Nothing delivers until the window expires.
	if rs.deliveries.Load() != 0 {
		t.Fatalf("deliveries before flush = %d, want 0", rs.deliveries.Load())
	}
	if armed := rec.armed(); len(armed) != 1 || armed[0] != 5*time.Second {
		t.Fatalf("armed durations = %v, want [5s]", armed)
	}
	if !w.Flush("r1") {
		t.Fatal("Flush reported no open window, want true")
	}
	if rs.deliveries.Load() != 1 {
		t.Fatalf("deliveries after flush = %d, want 1", rs.deliveries.Load())
	}
	body := parseBatch(t, rs.bodies[0])
	if body["event"] != "batch" {
		t.Errorf("event = %v, want batch", body["event"])
	}
	if body["count"] != float64(3) {
		t.Errorf("count = %v, want 3", body["count"])
	}
	events, _ := body["events"].([]any)
	if len(events) != 3 {
		t.Fatalf("events length = %d, want 3", len(events))
	}
	// The envelope's top-level trace_id is the first event's trace.
	if body["trace_id"] != "tr-1" {
		t.Errorf("envelope trace_id = %v, want tr-1", body["trace_id"])
	}
	// window.start is the pinned clock.
	window, _ := body["window"].(map[string]any)
	if window["start"] != "2026-06-30T12:00:00Z" {
		t.Errorf("window.start = %v, want 2026-06-30T12:00:00Z", window["start"])
	}
}

// Spec: §7.3.2 — the window deduplicates held events by (event type, key). A
// second layer.ingested for the same layer overwrites the earlier held copy
// rather than adding a second element.
func TestDebounce_DeduplicatesByEventTypeAndKey(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "k")
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	w, _ := debounceWorker(t, rs, now)
	recv := webhook.Receiver{
		ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "k",
		Debounce: 5 * time.Second,
	}
	ctx := context.Background()
	w.Enqueue(ctx, recv, "layer.ingested", "tr-1", nil, map[string]any{"layer": "team-shared", "reference": "a"})
	w.Enqueue(ctx, recv, "layer.ingested", "tr-2", nil, map[string]any{"layer": "platform"})
	// Same layer as the first event: coalesces to one held copy, last wins.
	w.Enqueue(ctx, recv, "layer.ingested", "tr-3", nil, map[string]any{"layer": "team-shared", "reference": "b"})
	w.Flush("r1")

	body := parseBatch(t, rs.bodies[0])
	if body["count"] != float64(2) {
		t.Fatalf("count = %v, want 2 (team-shared deduplicated)", body["count"])
	}
	events, _ := body["events"].([]any)
	// The team-shared element is the last write (reference "b") and stays in
	// the first arrival slot.
	first, _ := events[0].(map[string]any)
	if first["trace_id"] != "tr-3" {
		t.Errorf("events[0].trace_id = %v, want tr-3 (last write for team-shared)", first["trace_id"])
	}
	firstData, _ := first["data"].(map[string]any)
	if firstData["reference"] != "b" {
		t.Errorf("events[0].data.reference = %v, want b", firstData["reference"])
	}
}

// Spec: §7.3.2 — the dedup key differs per event type. artifact.published keys
// on the artifact ID, layer.ingested on the layer ID, and domain.published on
// the domain path, so events of different types and keys all coalesce
// distinctly within one window.
func TestDebounce_DedupKeyVariesByEventType(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "k")
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	w, _ := debounceWorker(t, rs, now)
	recv := webhook.Receiver{ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "k", Debounce: time.Second}
	ctx := context.Background()
	// Two artifact.published for the same id coalesce to one.
	w.Enqueue(ctx, recv, "artifact.published", "a1", nil, map[string]any{"id": "finance/run"})
	w.Enqueue(ctx, recv, "artifact.published", "a2", nil, map[string]any{"id": "finance/run"})
	// A different artifact id is distinct.
	w.Enqueue(ctx, recv, "artifact.published", "a3", nil, map[string]any{"id": "finance/close"})
	// layer.ingested keys on layer.
	w.Enqueue(ctx, recv, "layer.ingested", "l1", nil, map[string]any{"layer": "prod"})
	// domain.published keys on domain.
	w.Enqueue(ctx, recv, "domain.published", "d1", nil, map[string]any{"domain": "finance/invoicing"})
	w.Flush("r1")

	body := parseBatch(t, rs.bodies[0])
	if body["count"] != float64(4) {
		t.Fatalf("count = %v, want 4 (one artifact id deduped)", body["count"])
	}
}

// Spec: §7.3.2 — events with no recognized key field for their type coalesce to
// one held copy per window under the empty key, so an unkeyed type does not fan
// out within a window.
func TestDebounce_UnkeyedEventsCoalesceToOne(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "k")
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	w, _ := debounceWorker(t, rs, now)
	recv := webhook.Receiver{ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "k", Debounce: time.Second}
	ctx := context.Background()
	// An unrecognized event type has no key field, so both events share the
	// empty key and coalesce.
	w.Enqueue(ctx, recv, "custom.event", "c1", nil, map[string]any{"foo": "1"})
	w.Enqueue(ctx, recv, "custom.event", "c2", nil, map[string]any{"foo": "2"})
	// A layer.ingested missing its layer field also uses the empty key.
	w.Enqueue(ctx, recv, "layer.ingested", "l1", nil, map[string]any{})
	w.Flush("r1")

	body := parseBatch(t, rs.bodies[0])
	if body["count"] != float64(2) {
		t.Fatalf("count = %v, want 2 (custom.event deduped, layer.ingested distinct)", body["count"])
	}
}

// Spec: §7.3.2 — a separate window is kept per receiver ID, so a burst for one
// receiver does not affect another's window, and Flush addresses one receiver.
func TestDebounce_PerReceiverWindows(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "k")
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	w, rec := debounceWorker(t, rs, now)
	r1 := webhook.Receiver{ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "k", Debounce: time.Second}
	r2 := webhook.Receiver{ID: "r2", TenantID: "t", URL: rs.srv.URL, Secret: "k", Debounce: 2 * time.Second}
	ctx := context.Background()
	w.Enqueue(ctx, r1, "layer.ingested", "a", nil, map[string]any{"layer": "x"})
	w.Enqueue(ctx, r2, "layer.ingested", "b", nil, map[string]any{"layer": "y"})
	if armed := rec.armed(); len(armed) != 2 {
		t.Fatalf("armed %d windows, want 2 (one per receiver)", len(armed))
	}
	// Flushing r1 delivers only r1's batch; r2's window stays open.
	w.Flush("r1")
	if rs.deliveries.Load() != 1 {
		t.Fatalf("deliveries after r1 flush = %d, want 1", rs.deliveries.Load())
	}
	w.Flush("r2")
	if rs.deliveries.Load() != 2 {
		t.Fatalf("deliveries after r2 flush = %d, want 2", rs.deliveries.Load())
	}
}

// Spec: §7.3.2 — a receiver with a zero Debounce has no window. Enqueue
// delivers the single event immediately as a one-element batch, so the publish
// path can route every matched delivery for a receiver through one entry point.
func TestDebounce_ZeroDebounceDeliversImmediately(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "k")
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	w, rec := debounceWorker(t, rs, now)
	recv := webhook.Receiver{ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "k"}
	w.Enqueue(context.Background(), recv, "layer.ingested", "tr-1", nil, map[string]any{"layer": "x"})
	if rs.deliveries.Load() != 1 {
		t.Fatalf("deliveries = %d, want 1 (zero Debounce delivers immediately)", rs.deliveries.Load())
	}
	if armed := rec.armed(); len(armed) != 0 {
		t.Fatalf("armed %d windows, want 0 (no window for zero Debounce)", len(armed))
	}
	body := parseBatch(t, rs.bodies[0])
	if body["count"] != float64(1) {
		t.Errorf("count = %v, want 1", body["count"])
	}
}

// Spec: §7.3.2 — Enqueue drops an event for a disabled receiver and opens no
// window, mirroring the single-event fan-out that skips disabled receivers.
func TestDebounce_SkipsDisabledReceiver(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "k")
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	w, rec := debounceWorker(t, rs, now)
	recv := webhook.Receiver{ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "k", Debounce: time.Second, Disabled: true}
	w.Enqueue(context.Background(), recv, "layer.ingested", "tr-1", nil, map[string]any{"layer": "x"})
	if armed := rec.armed(); len(armed) != 0 {
		t.Fatalf("armed %d windows, want 0 (disabled receiver opens none)", len(armed))
	}
	if w.Flush("r1") {
		t.Fatal("Flush reported an open window for a disabled receiver, want false")
	}
}

// Spec: §7.3.2 — Flush on a receiver with no open window is a no-op and reports
// false, so a flush after the window already fired does not deliver twice.
func TestDebounce_FlushWithoutWindowIsNoOp(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "k")
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	w, _ := debounceWorker(t, rs, now)
	recv := webhook.Receiver{ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "k", Debounce: time.Second}
	w.Enqueue(context.Background(), recv, "layer.ingested", "tr-1", nil, map[string]any{"layer": "x"})
	if !w.Flush("r1") {
		t.Fatal("first Flush should report an open window")
	}
	if w.Flush("r1") {
		t.Fatal("second Flush should report no open window after delivery")
	}
	if w.Flush("never-opened") {
		t.Fatal("Flush on an unknown receiver should report false")
	}
	if rs.deliveries.Load() != 1 {
		t.Fatalf("deliveries = %d, want 1 (no double delivery)", rs.deliveries.Load())
	}
}

// Spec: §7.3.2 — a timer that fires after Flush already closed and delivered
// the window is a no-op, so a delivery cannot run twice for one window. This
// exercises the fire guard that finds no open window.
func TestDebounce_TimerAfterFlushIsNoOp(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "k")
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	w, rec := debounceWorker(t, rs, now)
	recv := webhook.Receiver{ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "k", Debounce: time.Second}
	w.Enqueue(context.Background(), recv, "layer.ingested", "tr-1", nil, map[string]any{"layer": "x"})
	if !w.Flush("r1") {
		t.Fatal("Flush should report an open window")
	}
	// The wall-clock timer would have fired here; invoke its callback after the
	// window already delivered through Flush.
	rec.mu.Lock()
	fire := rec.fires[0]
	rec.mu.Unlock()
	fire()
	if rs.deliveries.Load() != 1 {
		t.Fatalf("deliveries = %d, want 1 (late timer fire is a no-op)", rs.deliveries.Load())
	}
}

// Spec: §7.3.2 — the default timer (no factory override) fires the window
// through wall-clock expiry. A short Debounce delivers the batch without a
// Flush call, covering the time.AfterFunc path.
func TestDebounce_DefaultTimerFires(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "k")
	w := &webhook.Worker{
		Store:      webhook.NewMemoryStore(),
		HTTPClient: rs.srv.Client(),
		Backoff:    []time.Duration{},
	}
	recv := webhook.Receiver{ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "k", Debounce: 20 * time.Millisecond}
	w.Enqueue(context.Background(), recv, "layer.ingested", "tr-1", nil, map[string]any{"layer": "x"})
	deadline := time.Now().Add(2 * time.Second)
	for rs.deliveries.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if rs.deliveries.Load() != 1 {
		t.Fatalf("deliveries = %d, want 1 (default timer should fire)", rs.deliveries.Load())
	}
}

// parseBatch unmarshals a batch envelope body for assertions.
func parseBatch(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body parse: %v", err)
	}
	return body
}
