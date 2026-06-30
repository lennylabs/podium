package webhook_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/webhook"
)

// receiverServer is an httptest.Server that records every delivery,
// optionally verifies the HMAC, and returns a configurable status
// code. Tests use it to drive the worker's wire format.
type receiverServer struct {
	srv          *httptest.Server
	deliveries   atomic.Int64
	failuresLeft atomic.Int64
	secret       string
	bodies       [][]byte
}

// handler records every delivery, verifies the HMAC, and returns a
// configurable status code.
func (rs *receiverServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rs.bodies = append(rs.bodies, body)
		rs.deliveries.Add(1)
		sig := r.Header.Get("X-Podium-Signature")
		if err := webhook.VerifyBody(body, sig, rs.secret); err != nil {
			http.Error(w, "bad signature", http.StatusBadRequest)
			return
		}
		if rs.failuresLeft.Load() > 0 {
			rs.failuresLeft.Add(-1)
			http.Error(w, "transient", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// newReceiverServer returns a plaintext httptest server that responds 200
// to every request and validates the HMAC signature against the configured
// secret.
func newReceiverServer(t *testing.T, secret string) *receiverServer {
	t.Helper()
	rs := &receiverServer{secret: secret}
	rs.srv = httptest.NewServer(rs.handler())
	t.Cleanup(rs.srv.Close)
	return rs
}

// newTLSReceiverServer is the https variant of newReceiverServer, used by
// the SSRF tests that exercise the https-only delivery-time re-check.
func newTLSReceiverServer(t *testing.T, secret string) *receiverServer {
	t.Helper()
	rs := &receiverServer{secret: secret}
	rs.srv = httptest.NewTLSServer(rs.handler())
	t.Cleanup(rs.srv.Close)
	return rs
}

// Spec: §7.3.2 — Worker.Deliver POSTs the event body to every
// matching receiver, signed with X-Podium-Signature: sha256=...
// over the body bytes using the receiver's per-receiver secret.
func TestWorker_DeliversWithHMACSignature(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "secret-1")
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "secret-1",
	})
	w := &webhook.Worker{Store: store, HTTPClient: rs.srv.Client(),
		Backoff: []time.Duration{}}
	if err := w.Deliver(context.Background(), "t", "artifact.published", "", nil, map[string]any{
		"id": "x", "version": "1.0.0",
	}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if rs.deliveries.Load() != 1 {
		t.Errorf("deliveries = %d, want 1", rs.deliveries.Load())
	}
	var body map[string]any
	if err := json.Unmarshal(rs.bodies[0], &body); err != nil {
		t.Fatalf("body parse: %v", err)
	}
	if body["event"] != "artifact.published" {
		t.Errorf("body.event = %v, want artifact.published", body["event"])
	}
}

// spec: §7.3.2 — the worker's default delivery client does not follow a
// redirect, so a receiver that 30x-redirects to an internal target
// cannot bypass the registration-time SSRF check. With no injected
// HTTPClient the worker builds its own client with CheckRedirect set to
// NoRedirect; the 30x response then reads as a non-retryable failure and
// the redirect target is never contacted.
func TestWorker_DoesNotFollowRedirect(t *testing.T) {
	t.Parallel()
	var internalHits atomic.Int64
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		internalHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(internal.Close)

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, internal.URL, http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "t", URL: redirector.URL, Secret: "secret-1",
	})
	// No HTTPClient: the worker builds its default client with NoRedirect.
	w := &webhook.Worker{Store: store, Backoff: []time.Duration{}}
	if err := w.Deliver(context.Background(), "t", "artifact.published", "", nil,
		map[string]any{"id": "x"}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if internalHits.Load() != 0 {
		t.Fatalf("redirect target was contacted %d times, want 0", internalHits.Load())
	}
	// The redirect counts as a failure, so the receiver's failure counter
	// advances rather than resetting on a 2xx.
	got, err := store.Get(context.Background(), "t", "r1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.FailureCount == 0 {
		t.Fatalf("a non-followed redirect should record a delivery failure")
	}
}

