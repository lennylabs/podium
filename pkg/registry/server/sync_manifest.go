package server

import "net/http"

// handleSyncManifest serves GET /v1/sync/manifest: the caller's full
// effective view as a flat artifact list, visibility-filtered server-side.
// `podium sync` server-source mode (§7.5) walks this to discover which
// artifacts to load and materialize, then loads each via load_artifact.
// Unlike search_artifacts it carries no relevance ranking and no top-K cap,
// so a sync of more than 50 artifacts enumerates in one request.
//
// spec: §2.2 (the registry "composes the caller's effective view ... applies
// per-layer visibility"), §7.5 (server-source sync). F-2.2.2.
func (s *Server) handleSyncManifest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
		return
	}
	view, err := s.core.EffectiveView(r.Context(), s.identity(r))
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": view})
}
