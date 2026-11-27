package server

import (
	"encoding/base64"
	"net/http"
)

// attestationCacheControl is the cache policy of a served bundle: the bytes
// of a digest never change, and the response depends on the caller's
// visibility, so only the caller's own cache keeps it.
const attestationCacheControl = "private, max-age=31536000, immutable"

// AttestationResponse is the body of GET /v1/attestations/{digest}: the
// stored bundle in canonical base64 (§4.7.10 step 3).
type AttestationResponse struct {
	Bundle string `json:"bundle"`
}

// handleAttestation serves GET /v1/attestations/{digest}. A caller who can
// see no version that links the bundle, and a request for a digest the
// registry does not store, both receive the registry.not_found envelope, so
// the response tells such a caller nothing about the bundle's existence.
//
// Spec: §7.3.5 (Serving bundles)
func (s *Server) handleAttestation(w http.ResponseWriter, r *http.Request) {
	raw, err := s.core.LoadAttestation(r.Context(), s.identity(r), r.PathValue("digest"))
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", attestationCacheControl)
	writeJSON(w, http.StatusOK, AttestationResponse{Bundle: base64.StdEncoding.EncodeToString(raw)})
}