// spec: §7.3.2 — the delivered body carries the full
// {event, trace_id, timestamp, actor, data} schema. trace_id and actor
// are threaded from the publisher; actor is always an object so the
// schema stays stable for receivers that key on it.
func TestWorker_BodyCarriesFullSchema(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "secret-1")
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "secret-1",
	})
	w := &webhook.Worker{Store: store, HTTPClient: rs.srv.Client()}
	actor := map[string]any{"email": "alice@acme.com"}
	if err := w.Deliver(context.Background(), "t", "artifact.published", "trace-abc", actor,
		map[string]any{"id": "x"}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(rs.bodies[0], &body); err != nil {
		t.Fatalf("body parse: %v", err)
	}
	for _, k := range []string{"event", "trace_id", "timestamp", "actor", "data"} {
		if _, ok := body[k]; !ok {
			t.Errorf("body missing %q key: %v", k, body)
		}
	}
	if body["trace_id"] != "trace-abc" {
		t.Errorf("trace_id = %v, want trace-abc", body["trace_id"])
	}
	gotActor, _ := body["actor"].(map[string]any)
	if gotActor["email"] != "alice@acme.com" {
		t.Errorf("actor.email = %v, want alice@acme.com", gotActor["email"])
	}
}

// spec: §7.3.2 — a delivery with no resolved caller still carries an
// `actor` object (empty), keeping the wire schema stable.
func TestWorker_NilActorMarshalsAsEmptyObject(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "secret-1")
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "secret-1",
	})
	w := &webhook.Worker{Store: store, HTTPClient: rs.srv.Client()}
	if err := w.Deliver(context.Background(), "t", "artifact.published", "", nil, nil); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(rs.bodies[0], &body); err != nil {
		t.Fatalf("body parse: %v", err)
	}
	actor, ok := body["actor"].(map[string]any)
	if !ok {
		t.Fatalf("actor key absent or not an object: %v", body["actor"])
	}
	if len(actor) != 0 {
		t.Errorf("actor = %v, want empty object", actor)
	}
}

// spec: §7.3.2 — two events firing close together against the same
// receiver must not lose a failure-count increment. The persisted
// counter reflects both failures, so the 32-failure auto-disable
// threshold is reached on time rather than late.
func TestWorker_ConcurrentDeliveriesDoNotLoseFailureCount(t *testing.T) {
	t.Parallel()
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "always 503", http.StatusServiceUnavailable)
	}))
	t.Cleanup(dead.Close)
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "t", URL: dead.URL, Secret: "k",
	})
	w := &webhook.Worker{
		Store:       store,
		HTTPClient:  dead.Client(),
		MaxFailures: 100,
		Backoff:     []time.Duration{time.Microsecond}, // fail fast
	}
	const events = 8
	var wg sync.WaitGroup
	for i := 0; i < events; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = w.Deliver(context.Background(), "t", "artifact.published", "", nil, nil)
		}()
	}
	wg.Wait()
	got, _ := store.Get(context.Background(), "t", "r1")
	if got.FailureCount != events {
		t.Errorf("FailureCount = %d, want %d (no lost increments under concurrency)", got.FailureCount, events)
	}
}

// Spec: §7.3.2 — receivers whose EventFilter does not include the
// fired event are skipped; subscribers control which events they
// receive.
func TestWorker_SkipsFilteredOutEventTypes(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "secret-1")
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "secret-1",
		EventFilter: []string{"artifact.deprecated"},
	})
	w := &webhook.Worker{Store: store, HTTPClient: rs.srv.Client()}
	if err := w.Deliver(context.Background(), "t", "artifact.published", "", nil, nil); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if rs.deliveries.Load() != 0 {
		t.Errorf("deliveries = %d, want 0 (filter mismatch)", rs.deliveries.Load())
	}
}

// Spec: §7.3.2 — transient 5xx failures are retried per the
// configured backoff schedule. The worker eventually succeeds when
// the receiver recovers.
func TestWorker_RetriesOnTransientFailure(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "secret-1")
	rs.failuresLeft.Store(2) // first two attempts return 503
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "secret-1",
	})
	w := &webhook.Worker{
		Store:      store,
		HTTPClient: rs.srv.Client(),
		Backoff:    []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond},
	}
	if err := w.Deliver(context.Background(), "t", "artifact.published", "", nil, nil); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if rs.deliveries.Load() != 3 {
		t.Errorf("deliveries = %d, want 3 (2 transient + 1 success)", rs.deliveries.Load())
	}
	got, _ := store.Get(context.Background(), "t", "r1")
	if got.FailureCount != 0 {
		t.Errorf("FailureCount = %d, want 0 (eventual success resets)", got.FailureCount)
	}
}

