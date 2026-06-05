package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

func newLayerHarness(t *testing.T) (string, store.Store, func()) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker())
	ts := httptest.NewServer(endpoint.Handler())
	return ts.URL, st, ts.Close
}

func mustPost(t *testing.T, base, path string, body any) (*http.Response, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(base+path, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

func mustDelete(t *testing.T, base, path string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, base+path, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

// Spec: §8.4 — unregistering a layer soft-deletes it (and the artifacts
// ingested from it) into a 30-day recovery window: the layer disappears
// from the normal list but appears under ?deleted=true, and
// /v1/layers/restore recovers it.
func TestLayerEndpoint_UnregisterSoftDeletesAndRestoreRecovers(t *testing.T) {
	t.Parallel()
	base, st, cleanup := newLayerHarness(t)
	defer cleanup()

	// Register a user-defined layer (no admin auth needed to manage it).
	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "alice-personal", "source_type": "local", "local_path": "/tmp/x",
		"user_defined": true, "owner": "alice",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status %d, body=%s", resp.StatusCode, body)
	}
	// Seed an artifact ingested from that layer.
	if err := st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "t", ArtifactID: "skill/a", Version: "1.0.0", ContentHash: "h",
		Type: "skill", Layer: "alice-personal",
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}

	// Unregister: soft-delete.
	resp, body = mustDelete(t, base, "/v1/layers?id=alice-personal")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unregister status %d, body=%s", resp.StatusCode, body)
	}
	if _, err := st.GetLayerConfig(context.Background(), "t", "alice-personal"); err == nil {
		t.Errorf("layer still visible after unregister")
	}
	if _, err := st.GetManifest(context.Background(), "t", "skill/a", "1.0.0"); err == nil {
		t.Errorf("artifact still visible after unregister")
	}

	// The normal list excludes it; the deleted list includes it.
	if active := mustGet(t, base, "/v1/layers"); strings.Contains(string(active), "alice-personal") {
		t.Errorf("active list should not contain soft-deleted layer: %s", active)
	}
	if del := mustGet(t, base, "/v1/layers?deleted=true"); !strings.Contains(string(del), "alice-personal") {
		t.Fatalf("deleted list missing layer: %s", del)
	}

	// Restore: recover layer and artifact.
	resp, body = mustPost(t, base, "/v1/layers/restore?id=alice-personal", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restore status %d, body=%s", resp.StatusCode, body)
	}
	if _, err := st.GetLayerConfig(context.Background(), "t", "alice-personal"); err != nil {
		t.Errorf("layer not recovered: %v", err)
	}
	if _, err := st.GetManifest(context.Background(), "t", "skill/a", "1.0.0"); err != nil {
		t.Errorf("artifact not recovered: %v", err)
	}

	// Restoring an unknown / non-deleted layer is 404.
	resp, _ = mustPost(t, base, "/v1/layers/restore?id=nope", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("restore of missing layer status = %d, want 404", resp.StatusCode)
	}
}

// Spec: §7.3.1 — POST /v1/layers registers a layer and returns the
// webhook URL + HMAC secret for git sources.
func TestLayerEndpoint_RegisterGitLayer(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()

	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id":           "team-finance",
		"source_type":  "git",
		"repo":         "git@github.com:acme/finance.git",
		"ref":          "main",
		"organization": true,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, body=%s", resp.StatusCode, body)
	}
	var got server.LayerRegisterResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Layer.ID != "team-finance" {
		t.Errorf("ID = %q", got.Layer.ID)
	}
	if got.WebhookSecret == "" {
		t.Errorf("WebhookSecret empty for git source")
	}
	if got.WebhookURL == "" {
		t.Errorf("WebhookURL empty for git source")
	}
}

// Spec: §14.10 — with a configured public base URL, register
// advertises an absolute webhook URL a developer can paste into a Git host's
// webhook configuration. A trailing slash on the base is collapsed.
func TestLayerEndpoint_RegisterGitLayer_AbsoluteWebhookURL(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithPublicBaseURL("https://podium.acme.com/")
	ts := httptest.NewServer(endpoint.Handler())
	defer ts.Close()

	_, body := mustPost(t, ts.URL, "/v1/layers", map[string]any{
		"id": "community-skills", "source_type": "git",
		"repo": "https://github.com/podium-community/skills.git", "ref": "main",
	})
	var got server.LayerRegisterResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, body)
	}
	if want := "https://podium.acme.com/v1/ingest/webhook/community-skills"; got.WebhookURL != want {
		t.Errorf("WebhookURL = %q, want %q", got.WebhookURL, want)
	}
}

