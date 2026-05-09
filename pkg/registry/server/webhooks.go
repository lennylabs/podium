package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/webhook"
)

// handleWebhooksList serves §7.3.2 receiver CRUD at the collection
// level: GET /v1/webhooks lists, POST /v1/webhooks creates.
func (s *Server) handleWebhooksList(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rs, err := s.webhooks.Store.List(r.Context(), s.tenant)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
			return
		}
		// Strip secrets from the response so listing does not leak
		// the HMAC key to anyone with read access.
		out := make([]webhook.Receiver, len(rs))
		for i, r := range rs {
			r.Secret = "***"
			out[i] = r
		}
		writeJSON(w, http.StatusOK, map[string]any{"receivers": out})
	case http.MethodPost:
		var body struct {
			URL         string   `json:"url"`
			Secret      string   `json:"secret"`
			EventFilter []string `json:"event_filter"`
			Disabled    bool     `json:"disabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "registry.invalid_argument", err.Error())
			return
		}
		if body.URL == "" {
			writeError(w, http.StatusBadRequest, "registry.invalid_argument", "url is required")
			return
		}
		if body.Secret == "" {
			body.Secret = generateWebhookSecret()
		}
		rec := webhook.Receiver{
			ID:          newWebhookID(),
			TenantID:    s.tenant,
			URL:         body.URL,
			Secret:      body.Secret,
			EventFilter: body.EventFilter,
			Disabled:    body.Disabled,
			CreatedAt:   time.Now().UTC(),
		}
		if err := s.webhooks.Store.Put(r.Context(), rec); err != nil {
			writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
			return
		}
		// Return the secret on creation so the operator can record it;
		// subsequent List calls mask it.
		writeJSON(w, http.StatusCreated, rec)
	default:
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
	}
}

// handleWebhookOne serves the per-receiver routes at /v1/webhooks/{id}:
// GET reads, PUT updates (re-enable / change filter), DELETE removes.
func (s *Server) handleWebhookOne(w http.ResponseWriter, r *http.Request) {
	const prefix = "/v1/webhooks/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		writeError(w, http.StatusNotFound, "registry.not_found", "no such webhook")
		return
	}
	id := r.URL.Path[len(prefix):]
	if id == "" {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "id is required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		rec, err := s.webhooks.Store.Get(r.Context(), s.tenant, id)
		if err != nil {
			writeError(w, http.StatusNotFound, "registry.not_found", err.Error())
			return
		}
		rec.Secret = "***"
		writeJSON(w, http.StatusOK, rec)
	case http.MethodPut:
		current, err := s.webhooks.Store.Get(r.Context(), s.tenant, id)
		if err != nil {
			writeError(w, http.StatusNotFound, "registry.not_found", err.Error())
			return
		}
		var body struct {
			URL         *string   `json:"url,omitempty"`
			Secret      *string   `json:"secret,omitempty"`
			EventFilter *[]string `json:"event_filter,omitempty"`
			Disabled    *bool     `json:"disabled,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "registry.invalid_argument", err.Error())
			return
		}
		if body.URL != nil {
			current.URL = *body.URL
		}
		if body.Secret != nil {
			current.Secret = *body.Secret
		}
		if body.EventFilter != nil {
			current.EventFilter = *body.EventFilter
		}
		if body.Disabled != nil {
			current.Disabled = *body.Disabled
			if !*body.Disabled {
				// Re-enabling clears the failure counter so the worker
				// gives the receiver a fresh budget.
				current.FailureCount = 0
			}
		}
		if err := s.webhooks.Store.Put(r.Context(), current); err != nil {
			writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
			return
		}
		current.Secret = "***"
		writeJSON(w, http.StatusOK, current)
	case http.MethodDelete:
		if err := s.webhooks.Store.Delete(r.Context(), s.tenant, id); err != nil {
			writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
	}
}

// newWebhookID returns a short opaque identifier; the format is not
// load-bearing — receivers reference themselves by URL in practice
// and the ID is just for CRUD addressing.
func newWebhookID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return "wh-" + hex.EncodeToString(buf)
}

// generateWebhookSecret returns a 32-byte hex string. Operators
// who supply their own secret on POST take precedence; the auto-
// generated value is convenient when the receiver also generates
// the secret out-of-band.
func generateWebhookSecret() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// quiet unused-import warning when build paths exclude fmt.
var _ = fmt.Sprintf