// Spec: §7.3.2 — receivers that exceed MaxFailures consecutive
// failures auto-disable so the worker doesn't keep hitting a dead
// endpoint. Operators re-enable explicitly.
func TestWorker_AutoDisablesAfterMaxFailures(t *testing.T) {
	t.Parallel()
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "always 503", http.StatusServiceUnavailable)
	}))
	t.Cleanup(dead.Close)
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "t", URL: dead.URL, Secret: "secret-1",
		FailureCount: 31,
	})
	w := &webhook.Worker{
		Store:       store,
		HTTPClient:  dead.Client(),
		MaxFailures: 32,
		Backoff:     []time.Duration{time.Microsecond}, // single retry
	}
	if err := w.Deliver(context.Background(), "t", "artifact.published", "", nil, nil); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	got, _ := store.Get(context.Background(), "t", "r1")
	if !got.Disabled {
		t.Errorf("receiver should be auto-disabled after %d failures", got.FailureCount)
	}
}

// Spec: §7.3.2 — disabled receivers are skipped silently. Operators
// flip Disabled=false to re-enable.
func TestWorker_SkipsDisabledReceivers(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "secret-1")
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "secret-1",
		Disabled: true,
	})
	w := &webhook.Worker{Store: store, HTTPClient: rs.srv.Client()}
	if err := w.Deliver(context.Background(), "t", "artifact.published", "", nil, nil); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if rs.deliveries.Load() != 0 {
		t.Errorf("deliveries = %d, want 0 (receiver disabled)", rs.deliveries.Load())
	}
}

// Spec: §7.3.2 — VerifyBody mirrors SignBody so receivers can
// validate the signature with the same secret.
func TestSignBody_RoundTrip(t *testing.T) {
	t.Parallel()
	body := []byte(`{"event":"artifact.published","data":{"id":"x"}}`)
	sig := webhook.SignBody(body, "secret")
	if err := webhook.VerifyBody(body, "sha256="+sig, "secret"); err != nil {
		t.Errorf("Verify: %v", err)
	}
	if err := webhook.VerifyBody(body, "sha256="+sig, "wrong"); err == nil {
		t.Errorf("Verify: expected mismatch with wrong secret")
	}
	tampered := append([]byte{}, body...)
	tampered[0] = 'X'
	if err := webhook.VerifyBody(tampered, "sha256="+sig, "secret"); err == nil {
		t.Errorf("Verify: expected mismatch on tampered body")
	}
}

// Spec: §7.3.2 — 4xx responses are non-retryable; the worker
// records a failure and moves on.
func TestWorker_4xxIsNotRetryable(t *testing.T) {
	t.Parallel()
	hits := atomic.Int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "client error", http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "t", URL: srv.URL, Secret: "k",
	})
	w := &webhook.Worker{
		Store:      store,
		HTTPClient: srv.Client(),
		Backoff:    []time.Duration{time.Microsecond, time.Microsecond},
	}
	_ = w.Deliver(context.Background(), "t", "artifact.published", "", nil, nil)
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1 (4xx must not retry)", hits.Load())
	}
	got, _ := store.Get(context.Background(), "t", "r1")
	if got.FailureCount == 0 {
		t.Errorf("FailureCount = 0; want non-zero (4xx counts as failure)")
	}
}

