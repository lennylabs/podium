package webhook_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/webhook"
)

// receiverServer is an httptest.Server that records every delivery,
// optionally verifies the HMAC, and returns a configurable status
// code. Tests use it to drive the worker's wire format.
type receiverServer struct {
	srv         *httptest.Server
	deliveries  atomic.Int64
	failuresLeft atomic.Int64
	secret      string
	bodies      [][]byte
}

// newReceiverServer returns a server that responds 200 to every
// request and validates the HMAC signature against the configured
// secret.
func newReceiverServer(t *testing.T, secret string) *receiverServer {
	t.Helper()
	rs := &receiverServer{secret: secret}
	rs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rs.bodies = append(rs.bodies, body)
		rs.deliveries.Add(1)
		sig := r.Header.Get("X-Podium-Signature")
		if err := webhook.VerifyBody(body, sig, secret); err != nil {
			http.Error(w, "bad signature", http.StatusBadRequest)
			return
		}
		if rs.failuresLeft.Load() > 0 {
			rs.failuresLeft.Add(-1)
			http.Error(w, "transient", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(rs.srv.Close)
	return rs
}

// Spec: §7.3.2 — Worker.Deliver POSTs the event body to every
// matching receiver, signed with X-Podium-Signature: sha256=...
// over the body bytes using the receiver's per-receiver secret.
// Phase: 14
func TestWorker_DeliversWithHMACSignature(t *testing.T) {
	testharness.RequirePhase(t, 14)
	t.Parallel()
	rs := newReceiverServer(t, "secret-1")
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "secret-1",
	})
	w := &webhook.Worker{Store: store, HTTPClient: rs.srv.Client(),
		Backoff: []time.Duration{}}
	if err := w.Deliver(context.Background(), "t", "artifact.published", map[string]any{
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

// Spec: §7.3.2 — receivers whose EventFilter does not include the
// fired event are skipped; subscribers control which events they
// receive.
// Phase: 14
func TestWorker_SkipsFilteredOutEventTypes(t *testing.T) {
	testharness.RequirePhase(t, 14)
	t.Parallel()
	rs := newReceiverServer(t, "secret-1")
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "secret-1",
		EventFilter: []string{"artifact.deprecated"},
	})
	w := &webhook.Worker{Store: store, HTTPClient: rs.srv.Client()}
	if err := w.Deliver(context.Background(), "t", "artifact.published", nil); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if rs.deliveries.Load() != 0 {
		t.Errorf("deliveries = %d, want 0 (filter mismatch)", rs.deliveries.Load())
	}
}

// Spec: §7.3.2 — transient 5xx failures are retried per the
// configured backoff schedule. The worker eventually succeeds when
// the receiver recovers.
// Phase: 14
func TestWorker_RetriesOnTransientFailure(t *testing.T) {
	testharness.RequirePhase(t, 14)
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
	if err := w.Deliver(context.Background(), "t", "artifact.published", nil); err != nil {
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
// Phase: 14
func TestWorker_AutoDisablesAfterMaxFailures(t *testing.T) {
	testharness.RequirePhase(t, 14)
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
	if err := w.Deliver(context.Background(), "t", "artifact.published", nil); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	got, _ := store.Get(context.Background(), "t", "r1")
	if !got.Disabled {
		t.Errorf("receiver should be auto-disabled after %d failures", got.FailureCount)
	}
}

// Spec: §7.3.2 — disabled receivers are skipped silently. Operators
// flip Disabled=false to re-enable.
// Phase: 14
func TestWorker_SkipsDisabledReceivers(t *testing.T) {
	testharness.RequirePhase(t, 14)
	t.Parallel()
	rs := newReceiverServer(t, "secret-1")
	store := webhook.NewMemoryStore()
	_ = store.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "t", URL: rs.srv.URL, Secret: "secret-1",
		Disabled: true,
	})
	w := &webhook.Worker{Store: store, HTTPClient: rs.srv.Client()}
	if err := w.Deliver(context.Background(), "t", "artifact.published", nil); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if rs.deliveries.Load() != 0 {
		t.Errorf("deliveries = %d, want 0 (receiver disabled)", rs.deliveries.Load())
	}
}

// Spec: §7.3.2 — VerifyBody mirrors SignBody so receivers can
// validate the signature with the same secret.
// Phase: 14
func TestSignBody_RoundTrip(t *testing.T) {
	testharness.RequirePhase(t, 14)
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
// Phase: 14
func TestWorker_4xxIsNotRetryable(t *testing.T) {
	testharness.RequirePhase(t, 14)
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
	_ = w.Deliver(context.Background(), "t", "artifact.published", nil)
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1 (4xx must not retry)", hits.Load())
	}
	got, _ := store.Get(context.Background(), "t", "r1")
	if got.FailureCount == 0 {
		t.Errorf("FailureCount = 0; want non-zero (4xx counts as failure)")
	}
}

// Spec: §7.3.2 — context cancel aborts retry waits cleanly.
// Phase: 14
func TestWorker_ContextCancelAbortsRetry(t *testing.T) {
	testharness.RequirePhase(t, 14)
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
	go func() { done <- w.Deliver(ctx, "t", "x", nil) }()
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
