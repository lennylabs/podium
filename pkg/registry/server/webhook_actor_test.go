package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/webhook"
)

// newServerWithReceiver wires a server with an outbound webhook worker
// delivering to an httptest receiver that publishes each body on bodies.
func newServerWithReceiver(t *testing.T) (*Server, <-chan []byte) {
	t.Helper()
	bodies := make(chan []byte, 4)
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		select {
		case bodies <- b:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(recv.Close)

	wstore := webhook.NewMemoryStore()
	if err := wstore.Put(context.Background(), webhook.Receiver{
		ID: "r1", TenantID: "default", URL: recv.URL, Secret: "s",
	}); err != nil {
		t.Fatalf("seed receiver: %v", err)
	}
	worker := &webhook.Worker{Store: wstore, HTTPClient: recv.Client()}

	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	srv := New(core.New(st, "default", nil), WithWebhooks(worker), WithTenant("default"))
	return srv, bodies
}

// spec: §7.3.2 — PublishEvent threads the per-request trace id and an
// authenticated caller's identity (the actor) from the request audit
// metadata into the outbound webhook body so receivers can correlate
// and attribute the event (F-7.3.1).
func TestPublishEvent_WebhookCarriesTraceAndAuthenticatedActor(t *testing.T) {
	srv, bodies := newServerWithReceiver(t)
	ctx := withAuditMeta(context.Background(), AuditMeta{
		TraceID: "trace-xyz",
		Email:   "alice@acme.com",
		Groups:  []string{"eng"},
	})
	srv.PublishEvent(ctx, "artifact.published", map[string]any{"id": "finance/run"})

	select {
	case raw := <-bodies:
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("body parse: %v", err)
		}
		for _, k := range []string{"event", "trace_id", "timestamp", "actor", "data"} {
			if _, ok := m[k]; !ok {
				t.Errorf("body missing %q: %v", k, m)
			}
		}
		if m["trace_id"] != "trace-xyz" {
			t.Errorf("trace_id = %v, want trace-xyz", m["trace_id"])
		}
		actor, _ := m["actor"].(map[string]any)
		if actor["email"] != "alice@acme.com" {
			t.Errorf("actor.email = %v, want alice@acme.com", actor["email"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no outbound webhook delivery within deadline")
	}
}

// spec: §7.3.2 — a public-mode caller contributes a public actor with the
// source network attributes rather than an email/groups identity (F-7.3.1).
func TestPublishEvent_WebhookCarriesPublicActor(t *testing.T) {
	srv, bodies := newServerWithReceiver(t)
	ctx := withAuditMeta(context.Background(), AuditMeta{
		TraceID:       "trace-pub",
		PublicMode:    true,
		SourceIP:      "203.0.113.7",
		ForwardedUser: "upstream-bob",
	})
	srv.PublishEvent(ctx, "artifact.published", map[string]any{"id": "x"})

	select {
	case raw := <-bodies:
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("body parse: %v", err)
		}
		actor, _ := m["actor"].(map[string]any)
		if actor["type"] != "public" {
			t.Errorf("actor.type = %v, want public", actor["type"])
		}
		if actor["source_ip"] != "203.0.113.7" {
			t.Errorf("actor.source_ip = %v, want 203.0.113.7", actor["source_ip"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no outbound webhook delivery within deadline")
	}
}

// spec: §7.3.2 — an event published with no request audit metadata still
// carries a stable schema: an empty trace_id and an empty actor object,
// never a missing key (F-7.3.1).
func TestPublishEvent_WebhookStableSchemaWithoutMeta(t *testing.T) {
	srv, bodies := newServerWithReceiver(t)
	srv.PublishEvent(context.Background(), "layer.ingested", map[string]any{"layer": "L"})

	select {
	case raw := <-bodies:
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("body parse: %v", err)
		}
		if _, ok := m["trace_id"]; !ok {
			t.Errorf("trace_id key absent: %v", m)
		}
		actor, ok := m["actor"].(map[string]any)
		if !ok {
			t.Fatalf("actor absent or not an object: %v", m["actor"])
		}
		if len(actor) != 0 {
			t.Errorf("actor = %v, want empty object", actor)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no outbound webhook delivery within deadline")
	}
}
