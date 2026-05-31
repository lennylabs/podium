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
// (POST /v1/layers/webhook?id=<layer>). It loads the layer's configured
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
func (e *LayerEndpoint) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
		return
	}
	if e.mode != nil {
		if err := e.mode.CheckConfig(); err != nil {
			writeError(w, http.StatusServiceUnavailable, "config.read_only", err.Error())
			return
		}
	}
	id := r.PathValue("id")
	if id == "" {
		id = r.URL.Query().Get("id")
	}
	if id == "" {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "layer id required")
		return
	}
	cfg, err := e.store.GetLayerConfig(r.Context(), e.tenantID, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "registry.not_found", "no such layer: "+id)
		} else {
			writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		}
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
	// §7.3.1: a verified delivery "fetches the new commit, ingests". Drive
	// the ingest pipeline (no break-glass on the webhook path) and return its
	// result summary. Without a runner wired the handler records the intent.
	e.runIngestAndRespond(w, r, cfg, nil)
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
