package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// eraseTestEndpoint builds a LayerEndpoint wired with a file-backed audit
// sink (so the §8.5 redaction has a registry stream to rewrite), an admin
// identity, and an admin-auth gate. It returns the endpoint, the running
// test server, the sink path, and the store.
func eraseTestEndpoint(t *testing.T, admin layer.Identity, authErr error) (*httptest.Server, string, store.Store) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	sinkPath := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(sinkPath)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	ep := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithAudit(sink).
		WithEraseSink(sink).
		WithIdentityResolver(func(*http.Request) layer.Identity { return admin }).
		WithAdminAuth(func(*http.Request) error { return authErr })
	mux := http.NewServeMux()
	mux.Handle("/v1/layers", ep.Handler())
	mux.Handle("/v1/layers/", ep.Handler())
	mux.Handle("/v1/admin/erase", ep.EraseHandler())
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, sinkPath, st
}

func postErase(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url+"/v1/admin/erase", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST erase: %v", err)
	}
	return resp
}

// Spec: §8.5 — erase unregisters and soft-deletes
// the user's owned layers and the artifacts ingested from them, redacts the
// user identity across the registry audit stream, and appends a user.erased
// event naming the invoking admin.
func TestErase_PurgesLayersAndRedactsRegistryStream(t *testing.T) {
	t.Parallel()
	admin := layer.Identity{Sub: "carol@acme.com", IsAuthenticated: true}
	ts, sinkPath, st := eraseTestEndpoint(t, admin, nil)
	ctx := context.Background()

	// alice owns a user-defined layer with an artifact ingested from it.
	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "alice-personal", SourceType: "local", LocalPath: "/tmp/x",
		UserDefined: true, Owner: "alice@acme.com", Users: []string{"alice@acme.com"},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	// A separate layer owned by bob must survive.
	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "bob-personal", SourceType: "local", LocalPath: "/tmp/y",
		UserDefined: true, Owner: "bob@acme.com", Users: []string{"bob@acme.com"},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PutLayerConfig bob: %v", err)
	}
	if err := st.PutManifest(ctx, store.ManifestRecord{
		TenantID: "t", ArtifactID: "skill/a", Version: "1.0.0", ContentHash: "h",
		Type: "skill", Layer: "alice-personal",
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	// Seed an audit event whose caller is the erased identity, carrying the
	// §8.1 attached email and group membership so the redaction pass has PII to
	// remove.
	sink, _ := audit.NewFileSink(sinkPath)
	_ = sink.Append(ctx, audit.Event{
		Type: audit.EventArtifactsSearched, Caller: "alice@acme.com",
		CallerEmail: "alice@acme.com", CallerGroups: []string{"acme-engineering"},
		Timestamp: time.Now().UTC(),
	})

	resp := postErase(t, ts.URL, map[string]any{"user_id": "alice@acme.com", "salt": "tenant-salt"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Erased       string   `json:"erased"`
		LayersPurged []string `json:"layers_purged"`
		Redacted     int      `json:"audit_events_redacted"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.LayersPurged) != 1 || out.LayersPurged[0] != "alice-personal" {
		t.Errorf("layers_purged = %v, want [alice-personal]", out.LayersPurged)
	}

	// Layer + artifact soft-deleted.
	if _, err := st.GetLayerConfig(ctx, "t", "alice-personal"); err == nil {
		t.Errorf("alice's layer still visible after erase")
	}
	if _, err := st.GetManifest(ctx, "t", "skill/a", "1.0.0"); err == nil {
		t.Errorf("artifact still visible after erase (no soft-delete)")
	}
	// bob's layer survives.
	if _, err := st.GetLayerConfig(ctx, "t", "bob-personal"); err != nil {
		t.Errorf("bob's layer wrongly purged: %v", err)
	}

	// Registry audit stream redacted: original identity gone, tombstone and
	// admin-named user.erased present, chain still valid.
	data, _ := os.ReadFile(sinkPath)
	if strings.Contains(string(data), "alice@acme.com") {
		t.Errorf("erased identity still present in registry audit stream")
	}
	// the attached email and group membership are gone too.
	if strings.Contains(string(data), "acme-engineering") {
		t.Errorf("erased user's group membership still present in registry audit stream")
	}
	if !strings.Contains(string(data), "user.erased") {
		t.Errorf("user.erased event not appended")
	}
	if !strings.Contains(string(data), "carol@acme.com") {
		t.Errorf("invoking admin carol@acme.com not recorded on user.erased")
	}
	verifySink, _ := audit.NewFileSink(sinkPath)
	if err := verifySink.Verify(ctx); err != nil {
		t.Errorf("hash chain broken after erase: %v", err)
	}
}

// Spec: §8.5 — an empty salt is rejected with a 400 before any
// state mutates.
func TestErase_EmptySaltRejected(t *testing.T) {
	t.Parallel()
	ts, sinkPath, st := eraseTestEndpoint(t, layer.Identity{Sub: "carol@acme.com", IsAuthenticated: true}, nil)
	if err := st.PutLayerConfig(context.Background(), store.LayerConfig{
		TenantID: "t", ID: "alice-personal", SourceType: "local", UserDefined: true,
		Owner: "alice@acme.com", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	resp := postErase(t, ts.URL, map[string]any{"user_id": "alice@acme.com", "salt": ""})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	// No layer purged, no audit rewrite.
	if _, err := st.GetLayerConfig(context.Background(), "t", "alice-personal"); err != nil {
		t.Errorf("layer purged despite rejected erase: %v", err)
	}
	if data, _ := os.ReadFile(sinkPath); strings.Contains(string(data), "user.erased") {
		t.Errorf("user.erased appended despite rejected erase")
	}
}

// Spec: §8.5 — erase is admin-only; a non-admin caller is forbidden.
func TestErase_RequiresAdmin(t *testing.T) {
	t.Parallel()
	ts, _, _ := eraseTestEndpoint(t, layer.Identity{IsPublic: true}, server.ErrAdminRequired)
	resp := postErase(t, ts.URL, map[string]any{"user_id": "alice@acme.com", "salt": "s"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

// Spec: §8.5 — erase is POST-only.
func TestErase_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	ts, _, _ := eraseTestEndpoint(t, layer.Identity{Sub: "carol@acme.com", IsAuthenticated: true}, nil)
	resp, err := http.Get(ts.URL + "/v1/admin/erase")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

// Spec: §8.5 — missing user_id is a 400.
func TestErase_MissingUserID(t *testing.T) {
	t.Parallel()
	ts, _, _ := eraseTestEndpoint(t, layer.Identity{Sub: "carol@acme.com", IsAuthenticated: true}, nil)
	resp := postErase(t, ts.URL, map[string]any{"salt": "s"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
