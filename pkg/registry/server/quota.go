package server

import "net/http"

// handleQuota serves §4.7.8 GET /v1/quota. Returns the configured
// tenant's limits plus current usage. Read-only; not admin-gated
// since quota visibility is informational and useful to anyone
// authoring against the catalog.
func (s *Server) handleQuota(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
		return
	}
	info, err := s.core.Quota(r.Context())
	if err != nil {
		writeError(w, http.StatusNotFound, "registry.not_found", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id": info.TenantID,
		"limits":    info.Limits,
		"usage": map[string]any{
			"storage_bytes": info.StorageBytes,
		},
	})
}
