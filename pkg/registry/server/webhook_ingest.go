package server

import (
	"errors"
	"io"
	"net/http"

	"github.com/lennylabs/podium/pkg/layer/webhook"
	"github.com/lennylabs/podium/pkg/store"
)

// maxWebhookBody bounds the inbound webhook payload read so a hostile or
// misconfigured sender cannot exhaust memory. §9.3 "Bounded payloads": the
// verifier only needs the raw body to recompute the HMAC.
const maxWebhookBody = 1 << 20 // 1 MiB

// handleWebhook is the §7.3.1 inbound webhook ingest trigger
// (POST /v1/ingest/webhook/{id}, or /v1/ingest/webhook/{tenant}/{id} on a
// multi-tenant endpoint). It loads the layer's configured
// GitProvider and webhook secret, verifies the delivery signature through
// the process-global webhook.Default GitProvider registry (§9.1/§9.2), and
// only on success queues the reingest the polling endpoint records. A
// failed verification returns 401 ingest.webhook_invalid (§6.10) and never
// reaches the content store.
//
// The GitProvider registry is the build-path consumer that makes "import a
// custom GitProvider into a source build" change behavior: a provider
// registered via webhook.Default.Register is selected here by the layer's
// configured provider id without editing this handler.
//
// Spec: §7.3.1
func (e *LayerEndpoint) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
		return
	}
	if rejectIfReadOnly(w, e.mode) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		id = r.URL.Query().Get("id")
	}
	if id == "" {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "layer id required")
		return
	}
	cfg, ok := e.webhookLayer(w, r, id)
	if !ok {
		return
	}
	if cfg.SourceType != "git" {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "layer is not a git source")
		return
	}
	provID := cfg.GitProvider
	if provID == "" {
		provID = "github"
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "could not read body")
		return
	}
	sig := webhookSignatureHeader(r, provID)
	if err := webhook.Default.Verify(provID, body, sig, cfg.WebhookSecret); err != nil {
		// §6.10 ingest.webhook_invalid: the signature did not verify, or the
		// provider id has no registered GitProvider. The delivery is rejected
		// before any ingest so unverified content never reaches the store.
		writeError(w, http.StatusUnauthorized, "ingest.webhook_invalid", err.Error())
		return
	}
	// spec: §7.3.1 — the local-source authorization rule. A verified
	// delivery drives the same ingest the guarded reingest drives, so a
	// stored git layer whose repository string resolves to go-git's file
	// transport is refused here rather than re-read with the registry's own
	// rights by the holder of the per-layer secret. A repository naming a
	// network endpoint is not classified, so every existing webhook for such
	// a layer keeps working, its stored local_path included.
	if !e.authorizeLocalSource(w, r, cfg.SourceType, cfg.LocalPath, cfg.Repo) {
		return
	}
	// §7.3.1: a verified delivery "fetches the new commit, ingests". Drive
	// the ingest pipeline (no break-glass on the webhook path) and return its
	// result summary. Without a runner wired the handler records the intent.
	e.runIngestAndRespond(w, r, cfg, nil)
}

// webhookLayer loads the layer a delivery names and writes the refusal when
// there is none. A single-tenant endpoint reads its bound tenant. A
// multi-tenant endpoint reads the tenant ID from the path, because the
// delivery carries no caller organization from which §6.3.1 could select one,
// and requires that tenant to be provisioned and active. A deactivated
// tenant's rows persist, so the layer read alone would still find its layer;
// both an unknown and an inactive tenant answer 404 registry.not_found, as a
// delivery naming no layer does.
//
// Spec: §7.3.1
func (e *LayerEndpoint) webhookLayer(w http.ResponseWriter, r *http.Request, id string) (store.LayerConfig, bool) {
	ctx := r.Context()
	tenantID, _ := e.tenant(ctx)
	if e.multiTenant {
		tenantID = r.PathValue("tenant")
		t, err := e.store.GetTenant(ctx, tenantID)
		if err != nil && !errors.Is(err, store.ErrTenantNotFound) {
			writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
			return store.LayerConfig{}, false
		}
		if err != nil || !t.Active {
			writeError(w, http.StatusNotFound, "registry.not_found", "no such layer: "+id)
			return store.LayerConfig{}, false
		}
	}
	cfg, err := e.store.GetLayerConfig(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "registry.not_found", "no such layer: "+id)
		} else {
			writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		}
		return store.LayerConfig{}, false
	}
	return cfg, true
}

// webhookSignatureHeader returns the signature credential for the named
// GitProvider from the delivery headers. GitHub and Bitbucket sign in
// X-Hub-Signature-256 / X-Hub-Signature; GitLab sends a shared token in
// X-Gitlab-Token. A custom provider's deliveries fall back to the GitHub
// header convention.
func webhookSignatureHeader(r *http.Request, provID string) string {
	switch provID {
	case "gitlab":
		return r.Header.Get("X-Gitlab-Token")
	case "bitbucket":
		if v := r.Header.Get("X-Hub-Signature"); v != "" {
			return v
		}
		return r.Header.Get("X-Hub-Signature-256")
	default: // github and custom providers
		if v := r.Header.Get("X-Hub-Signature-256"); v != "" {
			return v
		}
		return r.Header.Get("X-Hub-Signature")
	}
}
