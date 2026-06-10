package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// This file serves the §7.3.3 tenant-management API under /v1/admin/tenants.
// Every endpoint is operator-authorized (§4.7.1 Operator role) and available
// only on a multi-tenant registry; the mutating endpoints are write endpoints
// under §13.2.1. The routes bypass per-request tenant routing (see
// withTenantRouting) so an operator reaches requireOperator regardless of the
// caller's organization value.

// tenantQuotaOut is the response quota with concrete values.
type tenantQuotaOut struct {
	StorageBytes      int64 `json:"storage_bytes"`
	SearchQPS         int   `json:"search_qps"`
	MaterializeRate   int   `json:"materialize_rate"`
	AuditVolumePerDay int64 `json:"audit_volume_per_day"`
	MaxUserLayers     int   `json:"max_user_layers"`
}

// tenantOut is the §7.3.3 tenant object every endpoint returns.
type tenantOut struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	Quota              tenantQuotaOut `json:"quota"`
	ExposeScopePreview *bool          `json:"expose_scope_preview,omitempty"`
	Active             bool           `json:"active"`
}

func tenantToWire(t store.Tenant) tenantOut {
	return tenantOut{
		ID:   t.ID,
		Name: t.Name,
		Quota: tenantQuotaOut{
			StorageBytes:      t.Quota.StorageBytes,
			SearchQPS:         t.Quota.SearchQPS,
			MaterializeRate:   t.Quota.MaterializeRate,
			AuditVolumePerDay: t.Quota.AuditVolumePerDay,
			MaxUserLayers:     t.Quota.MaxUserLayers,
		},
		ExposeScopePreview: t.ExposeScopePreview,
		Active:             t.Active,
	}
}

// tenantQuotaIn is the request-body quota. Each sub-field is nullable so an
// explicit 0 is distinguished from omission (§4.7.8 gives 0 a meaning).
type tenantQuotaIn struct {
	StorageBytes      *int64 `json:"storage_bytes"`
	SearchQPS         *int   `json:"search_qps"`
	MaterializeRate   *int   `json:"materialize_rate"`
	AuditVolumePerDay *int64 `json:"audit_volume_per_day"`
	MaxUserLayers     *int   `json:"max_user_layers"`
}

// applyTo overlays the present sub-fields onto q, preserving every omitted
// sub-field at its current value.
func (in tenantQuotaIn) applyTo(q store.Quota) store.Quota {
	if in.StorageBytes != nil {
		q.StorageBytes = *in.StorageBytes
	}
	if in.SearchQPS != nil {
		q.SearchQPS = *in.SearchQPS
	}
	if in.MaterializeRate != nil {
		q.MaterializeRate = *in.MaterializeRate
	}
	if in.AuditVolumePerDay != nil {
		q.AuditVolumePerDay = *in.AuditVolumePerDay
	}
	if in.MaxUserLayers != nil {
		q.MaxUserLayers = *in.MaxUserLayers
	}
	return q
}

// requireOperator enforces the §4.7.1 instance operator role on the caller.
func (s *Server) requireOperator(r *http.Request) error {
	id := s.identity(r)
	if err := s.core.OperatorAuthorize(r.Context(), id); err != nil {
		if errors.Is(err, core.ErrForbidden) {
			return err
		}
		return fmt.Errorf("operator authorization: %w", err)
	}
	return nil
}

// tenantAdminGate runs the §7.3.3 preamble shared by every tenant-management
// endpoint: it rejects when the registry is not multi-tenant
// (registry.tenant_management_unavailable, regardless of operator
// authorization, §4.7.1), then requires operator authorization. It returns
// false and writes the error when the request must not proceed.
func (s *Server) tenantAdminGate(w http.ResponseWriter, r *http.Request) bool {
	if s.tenantRouter == nil {
		writeError(w, http.StatusNotFound, "registry.tenant_management_unavailable",
			"Tenant management requires a multi-tenant registry started with PODIUM_MULTI_TENANT.")
		return false
	}
	if err := s.requireOperator(r); err != nil {
		writeError(w, http.StatusForbidden, "auth.forbidden", err.Error())
		return false
	}
	return true
}

