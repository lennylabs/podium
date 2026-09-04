package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/webhook"
)

// receiverObjectMembers is the §7.3.2 receiver object minus debounce, which
// §7.3.2 states is omitted by a receiver that sets none. Every arm asserts
// set equality against this list plus debounce where its fixture sets one, so
// a member added later without a spec decision fails the test rather than
// reaching the wire unnoticed. That is what pins the withheld tenant
// identifier: the projection drops it by not naming it, which is invisible at
// the edit site.
var receiverObjectMembers = []string{
	"id", "url", "secret", "event_filter", "disabled", "failure_count",
	"last_delivery", "last_failure", "created_at",
}

// wantReceiverMembers returns the base member list plus the conditional ones.
func wantReceiverMembers(extra ...string) []string {
	out := append(append([]string{}, receiverObjectMembers...), extra...)
	sort.Strings(out)
	return out
}

// receiverObject decodes one receiver object without interpreting any member,
// so an assertion reads the wire names rather than a Go struct's fields.
func receiverObject(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("decode receiver object: %v\nobject: %s", err, raw)
	}
	return obj
}

// assertReceiverMembers asserts set equality between a receiver object's
// members and the expected set.
func assertReceiverMembers(t *testing.T, where string, obj map[string]json.RawMessage, want []string) {
	t.Helper()
	got := make([]string, 0, len(obj))
	for k := range obj {
		got = append(got, k)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: receiver members = %v, want %v", where, got, want)
	}
}

// receiverString reads a string member out of a decoded receiver object.
func receiverString(t *testing.T, obj map[string]json.RawMessage, name string) string {
	t.Helper()
	raw, ok := obj[name]
	if !ok {
		t.Fatalf("receiver object carries no %q member", name)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("decode %q: %v", name, err)
	}
	return s
}

// postReceiver creates a receiver and returns its decoded create response.
func postReceiver(t *testing.T, base string, body map[string]any) map[string]json.RawMessage {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(base+"/v1/webhooks", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("POST /v1/webhooks: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/webhooks status = %d: %s", resp.StatusCode, out)
	}
	return receiverObject(t, out)
}

