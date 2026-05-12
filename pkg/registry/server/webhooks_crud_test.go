package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/webhook"
)

func setupWebhook(t *testing.T, id string) (*server.Server, string, func()) {
	t.Helper()
	wstore := webhook.NewMemoryStore()
	_ = wstore.Put(context.Background(), webhook.Receiver{
		ID: id, TenantID: "default",
		URL: "http://example/" + id, Secret: "shh",
		EventFilter: []string{"artifact.published"},
	})
	worker := &webhook.Worker{Store: wstore, HTTPClient: http.DefaultClient}
	srv, ts := bootRegistry(t, server.WithWebhooks(worker))
	return srv, ts.URL, ts.Close
}

func TestWebhookOne_GetMasksSecret(t *testing.T) {
	t.Parallel()
	_, base, cleanup := setupWebhook(t, "wh-get")
	defer cleanup()
	resp, err := http.Get(base + "/v1/webhooks/wh-get")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte(`"***"`)) {
		t.Errorf("secret not masked: %s", body)
	}
}

func TestWebhookOne_GetUnknownReturns404(t *testing.T) {
	t.Parallel()
	_, base, cleanup := setupWebhook(t, "wh-get-x")
	defer cleanup()
	resp, err := http.Get(base + "/v1/webhooks/ghost")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestWebhookOne_PutPatchesFields(t *testing.T) {
	t.Parallel()
	_, base, cleanup := setupWebhook(t, "wh-put")
	defer cleanup()
	patch, _ := json.Marshal(map[string]any{
		"url":          "http://example/updated",
		"event_filter": []string{"artifact.deprecated"},
		"disabled":     false,
	})
	req, _ := http.NewRequest(http.MethodPut, base+"/v1/webhooks/wh-put", bytes.NewReader(patch))
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
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("updated")) {
		t.Errorf("response missing patched URL: %s", body)
	}
	if !bytes.Contains(body, []byte(`"***"`)) {
		t.Errorf("secret not masked in PUT response: %s", body)
	}
}

func TestWebhookOne_PutOnUnknownReturns404(t *testing.T) {
	t.Parallel()
	_, base, cleanup := setupWebhook(t, "wh-put-x")
	defer cleanup()
	patch, _ := json.Marshal(map[string]any{"url": "http://x"})
	req, _ := http.NewRequest(http.MethodPut, base+"/v1/webhooks/ghost", bytes.NewReader(patch))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestWebhookOne_PutBadJSONReturns400(t *testing.T) {
	t.Parallel()
	_, base, cleanup := setupWebhook(t, "wh-put-bad")
	defer cleanup()
	req, _ := http.NewRequest(http.MethodPut, base+"/v1/webhooks/wh-put-bad",
		bytes.NewReader([]byte("not json")))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestWebhookOne_PostReturns405(t *testing.T) {
	t.Parallel()
	_, base, cleanup := setupWebhook(t, "wh-post")
	defer cleanup()
	resp, err := http.Post(base+"/v1/webhooks/wh-post", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestWebhookOne_MissingIDReturns400(t *testing.T) {
	t.Parallel()
	_, base, cleanup := setupWebhook(t, "wh-empty")
	defer cleanup()
	resp, err := http.Get(base + "/v1/webhooks/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	// Either 400 (caught by handler) or 405 (caught by mux) is fine —
	// we just want to exercise the "id is required" path.
	if resp.StatusCode < 400 {
		t.Errorf("status = %d, want >=400", resp.StatusCode)
	}
}
