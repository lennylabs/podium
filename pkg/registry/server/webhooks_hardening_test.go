package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/webhook"
)

// webhookError decodes a §6.10 error envelope and returns its code and message.
func webhookError(t *testing.T, resp *http.Response) (string, string) {
	t.Helper()
	var env struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	buf, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(buf, &env); err != nil {
		t.Fatalf("decode envelope %q: %v", buf, err)
	}
	return env.Code, env.Message
}

// Spec: §7.3.2 / §6.10 — the receiver CRUD endpoints are gated on the
// per-tenant admin role. A non-admin caller is refused with auth.forbidden
// (403) on every verb of both the collection and per-receiver routes, before
// the read-only check or any store access.
func TestWebhookReceiverCRUD_NonAdminForbidden(t *testing.T) {
	t.Parallel()
	wstore := webhook.NewMemoryStore()
	if err := wstore.Put(context.Background(), webhook.Receiver{
		ID: "wh-1", TenantID: "default", URL: "https://example.test/hook", Secret: "shh",
	}); err != nil {
		t.Fatalf("seed receiver: %v", err)
	}
	worker := &webhook.Worker{Store: wstore, HTTPClient: http.DefaultClient}
	// bootRegistry installs the default anonymous (public) caller, which
	// AdminAuthorize rejects, so every receiver verb is forbidden.
	_, ts := bootRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"list", http.MethodGet, "/v1/webhooks", ""},
		{"create", http.MethodPost, "/v1/webhooks", `{"url":"https://example.test/new"}`},
		{"get", http.MethodGet, "/v1/webhooks/wh-1", ""},
		{"update", http.MethodPut, "/v1/webhooks/wh-1", `{"disabled":true}`},
		{"delete", http.MethodDelete, "/v1/webhooks/wh-1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(tc.method, ts.URL+tc.path, strings.NewReader(tc.body))
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", tc.method, tc.path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", resp.StatusCode)
			}
			if code, _ := webhookError(t, resp); code != "auth.forbidden" {
				t.Errorf("code = %q, want auth.forbidden", code)
			}
		})
	}
}

// Spec: §7.3.2 — the admin gate runs before the read-only check, so a
// non-admin caller against a read-only registry is refused with
// auth.forbidden rather than registry.read_only.
func TestWebhookReceiverCreate_AdminGateBeforeReadOnly(t *testing.T) {
	t.Parallel()
	worker := &webhook.Worker{Store: webhook.NewMemoryStore()}
	mode := server.NewModeTracker()
	mode.Set(server.ModeReadOnly)
	_, ts := bootRegistry(t, server.WithWebhooks(worker), server.WithMode(mode))
	t.Cleanup(ts.Close)

	resp, err := http.Post(ts.URL+"/v1/webhooks", "application/json",
		strings.NewReader(`{"url":"https://example.test/new"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if code, _ := webhookError(t, resp); code != "auth.forbidden" {
		t.Errorf("code = %q, want auth.forbidden", code)
	}
}

// Spec: §7.3.2 — POST /v1/webhooks accepts a debounce window as a Go
// duration string and stores it on the receiver. The created receiver
// reports the parsed window back to the worker store.
func TestWebhookReceiverCreate_DebounceStored(t *testing.T) {
	t.Parallel()
	wstore := webhook.NewMemoryStore()
	worker := &webhook.Worker{Store: wstore}
	_, ts := bootWebhookRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]any{
		"url":      "https://example.test/hook",
		"debounce": "30s",
	})
	resp, err := http.Post(ts.URL+"/v1/webhooks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d: %s", resp.StatusCode, buf)
	}
	var created struct {
		ID string `json:"ID"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	rec, err := wstore.Get(context.Background(), "default", created.ID)
	if err != nil {
		t.Fatalf("Get receiver: %v", err)
	}
	if rec.Debounce != 30*time.Second {
		t.Errorf("stored Debounce = %v, want 30s", rec.Debounce)
	}
}

// Spec: §7.3.2 / §6.10 — an unparsable debounce value is rejected with
// registry.invalid_argument naming the debounce field.
func TestWebhookReceiverCreate_DebounceInvalid(t *testing.T) {
	t.Parallel()
	worker := &webhook.Worker{Store: webhook.NewMemoryStore()}
	_, ts := bootWebhookRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]any{
		"url":      "https://example.test/hook",
		"debounce": "soon",
	})
	resp, err := http.Post(ts.URL+"/v1/webhooks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	code, msg := webhookError(t, resp)
	if code != "registry.invalid_argument" {
		t.Errorf("code = %q, want registry.invalid_argument", code)
	}
	if !strings.Contains(msg, "debounce") {
		t.Errorf("message %q does not name the debounce field", msg)
	}
}

// Spec: §7.3.2 / §6.10 — a negative debounce window is rejected with
// registry.invalid_argument; a trailing window cannot run backward.
func TestWebhookReceiverCreate_DebounceNegative(t *testing.T) {
	t.Parallel()
	worker := &webhook.Worker{Store: webhook.NewMemoryStore()}
	_, ts := bootWebhookRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]any{
		"url":      "https://example.test/hook",
		"debounce": "-5s",
	})
	resp, err := http.Post(ts.URL+"/v1/webhooks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	code, msg := webhookError(t, resp)
	if code != "registry.invalid_argument" {
		t.Errorf("code = %q, want registry.invalid_argument", code)
	}
	if !strings.Contains(msg, "debounce") {
		t.Errorf("message %q does not name the debounce field", msg)
	}
}

