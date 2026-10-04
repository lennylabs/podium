package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer/webhook"
	"github.com/lennylabs/podium/pkg/store"
)

// tenantFaultStore fails the tenant read so the webhook handler's
// store-unavailable arm is reachable.
type tenantFaultStore struct {
	*store.Memory
}

func (tenantFaultStore) GetTenant(context.Context, string) (store.Tenant, error) {
	return store.Tenant{}, errors.New("simulated outage")
}

// Spec: §7.3.1 — the advertised webhook URL carries the layer's tenant ID on a
// multi-tenant registry and keeps the one-segment form on a single-tenant
// registry. Each segment is escaped as a single path segment.
func TestWebhookURL_TenantForms(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		multiTenant bool
		base        string
		want        string
	}{
		{name: "single-tenant", want: "/v1/ingest/webhook/team%20space%2Flayer"},
		{name: "single-tenant-absolute", base: "https://podium.acme.com/", want: "https://podium.acme.com/v1/ingest/webhook/team%20space%2Flayer"},
		{name: "multi-tenant", multiTenant: true, want: "/v1/ingest/webhook/acme%2Fid%20x/team%20space%2Flayer"},
		{name: "multi-tenant-absolute", multiTenant: true, base: "https://podium.acme.com", want: "https://podium.acme.com/v1/ingest/webhook/acme%2Fid%20x/team%20space%2Flayer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := NewLayerEndpoint(store.NewMemory(), "boot", NewModeTracker()).WithPublicBaseURL(tc.base)
			if tc.multiTenant {
				e = e.WithTenantRouting()
			}
			if got := e.webhookURL("acme/id x", "team space/layer"); got != tc.want {
				t.Errorf("webhookURL = %q, want %q", got, tc.want)
			}
		})
	}
}

// newMultiTenantWebhookEndpoint provisions an active tenant "acme-id", a
// deactivated tenant "gone-id", and the boot tenant "boot", each holding a
// git layer "vendor" signed with the tenant's own secret, and returns a
// multi-tenant endpoint bound to the boot tenant.
func newMultiTenantWebhookEndpoint(t *testing.T) *LayerEndpoint {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	for _, id := range []string{"acme-id", "gone-id", "boot"} {
		if err := st.CreateTenant(ctx, store.Tenant{ID: id, Name: id}); err != nil {
			t.Fatalf("CreateTenant(%s): %v", id, err)
		}
		if err := st.PutLayerConfig(ctx, store.LayerConfig{
			TenantID: id, ID: "vendor", SourceType: "git", GitProvider: "github",
			WebhookSecret: id + "-secret",
		}); err != nil {
			t.Fatalf("PutLayerConfig(%s): %v", id, err)
		}
	}
	if err := st.DeactivateTenant(ctx, "gone-id"); err != nil {
		t.Fatalf("DeactivateTenant: %v", err)
	}
	return NewLayerEndpoint(st, "boot", NewModeTracker()).WithTenantRouting()
}

// deliverSigned posts a GitHub-signed delivery to path on h.
func deliverSigned(t *testing.T, h http.Handler, path, secret string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"ref":"refs/heads/main"}`
	sig, err := webhook.Sign("github", []byte(body), secret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Spec: §7.3.1 — on a multi-tenant registry a delivery names its tenant in the
// path. The handler reads the layer in that tenant alone, and a delivery
// naming a tenant that is not provisioned and active is refused as naming no
// layer. The one-segment route is not served there.
func TestWebhook_MultiTenantTenantSegment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		path     string
		secret   string
		want     int
		wantCode string
	}{
		{name: "active-tenant", path: "/v1/ingest/webhook/acme-id/vendor", secret: "acme-id-secret", want: http.StatusOK},
		{name: "other-tenant-secret", path: "/v1/ingest/webhook/acme-id/vendor", secret: "boot-secret", want: http.StatusUnauthorized, wantCode: "ingest.webhook_invalid"},
		{name: "unknown-tenant", path: "/v1/ingest/webhook/nope-id/vendor", secret: "acme-id-secret", want: http.StatusNotFound, wantCode: "registry.not_found"},
		{name: "inactive-tenant", path: "/v1/ingest/webhook/gone-id/vendor", secret: "gone-id-secret", want: http.StatusNotFound, wantCode: "registry.not_found"},
		{name: "unknown-layer", path: "/v1/ingest/webhook/acme-id/missing", secret: "acme-id-secret", want: http.StatusNotFound, wantCode: "registry.not_found"},
		{name: "one-segment-route", path: "/v1/ingest/webhook/vendor", secret: "boot-secret", want: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newMultiTenantWebhookEndpoint(t)
			rec := deliverSigned(t, e.WebhookHandler(), tc.path, tc.secret)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.wantCode != "" && !strings.Contains(rec.Body.String(), tc.wantCode) {
				t.Errorf("body = %s, want code %s", rec.Body.String(), tc.wantCode)
			}
		})
	}
}

// Spec: §7.3.1 — a tenant read that fails answers 500
// registry.unavailable rather than admitting the delivery.
func TestWebhook_MultiTenantTenantReadFails(t *testing.T) {
	t.Parallel()
	e := NewLayerEndpoint(tenantFaultStore{store.NewMemory()}, "boot", NewModeTracker()).WithTenantRouting()
	rec := deliverSigned(t, e.WebhookHandler(), "/v1/ingest/webhook/acme-id/vendor", "x")
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "registry.unavailable") {
		t.Errorf("status = %d body = %s, want 500 registry.unavailable", rec.Code, rec.Body.String())
	}
}

// Spec: §7.3.1 — on a multi-tenant registry, register and a secret rotation
// advertise a webhook URL that names the routed tenant's ID rather than the
// bound tenant, and a delivery signed with the returned secret to that URL
// reaches the layer's ingest.
func TestWebhook_MultiTenantAdvertisedURL(t *testing.T) {
	t.Parallel()
	f := newTenantFixture(t)
	steps := []struct {
		name, method, target string
		body                 map[string]any
		want                 int
		wantURL              string
	}{
		{name: "register", method: http.MethodPost, target: "/v1/layers",
			body: map[string]any{"id": "new", "source_type": "git", "repo": "https://example.com/n.git"},
			want: http.StatusCreated, wantURL: "/v1/ingest/webhook/acme/new"},
		{name: "rotate", method: http.MethodPut, target: "/v1/layers/update?id=shared",
			body: map[string]any{"rotate_webhook_secret": true},
			want: http.StatusOK, wantURL: "/v1/ingest/webhook/acme/shared"},
	}
	for _, s := range steps {
		rec := serveLayer(f.ep.Handler(), s.method, s.target, s.body, "acme")
		if rec.Code != s.want {
			t.Fatalf("%s status = %d, want %d; body %s", s.name, rec.Code, s.want, rec.Body.String())
		}
		var got LayerRegisterResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s unmarshal: %v", s.name, err)
		}
		if got.WebhookURL != s.wantURL {
			t.Errorf("%s webhook_url = %q, want %q", s.name, got.WebhookURL, s.wantURL)
		}
		if hook := deliverSigned(t, f.ep.WebhookHandler(), got.WebhookURL, got.WebhookSecret); hook.Code != http.StatusOK {
			t.Errorf("%s delivery status = %d, want 200; body %s", s.name, hook.Code, hook.Body.String())
		}
	}
}
