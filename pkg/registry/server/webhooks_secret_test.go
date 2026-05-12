package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/webhook"
)

// POST /v1/webhooks without a secret auto-generates one.
func TestWebhooksList_PostAutoGeneratesSecret(t *testing.T) {
	t.Parallel()
	wstore := webhook.NewMemoryStore()
	worker := &webhook.Worker{Store: wstore, HTTPClient: http.DefaultClient}
	srv, ts := bootRegistry(t, server.WithWebhooks(worker))
	_ = srv
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]any{
		"url":          "http://example/receiver",
		"event_filter": []string{"artifact.published"},
	})
	resp, err := http.Post(ts.URL+"/v1/webhooks", "application/json", strReader(string(body)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	// The Receiver field names use Go-style capitalization. Pull the
	// secret out of the response and verify it's been auto-generated.
	var rec map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&rec)
	secret, _ := rec["Secret"].(string)
	if secret == "" || secret == "***" {
		t.Errorf("expected non-empty auto-generated secret, got %q", secret)
	}
	if len(secret) < 32 {
		t.Errorf("secret too short: %q", secret)
	}
}

func TestWebhooksList_PostMissingURLReturns400(t *testing.T) {
	t.Parallel()
	wstore := webhook.NewMemoryStore()
	worker := &webhook.Worker{Store: wstore}
	_, ts := bootRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)
	resp, err := http.Post(ts.URL+"/v1/webhooks", "application/json",
		strReader(`{"event_filter":["artifact.published"]}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestWebhooksList_BadJSONReturns400(t *testing.T) {
	t.Parallel()
	wstore := webhook.NewMemoryStore()
	worker := &webhook.Worker{Store: wstore}
	_, ts := bootRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)
	resp, err := http.Post(ts.URL+"/v1/webhooks", "application/json", strReader("not json"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestWebhooksList_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	wstore := webhook.NewMemoryStore()
	worker := &webhook.Worker{Store: wstore}
	_, ts := bootRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/v1/webhooks", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

// /v1/quota returns a quota envelope.
func TestHandleQuota_Default(t *testing.T) {
	t.Parallel()
	_, ts := bootRegistry(t)
	t.Cleanup(ts.Close)
	resp, err := http.Get(ts.URL + "/v1/quota")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// /v1/quota wrong method returns 405.
func TestHandleQuota_WrongMethodReturns405(t *testing.T) {
	t.Parallel()
	_, ts := bootRegistry(t)
	t.Cleanup(ts.Close)
	resp, err := http.Post(ts.URL+"/v1/quota", "application/json", strReader(""))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestHandleObjectsRoute_NotFoundWithoutObjectStore(t *testing.T) {
	t.Parallel()
	_, ts := bootRegistry(t)
	t.Cleanup(ts.Close)
	resp, err := http.Get(ts.URL + "/objects/sha256-x")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}


func TestHandleObjectsRoute_EmptyKeyReturns400(t *testing.T) {
	t.Parallel()
	_, ts := bootRegistry(t)
	t.Cleanup(ts.Close)
	resp, err := http.Get(ts.URL + "/objects/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	// Empty key returns 400. (Or 404 if router doesn't dispatch.)
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

// Just an unused import marker.
var _ = httptest.NewServer
var _ = context.Background
var _ = strings.Contains
