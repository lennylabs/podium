package e2e

// Notification delivery sink and override seam.
//
// The standalone harness boots an outbound webhook worker (§7.3.2) and a
// §7.6 /v1/events change stream, but three capabilities were missing from the
// e2e layer, so delivery, filtering, auto-disable, and subscription journeys
// skipped:
//
//   - An in-test recorder reachable from the standalone subprocess that
//     captures every delivered body and verifies the HMAC signature.
//   - Harness control over the §7.3.2 auto-disable threshold (MaxFailures), so
//     the auto-disable path can be driven with a small number of induced
//     failures instead of the production default of 32.
//   - A bounded streaming client for the NDJSON /v1/events change stream, so a
//     subscription can be asserted without an unbounded read.
//
// This file packages all three. notificationSink is an httptest receiver that
// records deliveries, optionally verifies X-Podium-Signature against a
// configured secret, and can be told to fail every delivery (for the
// auto-disable path) or to fail a fixed number of times then recover (for the
// retry path). registerWebhook / getWebhook drive the §7.3.2 receiver CRUD
// over the standalone HTTP surface. startServerWebhooks boots the standalone
// server with the PODIUM_WEBHOOK_MAX_FAILURES override (the new operator
// tunable) so a low threshold makes auto-disable reachable. sseClient opens a
// bounded read of /v1/events, filters heartbeats, and yields decoded events.
//
// This lifts the receiverServer recorder from pkg/webhook/webhook_test.go and
// the extWebhookHarness capture channel from plugin_spi_test.go into one
// reusable primitive that works against the shipped binary over HTTP, plus the
// bounded SSE read pattern from http_api_test.go. Driving the real binary
// (rather than an in-process server) keeps the primitive reusable for the
// CLI-driven outbound-delivery journeys and the SDK subscribe journeys, which
// both run against a standalone subprocess.
//
// Spec: §7.3.2 (outbound webhooks: one POST per (event, receiver) signed with
// X-Podium-Signature: sha256=<hex> over the body; receivers carry an event
// filter; a receiver auto-disables after MaxFailures consecutive failures),
// §7.6 (the registry streams change events as NDJSON over /v1/events?type=...,
// with a periodic {"event":"_heartbeat"} keepalive), §9.1 (the
// NotificationProvider delivers operational notifications; the webhook provider
// posts a signed JSON body).

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/webhook"
)

// recordedDelivery is one captured POST to a notificationSink: the parsed JSON
// body, the raw bytes (for signature re-verification), and whether the HMAC
// signature header verified against the sink's secret.
type recordedDelivery struct {
	Body     map[string]any
	Raw      []byte
	SigValid bool
}

// notificationSink is an in-test receiver that records every delivery. It backs
// both the §7.3.2 outbound webhook receiver and the §9.1 webhook
// NotificationProvider: both POST a JSON body, optionally with an
// X-Podium-Signature HMAC header, so one recorder captures either.
//
// FailEvery, when true, makes the sink reject every delivery with HTTP 503 so
// the worker records a delivery failure (the auto-disable path). FailFirst, when
// positive, rejects the first N deliveries with 503 then accepts (the retry
// path). The two are independent; with both unset the sink accepts every
// delivery with 200.
type notificationSink struct {
	srv    *httptest.Server
	secret string

	mu         sync.Mutex
	deliveries []recordedDelivery

	failEvery atomic.Bool
	failFirst atomic.Int64
}

// sinkOption configures a notificationSink at construction.
type sinkOption func(*notificationSink)

// withSinkSecret sets the HMAC secret the sink verifies X-Podium-Signature
// against. A delivery whose signature does not verify is still recorded (with
// SigValid=false) so a test can assert verification rather than silently drop.
func withSinkSecret(secret string) sinkOption {
	return func(s *notificationSink) { s.secret = secret }
}

// withSinkFailEvery makes the sink reject every delivery with HTTP 503,
// inducing a §7.3.2 delivery failure on each fired event. Used to drive
// auto-disable.
func withSinkFailEvery() sinkOption {
	return func(s *notificationSink) { s.failEvery.Store(true) }
}

