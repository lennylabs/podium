package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/webhook"
)

// recordingReceiver is an httptest receiver that captures every delivered body
// so the debounce integration tests can assert per-event versus batch delivery.
type recordingReceiver struct {
	srv    *httptest.Server
	mu     sync.Mutex
	bodies [][]byte
	hits   atomic.Int64
}

func newRecordingReceiver(t *testing.T) *recordingReceiver {
	t.Helper()
	rr := &recordingReceiver{}
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

func (rr *recordingReceiver) snapshot() [][]byte {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	out := make([][]byte, len(rr.bodies))
	copy(out, rr.bodies)
	return out
}

// registerReceiver registers a receiver through the admin-gated HTTP CRUD
// endpoint and returns the receiver ID the worker uses to address its window.
// debounce is sent only when non-empty, so the windowless caller registers a
// receiver with a zero Debounce.
func registerReceiver(t *testing.T, base, url, eventType, debounce string) string {
	t.Helper()
	payload := map[string]any{
		"url":          url,
		"secret":       "s",
		"event_filter": []string{eventType},
	}
	if debounce != "" {
		payload["debounce"] = debounce
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(base+"/v1/webhooks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/webhooks: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("register status = %d: %s", resp.StatusCode, buf)
	}
	var created struct {
		ID string `json:"ID"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	if created.ID == "" {
		t.Fatal("register response omitted the receiver ID")
	}
	return created.ID
}

// waitForHits polls until rr has received want deliveries or the deadline
// passes. PublishEvent fans out on a goroutine, so the assertions wait for the
// asynchronous delivery rather than reading the counter immediately.
func waitForHits(t *testing.T, rr *recordingReceiver, want int64) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if rr.hits.Load() == want {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return rr.hits.Load() == want
}

// Spec: §7.3.2 — a debounced receiver registered through the admin-gated CRUD
// endpoint collapses a burst of matched events into one batch delivery, and a
// windowless receiver registered alongside it still receives the per-event
// single-event body. The two paths route independently off the same publish, so
// the debounced receiver never also gets the per-event single deliveries
// (guarding against the double-delivery the per-receiver routing prevents).
func TestWebhookDebounce_BatchAndWindowlessCoexist(t *testing.T) {
	t.Parallel()
	windowless := newRecordingReceiver(t)
	debounced := newRecordingReceiver(t)

	// The receiver URLs are http://127.0.0.1 httptest endpoints, so the worker
	// runs with a nil URLPolicy: registration skips the SSRF check and the
	// worker delivers to the loopback test servers. The SSRF policy itself is
	// covered by the hardening tests with a configured policy.
	worker := &webhook.Worker{
		Store:      webhook.NewMemoryStore(),
		HTTPClient: windowless.srv.Client(),
		Backoff:    []time.Duration{},
	}

	srv, ts := bootWebhookRegistry(t, server.WithWebhooks(worker), server.WithTenant("default"))
	t.Cleanup(ts.Close)

	registerReceiver(t, ts.URL, windowless.srv.URL, "layer.ingested", "")
	ciID := registerReceiver(t, ts.URL, debounced.srv.URL, "layer.ingested", "1m")

	// A burst: three distinct layers plus a duplicate of the first.
	for _, l := range []string{"team-shared", "platform", "team-shared", "personal"} {
		srv.PublishEvent(context.Background(), "layer.ingested", map[string]any{"layer": l})
	}

	// The windowless receiver gets one single-event delivery per event.
	if !waitForHits(t, windowless, 4) {
		t.Fatalf("windowless deliveries = %d, want 4 (one per event)", windowless.hits.Load())
	}
	for _, raw := range windowless.snapshot() {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("windowless body parse: %v", err)
		}
		if m["event"] != "layer.ingested" {
			t.Errorf("windowless event = %v, want layer.ingested", m["event"])
		}
		for _, k := range []string{"window", "count", "events"} {
			if _, ok := m[k]; ok {
				t.Errorf("windowless delivery should not carry batch key %q: %v", k, m)
			}
		}
	}

	// The debounced receiver has received nothing during the burst: no
	// single-event POST and no batch until the window expires.
	if debounced.hits.Load() != 0 {
		t.Fatalf("debounced deliveries during burst = %d, want 0", debounced.hits.Load())
	}

	// Expire the window deterministically.
	if !worker.Flush(ciID) {
		t.Fatal("Flush reported no open window for the debounced receiver")
	}
	if !waitForHits(t, debounced, 1) {
		t.Fatalf("debounced deliveries after flush = %d, want exactly 1 batch", debounced.hits.Load())
	}

	var batch map[string]any
	if err := json.Unmarshal(debounced.snapshot()[0], &batch); err != nil {
		t.Fatalf("batch body parse: %v", err)
	}
	if batch["event"] != "batch" {
		t.Errorf("debounced event = %v, want batch", batch["event"])
	}
	// team-shared, platform, personal: the duplicate team-shared coalesces.
	if got := batch["count"]; got != float64(3) {
		t.Errorf("batch count = %v, want 3 (duplicate layer coalesces)", got)
	}
	events, ok := batch["events"].([]any)
	if !ok || len(events) != 3 {
		t.Fatalf("batch events should be a 3-element array, got %v", batch["events"])
	}
	win, ok := batch["window"].(map[string]any)
	if !ok {
		t.Fatalf("batch window should be an object, got %v", batch["window"])
	}
	for _, k := range []string{"start", "end"} {
		if _, ok := win[k]; !ok {
			t.Errorf("batch window missing %q: %v", k, win)
		}
	}
	// Each element of the batch is the complete single-event body.
	for _, e := range events {
		em, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("batch element is not an object: %v", e)
		}
		for _, k := range []string{"event", "trace_id", "timestamp", "actor", "data"} {
			if _, ok := em[k]; !ok {
				t.Errorf("batch element missing single-event field %q: %v", k, em)
			}
		}
		if em["event"] != "layer.ingested" {
			t.Errorf("batch element event = %v, want layer.ingested", em["event"])
		}
	}

	// The debounced receiver received exactly the one batch and no stray
	// single-event delivery from the burst.
	if got := debounced.hits.Load(); got != 1 {
		t.Errorf("debounced deliveries total = %d, want exactly 1", got)
	}
}
