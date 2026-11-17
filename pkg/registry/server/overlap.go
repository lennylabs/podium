package server

import (
	"net/http"

	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/core"
)

// handleOverlap serves the §7.3.1 catalog overlap report, GET /v1/overlap.
// Any caller that can search can call it: the request passes through the same
// identity verification, tenant routing, and browser-session cookie handling
// as /v1/search_artifacts, and the report covers only what the caller's
// identity can see (§4.6). The optional threshold parameter must be a number
// from 0.5 to 1.0, and the optional artifact parameter restricts the response
// to one artifact's diagnostics. It reads stored records and issues no index
// query, and it is a read endpoint, so a read-only registry (§13.2.1) serves
// it.
//
// Spec: §7.3.1
func (s *Server) handleOverlap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
		return
	}
	q := r.URL.Query()
	query := core.OverlapQuery{ArtifactID: q.Get("artifact")}
	if q.Has("threshold") {
		raw := q.Get("threshold")
		t, ok := lint.ParseSimilarity(raw)
		if !ok {
			writeError(w, http.StatusBadRequest, "registry.invalid_argument",
				"threshold must be a number from 0.5 to 1.0: "+raw)
			return
		}
		query.Threshold = &t
	}
	report, err := s.core.CatalogOverlap(r.Context(), s.identity(r), query)
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// handleOverlapRebuild serves POST /v1/admin/overlap/rebuild, which computes
// the §7.3.1 overlap records of every served artifact of the caller's tenant
// and answers {computed, failed}. It is authorized as the re-embed endpoint
// is (§4.7): the per-tenant admin role, except on a deployment that
// authenticates no caller, where WithUnauthenticatedReembed admits the
// request. It is a write endpoint, so a read-only registry (§13.2.1) rejects
// it with registry.read_only.
//
// Spec: §7.3.1, §4.7
func (s *Server) handleOverlapRebuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
		return
	}
	if !s.unauthenticatedReembed {
		// An authorization the registry cannot evaluate denies rather than
		// admits, as on the re-embed endpoint.
		if err := s.requireAdmin(r); err != nil {
			writeError(w, http.StatusForbidden, "auth.forbidden", err.Error())
			return
		}
	}
	if rejectIfReadOnly(w, s.mode) {
		return
	}
	res, err := s.core.RebuildOverlap(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