// newNotificationSink starts a recording receiver and registers teardown. The
// returned sink is reachable from a standalone subprocess at sink.URL().
func newNotificationSink(t testing.TB, opts ...sinkOption) *notificationSink {
	t.Helper()
	s := &notificationSink{}
	for _, opt := range opts {
		opt(s)
	}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := make([]byte, 0, 1024)
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			raw = append(raw, buf[:n]...)
			if err != nil {
				break
			}
		}
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		sigValid := false
		if s.secret != "" {
			sigValid = webhook.VerifyBody(raw, r.Header.Get("X-Podium-Signature"), s.secret) == nil
		}
		s.mu.Lock()
		s.deliveries = append(s.deliveries, recordedDelivery{Body: body, Raw: raw, SigValid: sigValid})
		s.mu.Unlock()

		if s.failEvery.Load() {
			http.Error(w, "induced failure", http.StatusServiceUnavailable)
			return
		}
		if s.failFirst.Load() > 0 {
			s.failFirst.Add(-1)
			http.Error(w, "transient", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the receiver endpoint the registry POSTs to. It is reachable from a
// standalone subprocess because httptest binds a loopback port.
func (s *notificationSink) URL() string { return s.srv.URL }

// count returns how many deliveries the sink has recorded so far.
func (s *notificationSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.deliveries)
}

// all returns a copy of every recorded delivery in arrival order.
func (s *notificationSink) all() []recordedDelivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]recordedDelivery, len(s.deliveries))
	copy(out, s.deliveries)
	return out
}