// Spec: §7.3.2 — PUT /v1/webhooks/{id} accepts a debounce edit and updates
// the stored window.
func TestWebhookReceiverUpdate_DebounceStored(t *testing.T) {
	t.Parallel()
	wstore := webhook.NewMemoryStore()
	const id = "wh-debounce"
	if err := wstore.Put(context.Background(), webhook.Receiver{
		ID: id, TenantID: "default", URL: "https://example.test/hook", Secret: "shh",
	}); err != nil {
		t.Fatalf("seed receiver: %v", err)
	}
	worker := &webhook.Worker{Store: wstore}
	_, ts := bootWebhookRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)

	patch, _ := json.Marshal(map[string]any{"debounce": "1m"})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/webhooks/"+id, bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d: %s", resp.StatusCode, buf)
	}
	rec, err := wstore.Get(context.Background(), "default", id)
	if err != nil {
		t.Fatalf("Get receiver: %v", err)
	}
	if rec.Debounce != time.Minute {
		t.Errorf("stored Debounce = %v, want 1m", rec.Debounce)
	}
}

// Spec: §7.3.2 / §6.10 — a debounce edit with an unparsable value is
// rejected with registry.invalid_argument naming the debounce field.
func TestWebhookReceiverUpdate_DebounceInvalid(t *testing.T) {
	t.Parallel()
	wstore := webhook.NewMemoryStore()
	const id = "wh-debounce-bad"
	if err := wstore.Put(context.Background(), webhook.Receiver{
		ID: id, TenantID: "default", URL: "https://example.test/hook", Secret: "shh",
	}); err != nil {
		t.Fatalf("seed receiver: %v", err)
	}
	worker := &webhook.Worker{Store: wstore}
	_, ts := bootWebhookRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)

	patch, _ := json.Marshal(map[string]any{"debounce": "later"})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/webhooks/"+id, bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	code, msg := webhookError(t, resp)
	if code != "registry.invalid_argument" {
		t.Errorf("code = %q, want registry.invalid_argument", code)
	}
	if !strings.Contains(msg, "debounce") {
		t.Errorf("message %q does not name the debounce field", msg)
	}
}

// Spec: §7.3.2 — POST /v1/webhooks validates the receiver URL against the
// worker's SSRF policy. A loopback target is refused with
// registry.invalid_argument naming the disallowed host.
func TestWebhookReceiverCreate_SSRFRejectsLoopback(t *testing.T) {
	t.Parallel()
	policy, err := webhook.NewURLPolicy(nil)
	if err != nil {
		t.Fatalf("NewURLPolicy: %v", err)
	}
	worker := &webhook.Worker{Store: webhook.NewMemoryStore(), URLPolicy: policy}
	_, ts := bootWebhookRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]any{"url": "https://127.0.0.1/hook"})
	resp, err := http.Post(ts.URL+"/v1/webhooks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	code, msg := webhookError(t, resp)
	if code != "registry.invalid_argument" {
		t.Errorf("code = %q, want registry.invalid_argument", code)
	}
	if !strings.Contains(msg, "127.0.0.1") {
		t.Errorf("message %q does not name the disallowed host", msg)
	}
}

