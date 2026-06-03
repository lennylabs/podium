package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §8.3 (F-8.3.1) — when the registry sink is redirected to an external
// endpoint, the §8.5 erase endpoint still purges the user's layers and
// forwards the lifecycle events to the endpoint. The on-disk redaction pass
// is skipped because there is no local log to rewrite; the receiving
// aggregator owns redaction of the shipped stream.
func TestErase_EndpointRedirectPurgesAndForwards(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var mu sync.Mutex
	var received []string
	recorder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, string(b))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(recorder.Close)

	endpoint, err := audit.NewEndpointSink(recorder.URL + "/sink")
	if err != nil {
		t.Fatalf("NewEndpointSink: %v", err)
	}

	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	admin := layer.Identity{Sub: "carol@acme.com", IsAuthenticated: true}
	ep := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithAudit(endpoint).
		WithEraseSink(nil). // redirected to an endpoint: no local file to rewrite
		WithIdentityResolver(func(*http.Request) layer.Identity { return admin }).
		WithAdminAuth(func(*http.Request) error { return nil })
	mux := http.NewServeMux()
	mux.Handle("/v1/admin/erase", ep.EraseHandler())
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "alice-personal", SourceType: "local", LocalPath: "/tmp/x",
		UserDefined: true, Owner: "alice@acme.com", Users: []string{"alice@acme.com"},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}

	body, _ := json.Marshal(map[string]any{"user_id": "alice@acme.com", "salt": "tenant-salt"})
	resp, err := http.Post(ts.URL+"/v1/admin/erase", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST erase: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		LayersPurged        []string `json:"layers_purged"`
		AuditEventsRedacted int      `json:"audit_events_redacted"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode erase response: %v", err)
	}
	if len(out.LayersPurged) != 1 || out.LayersPurged[0] != "alice-personal" {
		t.Errorf("layers_purged = %v, want [alice-personal]", out.LayersPurged)
	}
	// No local file → no on-disk redaction performed.
	if out.AuditEventsRedacted != 0 {
		t.Errorf("audit_events_redacted = %d, want 0 (redirected, no local log)", out.AuditEventsRedacted)
	}
	// Layer soft-deleted (still recoverable within the §8.4 window).
	if _, err := st.GetLayerConfig(ctx, "t", "alice-personal"); err == nil {
		t.Errorf("layer still visible after erase")
	}

	// The lifecycle event was forwarded to the endpoint rather than written
	// to a file.
	deadline := time.Now().Add(3 * time.Second)
	found := false
	for time.Now().Before(deadline) && !found {
		mu.Lock()
		for _, b := range received {
			if strings.Contains(b, "layer.user_registered") && strings.Contains(b, "erase") {
				found = true
				break
			}
		}
		mu.Unlock()
		if !found {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if !found {
		mu.Lock()
		got := strings.Join(received, "\n")
		mu.Unlock()
		t.Errorf("endpoint did not receive a forwarded layer lifecycle event; got:\n%s", got)
	}
}