// waitForDelivery polls until at least want deliveries have been recorded or
// the deadline elapses, returning whether the count was reached. Outbound
// delivery is fired from a background goroutine in PublishEvent, so a test that
// triggers an event must wait for the asynchronous POST rather than read
// immediately.
func (s *notificationSink) waitForDelivery(want int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if s.count() >= want {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return s.count() >= want
}

// firstMatching returns the first recorded delivery whose body event field
// equals eventType, plus whether one was found.
func (s *notificationSink) firstMatching(eventType string) (recordedDelivery, bool) {
	for _, d := range s.all() {
		if d.Body["event"] == eventType {
			return d, true
		}
	}
	return recordedDelivery{}, false
}

// ---- standalone server with the §7.3.2 MaxFailures override ----------------

// startServerWebhooks boots a standalone server over the given filesystem
// registry with the §7.3.2 auto-disable threshold set to maxFailures (0 leaves
// the worker default of 32) and a fast retry backoff so an induced delivery
// failure is recorded promptly rather than after the full default schedule
// (1s..60s). The outbound webhook worker and the /v1/events change stream are
// always mounted; this only tunes the failure cap and retry cadence so the
// auto-disable path is reachable in a bounded test.
func startServerWebhooks(t testing.TB, registry string, maxFailures int) *serverProc {
	t.Helper()
	env := []string{
		"HOME=" + t.TempDir(),
		// A single 1ms retry: a 5xx delivery fails over once and records the
		// failure immediately, so the consecutive-failure counter advances per
		// event without waiting on the production backoff.
		"PODIUM_WEBHOOK_RETRY_BACKOFF=1ms",
	}
	if maxFailures > 0 {
		env = append(env, "PODIUM_WEBHOOK_MAX_FAILURES="+strconv.Itoa(maxFailures))
	}
	args := []string{"serve", "--standalone"}
	if registry != "" {
		args = append(args, "--layer-path", registry)
	}
	return startServerArgs(t, env, args...)
}

// webhookReceiver is the §7.3.2 receiver record the CRUD surface returns. The
// server marshals pkg/webhook.Receiver, which carries no struct tags, so the
// wire keys are the Go field names (ID, URL, Secret, Disabled, FailureCount).
// The tags below match those names exactly so FailureCount and Disabled decode
// (case-insensitive matching does not bridge the snake_case underscore). The
// secret is present only on the create response (POST); a later GET masks it.
type webhookReceiver struct {
	ID           string   `json:"ID"`
	URL          string   `json:"URL"`
	Secret       string   `json:"Secret"`
	EventFilter  []string `json:"EventFilter"`
	Disabled     bool     `json:"Disabled"`
	FailureCount int      `json:"FailureCount"`
}

// registerWebhook creates a §7.3.2 receiver over POST /v1/webhooks pointing at
// url, with the supplied HMAC secret and event filter (empty filter = all
// events). Supplying the secret (rather than letting the server generate one)
// lets a notificationSink verify the delivery signature against the same value.
// Receiver CRUD is admin-gated (§7.3.2): the callers that drive it run against a
// server whose caller holds the admin role. The standalone de facto admin
// bypass for the webhook route is restored in the serverboot step, so the
// callers of this helper are skipped until then.
func registerWebhook(t testing.TB, srv *serverProc, url, secret string, eventFilter ...string) webhookReceiver {
	t.Helper()
	req := map[string]any{"url": url}
	if secret != "" {
		req["secret"] = secret
	}
	if len(eventFilter) > 0 {
		req["event_filter"] = eventFilter
	}
	st, body := postJSON(t, srv.BaseURL+"/v1/webhooks", req)
	if st != http.StatusCreated {
		t.Fatalf("POST /v1/webhooks = HTTP %d, want 201\nbody: %s", st, body)
	}
	var rec webhookReceiver
	if err := json.Unmarshal(body, &rec); err != nil {
		t.Fatalf("decode webhook create response: %v\nbody: %s", err, body)
	}
	if rec.ID == "" {
		t.Fatalf("webhook create returned empty id: %s", body)
	}
	return rec
}

// getWebhook reads the current §7.3.2 receiver record over GET
// /v1/webhooks/{id}. The returned Disabled and FailureCount reflect the
// worker's persisted delivery state, so a test can assert auto-disable.
func getWebhook(t testing.TB, srv *serverProc, id string) webhookReceiver {
	t.Helper()
	var rec webhookReceiver
	getJSON(t, srv.BaseURL+"/v1/webhooks/"+id, &rec)
	return rec
}

// waitForWebhookDisabled polls GET /v1/webhooks/{id} until the receiver reports
// disabled or the deadline elapses, returning the final record and whether it
// was disabled. The §7.3.2 auto-disable write happens in the worker's delivery
// goroutine after the triggering request returns, so the flip is observed by
// polling rather than reading once.
func waitForWebhookDisabled(t testing.TB, srv *serverProc, id string, within time.Duration) (webhookReceiver, bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	var rec webhookReceiver
	for time.Now().Before(deadline) {
		rec = getWebhook(t, srv, id)
		if rec.Disabled {
			return rec, true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return rec, rec.Disabled
}

// waitForWebhookFailureCount polls GET /v1/webhooks/{id} until the receiver's
// persisted FailureCount reaches at least want or the deadline elapses,
// returning the final record and whether the threshold was reached. The §7.3.2
// failure counter is written by the worker's delivery goroutine, so the
// increment is observed by polling rather than reading once.
func waitForWebhookFailureCount(t testing.TB, srv *serverProc, id string, want int, within time.Duration) (webhookReceiver, bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	var rec webhookReceiver
	for time.Now().Before(deadline) {
		rec = getWebhook(t, srv, id)
		if rec.FailureCount >= want || rec.Disabled {
			return rec, rec.FailureCount >= want || rec.Disabled
		}
		time.Sleep(50 * time.Millisecond)
	}
	return rec, rec.FailureCount >= want || rec.Disabled
}

// ---- bounded SSE / NDJSON change-stream client -----------------------------

// sseClient is a bounded reader of the §7.6 /v1/events NDJSON change stream. It
// opens the connection, reads lines on a background goroutine into a channel,
// and yields decoded events through next, dropping {"event":"_heartbeat"}
// keepalives. close tears the connection down; the client never reads
// unbounded.
type sseClient struct {
	resp   *http.Response
	cancel context.CancelFunc
	events chan registryEventLine
	closed atomic.Bool
}

// registryEventLine is the decoded form of one NDJSON line on the change
// stream. The fields mirror the §7.6 wire schema; permissive typing keeps the
// reader tolerant of added keys.
type registryEventLine struct {
	Event     string         `json:"event"`
	TraceID   string         `json:"trace_id"`
	Timestamp string         `json:"timestamp"`
	Actor     map[string]any `json:"actor"`
	Data      map[string]any `json:"data"`
}

// openSSE opens GET /v1/events?type=... against srv and returns a bounded
// client. eventTypes filters the subscription server-side (empty = all). The
// connection is established synchronously (so the subscription is registered
// before the caller fires a triggering event) and read on a background
// goroutine. The caller must call close, which t.Cleanup also enforces.
func openSSE(t testing.TB, srv *serverProc, eventTypes ...string) *sseClient {
	t.Helper()
	url := srv.BaseURL + "/v1/events"
	for i, et := range eventTypes {
		sep := "&"
		if i == 0 {
			sep = "?"
		}
		url += sep + "type=" + et
	}
	// A dedicated client with no overall timeout: the stream stays open until
	// the test cancels it. The per-test deadline lives on the context.
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		t.Fatalf("build SSE request: %v", err)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		cancel()
		t.Fatalf("GET %s: %v", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		t.Fatalf("GET /v1/events = HTTP %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-ndjson" {
		resp.Body.Close()
		cancel()
		t.Fatalf("Content-Type=%q, want application/x-ndjson", ct)
	}
	c := &sseClient{resp: resp, cancel: cancel, events: make(chan registryEventLine, 16)}
	go c.readLoop()
	t.Cleanup(c.close)
	return c
}

// readLoop reads NDJSON lines until the connection closes, decoding each into
// the events channel. Heartbeat lines are dropped so a caller never has to
// account for them.
func (c *sseClient) readLoop() {
	defer close(c.events)
	r := bufio.NewReader(c.resp.Body)
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			var ev registryEventLine
			if json.Unmarshal([]byte(line), &ev) == nil && ev.Event != "" && ev.Event != "_heartbeat" {
				select {
				case c.events <- ev:
				default:
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// next returns the next non-heartbeat event, or fails the test if none arrives
// within the deadline. It is the bounded read: a wedged stream surfaces as a
// test failure rather than a hang.
func (c *sseClient) next(t testing.TB, within time.Duration) registryEventLine {
	t.Helper()
	select {
	case ev, ok := <-c.events:
		if !ok {
			t.Fatalf("SSE stream closed before an event arrived")
		}
		return ev
	case <-time.After(within):
		t.Fatalf("no SSE event within %s", within)
		return registryEventLine{}
	}
}

// waitForEvent reads events until one with the given type arrives or the
// deadline elapses, returning the matching event and whether it was seen.
// Unlike next it tolerates intervening events of other types.
func (c *sseClient) waitForEvent(t testing.TB, eventType string, within time.Duration) (registryEventLine, bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return registryEventLine{}, false
		}
		select {
		case ev, ok := <-c.events:
			if !ok {
				return registryEventLine{}, false
			}
			if ev.Event == eventType {
				return ev, true
			}
		case <-time.After(remaining):
			return registryEventLine{}, false
		}
	}
}

// close cancels the request context and drains the body so the background read
// loop returns. Safe to call more than once (t.Cleanup plus an explicit call).
func (c *sseClient) close() {
	if !c.closed.CompareAndSwap(false, true) {
		return
	}
	c.cancel()
	if c.resp != nil {
		c.resp.Body.Close()
	}
}

// next is exercised by no test in this file but is provided for the downstream
// subscription gaps that expect exactly one event on the stream; reference it so
// the unused linter keeps the reusable primitive.
var _ = (*sseClient).next
