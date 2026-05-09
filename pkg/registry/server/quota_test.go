package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7.8 — GET /v1/quota returns the tenant's configured
// limits and the current measured usage. v1 derives storage_bytes
// from the manifest list.
// Phase: 14
func TestQuota_ReturnsLimitsAndUsage(t *testing.T) {
	testharness.RequirePhase(t, 14)
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{
		ID: "default", Name: "default",
		Quota: store.Quota{StorageBytes: 1 << 20, SearchQPS: 10},
	}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	body := []byte("frontmatter+body bytes")
	if err := st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "default", ArtifactID: "x", Version: "1.0.0",
		ContentHash: "sha256:x", Type: "context", Layer: "L",
		Frontmatter: body, Body: body,
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	srv := server.New(core.New(st, "default", nil))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/v1/quota")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d: %s", resp.StatusCode, buf)
	}
	var parsed struct {
		TenantID string         `json:"tenant_id"`
		Limits   map[string]int `json:"limits"`
		Usage    map[string]int `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if parsed.TenantID != "default" {
		t.Errorf("tenant_id = %q, want default", parsed.TenantID)
	}
	if parsed.Limits["StorageBytes"] != 1<<20 {
		t.Errorf("limits.StorageBytes = %d, want %d", parsed.Limits["StorageBytes"], 1<<20)
	}
	want := 2 * len(body)
	if parsed.Usage["storage_bytes"] != want {
		t.Errorf("usage.storage_bytes = %d, want %d", parsed.Usage["storage_bytes"], want)
	}
}