// Spec: §7.3.2 — a debounced receiver receives a batch envelope through
// DeliverBatch. The envelope carries event:"batch", a top-level trace_id,
// a window, a count, and an events array, where each element is the
// complete single-event body with its own trace_id independent of the
// envelope's. The body is signed with the receiver secret like a
// single-event delivery.
func TestWorker_DeliverBatchEnvelope(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "secret-1")
	recv := webhook.Receiver{
		ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "secret-1",
		EventFilter: []string{"layer.ingested"}, Debounce: time.Minute,
	}
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), recv)
	w := &webhook.Worker{Store: store, HTTPClient: rs.srv.Client(), Backoff: []time.Duration{}}
	windowStart := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	events := []webhook.BatchEvent{
		{EventType: "layer.ingested", TraceID: "trace-1", Timestamp: windowStart,
			Actor: map[string]any{"email": "alice@acme.com"}, Data: map[string]any{"layer": "team-shared"}},
		{EventType: "layer.ingested", TraceID: "trace-2", Timestamp: windowStart.Add(time.Second),
			Data: map[string]any{"layer": "platform"}},
	}
	if err := w.DeliverBatch(context.Background(), recv, "batch-trace", windowStart, events); err != nil {
		t.Fatalf("DeliverBatch: %v", err)
	}
	if rs.deliveries.Load() != 1 {
		t.Fatalf("deliveries = %d, want 1", rs.deliveries.Load())
	}
	var body map[string]any
	if err := json.Unmarshal(rs.bodies[0], &body); err != nil {
		t.Fatalf("body parse: %v", err)
	}
	if body["event"] != "batch" {
		t.Errorf("event = %v, want batch", body["event"])
	}
	if body["trace_id"] != "batch-trace" {
		t.Errorf("top-level trace_id = %v, want batch-trace", body["trace_id"])
	}
	if got := body["count"]; got != float64(2) {
		t.Errorf("count = %v, want 2", got)
	}
	window, ok := body["window"].(map[string]any)
	if !ok {
		t.Fatalf("window key absent or not an object: %v", body["window"])
	}
	if window["start"] != "2026-06-30T12:00:00Z" {
		t.Errorf("window.start = %v, want 2026-06-30T12:00:00Z", window["start"])
	}
	if _, ok := window["end"].(string); !ok {
		t.Errorf("window.end absent or not a string: %v", window["end"])
	}
	arr, ok := body["events"].([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("events should be a 2-element array, got %v", body["events"])
	}
	first, _ := arr[0].(map[string]any)
	for _, k := range []string{"event", "trace_id", "timestamp", "actor", "data"} {
		if _, ok := first[k]; !ok {
			t.Errorf("events[0] missing %q: %v", k, first)
		}
	}
	if first["trace_id"] != "trace-1" {
		t.Errorf("events[0].trace_id = %v, want trace-1 (independent of envelope)", first["trace_id"])
	}
	if first["event"] != "layer.ingested" {
		t.Errorf("events[0].event = %v, want layer.ingested", first["event"])
	}
	gotActor, _ := first["actor"].(map[string]any)
	if gotActor["email"] != "alice@acme.com" {
		t.Errorf("events[0].actor.email = %v, want alice@acme.com", gotActor["email"])
	}
	// A coalesced event with no resolved caller still carries an actor object.
	second, _ := arr[1].(map[string]any)
	secondActor, ok := second["actor"].(map[string]any)
	if !ok || len(secondActor) != 0 {
		t.Errorf("events[1].actor = %v, want empty object", second["actor"])
	}
}

// Spec: §7.3.2 — Worker.Deliver routes a debounced receiver into its trailing
// window instead of delivering an immediate single-event POST, so a burst of
// matched events yields no immediate delivery and exactly one batch on window
// expiry. The window is expired deterministically through Flush.
func TestWorker_DeliverRoutesDebouncedToWindow(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "secret-1")
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "ci", TenantID: "t", URL: rs.srv.URL, Secret: "secret-1",
		EventFilter: []string{"layer.ingested"}, Debounce: time.Minute,
	})
	w := &webhook.Worker{Store: store, HTTPClient: rs.srv.Client(), Backoff: []time.Duration{}}

	for _, layer := range []string{"team-shared", "platform", "team-shared"} {
		if err := w.Deliver(context.Background(), "t", "layer.ingested", "trace-1", nil,
			map[string]any{"layer": layer}); err != nil {
			t.Fatalf("Deliver: %v", err)
		}
	}
	// A debounced receiver receives no immediate single-event POST.
	if rs.deliveries.Load() != 0 {
		t.Fatalf("deliveries during burst = %d, want 0 (debounced receiver buffers)", rs.deliveries.Load())
	}

	if !w.Flush("ci") {
		t.Fatal("Flush reported no open window for the debounced receiver")
	}
	if rs.deliveries.Load() != 1 {
		t.Fatalf("deliveries after flush = %d, want exactly 1 batch", rs.deliveries.Load())
	}
	var body map[string]any
	if err := json.Unmarshal(rs.bodies[0], &body); err != nil {
		t.Fatalf("body parse: %v", err)
	}
	if body["event"] != "batch" {
		t.Errorf("event = %v, want batch", body["event"])
	}
	// team-shared and platform: the duplicate team-shared coalesces.
	if got := body["count"]; got != float64(2) {
		t.Errorf("count = %v, want 2 (duplicate layer coalesces)", got)
	}
}