// handleTenantList serves GET /v1/admin/tenants (§7.3.3): the cross-org list,
// operator-only, available in read-only mode.
func (s *Server) handleTenantList(w http.ResponseWriter, r *http.Request) {
	if !s.tenantAdminGate(w, r) {
		return
	}
	tenants, err := s.core.ListTenants(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}
	out := make([]tenantOut, 0, len(tenants))
	for _, t := range tenants {
		out = append(out, tenantToWire(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": out})
}

// handleTenantCreate serves POST /v1/admin/tenants (§7.3.3). Idempotent:
// re-creating an already-provisioned name returns 200 with the existing
// tenant; a new tenant returns 201.
func (s *Server) handleTenantCreate(w http.ResponseWriter, r *http.Request) {
	if !s.tenantAdminGate(w, r) {
		return
	}
	if rejectIfReadOnly(w, s.mode) {
		return
	}
	var body struct {
		Name               string         `json:"name"`
		Quota              *tenantQuotaIn `json:"quota"`
		ExposeScopePreview *bool          `json:"expose_scope_preview"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", err.Error())
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "name is required")
		return
	}
	var quota store.Quota
	if body.Quota != nil {
		quota = body.Quota.applyTo(quota)
	}
	t, created, err := s.core.ProvisionTenant(r.Context(), body.Name, quota, body.ExposeScopePreview)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}
	status, action := http.StatusOK, "create_existing"
	if created {
		status, action = http.StatusCreated, "create"
	}
	emitAuditEvent(s.auditSink, r, s.identity(r), audit.EventTenantManaged, t.ID,
		map[string]string{"action": action, "name": t.Name})
	writeJSON(w, status, tenantToWire(t))
}

// handleTenantUpdate serves PATCH /v1/admin/tenants/{id} (§7.3.3). It reads the
// current tenant and overlays only the supplied fields: a present key is
// applied (including expose_scope_preview: null, which clears the gate), an
// omitted key is preserved. It does not change the name.
func (s *Server) handleTenantUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.tenantAdminGate(w, r) {
		return
	}
	if rejectIfReadOnly(w, s.mode) {
		return
	}
	id := r.PathValue("id")
	current, err := s.core.GetTenant(r.Context(), id)
	if err != nil {
		s.writeTenantLookupError(w, id, err)
		return
	}
	// Decode into a presence map so an omitted field is preserved while a
	// present field (including expose_scope_preview: null) is applied.
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", err.Error())
		return
	}
	merged := current
	if q, ok := raw["quota"]; ok {
		var qin tenantQuotaIn
		if err := json.Unmarshal(q, &qin); err != nil {
			writeError(w, http.StatusBadRequest, "registry.invalid_argument", "quota: "+err.Error())
			return
		}
		merged.Quota = qin.applyTo(merged.Quota)
	}
	if esp, ok := raw["expose_scope_preview"]; ok {
		var v *bool
		if err := json.Unmarshal(esp, &v); err != nil {
			writeError(w, http.StatusBadRequest, "registry.invalid_argument", "expose_scope_preview: "+err.Error())
			return
		}
		merged.ExposeScopePreview = v
	}
	if a, ok := raw["active"]; ok {
		var v bool
		if err := json.Unmarshal(a, &v); err != nil {
			writeError(w, http.StatusBadRequest, "registry.invalid_argument", "active: "+err.Error())
			return
		}
		merged.Active = v
	}
	if err := s.core.UpdateTenant(r.Context(), merged); err != nil {
		s.writeTenantLookupError(w, id, err)
		return
	}
	emitAuditEvent(s.auditSink, r, s.identity(r), audit.EventTenantManaged, id,
		map[string]string{"action": "update"})
	writeJSON(w, http.StatusOK, tenantToWire(merged))
}

// handleTenantDeactivate serves DELETE /v1/admin/tenants/{id} (§7.3.3). Soft:
// the tenant stops resolving while its data persists (§4.7.1).
func (s *Server) handleTenantDeactivate(w http.ResponseWriter, r *http.Request) {
	if !s.tenantAdminGate(w, r) {
		return
	}
	if rejectIfReadOnly(w, s.mode) {
		return
	}
	id := r.PathValue("id")
	if err := s.core.DeactivateTenant(r.Context(), id); err != nil {
		s.writeTenantLookupError(w, id, err)
		return
	}
	emitAuditEvent(s.auditSink, r, s.identity(r), audit.EventTenantManaged, id,
		map[string]string{"action": "deactivate"})
	w.WriteHeader(http.StatusNoContent)
}

// writeTenantLookupError maps a store error from a tenant lookup or write to
// the §6.10 envelope: an unknown ID is 404 registry.tenant_not_found, anything
// else is 500 registry.unavailable.
func (s *Server) writeTenantLookupError(w http.ResponseWriter, id string, err error) {
	if errors.Is(err, store.ErrTenantNotFound) {
		writeError(w, http.StatusNotFound, "registry.tenant_not_found", "no tenant with id "+id)
		return
	}
	writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
}
