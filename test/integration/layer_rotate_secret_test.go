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

// Spec: §12 — "Per-layer HMAC secret rotated via `podium layer update`."
// This drives the real LayerEndpoint over HTTP against a persistent SQLite
// backend so the rotation is read back from the SQL store, confirming the
// new secret is durable and differs from the prior value.
func TestLayerRotateWebhookSecret_SQLitePersists(t *testing.T) {
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
	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "vendor", SourceType: "git",
		Repo: "git@example/vendor.git", Ref: "main",
		WebhookSecret: "old-secret", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker())
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]any{"rotate_webhook_secret": true})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/layers/update?id=vendor", bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", resp.StatusCode, out)
	}
	var got struct {
		WebhookSecret string `json:"webhook_secret"`
	}
	_ = json.Unmarshal(out, &got)
	if got.WebhookSecret == "" || got.WebhookSecret == "old-secret" {
		t.Fatalf("returned secret = %q, want a fresh value", got.WebhookSecret)
	}
	// Read back from the SQL store: the rotation is durable.
	stored, err := st.GetLayerConfig(ctx, "t", "vendor")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if stored.WebhookSecret != got.WebhookSecret {
		t.Errorf("persisted secret %q != returned %q", stored.WebhookSecret, got.WebhookSecret)
	}
}
