package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer/webhook"
	"github.com/lennylabs/podium/pkg/store"
)

// webhookEndpointOpt wires one collaborator onto the endpoint the fixture
// returns. The §7.3.1 local-source rule reads the admin arm, and
// NewLayerEndpoint installs one that admits every caller, so a case that
// exercises the refusal installs a denying arm through denyAdminArm.
type webhookEndpointOpt func(*LayerEndpoint) *LayerEndpoint

// denyAdminArm installs the admin arm a deployment with an identity provider
// configured wires for a caller who holds no §4.7.2 admin role. A webhook
// delivery carries the per-layer secret rather than a session, so it resolves
// no admin there.
func denyAdminArm(e *LayerEndpoint) *LayerEndpoint {
	return e.WithAdminAuth(func(*http.Request) error { return ErrAdminRequired })
}

// newWebhookEndpoint seeds a git layer with a known webhook secret so the
// §7.3.1 inbound-webhook handler has a verifiable target.
func newWebhookEndpoint(t *testing.T, lc store.LayerConfig, opts ...webhookEndpointOpt) (*LayerEndpoint, string) {
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
	e := NewLayerEndpoint(st, tenantID, NewModeTracker())
	for _, opt := range opts {
		e = opt(e)
	}
	return e, tenantID
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

// trHookID needs escaping in a path segment, so the advertised URL pins that
// each segment round-trips.
const trHookID = "team space/hook"

// deliver posts a GitHub-signed delivery to path, signed with secret.
func (f *trFixture) deliver(t *testing.T, path, secret string) (int, []byte) {
	t.Helper()
	body := []byte(`{"ref":"refs/heads/main"}`)
	sig, err := webhook.Sign("github", body, secret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	status, data := trSend(t, f.ts.URL, map[string]string{"X-Hub-Signature-256": sig}, http.MethodPost, path, body)
	if bytes.Contains(data, []byte(trPoison)) {
		t.Errorf("delivery to %s answered a body naming the boot tenant: %s", path, data)
	}
	return status, data
}

// trWebhookPath returns the escaped path of an advertised webhook URL.
func trWebhookPath(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse webhook_url %q: %v", raw, err)
	}
	return u.EscapedPath()
}

// Spec: §7.3.1 — Case 9: on a multi-tenant registry the advertised webhook
// URL names the layer's tenant ID, a delivery is resolved in the tenant its
// path names, an unknown, inactive, or wrong tenant answers 404
// registry.not_found, the one-segment route is not served, an ingest failure
// is notified under the layer's tenant, and a rotated secret is advertised
// on the same tenant-qualified URL and verifies a delivery to the B layer.
func TestWebhook_TenantQualifiedRoute(t *testing.T) {
	t.Parallel()
	f := newTenantRoutingFixture(t, trOptions{layers: []store.LayerConfig{{
		TenantID: trTenantC, ID: trHookID, SourceType: "git", Repo: trRepo(trTenantC, trHookID), WebhookSecret: "c-secret",
	}}})
	wantPath := "/v1/ingest/webhook/B/" + url.PathEscape(trHookID)
	data := f.expect(t, trDave, http.MethodPost, "/v1/layers",
		map[string]any{"id": trHookID, "source_type": "git", "repo": trRepo(trTenantB, trHookID)}, http.StatusCreated, "")
	reg := trRegistered(t, data)
	if got := trWebhookPath(t, reg.WebhookURL); got != wantPath {
		t.Fatalf("webhook_url path = %q, want %q", got, wantPath)
	}

	if status, body := f.deliver(t, wantPath, "wrong"); status != http.StatusUnauthorized || trErrCode(body) != "ingest.webhook_invalid" {
		t.Errorf("bad signature: %d %s, want 401 ingest.webhook_invalid", status, body)
	}
	for _, seg := range []string{trTenantA, trUnknownOrg, trTenantC} {
		path := "/v1/ingest/webhook/" + seg + "/" + url.PathEscape(trHookID)
		if status, body := f.deliver(t, path, "c-secret"); status != http.StatusNotFound || trErrCode(body) != "registry.not_found" {
			t.Errorf("tenant segment %s: %d %s, want 404 registry.not_found", seg, status, body)
		}
	}
	if status, _ := f.deliver(t, "/v1/ingest/webhook/"+url.PathEscape(trHookID), reg.WebhookSecret); status != http.StatusNotFound {
		t.Errorf("one-segment route: status %d, want 404", status)
	}

	f.failIngest.Store(true)
	if status, body := f.deliver(t, wantPath, reg.WebhookSecret); status != http.StatusInternalServerError {
		t.Errorf("failing ingest: %d %s, want 500", status, body)
	}
	f.failIngest.Store(false)
	f.mu.Lock()
	notes := slices.Clone(f.notes)
	f.mu.Unlock()
	if len(notes) != 1 || notes[0]["tenant"] != trTenantB || notes[0]["layer"] != trHookID {
		t.Errorf("ingest-failure notifications = %v, want one under B for %s", notes, trHookID)
	}

	data = f.expect(t, trDave, http.MethodPut, "/v1/layers/update?id="+url.QueryEscape(trHookID),
		map[string]any{"rotate_webhook_secret": true}, http.StatusOK, "")
	rotated := trRegistered(t, data)
	if got := trWebhookPath(t, rotated.WebhookURL); got != wantPath {
		t.Errorf("rotated webhook_url path = %q, want %q", got, wantPath)
	}
	if rotated.WebhookSecret == "" || rotated.WebhookSecret == reg.WebhookSecret {
		t.Fatalf("rotation returned secret %q, want a new one", rotated.WebhookSecret)
	}
	if status, body := f.deliver(t, wantPath, rotated.WebhookSecret); status != http.StatusOK {
		t.Fatalf("rotated-secret delivery: %d %s, want 200", status, body)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	last := f.ingested[len(f.ingested)-1]
	if last.TenantID != trTenantB || last.ID != trHookID {
		t.Errorf("delivery ingested %s/%s, want B/%s", last.TenantID, last.ID, trHookID)
	}
}
