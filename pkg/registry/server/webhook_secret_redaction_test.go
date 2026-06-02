package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
)

// Spec: §7.3.1 (F-7.3.1) — the git webhook HMAC secret is a credential
// returned once at registration ("podium layer register returns the webhook
// URL and HMAC secret"). It must not leak through GET /v1/layers, which is not
// admin-gated; any caller who could list layers would otherwise read every
// layer's inbound secret and forge signed webhook deliveries. The secret is
// redacted from every marshaled LayerConfig and surfaced only via the
// dedicated webhook_secret response field, mirroring the outbound-receiver
// masking.
func TestLayerEndpoint_ListRedactsWebhookSecret(t *testing.T) {
	t.Parallel()
	base, st, cleanup := newLayerHarness(t)
	defer cleanup()

	// Register a git layer; the one-time response carries the secret.
	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id":          "team-finance",
		"source_type": "git",
		"repo":        "git@github.com:acme/finance.git",
		"ref":         "main",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d: %s", resp.StatusCode, body)
	}
	var reg server.LayerRegisterResponse
	if err := json.Unmarshal(body, &reg); err != nil {
		t.Fatalf("unmarshal register response: %v", err)
	}
	secret := reg.WebhookSecret
	if secret == "" {
		t.Fatalf("register response omitted the one-time webhook_secret")
	}
	// The embedded layer object must already redact the secret.
	if reg.Layer.WebhookSecret != "" {
		t.Errorf("register response's embedded layer leaked the secret: %q", reg.Layer.WebhookSecret)
	}
	// The secret must appear exactly once in the register body: only in the
	// sanctioned top-level webhook_secret field, never via the embedded layer.
	if n := strings.Count(string(body), secret); n != 1 {
		t.Errorf("secret appears %d times in register response, want 1 (the dedicated webhook_secret field):\n%s", n, body)
	}

	// The secret is persisted (the redaction is a marshaling concern only): the
	// store still holds it so inbound webhook verification keeps working.
	stored, err := st.GetLayerConfig(context.Background(), "t", "team-finance")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if stored.WebhookSecret != secret {
		t.Fatalf("stored secret = %q, want the registered %q (json:\"-\" must not affect storage)", stored.WebhookSecret, secret)
	}

	// GET /v1/layers must not expose the secret value or the field name.
	list := mustGet(t, base, "/v1/layers")
	if strings.Contains(string(list), secret) {
		t.Errorf("GET /v1/layers leaked the webhook secret value:\n%s", list)
	}
	if strings.Contains(string(list), "WebhookSecret") || strings.Contains(string(list), "webhook_secret") {
		t.Errorf("GET /v1/layers emitted a webhook-secret field:\n%s", list)
	}

	// The soft-deleted listing (?deleted=true) marshals the same rows; confirm
	// it also redacts. Unregister, then list deleted.
	if dresp, dbody := mustDelete(t, base, "/v1/layers?id=team-finance"); dresp.StatusCode != http.StatusOK {
		t.Fatalf("unregister status = %d: %s", dresp.StatusCode, dbody)
	}
	deleted := mustGet(t, base, "/v1/layers?deleted=true")
	if strings.Contains(string(deleted), secret) {
		t.Errorf("GET /v1/layers?deleted=true leaked the webhook secret:\n%s", deleted)
	}
}