// getReceiverList returns the receiver objects under "receivers".
func getReceiverList(t *testing.T, base string) []map[string]json.RawMessage {
	t.Helper()
	resp, err := http.Get(base + "/v1/webhooks")
	if err != nil {
		t.Fatalf("GET /v1/webhooks: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/webhooks status = %d: %s", resp.StatusCode, out)
	}
	var env struct {
		Receivers []json.RawMessage `json:"receivers"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("decode list envelope: %v\nbody: %s", err, out)
	}
	list := make([]map[string]json.RawMessage, 0, len(env.Receivers))
	for _, raw := range env.Receivers {
		list = append(list, receiverObject(t, raw))
	}
	return list
}

// getReceiver reads one receiver.
func getReceiver(t *testing.T, base, id string) map[string]json.RawMessage {
	t.Helper()
	resp, err := http.Get(base + "/v1/webhooks/" + id)
	if err != nil {
		t.Fatalf("GET /v1/webhooks/%s: %v", id, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/webhooks/%s status = %d: %s", id, resp.StatusCode, out)
	}
	return receiverObject(t, out)
}

// Spec: §7.2.1 / §7.3.2 — the receiver object names its members in lower
// snake_case, carries no tenant identifier, reports debounce as the duration
// string the request body accepts, and returns the secret unmasked on the
// creating response alone.
func TestReceiverWire_NamesTheSpecMembersOnEveryPath(t *testing.T) {
	t.Parallel()
	wstore := webhook.NewMemoryStore()
	worker := &webhook.Worker{Store: wstore, HTTPClient: http.DefaultClient}
	_, ts := bootWebhookRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)

	created := postReceiver(t, ts.URL, map[string]any{
		"url":          "https://example.test/receiver",
		"event_filter": []string{"artifact.published"},
		"secret":       "s3cret-value",
		"debounce":     "60s",
	})
	want := wantReceiverMembers("debounce")
	assertReceiverMembers(t, "create", created, want)
	if got := receiverString(t, created, "secret"); got != "s3cret-value" {
		t.Errorf("create secret = %q, want the unmasked value", got)
	}
	if got := receiverString(t, created, "debounce"); got != "1m0s" {
		t.Errorf("create debounce = %q, want %q", got, "1m0s")
	}
	id := receiverString(t, created, "id")

	list := getReceiverList(t, ts.URL)
	if len(list) != 1 {
		t.Fatalf("list returned %d receivers, want 1", len(list))
	}
	assertReceiverMembers(t, "list", list[0], want)
	if got := receiverString(t, list[0], "secret"); got != "***" {
		t.Errorf("list secret = %q, want the masked value", got)
	}

	read := getReceiver(t, ts.URL, id)
	assertReceiverMembers(t, "read", read, want)
	if got := receiverString(t, read, "secret"); got != "***" {
		t.Errorf("read secret = %q, want the masked value", got)
	}
	if got := receiverString(t, read, "debounce"); got != "1m0s" {
		t.Errorf("read debounce = %q, want %q", got, "1m0s")
	}

	// DEFECT-3: the read is fed back into the update verbatim. The update
	// accepts the reported debounce string without conversion, which is the
	// round trip an integer-valued debounce made impossible.
	resp, body := mustPut(t, ts.URL, "/v1/webhooks/"+id, read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT of the read-back receiver status = %d: %s", resp.StatusCode, body)
	}
	updated := receiverObject(t, body)
	assertReceiverMembers(t, "update", updated, want)
	if got := receiverString(t, updated, "secret"); got != "***" {
		t.Errorf("update secret = %q, want the masked value", got)
	}
	if got := receiverString(t, updated, "debounce"); got != "1m0s" {
		t.Errorf("update debounce = %q, want %q", got, "1m0s")
	}
	// §7.2.1: the read carries the mask in the secret member's place, so the
	// round trip resends it. The mask is not a credential the update minted,
	// and storing it would sign every later delivery with "***".
	stored, err := wstore.Get(context.Background(), "default", id)
	if err != nil {
		t.Fatalf("read the receiver back out of the store: %v", err)
	}
	if stored.Secret != "s3cret-value" {
		t.Errorf("stored secret after the round trip = %q, want the minted value", stored.Secret)
	}
}

// Spec: §7.2.1 / §7.3.2 — a PUT naming a secret rotates the receiver's HMAC
// key, and the response reports the rotated value under the mask.
func TestReceiverWire_UpdateRotatesTheSecretWhenTheBodyNamesANewOne(t *testing.T) {
	t.Parallel()
	wstore := webhook.NewMemoryStore()
	worker := &webhook.Worker{Store: wstore, HTTPClient: http.DefaultClient}
	_, ts := bootWebhookRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)

	created := postReceiver(t, ts.URL, map[string]any{
		"url":    "https://example.test/receiver",
		"secret": "s3cret-value",
	})
	id := receiverString(t, created, "id")

	resp, body := mustPut(t, ts.URL, "/v1/webhooks/"+id, map[string]any{"secret": "rotated-value"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d: %s", resp.StatusCode, body)
	}
	if got := receiverString(t, receiverObject(t, body), "secret"); got != "***" {
		t.Errorf("update secret = %q, want the masked value", got)
	}
	stored, err := wstore.Get(context.Background(), "default", id)
	if err != nil {
		t.Fatalf("read the receiver back out of the store: %v", err)
	}
	if stored.Secret != "rotated-value" {
		t.Errorf("stored secret = %q, want the rotated value", stored.Secret)
	}
}

// Spec: §7.3.2 — re-enabling a disabled receiver clears its failure budget, so
// the worker retries it. The projection reports both members, and the update
// keeps applying the fields the body carries.
func TestReceiverWire_ReEnableClearsTheFailureCount(t *testing.T) {
	t.Parallel()
	wstore := webhook.NewMemoryStore()
	worker := &webhook.Worker{Store: wstore, HTTPClient: http.DefaultClient}
	_, ts := bootWebhookRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)

	ctx := context.Background()
	rec := webhook.Receiver{
		ID: "wh-disabled", TenantID: "default", URL: "https://example.test/receiver",
		Secret: "k", Disabled: true, FailureCount: 31,
	}
	if err := wstore.Put(ctx, rec); err != nil {
		t.Fatalf("Put: %v", err)
	}
	resp, body := mustPut(t, ts.URL, "/v1/webhooks/wh-disabled", map[string]any{"disabled": false})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d: %s", resp.StatusCode, body)
	}
	stored, err := wstore.Get(ctx, "default", "wh-disabled")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Disabled || stored.FailureCount != 0 {
		t.Errorf("after the re-enable: disabled = %v, failure count = %d, want false and 0",
			stored.Disabled, stored.FailureCount)
	}
	updated := receiverObject(t, body)
	if got := receiverString(t, updated, "id"); got != "wh-disabled" {
		t.Errorf("update id = %q, want wh-disabled", got)
	}
	assertReceiverMembers(t, "update", updated, wantReceiverMembers())
}

// Spec: §7.3.2 — a receiver that sets no debounce omits the member on every
// path, and the object still carries no tenant identifier.
func TestReceiverWire_OmitsDebounceWhenTheReceiverSetsNone(t *testing.T) {
	t.Parallel()
	worker := &webhook.Worker{Store: webhook.NewMemoryStore(), HTTPClient: http.DefaultClient}
	_, ts := bootWebhookRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)

	created := postReceiver(t, ts.URL, map[string]any{
		"url":          "https://example.test/receiver",
		"event_filter": []string{"artifact.published"},
	})
	want := wantReceiverMembers()
	assertReceiverMembers(t, "create", created, want)
	id := receiverString(t, created, "id")

	list := getReceiverList(t, ts.URL)
	if len(list) != 1 {
		t.Fatalf("list returned %d receivers, want 1", len(list))
	}
	assertReceiverMembers(t, "list", list[0], want)
	assertReceiverMembers(t, "read", getReceiver(t, ts.URL, id), want)

	resp, body := mustPut(t, ts.URL, "/v1/webhooks/"+id, map[string]any{"disabled": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d: %s", resp.StatusCode, body)
	}
	assertReceiverMembers(t, "update", receiverObject(t, body), want)
}

// assertUTC asserts that every timestamp member of a receiver object is
// emitted in UTC, which RFC 3339 spells with a trailing Z.
func assertUTC(t *testing.T, where string, obj map[string]json.RawMessage) {
	t.Helper()
	for _, name := range []string{"created_at", "last_delivery", "last_failure"} {
		if got := receiverString(t, obj, name); !strings.HasSuffix(got, "Z") {
			t.Errorf("%s: %s = %q, want a UTC timestamp", where, name, got)
		}
	}
}

// Spec: §7.2.1 — a stored receiver whose timestamps carry a zone offset is
// still emitted in UTC, because neither store normalizes on the round trip and
// the projection is the one site that governs the emitted form.
func TestReceiverWire_StoredOffsetTimestampsAreEmittedInUTC(t *testing.T) {
	t.Parallel()
	zone := time.FixedZone("IST", 5*3600+1800)
	stamp := time.Date(2026, 6, 30, 12, 0, 0, 0, zone)
	wstore := webhook.NewMemoryStore()
	rec := webhook.Receiver{
		ID: "wh-seeded", TenantID: "default", URL: "https://example.test/receiver",
		Secret: "k", CreatedAt: stamp, LastDelivery: stamp, LastFailure: stamp,
	}
	if err := wstore.Put(context.Background(), rec); err != nil {
		t.Fatalf("Put: %v", err)
	}
	worker := &webhook.Worker{Store: wstore, HTTPClient: http.DefaultClient}
	_, ts := bootWebhookRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)

	list := getReceiverList(t, ts.URL)
	if len(list) != 1 {
		t.Fatalf("list returned %d receivers, want 1", len(list))
	}
	assertUTC(t, "list", list[0])
	assertUTC(t, "read", getReceiver(t, ts.URL, "wh-seeded"))
}

// Spec: §7.2.1 — the delivery stamps the worker writes are emitted in UTC.
// Worker.recordResult assigns them from a clock that falls back to bare
// time.Now, so a registry process outside UTC persists an offset that only the
// projection removes.
func TestReceiverWire_WorkerWrittenStampsAreEmittedInUTC(t *testing.T) {
	t.Parallel()
	zone := time.FixedZone("IST", 5*3600+1800)
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, zone)

	// The sink refuses with 400 rather than 500, which the worker treats as
	// non-retryable, so the arm records a failure without walking the retry
	// schedule.
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(failing.Close)
	succeeding := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(succeeding.Close)

	wstore := webhook.NewMemoryStore()
	worker := &webhook.Worker{
		Store:      wstore,
		HTTPClient: failing.Client(),
		Now:        func() time.Time { return now },
	}
	ctx := context.Background()
	rec := webhook.Receiver{
		ID: "wh-worker", TenantID: "default", URL: failing.URL, Secret: "k",
		EventFilter: []string{"artifact.published"}, CreatedAt: now,
	}
	if err := wstore.Put(ctx, rec); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := worker.Deliver(ctx, "default", "artifact.published", "tr-1", nil, map[string]any{}); err != nil {
		t.Fatalf("Deliver against the failing sink: %v", err)
	}
	stored, err := wstore.Get(ctx, "default", "wh-worker")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.LastFailure.IsZero() {
		t.Fatal("the failing delivery recorded no last_failure")
	}
	stored.URL = succeeding.URL
	if err := wstore.Put(ctx, stored); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := worker.Deliver(ctx, "default", "artifact.published", "tr-2", nil, map[string]any{}); err != nil {
		t.Fatalf("Deliver against the succeeding sink: %v", err)
	}

	_, ts := bootWebhookRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)
	assertUTC(t, "read", getReceiver(t, ts.URL, "wh-worker"))
}
