package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §8.3, §8.5 — when the registry stream has only an HTTP destination,
// the §8.5 erase endpoint still purges the user's layers and forwards the
// lifecycle events to the receiver as NDJSON batches. The on-disk redaction
// pass is skipped because there is no local file to rewrite; the receiving
// system owns redaction of the records it holds. The receiver also gets
// user.erased labeled with the tenant, naming the admin as caller, with a
// transformed count of 0.
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

	stream, err := audit.Open(ctx, recorder.URL+"/sink", audit.OpenOptions{})
	if err != nil {
		t.Fatalf("audit.Open: %v", err)
	}
	t.Cleanup(stream.Shutdown)

	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	admin := layer.Identity{Sub: "carol@acme.com", IsAuthenticated: true}
	ep := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithAudit(stream).
		WithEraseSink(nil). // no file destination: no local file to rewrite
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) { return admin, nil }).
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
	defer func() { _ = resp.Body.Close() }()
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

	// Close flushes every queued batch, so the receiver holds every record
	// once it returns.
	if err := stream.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	mu.Lock()
	forwarded := ndjsonLines(slices.Clone(received))
	mu.Unlock()
	// The lifecycle event was forwarded to the receiver rather than written
	// to a file.
	found := slices.ContainsFunc(forwarded, func(line string) bool {
		return strings.Contains(line, "layer.user_registered") && strings.Contains(line, "erase")
	})
	if !found {
		t.Errorf("receiver did not get a forwarded layer lifecycle event; got:\n%s", strings.Join(forwarded, "\n"))
	}
	assertForwardedUserErased(t, forwarded, "t", "carol@acme.com")
}

// ndjsonLines splits each NDJSON request body into its non-empty lines, one
// record per line.
func ndjsonLines(bodies []string) []string {
	var lines []string
	for _, b := range bodies {
		for _, line := range strings.Split(b, "\n") {
			if line != "" {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

// assertForwardedUserErased finds the user.erased record among the records
// the receiver got and checks its tenant, its caller, and its transformed
// count of 0, since a registry with no file destination rewrites no local
// record.
//
// Spec: §8.5
func assertForwardedUserErased(t *testing.T, records []string, tenant, admin string) {
	t.Helper()
	for _, b := range records {
		var ev struct {
			Type   string `json:"type"`
			Tenant string `json:"tenant"`
			Caller struct {
				Identity string `json:"identity"`
			} `json:"caller"`
			Context map[string]string `json:"context"`
		}
		if err := json.Unmarshal([]byte(b), &ev); err != nil || ev.Type != string(audit.EventUserErased) {
			continue
		}
		if ev.Tenant != tenant || ev.Caller.Identity != admin || ev.Context["transformed"] != "0" {
			t.Errorf("user.erased = %s, want tenant %q, caller %q, transformed 0", b, tenant, admin)
		}
		return
	}
	t.Errorf("receiver did not get user.erased; got:\n%s", strings.Join(records, "\n"))
}
