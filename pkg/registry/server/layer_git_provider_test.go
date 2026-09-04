package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer/webhook"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// gitProviderHarness mounts the layer-management surface and the inbound
// webhook surface over one store, so an arm registers a layer through the
// §7.3.1 request body and then drives a delivery against the provider that
// registration selected.
type gitProviderHarness struct {
	base string
	hook string
}

func newGitProviderHarness(t *testing.T) gitProviderHarness {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	e := server.NewLayerEndpoint(st, "t", server.NewModeTracker())
	api := httptest.NewServer(e.Handler())
	t.Cleanup(api.Close)
	hook := httptest.NewServer(e.WebhookHandler())
	t.Cleanup(hook.Close)
	return gitProviderHarness{base: api.URL, hook: hook.URL}
}

// deliver posts a webhook body to the layer's ingest endpoint with the
// signature credential in the header the named provider sends it in.
func (h gitProviderHarness) deliver(t *testing.T, id, providerID, body, secret string) (int, string) {
	t.Helper()
	sig, err := webhook.Sign(providerID, []byte(body), secret)
	if err != nil {
		t.Fatalf("Sign(%s): %v", providerID, err)
	}
	req, err := http.NewRequest(http.MethodPost, h.hook+"/v1/ingest/webhook/"+id, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build delivery: %v", err)
	}
	if providerID == "gitlab" {
		req.Header.Set("X-Gitlab-Token", sig)
	} else {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// registerResponse reads the members of a registration response an arm needs:
// the layer's stored git provider and the secret the envelope returns once.
func registerResponse(t *testing.T, body []byte) (gitProvider, secret string) {
	t.Helper()
	var env struct {
		Layer struct {
			GitProvider string `json:"git_provider"`
		} `json:"layer"`
		WebhookSecret string `json:"webhook_secret"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode register response: %v\nbody: %s", err, body)
	}
	return env.Layer.GitProvider, env.WebhookSecret
}

// Spec: §7.3.1 — a registration names the GitProvider whose signature scheme
// verifies the layer's inbound deliveries. The stored value selects the
// scheme at delivery time, so a GitLab-signed delivery verifies and a
// GitHub-signed one is refused.
func TestLayerEndpoint_GitProviderSelectsTheDeliveryScheme(t *testing.T) {
	t.Parallel()
	h := newGitProviderHarness(t)

	resp, body := mustPost(t, h.base, "/v1/layers", map[string]any{
		"id": "team-finance", "source_type": "git",
		"repo": "git@gitlab.com:acme/finance.git", "ref": "main",
		"git_provider": "gitlab",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, body=%s", resp.StatusCode, body)
	}
	provider, secret := registerResponse(t, body)
	if provider != "gitlab" {
		t.Errorf("git_provider = %q, want gitlab", provider)
	}
	if secret == "" {
		t.Fatalf("git registration returned no webhook_secret\nbody: %s", body)
	}

	if st, out := h.deliver(t, "team-finance", "gitlab", `{"ref":"refs/heads/main"}`, secret); st != http.StatusOK {
		t.Errorf("GitLab-signed delivery status = %d, want 200; body=%s", st, out)
	}
	st, out := h.deliver(t, "team-finance", "github", `{"ref":"refs/heads/main"}`, secret)
	if st != http.StatusUnauthorized {
		t.Errorf("GitHub-signed delivery to a gitlab layer status = %d, want 401; body=%s", st, out)
	}
	if !strings.Contains(out, "ingest.webhook_invalid") {
		t.Errorf("refusal body missing ingest.webhook_invalid: %s", out)
	}
}

// Spec: §7.3.1 — a layer that names no provider resolves to github, which is
// the value every layer stored before the field existed carries, so no
// migration is required.
func TestLayerEndpoint_GitProviderDefaultsToGitHub(t *testing.T) {
	t.Parallel()
	h := newGitProviderHarness(t)

	resp, body := mustPost(t, h.base, "/v1/layers", map[string]any{
		"id": "vendor", "source_type": "git",
		"repo": "git@github.com:acme/vendor.git", "ref": "main",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, body=%s", resp.StatusCode, body)
	}
	provider, secret := registerResponse(t, body)
	if provider != "" {
		t.Errorf("git_provider on a registration that names none = %q, want the empty string", provider)
	}
	if st, out := h.deliver(t, "vendor", "github", `{}`, secret); st != http.StatusOK {
		t.Errorf("GitHub-signed delivery status = %d, want 200; body=%s", st, out)
	}
}

// Spec: §7.3.1 / §6.10 — a value the registry has not registered, and a value
// on a local source, are each refused with 400 registry.invalid_argument
// naming the field.
func TestLayerEndpoint_GitProviderRefusals(t *testing.T) {
	t.Parallel()
	h := newGitProviderHarness(t)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"unregistered provider", map[string]any{
			"id": "team-finance", "source_type": "git",
			"repo": "git@example.com:acme/finance.git", "git_provider": "acme-forge-that-is-not-registered",
		}},
		{"local source", map[string]any{
			"id": "org-defaults", "source_type": "local",
			"local_path": "/tmp/x", "git_provider": "gitlab",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := mustPost(t, h.base, "/v1/layers", tc.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode, body)
			}
			for _, want := range []string{"registry.invalid_argument", "git_provider"} {
				if !strings.Contains(string(body), want) {
					t.Errorf("refusal body missing %q: %s", want, body)
				}
			}
		})
	}
}

// Spec: §7.3.1 — the update path patches git_provider on the same terms as
// the other patchable fields, and refuses an unregistered value.
func TestLayerEndpoint_UpdatePatchesGitProvider(t *testing.T) {
	t.Parallel()
	h := newGitProviderHarness(t)

	resp, body := mustPost(t, h.base, "/v1/layers", map[string]any{
		"id": "team-finance", "source_type": "git",
		"repo": "git@bitbucket.org:acme/finance.git", "ref": "main",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, body=%s", resp.StatusCode, body)
	}
	_, secret := registerResponse(t, body)

	resp, body = mustPut(t, h.base, "/v1/layers/update?id=team-finance", map[string]any{
		"git_provider": "bitbucket",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body=%s", resp.StatusCode, body)
	}
	if provider, _ := registerResponse(t, body); provider != "bitbucket" {
		t.Errorf("git_provider after update = %q, want bitbucket", provider)
	}
	// The patched provider governs the delivery scheme: Bitbucket signs with
	// a bare hex HMAC, which GitHub's scheme does not accept.
	if st, out := h.deliver(t, "team-finance", "bitbucket", `{}`, secret); st != http.StatusOK {
		t.Errorf("Bitbucket-signed delivery status = %d, want 200; body=%s", st, out)
	}

	resp, body = mustPut(t, h.base, "/v1/layers/update?id=team-finance", map[string]any{
		"git_provider": "acme-forge-that-is-not-registered",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("update with an unregistered provider status = %d, want 400; body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "git_provider") {
		t.Errorf("refusal body does not name the field: %s", body)
	}

	// The local-source refusal holds on the update path too, where the
	// source type is read from the stored layer.
	if resp, body := mustPost(t, h.base, "/v1/layers", map[string]any{
		"id": "org-defaults", "source_type": "local", "local_path": "/tmp/x",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("register local status = %d, body=%s", resp.StatusCode, body)
	}
	resp, body = mustPut(t, h.base, "/v1/layers/update?id=org-defaults", map[string]any{
		"git_provider": "gitlab",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("update of a local layer's git_provider status = %d, want 400; body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "git_provider") {
		t.Errorf("refusal body does not name the field: %s", body)
	}
}
