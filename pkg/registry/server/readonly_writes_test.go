package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/webhook"
)

// readOnlyErrorCode decodes a §6.10 error envelope and returns its code.
func readOnlyErrorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	var env struct {
		Code string `json:"code"`
	}
	buf, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(buf, &env); err != nil {
		t.Fatalf("decode envelope %q: %v", buf, err)
	}
	return env.Code
}

// Spec: §13.2.1 — admin grants are a configuration change, so POST and
// DELETE /v1/admin/grants are rejected in read-only mode with the §6.10
// config.read_only envelope (503), consistent with the layer handlers.
func TestAdminGrants_RejectedInReadOnly(t *testing.T) {
	t.Parallel()
	mode := server.NewModeTracker()
	mode.Set(server.ModeReadOnly)
	ts := bootRegistryWithAdmin(t, "alice", []layer.Layer{
		{ID: "team", Visibility: layer.Visibility{Public: true}},
	}, server.WithMode(mode))

	body, _ := json.Marshal(map[string]string{"user_id": "bob"})
	resp, err := http.Post(ts.URL+"/v1/admin/grants", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("POST status = %d, want 503", resp.StatusCode)
	}
	if code := readOnlyErrorCode(t, resp); code != "config.read_only" {
		t.Errorf("POST code = %q, want config.read_only", code)
	}
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/v1/admin/grants?user_id=bob", nil)
	del, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if del.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("DELETE status = %d, want 503", del.StatusCode)
	}
	if code := readOnlyErrorCode(t, del); code != "config.read_only" {
		t.Errorf("DELETE code = %q, want config.read_only", code)
	}
	del.Body.Close()
}

// newReadOnlyWebhookServer builds a webhook-CRUD server pre-flipped into
// read-only mode and seeds one receiver so the read paths have something
// to return.
func newReadOnlyWebhookServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	wstore := webhook.NewMemoryStore()
	const id = "wh-seed"
	if err := wstore.Put(context.Background(), webhook.Receiver{
		ID: id, TenantID: "default",
		URL: "https://example.test/hook", Secret: "shh",
		EventFilter: []string{"artifact.published"},
	}); err != nil {
		t.Fatalf("seed receiver: %v", err)
	}
	worker := &webhook.Worker{Store: wstore, HTTPClient: http.DefaultClient}
	mode := server.NewModeTracker()
	mode.Set(server.ModeReadOnly)
	_, ts := bootRegistry(t, server.WithWebhooks(worker), server.WithMode(mode))
	return ts, id
}

// Spec: §13.2.1 — webhook-receiver writes (POST create, PUT edit,
// DELETE) are configuration changes and are rejected in read-only mode
// with config.read_only (503).
func TestWebhookReceiverWrites_RejectedInReadOnly(t *testing.T) {
	t.Parallel()
	ts, id := newReadOnlyWebhookServer(t)

	// POST create.
	body, _ := json.Marshal(map[string]string{"url": "https://example.test/new"})
	post, err := http.Post(ts.URL+"/v1/webhooks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if post.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("POST status = %d, want 503", post.StatusCode)
	}
	if code := readOnlyErrorCode(t, post); code != "config.read_only" {
		t.Errorf("POST code = %q, want config.read_only", code)
	}
	post.Body.Close()

	// PUT edit.
	putBody, _ := json.Marshal(map[string]any{"disabled": true})
	putReq, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/webhooks/"+id, bytes.NewReader(putBody))
	putReq.Header.Set("Content-Type", "application/json")
	put, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	if put.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("PUT status = %d, want 503", put.StatusCode)
	}
	if code := readOnlyErrorCode(t, put); code != "config.read_only" {
		t.Errorf("PUT code = %q, want config.read_only", code)
	}
	put.Body.Close()

	// DELETE.
	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/v1/webhooks/"+id, nil)
	del, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if del.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("DELETE status = %d, want 503", del.StatusCode)
	}
	if code := readOnlyErrorCode(t, del); code != "config.read_only" {
		t.Errorf("DELETE code = %q, want config.read_only", code)
	}
	del.Body.Close()
}

// Spec: §13.2.1 — read access to webhook receivers stays available in
// read-only mode; only the mutating verbs are rejected.
func TestWebhookReceiverReads_AllowedInReadOnly(t *testing.T) {
	t.Parallel()
	ts, id := newReadOnlyWebhookServer(t)

	list, err := http.Get(ts.URL + "/v1/webhooks")
	if err != nil {
		t.Fatalf("GET list: %v", err)
	}
	if list.StatusCode != http.StatusOK {
		t.Errorf("GET list status = %d, want 200", list.StatusCode)
	}
	list.Body.Close()

	one, err := http.Get(ts.URL + "/v1/webhooks/" + id)
	if err != nil {
		t.Fatalf("GET one: %v", err)
	}
	if one.StatusCode != http.StatusOK {
		t.Errorf("GET one status = %d, want 200", one.StatusCode)
	}
	one.Body.Close()
}
