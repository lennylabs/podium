package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §8.4 — "Layers unregistered by their owners: 30 days (artifacts
// soft-deleted, recoverable via admin)". Drives the real LayerEndpoint
// over HTTP against a file-backed SQLite store: unregister soft-deletes
// the layer and its artifacts, /v1/layers/restore recovers them, and
// PurgeExpiredLayerDeletions hard-deletes them once the window passes.
func TestLayerRecovery_SQLiteSoftDeleteRestoreAndPurge(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "registry.db")
	st, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker())
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	// Register a user-defined layer and seed an artifact ingested from it.
	body, _ := json.Marshal(map[string]any{
		"id": "alice-personal", "source_type": "local", "local_path": "/tmp/x",
		"user_defined": true, "owner": "alice",
	})
	resp, err := http.Post(ts.URL+"/v1/layers", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status %d", resp.StatusCode)
	}
	if err := st.PutManifest(ctx, store.ManifestRecord{
		TenantID: "t", ArtifactID: "skill/a", Version: "1.0.0", ContentHash: "h",
		Type: "skill", Layer: "alice-personal",
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}

	// Unregister -> soft-delete (still recoverable, not gone).
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/v1/layers?id=alice-personal", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unregister: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unregister status %d", resp.StatusCode)
	}
	if _, err := st.GetManifest(ctx, "t", "skill/a", "1.0.0"); err == nil {
		t.Errorf("artifact still visible after unregister (no soft-delete)")
	}
	deleted, _ := st.ListDeletedLayerConfigs(ctx, "t")
	if len(deleted) != 1 {
		t.Fatalf("ListDeletedLayerConfigs = %+v, want 1", deleted)
	}

	// Restore -> recover layer + artifact.
	resp, err = http.Post(ts.URL+"/v1/layers/restore?id=alice-personal", "application/json", nil)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restore status %d, body=%s", resp.StatusCode, out)
	}
	if _, err := st.GetManifest(ctx, "t", "skill/a", "1.0.0"); err != nil {
		t.Errorf("artifact not recovered: %v", err)
	}

	// Unregister again, then simulate the 30-day window passing and purge.
	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/v1/layers?id=alice-personal", nil)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	// Purge with a cutoff in the future so the just-deleted layer is past
	// the window; the artifact and layer are hard-deleted.
	n, err := st.PurgeExpiredLayerDeletions(ctx, time.Now().UTC().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("PurgeExpiredLayerDeletions: %v", err)
	}
	if n != 1 {
		t.Errorf("purged = %d, want 1", n)
	}
	if rest, _ := st.ListDeletedLayerConfigs(ctx, "t"); len(rest) != 0 {
		t.Errorf("layer survived purge: %+v", rest)
	}
}