// Spec: §7.3.2 — Worker.Deliver delivers a windowless receiver and routes a
// debounced receiver to its window from the same fan-out, so the two receivers
// route independently off one Deliver call: the windowless receiver gets the
// single-event body per event and the debounced receiver gets one batch.
func TestWorker_DeliverMixedReceiversRouteIndependently(t *testing.T) {
	t.Parallel()
	immediate := newReceiverServer(t, "secret-1")
	debounced := newReceiverServer(t, "secret-1")
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "chat", TenantID: "t", URL: immediate.srv.URL, Secret: "secret-1",
		EventFilter: []string{"layer.ingested"},
	})
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "ci", TenantID: "t", URL: debounced.srv.URL, Secret: "secret-1",
		EventFilter: []string{"layer.ingested"}, Debounce: time.Minute,
	})
	w := &webhook.Worker{Store: store, HTTPClient: immediate.srv.Client(), Backoff: []time.Duration{}}

	for _, layer := range []string{"a", "b"} {
		if err := w.Deliver(context.Background(), "t", "layer.ingested", "trace-1", nil,
			map[string]any{"layer": layer}); err != nil {
			t.Fatalf("Deliver: %v", err)
		}
	}
	if immediate.deliveries.Load() != 2 {
		t.Fatalf("windowless deliveries = %d, want 2", immediate.deliveries.Load())
	}
	if debounced.deliveries.Load() != 0 {
		t.Fatalf("debounced deliveries before flush = %d, want 0", debounced.deliveries.Load())
	}
	if !w.Flush("ci") {
		t.Fatal("Flush reported no open window for the debounced receiver")
	}
	if debounced.deliveries.Load() != 1 {
		t.Fatalf("debounced deliveries after flush = %d, want 1 batch", debounced.deliveries.Load())
	}
}

// Spec: §7.3.2 — a successful batch delivery resets the receiver's
// failure counter through the shared recordResult path.
func TestWorker_DeliverBatchRecordsSuccess(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "secret-1")
	recv := webhook.Receiver{
		ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "secret-1",
		FailureCount: 5, Debounce: time.Minute,
	}
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), recv)
	w := &webhook.Worker{Store: store, HTTPClient: rs.srv.Client(), Backoff: []time.Duration{}}
	events := []webhook.BatchEvent{{EventType: "layer.ingested", TraceID: "t1", Data: map[string]any{"layer": "x"}}}
	if err := w.DeliverBatch(context.Background(), recv, "bt", time.Now(), events); err != nil {
		t.Fatalf("DeliverBatch: %v", err)
	}
	got, _ := store.Get(context.Background(), "t", "r1")
	if got.FailureCount != 0 {
		t.Errorf("FailureCount = %d, want 0 (batch success resets)", got.FailureCount)
	}
}

// Spec: §7.3.2 — a failed batch delivery advances the failure counter and
// auto-disables the receiver at the threshold, sharing the single-event
// failure machinery.
func TestWorker_DeliverBatchAutoDisablesOnFailure(t *testing.T) {
	t.Parallel()
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "always 503", http.StatusServiceUnavailable)
	}))
	t.Cleanup(dead.Close)
	recv := webhook.Receiver{
		ID: "r1", TenantID: "t", URL: dead.URL, Secret: "k",
		FailureCount: 31, Debounce: time.Minute,
	}
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), recv)
	w := &webhook.Worker{
		Store: store, HTTPClient: dead.Client(),
		MaxFailures: 32, Backoff: []time.Duration{time.Microsecond},
	}
	events := []webhook.BatchEvent{{EventType: "layer.ingested", TraceID: "t1"}}
	if err := w.DeliverBatch(context.Background(), recv, "bt", time.Now(), events); err != nil {
		t.Fatalf("DeliverBatch: %v", err)
	}
	got, _ := store.Get(context.Background(), "t", "r1")
	if !got.Disabled {
		t.Errorf("receiver should auto-disable after a failing batch reaches the threshold")
	}
}

