package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/webhook"
)

// Spec: §7.3.2 — receivers configured via POST /v1/webhooks receive
// outbound deliveries when the server publishes an event. The body
// is signed with X-Podium-Signature against the receiver's secret.
func TestWebhooks_EndToEndDelivery(t *testing.T) {
	t.Parallel()
	delivered := atomic.Int64{}
	bodyChan := make(chan []byte, 4)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sig := r.Header.Get("X-Podium-Signature")
		if err := webhook.VerifyBody(body, sig, "test-secret"); err != nil {
			http.Error(w, "bad signature", http.StatusBadRequest)
			return
		}
		delivered.Add(1)
		bodyChan <- body
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(receiver.Close)

	store := webhook.NewMemoryStore()
	worker := &webhook.Worker{
		Store:      store,
		HTTPClient: receiver.Client(),
		Backoff:    []time.Duration{},
	}

	srv, ts := bootRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)

	// Register a receiver via the HTTP API.
	body, _ := json.Marshal(map[string]any{
		"url":          receiver.URL,
		"secret":       "test-secret",
		"event_filter": []string{"artifact.published"},
	})
	resp, err := http.Post(ts.URL+"/v1/webhooks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/webhooks: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d: %s", resp.StatusCode, buf)
	}
	resp.Body.Close()

	// Publish an event and wait for delivery.
	srv.PublishEvent(context.Background(), "artifact.published", map[string]any{
		"id": "x", "version": "1.0.0",
	})
	select {
	case got := <-bodyChan:
		var parsed map[string]any
		_ = json.Unmarshal(got, &parsed)
		if parsed["event"] != "artifact.published" {
			t.Errorf("event = %v, want artifact.published", parsed["event"])
		}
		data, _ := parsed["data"].(map[string]any)
		if data["id"] != "x" {
			t.Errorf("data.id = %v, want x", data["id"])
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("delivery did not fire (delivered = %d)", delivered.Load())
	}
}

// Spec: §7.3.2 — GET /v1/webhooks lists every receiver but masks
// the secret so a read does not leak the HMAC key.
func TestWebhooks_ListMasksSecret(t *testing.T) {
	t.Parallel()
	wstore := webhook.NewMemoryStore()
	_ = wstore.Put(context.Background(), webhook.Receiver{
		ID: "wh-1", TenantID: "default", URL: "http://x", Secret: "real-secret",
	})
	worker := &webhook.Worker{Store: wstore}
	_, ts := bootRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/v1/webhooks")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if bytes.Contains(body, []byte("real-secret")) {
		t.Errorf("list leaked secret: %s", body)
	}
	if !bytes.Contains(body, []byte(`"***"`)) {
		t.Errorf("list did not mask secret: %s", body)
	}
}

// Spec: §7.3.2 — DELETE /v1/webhooks/{id} removes the receiver so
// no further events are delivered to it.
func TestWebhooks_DeleteStopsDeliveries(t *testing.T) {
	t.Parallel()
	deliveries := atomic.Int64{}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		deliveries.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(receiver.Close)
	wstore := webhook.NewMemoryStore()
	_ = wstore.Put(context.Background(), webhook.Receiver{
		ID: "wh-1", TenantID: "default", URL: receiver.URL, Secret: "k",
	})
	worker := &webhook.Worker{Store: wstore, HTTPClient: receiver.Client()}
	srv, ts := bootRegistry(t, server.WithWebhooks(worker))
	t.Cleanup(ts.Close)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/v1/webhooks/wh-1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	srv.PublishEvent(context.Background(), "artifact.published", nil)
	time.Sleep(100 * time.Millisecond)
	if deliveries.Load() != 0 {
		t.Errorf("deliveries = %d after delete", deliveries.Load())
	}
}

func bootRegistry(t *testing.T, opts ...server.Option) (*server.Server, *httptest.Server) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	srv := server.New(core.New(st, "default", nil), opts...)
	ts := httptest.NewServer(srv.Handler())
	return srv, ts
}
