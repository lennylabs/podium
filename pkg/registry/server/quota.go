package server

import "net/http"

// handleQuota serves §4.7.8 GET /v1/quota. Returns the configured
// tenant's limits plus current usage. Read-only; not admin-gated
// since quota visibility is informational and useful to anyone
// authoring against the catalog.
//
// The search QPS, materialization rate, and audit volume limits are the ones
// the limiter enforces against this request: the tenant record routing carried
// resolved against the deployment defaults, with no further store read. The
// storage and user-layer values are reported as stored.
//
// Spec: §4.7.8
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
	limits := info.Limits
	if s.quota != nil {
		eff := s.quota.Limits(tenantQuotaFrom(r.Context()))
		limits.SearchQPS = eff.SearchQPS
		limits.MaterializeRate = eff.MaterializeRate
		limits.AuditVolumePerDay = eff.AuditVolumePerDay
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id": info.TenantID,
		"limits":    limits,
		"usage": map[string]any{
			"storage_bytes": info.StorageBytes,
		},
	})
}