// Spec: §7.3.2 — DeliverBatch skips a disabled receiver and is a no-op for
// an empty event slice, so no POST is sent.
func TestWorker_DeliverBatchSkipsDisabledAndEmpty(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "secret-1")
	store := webhook.NewMemoryStore()
	w := &webhook.Worker{Store: store, HTTPClient: rs.srv.Client(), Backoff: []time.Duration{}}

	disabled := webhook.Receiver{ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "secret-1", Disabled: true}
	if err := w.DeliverBatch(context.Background(), disabled, "bt", time.Now(),
		[]webhook.BatchEvent{{EventType: "layer.ingested"}}); err != nil {
		t.Fatalf("DeliverBatch(disabled): %v", err)
	}
	enabled := webhook.Receiver{ID: "r2", TenantID: "t", URL: rs.srv.URL, Secret: "secret-1"}
	if err := w.DeliverBatch(context.Background(), enabled, "bt", time.Now(), nil); err != nil {
		t.Fatalf("DeliverBatch(empty): %v", err)
	}
	if rs.deliveries.Load() != 0 {
		t.Errorf("deliveries = %d, want 0 (disabled receiver and empty batch send nothing)", rs.deliveries.Load())
	}
}

// Spec: §7.3.2 — the single-event delivery body is unchanged for a
// receiver without a debounce window: it carries no batch keys.
func TestWorker_SingleEventBodyHasNoBatchKeys(t *testing.T) {
	t.Parallel()
	rs := newReceiverServer(t, "secret-1")
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "secret-1",
	})
	w := &webhook.Worker{Store: store, HTTPClient: rs.srv.Client(), Backoff: []time.Duration{}}
	if err := w.Deliver(context.Background(), "t", "artifact.published", "trace", nil,
		map[string]any{"id": "x"}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(rs.bodies[0], &body); err != nil {
		t.Fatalf("body parse: %v", err)
	}
	if body["event"] != "artifact.published" {
		t.Errorf("event = %v, want artifact.published (not a batch)", body["event"])
	}
	for _, k := range []string{"window", "count", "events"} {
		if _, ok := body[k]; ok {
			t.Errorf("single-event body should not carry %q: %v", k, body)
		}
	}
}

// Spec: §7.3.2 — the worker re-validates the receiver URL against its
// URLPolicy before each POST. A target the policy disallows (here a host
// that resolves to a private address after registration) is rejected
// without a POST, and the rejection records a delivery failure. The
// rejection is non-retryable, so the receiver is contacted no more than
// the single validation attempt allows.
func TestWorker_DeliveryRevalidatesURLPolicy(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	// The host moved to a private address after registration; delivery
	// re-resolves it through the policy and refuses to send.
	policy := newPolicy(t)
	webhook.SetResolver(policy, staticResolver("relay.acme.com", "10.0.0.5"))
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "t", URL: "https://relay.acme.com/ci", Secret: "k",
	})
	w := &webhook.Worker{
		Store: store, HTTPClient: srv.Client(),
		URLPolicy: policy, Backoff: []time.Duration{time.Microsecond},
	}
	if err := w.Deliver(context.Background(), "t", "artifact.published", "", nil, nil); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("a disallowed target was contacted %d times, want 0", hits.Load())
	}
	got, _ := store.Get(context.Background(), "t", "r1")
	if got.FailureCount == 0 {
		t.Errorf("a policy rejection at delivery should record a failure")
	}
}

