package server

import (
	"context"
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
	// spec: §7.3.2 — receiver CRUD is an org-level configuration gated on
	// the per-tenant admin role. A non-admin caller is refused with
	// auth.forbidden before any read or write, closing the gap where any
	// authenticated caller (or an unauthenticated standalone bind) could
	// register a receiver and point the registry at an internal endpoint.
	// Mirrors handleAdminGrants in admin.go.
	if err := s.requireAdmin(r); err != nil {
		writeError(w, http.StatusForbidden, "auth.forbidden", err.Error())
		return
	}
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
		// spec: §13.2.1 — creating a webhook receiver is a write endpoint,
		// rejected in read-only mode with registry.read_only (consistent
		// with the layer-admin handlers). GET above stays available so
		// operators can still inspect receivers.
		if rejectIfReadOnly(w, s.mode) {
			return
		}
		var body struct {
			URL         string   `json:"url"`
			Secret      string   `json:"secret"`
			EventFilter []string `json:"event_filter"`
			Disabled    bool     `json:"disabled"`
			Debounce    string   `json:"debounce"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "registry.invalid_argument", err.Error())
			return
		}
		if body.URL == "" {
			writeError(w, http.StatusBadRequest, "registry.invalid_argument", "url is required")
			return
		}
		debounce, ok := parseDebounce(w, body.Debounce)
		if !ok {
			return
		}
		if !s.validateReceiverURL(r.Context(), w, body.URL) {
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
			Debounce:    debounce,
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
	// spec: §7.3.2 — the per-receiver routes are admin-gated for the same
	// reason as the collection route (handleWebhooksList): a non-admin
	// caller is refused with auth.forbidden before the route is dispatched.
	if err := s.requireAdmin(r); err != nil {
		writeError(w, http.StatusForbidden, "auth.forbidden", err.Error())
		return
	}
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
		// spec: §13.2.1 — editing a webhook receiver is a write endpoint,
		// rejected in read-only mode with registry.read_only.
		if rejectIfReadOnly(w, s.mode) {
			return
		}
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
			Debounce    *string   `json:"debounce,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "registry.invalid_argument", err.Error())
			return
		}
		if body.URL != nil {
			// Re-validate the receiver URL against the SSRF policy when the
			// edit changes it, so a PUT cannot move a receiver to a target
			// the policy refuses (§7.3.2).
			if !s.validateReceiverURL(r.Context(), w, *body.URL) {
				return
			}
			current.URL = *body.URL
		}
		if body.Debounce != nil {
			debounce, ok := parseDebounce(w, *body.Debounce)
			if !ok {
				return
			}
			current.Debounce = debounce
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
		// spec: §13.2.1 — removing a webhook receiver is a write endpoint,
		// rejected in read-only mode with registry.read_only.
		if rejectIfReadOnly(w, s.mode) {
			return
		}
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

// parseDebounce parses the §7.3.2 per-receiver debounce window from the
// request body. An empty value leaves the window unset, which preserves
// per-event delivery. A non-empty value parses as a Go duration string;
// an unparsable or negative value writes registry.invalid_argument naming
// the debounce field and returns ok=false so the caller stops.
func parseDebounce(w http.ResponseWriter, raw string) (time.Duration, bool) {
	if raw == "" {
		return 0, true
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument",
			fmt.Sprintf("debounce: %v", err))
		return 0, false
	}
	if d < 0 {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument",
			"debounce: must not be negative")
		return 0, false
	}
	return d, true
}

// validateReceiverURL checks url against the worker's SSRF policy (§7.3.2)
// at registration so a receiver cannot be created or moved to a target the
// policy refuses. The registry originates the outbound request, so the URL
// is validated with the same policy the worker re-checks at delivery; the
// handler reads the policy through s.webhooks.Policy(). A nil policy skips
// the registration-time check, and the worker still re-validates before
// each POST. A rejected target writes registry.invalid_argument naming the
// disallowed host and returns false so the caller stops.
func (s *Server) validateReceiverURL(ctx context.Context, w http.ResponseWriter, url string) bool {
	policy := s.webhooks.Policy()
	if policy == nil {
		return true
	}
	if err := policy.Validate(ctx, url); err != nil {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", err.Error())
		return false
	}
	return true
}
