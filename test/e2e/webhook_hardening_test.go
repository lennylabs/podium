package e2e

// End-to-end tests for docs/reference/http-api.md outbound webhooks (D-webhook-hardening).
//
// The §7.3.2 receiver CRUD endpoints are admin-gated and validate the receiver
// URL against an SSRF policy at registration, and a receiver with a debounce
// window coalesces a burst of matched events into one batch delivery. These
// tests drive the shipped binary in injected-session-token mode with a
// bootstrap admin so the gate runs against a verified identity, and assert the
// observable results: the HTTP status, the §6.10 error envelope code and
// message, and the delivered body keys.
//
// Spec: §7.3.2 (receiver authorization, the receiver-URL SSRF policy, and the
// per-receiver debounce window with its batch delivery body), §6.10 (the
// auth.forbidden and registry.invalid_argument error codes).

import (
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// webhookHardeningServer boots an admin-gated webhook server with no seed
// registry, returning the server and the runtime private key so a test mints
// both the admin and a non-admin token. The boot options populate
// PODIUM_WEBHOOK_ALLOWED_TARGETS (withAllowedHost / withSink) and the TLS sink
// trust (withSink) so an otherwise-blocked internal receiver passes the SSRF
// policy; no options leaves the strict default in effect.
func webhookHardeningServer(t *testing.T, opts ...bootOption) (*serverProc, *rsa.PrivateKey) {
	t.Helper()
	return bootWebhookAdminServer(t, "", opts...)
}

// Spec: §7.3.2 — receiver CRUD is gated on the per-tenant admin role. A verified
// non-admin caller (carol) is refused with auth.forbidden (403) before any
// receiver is written, closing the gap where any authenticated caller could
// register a receiver.
func TestWebhookHardening_AdminGate(t *testing.T) {
	t.Parallel()
	// Allowlist the receiver host so the admin's create passes the SSRF address
	// check without a DNS lookup; the gate, rather than the SSRF check, is under
	// test here. The host's https URL satisfies the scheme requirement.
	const receiverHost = "receiver.acme.com"
	srv, priv := webhookHardeningServer(t, withAllowedHost(receiverHost))
	carolToken := injSignJWT(t, priv, injClaims("carol@acme.com")) // verified non-admin

	body, _ := json.Marshal(map[string]any{"url": "https://" + receiverHost + "/hook"})

	// A verified non-admin is refused with auth.forbidden.
	st, resp := webhookBearer(t, http.MethodPost, srv.BaseURL+"/v1/webhooks", carolToken, body)
	if st != http.StatusForbidden {
		t.Fatalf("non-admin POST /v1/webhooks = %d, want 403\nbody: %s", st, resp)
	}
	if code := envelopeCode(t, resp); code != "auth.forbidden" {
		t.Errorf("non-admin POST code = %q, want auth.forbidden\nbody: %s", code, resp)
	}

	// An unauthenticated caller is also refused (never 201).
	if st, _ := webhookBearer(t, http.MethodPost, srv.BaseURL+"/v1/webhooks", "", body); st == http.StatusCreated {
		t.Errorf("unauthenticated POST /v1/webhooks = 201, want a rejection")
	}

	// The admin can register: the same body that the non-admin was refused for
	// succeeds for alice, proving the gate (not the body) caused the 403. The
	// https public host passes the strict default SSRF policy.
	if st, resp := webhookBearer(t, http.MethodPost, srv.BaseURL+"/v1/webhooks", srv.adminToken, body); st != http.StatusCreated {
		t.Errorf("admin POST /v1/webhooks = %d, want 201\nbody: %s", st, resp)
	}
}

// Spec: §7.3.2 — the receiver-URL SSRF policy rejects an http or private-address
// target at registration with registry.invalid_argument naming the host. The
// request carries the admin token so the failure is the SSRF check rather than
// the admin gate.
func TestWebhookHardening_SSRFRejectsAtRegistration(t *testing.T) {
	t.Parallel()
	srv, _ := webhookHardeningServer(t)

	cases := []struct {
		name    string
		url     string
		hostHit string
	}{
		// http is rejected: the policy requires https.
		{name: "http scheme", url: "http://receiver.acme.com/hook", hostHit: "receiver.acme.com"},
		// A loopback literal is a blocked private target.
		{name: "loopback literal", url: "https://127.0.0.1:9000/hook", hostHit: "127.0.0.1"},
		// An RFC 1918 literal is a blocked private target.
		{name: "private literal", url: "https://10.1.2.3/hook", hostHit: "10.1.2.3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"url": tc.url})
			st, resp := webhookBearer(t, http.MethodPost, srv.BaseURL+"/v1/webhooks", srv.adminToken, body)
			if st != http.StatusBadRequest {
				t.Fatalf("admin POST %s = %d, want 400\nbody: %s", tc.url, st, resp)
			}
			var env struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(resp, &env); err != nil {
				t.Fatalf("decode error envelope: %v\nbody: %s", err, resp)
			}
			if env.Code != "registry.invalid_argument" {
				t.Errorf("SSRF rejection code = %q, want registry.invalid_argument", env.Code)
			}
			if !strings.Contains(env.Message, tc.hostHit) {
				t.Errorf("SSRF rejection message = %q, want it to name the host %q", env.Message, tc.hostHit)
			}
		})
	}
}