// Spec: §7.3.2 — POST /v1/webhooks rejects a non-https receiver URL under
// the SSRF policy.
func TestWebhookReceiverCreate_SSRFRejectsHTTP(t *testing.T) {
	t.Parallel()
	policy, err := webhook.NewURLPolicy(nil)
	if err != nil {
		t.Fatalf("NewURLPolicy: %v", err)
	}
	worker := &webhook.Worker{Store: webhook.NewMemoryStore(), URLPolicy: policy}
	_, ts := bootWebhookRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]any{"url": "http://example.test/hook"})
	resp, err := http.Post(ts.URL+"/v1/webhooks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if code, _ := webhookError(t, resp); code != "registry.invalid_argument" {
		t.Errorf("code = %q, want registry.invalid_argument", code)
	}
}

// Spec: §7.3.2 — PUT /v1/webhooks/{id} re-validates the receiver URL when
// the edit changes it, so a receiver cannot be moved to a disallowed target.
func TestWebhookReceiverUpdate_SSRFRejectsLoopback(t *testing.T) {
	t.Parallel()
	wstore := webhook.NewMemoryStore()
	const id = "wh-ssrf"
	if err := wstore.Put(context.Background(), webhook.Receiver{
		ID: id, TenantID: "default", URL: "https://example.test/hook", Secret: "shh",
	}); err != nil {
		t.Fatalf("seed receiver: %v", err)
	}
	policy, err := webhook.NewURLPolicy(nil)
	if err != nil {
		t.Fatalf("NewURLPolicy: %v", err)
	}
	worker := &webhook.Worker{Store: wstore, URLPolicy: policy}
	_, ts := bootWebhookRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)

	patch, _ := json.Marshal(map[string]any{"url": "https://10.0.0.5/hook"})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/webhooks/"+id, bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	code, msg := webhookError(t, resp)
	if code != "registry.invalid_argument" {
		t.Errorf("code = %q, want registry.invalid_argument", code)
	}
	if !strings.Contains(msg, "10.0.0.5") {
		t.Errorf("message %q does not name the disallowed host", msg)
	}

	// The rejected edit left the stored URL unchanged.
	rec, err := wstore.Get(context.Background(), "default", id)
	if err != nil {
		t.Fatalf("Get receiver: %v", err)
	}
	if rec.URL != "https://example.test/hook" {
		t.Errorf("stored URL = %q, want unchanged", rec.URL)
	}
}

// Spec: §7.3.2 — a receiver URL whose host is on the policy allowlist passes
// registration-time validation, so a deployment with a legitimately internal
// receiver registers it once the host is allowlisted.
func TestWebhookReceiverCreate_SSRFAllowlistedHostAccepted(t *testing.T) {
	t.Parallel()
	policy, err := webhook.NewURLPolicy([]string{"relay.internal"})
	if err != nil {
		t.Fatalf("NewURLPolicy: %v", err)
	}
	worker := &webhook.Worker{Store: webhook.NewMemoryStore(), URLPolicy: policy}
	_, ts := bootWebhookRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]any{"url": "https://relay.internal/hook"})
	resp, err := http.Post(ts.URL+"/v1/webhooks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d (allowlisted host should be accepted): %s", resp.StatusCode, buf)
	}
}

// Spec: §7.3.2 — a nil SSRF policy skips the registration-time check, so a
// deployment without a configured policy accepts a receiver URL the worker
// would re-check at delivery.
func TestWebhookReceiverCreate_NilPolicySkipsCheck(t *testing.T) {
	t.Parallel()
	worker := &webhook.Worker{Store: webhook.NewMemoryStore()} // URLPolicy nil
	_, ts := bootWebhookRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]any{"url": "https://127.0.0.1/hook"})
	resp, err := http.Post(ts.URL+"/v1/webhooks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d (nil policy should skip the check): %s", resp.StatusCode, buf)
	}
}