// Spec: §7.3.2 — a host on the allowlist passes the delivery-time
// re-check, so the POST is sent. The receiver binds on a loopback https
// URL; the allowlist permits the loopback host for an internal relay
// configured deliberately.
func TestWorker_DeliveryAllowsPolicyPermittedTarget(t *testing.T) {
	t.Parallel()
	rs := newTLSReceiverServer(t, "secret-1")
	policy := newPolicy(t, "127.0.0.1")
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "secret-1",
	})
	w := &webhook.Worker{Store: store, HTTPClient: rs.srv.Client(), URLPolicy: policy, Backoff: []time.Duration{}}
	if err := w.Deliver(context.Background(), "t", "artifact.published", "", nil, nil); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if rs.deliveries.Load() != 1 {
		t.Errorf("deliveries = %d, want 1 (allowlisted target delivers)", rs.deliveries.Load())
	}
}

// Spec: §7.3.2 — the worker's default delivery client uses the
// policy-aware CheckRedirect when a policy is configured, so a redirect to
// a disallowed target is refused; with no policy it follows no redirect at
// all.
func TestWorker_DefaultClientRedirectHookSelection(t *testing.T) {
	t.Parallel()
	// With a policy: the hook refuses a redirect to a private target and
	// allows a public one.
	policy := newPolicy(t)
	webhook.SetResolver(policy, func(_ context.Context, host string) ([]net.IP, error) {
		switch host {
		case "internal.acme.com":
			return []net.IP{net.ParseIP("10.1.2.3")}, nil
		case "hooks.acme.com":
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	})
	withPolicy := webhook.WorkerCheckRedirect(&webhook.Worker{URLPolicy: policy})
	badReq, _ := http.NewRequest(http.MethodGet, "https://internal.acme.com/x", nil)
	if err := withPolicy(badReq, nil); !errors.Is(err, webhook.ErrDisallowedTarget) {
		t.Fatalf("policy redirect hook should refuse a private target, got %v", err)
	}
	goodReq, _ := http.NewRequest(http.MethodGet, "https://hooks.acme.com/x", nil)
	if err := withPolicy(goodReq, nil); err != nil {
		t.Fatalf("policy redirect hook should allow a public target, got %v", err)
	}

	// With no policy: the hook stops at the first response.
	noPolicy := webhook.WorkerCheckRedirect(&webhook.Worker{})
	anyReq, _ := http.NewRequest(http.MethodGet, "https://hooks.acme.com/x", nil)
	if err := noPolicy(anyReq, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("no-policy redirect hook should stop at the first response, got %v", err)
	}
}

// Spec: §7.3.2 — Worker.Policy returns the configured SSRF policy so the
// registration handler validates with the same policy the worker enforces.
func TestWorker_PolicyAccessor(t *testing.T) {
	t.Parallel()
	policy := newPolicy(t)
	w := &webhook.Worker{URLPolicy: policy}
	if w.Policy() != policy {
		t.Errorf("Policy() = %v, want the configured policy", w.Policy())
	}
	if (&webhook.Worker{}).Policy() != nil {
		t.Errorf("Policy() should be nil when no policy is configured")
	}
}

// Spec: §7.3.2 — context cancel aborts retry waits cleanly.
func TestWorker_ContextCancelAbortsRetry(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "t", URL: srv.URL, Secret: "k",
	})
	w := &webhook.Worker{
		Store:      store,
		HTTPClient: srv.Client(),
		Backoff:    []time.Duration{time.Hour},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Deliver(ctx, "t", "x", "", nil, nil) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			// Deliver is best-effort and may swallow ctx.Canceled
			// because the per-receiver goroutine handles it.
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Deliver did not return after ctx cancel")
	}
}

// Spec: §7.3.2 — MemoryStore preserves the per-receiver debounce
// window across Put and Get. The store holds the Receiver by value,
// so Debounce is additive.
func TestMemoryStore_DebouncePersists(t *testing.T) {
	t.Parallel()
	store := webhook.NewMemoryStore()
	rec := webhook.Receiver{
		ID: "ci", TenantID: "t", URL: "https://example/hook", Secret: "k",
		EventFilter: []string{"layer.ingested"},
		Debounce:    90 * time.Second,
	}
	if err := store.Put(context.Background(), rec); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := store.Get(context.Background(), "t", "ci")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Debounce != rec.Debounce {
		t.Errorf("Debounce = %v, want %v", got.Debounce, rec.Debounce)
	}
}