// Spec: §14.10 — without a configured public base URL the webhook
// URL falls back to the relative path (e.g. an embedding harness that does not
// know its own external address).
func TestLayerEndpoint_RegisterGitLayer_RelativeWebhookURLWithoutBase(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t) // no WithPublicBaseURL
	defer cleanup()

	_, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "vendor", "source_type": "git",
		"repo": "git@github.com:acme/vendor.git", "ref": "main",
	})
	var got server.LayerRegisterResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, body)
	}
	if want := "/v1/ingest/webhook/vendor"; got.WebhookURL != want {
		t.Errorf("WebhookURL = %q, want relative %q", got.WebhookURL, want)
	}
}

// Spec: §7.3.1 — GET /v1/layers lists registered layers in Order.
func TestLayerEndpoint_ListReturnsRegisteredLayers(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()

	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "a", "source_type": "local", "local_path": "/tmp/a",
	})
	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "b", "source_type": "local", "local_path": "/tmp/b",
	})

	resp, err := http.Get(base + "/v1/layers")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var listResp struct {
		Layers []store.LayerConfig `json:"layers"`
	}
	if err := json.Unmarshal(body, &listResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(listResp.Layers) != 2 {
		t.Fatalf("got %d layers, want 2", len(listResp.Layers))
	}
}

// Spec: §7.3.1 — DELETE /v1/layers?id=X unregisters a user-defined layer.
func TestLayerEndpoint_Unregister(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()

	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "joan-personal", "source_type": "local",
		"local_path":   "/tmp/joan",
		"user_defined": true, "owner": "joan",
	})
	resp, _ := mustDelete(t, base, "/v1/layers?id=joan-personal")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status %d", resp.StatusCode)
	}
	resp2, _ := mustDelete(t, base, "/v1/layers?id=joan-personal")
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("second delete status = %d, want 404", resp2.StatusCode)
	}
}

// Spec: §7.3.1 — User-defined layers carry implicit users:[owner].
func TestLayerEndpoint_UserDefinedSetsImplicitUsers(t *testing.T) {
	t.Parallel()
	base, st, cleanup := newLayerHarness(t)
	defer cleanup()

	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "joan-personal", "source_type": "local",
		"local_path":   "/tmp/joan",
		"user_defined": true, "owner": "joan",
	})
	cfg, err := st.GetLayerConfig(context.Background(), "t", "joan-personal")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if len(cfg.Users) != 1 || cfg.Users[0] != "joan" {
		t.Errorf("Users = %v, want [joan]", cfg.Users)
	}
}

// Spec: §7.3.1 — POST /v1/layers/reorder re-sequences the list.
func TestLayerEndpoint_Reorder(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()

	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "a", "source_type": "local", "local_path": "/x",
	})
	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "b", "source_type": "local", "local_path": "/y",
	})

	resp, body := mustPost(t, base, "/v1/layers/reorder", map[string]any{
		"order": []string{"b", "a"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body=%s", resp.StatusCode, body)
	}
	var listResp struct {
		Layers []store.LayerConfig `json:"layers"`
	}
	if err := json.Unmarshal(body, &listResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(listResp.Layers) != 2 {
		t.Fatalf("got %d", len(listResp.Layers))
	}
	if listResp.Layers[0].ID != "b" {
		t.Errorf("first layer = %q, want b", listResp.Layers[0].ID)
	}
}

// Spec: §6.10 — admin-only ops without admin auth fail with auth.forbidden.
func TestLayerEndpoint_AdminAuthRequired(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithAdminAuth(func(*http.Request) error {
			return server.ErrAdminRequired
		})
	ts := httptest.NewServer(endpoint.Handler())
	defer ts.Close()

	resp, body := mustPost(t, ts.URL, "/v1/layers", map[string]any{
		"id": "admin-layer", "source_type": "local", "local_path": "/x",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	if !strings.Contains(string(body), "auth.forbidden") {
		t.Errorf("body missing auth.forbidden: %s", body)
	}
}
