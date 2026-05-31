package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer/webhook"
	"github.com/lennylabs/podium/pkg/store"
)

// newWebhookEndpoint seeds a git layer with a known webhook secret so the
// §7.3.1 inbound-webhook handler has a verifiable target.
func newWebhookEndpoint(t *testing.T, lc store.LayerConfig) (*LayerEndpoint, string) {
	t.Helper()
	st := store.NewMemory()
	const tenantID = "default"
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenantID, Name: tenantID}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	lc.TenantID = tenantID
	if err := st.PutLayerConfig(context.Background(), lc); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	return NewLayerEndpoint(st, tenantID, NewModeTracker()), tenantID
}

// spec: §7.3.1 / §9.1 GitProvider — a delivery with a valid GitHub
// signature verifies through webhook.Default and queues the reingest.
func TestWebhook_ValidGitHubSignature(t *testing.T) {
	secret := "hook-secret"
	e, _ := newWebhookEndpoint(t, store.LayerConfig{
		ID: "vendor", SourceType: "git", Repo: "git@github.com:acme/vendor.git",
		GitProvider: "github", WebhookSecret: secret,
	})
	body := `{"ref":"refs/heads/main"}`
	sig, err := webhook.Sign("github", []byte(body), secret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/ingest/webhook/vendor", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	rec := httptest.NewRecorder()
	e.WebhookHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "vendor") {
		t.Errorf("body missing queued layer: %s", rec.Body.String())
	}
}

// spec: §6.10 ingest.webhook_invalid — a bad signature is rejected with
// 401 and never queues a reingest.
func TestWebhook_InvalidSignature(t *testing.T) {
	e, _ := newWebhookEndpoint(t, store.LayerConfig{
		ID: "vendor", SourceType: "git", GitProvider: "github", WebhookSecret: "right",
	})
	body := `{"ref":"refs/heads/main"}`
	sig, _ := webhook.Sign("github", []byte(body), "wrong")
	req := httptest.NewRequest(http.MethodPost, "/v1/ingest/webhook/vendor", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	rec := httptest.NewRecorder()
	e.WebhookHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ingest.webhook_invalid") {
		t.Errorf("body missing ingest.webhook_invalid: %s", rec.Body.String())
	}
}

// spec: §9.1 GitProvider default — an empty provider id defaults to github.
func TestWebhook_DefaultsToGitHub(t *testing.T) {
	secret := "s"
	e, _ := newWebhookEndpoint(t, store.LayerConfig{
		ID: "vendor", SourceType: "git", WebhookSecret: secret,
	})
	body := `{}`
	sig, _ := webhook.Sign("github", []byte(body), secret)
	req := httptest.NewRequest(http.MethodPost, "/v1/ingest/webhook/vendor", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	rec := httptest.NewRecorder()
	e.WebhookHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// spec: §9.2 — a custom GitProvider registered via webhook.Default.Register
// is selected by the layer's configured id, the build-path consumer that
// makes importing a custom provider change behavior.
func TestWebhook_CustomRegisteredProvider(t *testing.T) {
	const id = "acme-forge"
	if _, ok := webhook.Default.Get(id); !ok {
		if err := webhook.Default.Register(acmeForge{}); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	e, _ := newWebhookEndpoint(t, store.LayerConfig{
		ID: "vendor", SourceType: "git", GitProvider: id, WebhookSecret: "tok",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/ingest/webhook/vendor", strings.NewReader("body"))
	req.Header.Set("X-Hub-Signature-256", "tok")
	rec := httptest.NewRecorder()
	e.WebhookHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("custom provider status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// spec: §4.6 — a non-git layer cannot receive a git webhook.
func TestWebhook_NonGitLayer(t *testing.T) {
	e, _ := newWebhookEndpoint(t, store.LayerConfig{ID: "local-layer", SourceType: "local"})
	req := httptest.NewRequest(http.MethodPost, "/v1/ingest/webhook/local-layer", strings.NewReader("x"))
	rec := httptest.NewRecorder()
	e.WebhookHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// acmeForge is a stub custom GitProvider: a shared-token scheme like
// GitLab, used to prove the registry seam selects an imported provider.
type acmeForge struct{}

func (acmeForge) ID() string { return "acme-forge" }
func (acmeForge) Verify(_ []byte, signature, secret string) error {
	if signature == "" || signature != secret {
		return webhook.ErrInvalidSignature
	}
	return nil
}
