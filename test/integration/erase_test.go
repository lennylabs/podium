package integration

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

// Spec: §8.5 — `podium admin erase <user_id>` unregisters and purges the
// user's owned layers and the artifacts ingested from them, redacts the user
// identity across the registry audit stream, and records a user.erased event
// naming the invoking admin. Drives the real LayerEndpoint over HTTP against a
// file-backed SQLite store and a file-backed audit sink.
func TestErase_SQLitePurgesLayersAndRedactsAudit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "registry.db")
	st, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	sinkPath := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(sinkPath)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}

	admin := layer.Identity{Sub: "carol@acme.com", IsAuthenticated: true}
	ep := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithAudit(sink).
		WithIdentityResolver(func(*http.Request) layer.Identity { return admin }).
		WithAdminAuth(func(*http.Request) error { return nil })
	mux := http.NewServeMux()
	mux.Handle("/v1/admin/erase", ep.EraseHandler())
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	// alice owns a user-defined layer with an artifact ingested from it.
	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "alice-personal", SourceType: "local", LocalPath: "/tmp/x",
		UserDefined: true, Owner: "alice@acme.com", Users: []string{"alice@acme.com"},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	if err := st.PutManifest(ctx, store.ManifestRecord{
		TenantID: "t", ArtifactID: "skill/a", Version: "1.0.0", ContentHash: "h",
		Type: "skill", Layer: "alice-personal",
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	_ = sink.Append(ctx, audit.Event{
		Type: audit.EventArtifactsSearched, Caller: "alice@acme.com", Timestamp: time.Now().UTC(),
	})

	body, _ := json.Marshal(map[string]any{"user_id": "alice@acme.com", "salt": "tenant-salt"})
	resp, err := http.Post(ts.URL+"/v1/admin/erase", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST erase: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Layer + artifact soft-deleted.
	if _, err := st.GetLayerConfig(ctx, "t", "alice-personal"); err == nil {
		t.Errorf("layer still visible after erase")
	}
	if _, err := st.GetManifest(ctx, "t", "skill/a", "1.0.0"); err == nil {
		t.Errorf("artifact still visible after erase")
	}
	// Layer recoverable within the §8.4 window (soft-delete, not hard-delete).
	if deleted, _ := st.ListDeletedLayerConfigs(ctx, "t"); len(deleted) != 1 {
		t.Errorf("ListDeletedLayerConfigs = %d, want 1", len(deleted))
	}

	// Registry audit stream redacted, chain intact.
	data, _ := os.ReadFile(sinkPath)
	if strings.Contains(string(data), "alice@acme.com") {
		t.Errorf("erased identity still present in audit stream")
	}
	if !strings.Contains(string(data), "carol@acme.com") {
		t.Errorf("invoking admin not recorded")
	}
	verify, _ := audit.NewFileSink(sinkPath)
	if err := verify.Verify(ctx); err != nil {
		t.Errorf("chain broken after erase: %v", err)
	}
}
