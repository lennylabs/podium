package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
)

// handleAdminGrants serves §4.7.2 admin-grant CRUD:
//
//	POST   /v1/admin/grants  body: {user_id}
//	DELETE /v1/admin/grants?user_id=...
//
// Both routes require an authenticated admin caller. The check
// runs through core.AdminAuthorize so public-mode and anonymous
// requests are rejected with auth.forbidden.
func (s *Server) handleAdminGrants(w http.ResponseWriter, r *http.Request) {
	if err := s.requireAdmin(r); err != nil {
		writeError(w, http.StatusForbidden, "auth.forbidden", err.Error())
		return
	}
	switch r.Method {
	case http.MethodPost:
		var body struct {
			UserID string `json:"user_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "registry.invalid_argument", err.Error())
			return
		}
		if body.UserID == "" {
			writeError(w, http.StatusBadRequest, "registry.invalid_argument", "user_id is required")
			return
		}
		if err := s.core.GrantAdmin(r.Context(), body.UserID); err != nil {
			writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"user_id": body.UserID})
	case http.MethodDelete:
		userID := r.URL.Query().Get("user_id")
		if userID == "" {
			writeError(w, http.StatusBadRequest, "registry.invalid_argument", "user_id is required")
			return
		}
		if err := s.core.RevokeAdmin(r.Context(), userID); err != nil {
			writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
	}
}

// handleAdminShowEffective serves §4.6.1 GET /v1/admin/show-effective?user_id=...
// returning the per-layer visibility for the named target. Admin-
// only because the visibility config is itself sensitive.
func (s *Server) handleAdminShowEffective(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
		return
	}
	if err := s.requireAdmin(r); err != nil {
		writeError(w, http.StatusForbidden, "auth.forbidden", err.Error())
		return
	}
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "user_id is required")
		return
	}
	groups := r.URL.Query()["group"]
	target := layer.Identity{
		Sub: userID, Groups: groups, IsAuthenticated: true,
	}
	rows, err := s.core.ShowEffective(r.Context(), target)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id": userID,
		"groups":  groups,
		"layers":  rows,
	})
}

// requireAdmin enforces admin auth on the caller. Tests bypass via
// WithIdentityResolver; production wires the JWT-decoded identity.
func (s *Server) requireAdmin(r *http.Request) error {
	id := s.identity(r)
	if err := s.core.AdminAuthorize(r.Context(), id); err != nil {
		if errors.Is(err, core.ErrForbidden) {
			return err
		}
		return fmt.Errorf("admin authorization: %w", err)
	}
	return nil
}
