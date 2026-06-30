package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/webhook"
)

// routingReceiver records every delivered body keyed by nothing in particular;
// the tests inspect the bodies channel and the count.
type routingReceiver struct {
	srv    *httptest.Server
	mu     sync.Mutex
	bodies [][]byte
	hits   atomic.Int64
}

func newRoutingReceiver(t *testing.T) *routingReceiver {
	t.Helper()
	rr := &routingReceiver{}
	rr.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rr.mu.Lock()
		rr.bodies = append(rr.bodies, b)
		rr.mu.Unlock()
		rr.hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(rr.srv.Close)
	return rr
}

func (rr *routingReceiver) snapshot() [][]byte {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	out := make([][]byte, len(rr.bodies))
	copy(out, rr.bodies)
	return out
}

// newRoutingServer wires a server with an outbound worker backed by the given
// store. Debounced receivers in these tests use a long Debounce so the
// wall-clock timer never fires during the test; a test expires a window
// deterministically through worker.Flush.
func newRoutingServer(t *testing.T, wstore *webhook.MemoryStore, client *http.Client) (*Server, *webhook.Worker) {
	t.Helper()
	worker := &webhook.Worker{Store: wstore, HTTPClient: client, Backoff: []time.Duration{}}
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	srv := New(core.New(st, "default", nil), WithWebhooks(worker), WithTenant("default"))
	return srv, worker
}

// waitFor polls cond until it holds or the deadline passes. PublishEvent fans
// out on a goroutine, so the assertions wait for the asynchronous delivery.
func waitFor(t *testing.T, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// Spec: §7.3.2 — PublishEvent routes a windowless receiver (zero Debounce)
// through the immediate single-event path, so the receiver gets the identical
// single-event body carrying {event, trace_id, timestamp, actor, data} and no
// batch keys.
func TestPublishEvent_WindowlessReceiverGetsSingleEvent(t *testing.T) {
	t.Parallel()
	rr := newRoutingReceiver(t)
	wstore := webhook.NewMemoryStore()
	_ = wstore.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "default", URL: rr.srv.URL, Secret: "s",
	})
	srv, _ := newRoutingServer(t, wstore, rr.srv.Client())

	ctx := withAuditMeta(context.Background(), AuditMeta{TraceID: "trace-1", Email: "alice@acme.com"})
	srv.PublishEvent(ctx, "artifact.published", map[string]any{"id": "finance/run"})

	if !waitFor(t, func() bool { return rr.hits.Load() == 1 }) {
		t.Fatalf("deliveries = %d, want 1", rr.hits.Load())
	}
	var m map[string]any
	if err := json.Unmarshal(rr.snapshot()[0], &m); err != nil {
		t.Fatalf("body parse: %v", err)
	}
	if m["event"] != "artifact.published" {
		t.Errorf("event = %v, want artifact.published", m["event"])
	}
	if m["trace_id"] != "trace-1" {
		t.Errorf("trace_id = %v, want trace-1", m["trace_id"])
	}
	for _, k := range []string{"window", "count", "events"} {
		if _, ok := m[k]; ok {
			t.Errorf("windowless delivery should not carry batch key %q: %v", k, m)
		}
	}
}

// Spec: §7.3.2 — a debounced receiver (non-zero Debounce) receives no immediate
// single-event delivery. The event opens a trailing window through the worker's
// enqueue path; nothing is POSTed until the window expires.
func TestPublishEvent_DebouncedReceiverNoImmediateDelivery(t *testing.T) {
	t.Parallel()
	rr := newRoutingReceiver(t)
	wstore := webhook.NewMemoryStore()
	_ = wstore.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "default", URL: rr.srv.URL, Secret: "s",
		EventFilter: []string{"layer.ingested"}, Debounce: time.Minute,
	})
	srv, _ := newRoutingServer(t, wstore, rr.srv.Client())

	srv.PublishEvent(context.Background(), "layer.ingested", map[string]any{"layer": "team-shared"})

	// Give the fan-out goroutine time to run; a debounced receiver must not
	// receive an immediate single-event POST.
	time.Sleep(150 * time.Millisecond)
	if rr.hits.Load() != 0 {
		t.Fatalf("deliveries = %d, want 0 (debounced receiver should not get an immediate single-event delivery)", rr.hits.Load())
	}
}