// Spec: §7.3.2 / §13.12 — PODIUM_WEBHOOK_ALLOWED_TARGETS overrides the SSRF
// rejection for a deployment with an internal receiver. With the loopback sink
// host allowlisted, the admin registers a receiver pointed at the loopback
// httptest target and the worker delivers the artifact.published event the
// reingest fires.
func TestWebhookHardening_AllowlistPermitsInternalTarget(t *testing.T) {
	t.Parallel()
	requireSubprocessTLSTrust(t)
	const secret = "wh-allowlist-secret"
	sink := newNotificationSink(t, withSinkTLS(), withSinkSecret(secret))

	// Without the allowlist the loopback address is rejected; with it the
	// registration succeeds (https satisfied by the TLS sink) and the worker
	// delivers to the loopback target.
	srv, _ := webhookHardeningServer(t, withSink(t, sink))
	registerWebhook(t, srv, sink.URL(), secret, "artifact.published")

	rl := newRepublishLayer(t, srv, "allowlist-layer")
	rl.publishVersion(t, versionSpec{
		ID:          "ops/allowlisted",
		Version:     "1.0.0",
		Description: "allowlisted internal receiver delivery probe here today",
	})

	if !sink.waitForDelivery(1, 5*time.Second) {
		t.Fatalf("allowlisted internal receiver recorded no delivery\nserver log:\n%s", srv.log())
	}
	d, ok := sink.firstMatching("artifact.published")
	if !ok {
		t.Fatalf("no artifact.published delivery to the allowlisted receiver: %+v", sink.all())
	}
	if !d.SigValid {
		t.Errorf("allowlisted delivery failed HMAC verification against the receiver secret")
	}
}

// Spec: §7.3.2 — a receiver with a debounce window collapses a burst of matched
// events into one batch delivery. The batch carries the {event:"batch", window,
// count, events} envelope, where each element of events is the complete
// single-event body. A single reingest of a layer with several artifacts fires
// several artifact.published events inside the window, so the burst is
// deterministic without racing the trailing timer.
func TestWebhookHardening_DebouncedBurstBatches(t *testing.T) {
	t.Parallel()
	requireSubprocessTLSTrust(t)
	const secret = "wh-debounce-secret"
	sink := newNotificationSink(t, withSinkTLS(), withSinkSecret(secret))

	srv, _ := webhookHardeningServer(t, withSink(t, sink))
	// A 3s window outlasts the single synchronous reingest below, so every
	// artifact.published in the burst lands in one window and one batch delivers
	// when the trailing timer expires.
	registerWebhookDebounced(t, srv, sink.URL(), secret, "3s", "artifact.published")

	// Stage three artifacts in one layer and reingest once: the ingest pipeline
	// fires one artifact.published per artifact, so one cycle is a burst of three
	// matched events inside the window.
	ids := []string{"ops/alpha", "ops/bravo", "ops/charlie"}
	newRepublishLayerMulti(t, srv, "debounce-layer", ids, "1.0.0")

	// Exactly one batch delivery after the window expires: three fired events
	// collapse into one delivery, which is the coalescing the window provides.
	if !sink.waitForDelivery(1, 8*time.Second) {
		t.Fatalf("no batch delivery after the debounce window\nserver log:\n%s", srv.log())
	}
	// Give any erroneous extra delivery time to arrive, then assert exactly one.
	time.Sleep(500 * time.Millisecond)
	if n := sink.count(); n != 1 {
		t.Fatalf("debounced receiver delivered %d times, want exactly 1 batch (3 events coalesced): %+v", n, sink.all())
	}

	d := sink.all()[0]
	if !d.SigValid {
		t.Errorf("batch delivery failed HMAC verification against the receiver secret")
	}
	if d.Body["event"] != "batch" {
		t.Fatalf("batch event = %v, want batch\nbody: %+v", d.Body["event"], d.Body)
	}
	// The envelope carries window, count, and events.
	win, ok := d.Body["window"].(map[string]any)
	if !ok {
		t.Fatalf("batch window is not an object: %v", d.Body["window"])
	}
	for _, k := range []string{"start", "end"} {
		if _, ok := win[k]; !ok {
			t.Errorf("batch window missing %q: %v", k, win)
		}
	}
	count, _ := d.Body["count"].(float64)
	if int(count) != len(ids) {
		t.Errorf("batch count = %v, want %d (one per published artifact)", d.Body["count"], len(ids))
	}
	events, ok := d.Body["events"].([]any)
	if !ok || len(events) != len(ids) {
		t.Fatalf("batch events should be a %d-element array, got %v", len(ids), d.Body["events"])
	}
	// Each element is the complete single-event body for one coalesced event.
	seen := map[string]bool{}
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
		if em["event"] != "artifact.published" {
			t.Errorf("batch element event = %v, want artifact.published", em["event"])
		}
		if data, ok := em["data"].(map[string]any); ok {
			if id, _ := data["id"].(string); id != "" {
				seen[id] = true
			}
		}
	}
	for _, id := range ids {
		if !seen[id] {
			t.Errorf("batch missing the published artifact %q; saw %v", id, seen)
		}
	}
}

// envelopeCode decodes the §6.10 error envelope and returns its code field.
func envelopeCode(t testing.TB, body []byte) string {
	t.Helper()
	var env struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v\nbody: %s", err, body)
	}
	return env.Code
}