// Spec: §7.3.2 — a burst of matched events to a debounced receiver yields
// exactly one batch delivery on window expiry. The events are coalesced and
// deduplicated by (event type, key); the batch envelope carries event:"batch",
// a count, a window, and the coalesced events array, and no single-event POST
// is sent during the burst.
func TestPublishEvent_DebouncedBurstYieldsOneBatch(t *testing.T) {
	t.Parallel()
	rr := newRoutingReceiver(t)
	wstore := webhook.NewMemoryStore()
	_ = wstore.Put(context.Background(), webhook.Receiver{
		ID: "ci", TenantID: "default", URL: rr.srv.URL, Secret: "s",
		EventFilter: []string{"layer.ingested"}, Debounce: time.Minute,
	})
	srv, worker := newRoutingServer(t, wstore, rr.srv.Client())

	// A burst: three distinct layers plus a duplicate of the first.
	srv.PublishEvent(context.Background(), "layer.ingested", map[string]any{"layer": "team-shared"})
	srv.PublishEvent(context.Background(), "layer.ingested", map[string]any{"layer": "platform"})
	srv.PublishEvent(context.Background(), "layer.ingested", map[string]any{"layer": "team-shared"})
	srv.PublishEvent(context.Background(), "layer.ingested", map[string]any{"layer": "personal"})

	// Wait until all four enqueues have opened/extended the window. The window
	// is open once at least one event has been enqueued; give the goroutines
	// time to drain so the batch carries every distinct layer.
	time.Sleep(150 * time.Millisecond)
	if rr.hits.Load() != 0 {
		t.Fatalf("deliveries = %d during burst, want 0 (no single-event delivery for a debounced receiver)", rr.hits.Load())
	}

	// Expire the window deterministically.
	if !worker.Flush("ci") {
		t.Fatal("Flush reported no open window for the debounced receiver")
	}
	if !waitFor(t, func() bool { return rr.hits.Load() == 1 }) {
		t.Fatalf("deliveries after flush = %d, want exactly 1 batch", rr.hits.Load())
	}

	var m map[string]any
	if err := json.Unmarshal(rr.snapshot()[0], &m); err != nil {
		t.Fatalf("body parse: %v", err)
	}
	if m["event"] != "batch" {
		t.Errorf("event = %v, want batch", m["event"])
	}
	// team-shared, platform, personal: the duplicate team-shared coalesces.
	if got := m["count"]; got != float64(3) {
		t.Errorf("count = %v, want 3 (duplicate layer coalesces)", got)
	}
	arr, ok := m["events"].([]any)
	if !ok || len(arr) != 3 {
		t.Fatalf("events should be a 3-element array, got %v", m["events"])
	}
}

// Spec: §7.3.2 — windowless and debounced receivers coexist for the same event.
// A burst delivers immediately to the windowless receiver per event and
// coalesces into one batch for the debounced receiver, so the two paths route
// independently off the same publish.
func TestPublishEvent_MixedReceiversRouteIndependently(t *testing.T) {
	t.Parallel()
	immediate := newRoutingReceiver(t)
	debounced := newRoutingReceiver(t)
	wstore := webhook.NewMemoryStore()
	_ = wstore.Put(context.Background(), webhook.Receiver{
		ID: "chat", TenantID: "default", URL: immediate.srv.URL, Secret: "s",
		EventFilter: []string{"layer.ingested"},
	})
	_ = wstore.Put(context.Background(), webhook.Receiver{
		ID: "ci", TenantID: "default", URL: debounced.srv.URL, Secret: "s",
		EventFilter: []string{"layer.ingested"}, Debounce: time.Minute,
	})
	srv, worker := newRoutingServer(t, wstore, immediate.srv.Client())

	srv.PublishEvent(context.Background(), "layer.ingested", map[string]any{"layer": "a"})
	srv.PublishEvent(context.Background(), "layer.ingested", map[string]any{"layer": "b"})

	// The windowless receiver gets one delivery per event.
	if !waitFor(t, func() bool { return immediate.hits.Load() == 2 }) {
		t.Fatalf("windowless deliveries = %d, want 2", immediate.hits.Load())
	}
	// The debounced receiver has received nothing yet.
	if debounced.hits.Load() != 0 {
		t.Fatalf("debounced deliveries before flush = %d, want 0", debounced.hits.Load())
	}

	if !worker.Flush("ci") {
		t.Fatal("Flush reported no open window for the debounced receiver")
	}
	if !waitFor(t, func() bool { return debounced.hits.Load() == 1 }) {
		t.Fatalf("debounced deliveries after flush = %d, want 1 batch", debounced.hits.Load())
	}
	var m map[string]any
	if err := json.Unmarshal(debounced.snapshot()[0], &m); err != nil {
		t.Fatalf("batch body parse: %v", err)
	}
	if m["event"] != "batch" {
		t.Errorf("debounced receiver event = %v, want batch", m["event"])
	}
}

// Spec: §7.3.2 — a Store.List failure in the fan-out skips outbound delivery for
// the event without panicking or blocking; the bus publish is unaffected. The
// failing store returns an error from List on every call.
func TestPublishEvent_ListErrorSkipsOutboundDelivery(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	worker := &webhook.Worker{Store: failingWebhookStore{}, HTTPClient: http.DefaultClient, Backoff: []time.Duration{}}
	srv := New(core.New(st, "default", nil), WithWebhooks(worker), WithTenant("default"))

	// Must not panic, and the bus publish proceeds.
	srv.PublishEvent(context.Background(), "artifact.published", map[string]any{"id": "x"})
	// Give the fan-out goroutine time to hit the List error and return.
	time.Sleep(100 * time.Millisecond)
}

// failingWebhookStore returns an error from List so the fan-out takes its
// list-error fallback. The other methods are unused.
type failingWebhookStore struct{}

func (failingWebhookStore) List(context.Context, string) ([]webhook.Receiver, error) {
	return nil, context.DeadlineExceeded
}
func (failingWebhookStore) Get(context.Context, string, string) (webhook.Receiver, error) {
	return webhook.Receiver{}, context.DeadlineExceeded
}
func (failingWebhookStore) Put(context.Context, webhook.Receiver) error { return nil }
func (failingWebhookStore) Delete(context.Context, string, string) error { return nil }
