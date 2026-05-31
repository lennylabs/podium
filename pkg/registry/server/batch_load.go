package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
)

// BatchLoadCap is the §7.6.2 hard cap on batch size. Larger
// requests fail with registry.invalid_argument.
const BatchLoadCap = 50

// BatchLoadRequest is the wire shape of POST
// /v1/artifacts:batchLoad.
type BatchLoadRequest struct {
	IDs         []string          `json:"ids"`
	SessionID   string            `json:"session_id,omitempty"`
	Harness     string            `json:"harness,omitempty"`
	VersionPins map[string]string `json:"version_pins,omitempty"`
}

// BatchLoadEnvelope is one per-item response. Status is "ok" or
// "error"; on error the Error field carries the §6.10 envelope.
type BatchLoadEnvelope struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Type         string `json:"type,omitempty"`
	Version      string `json:"version,omitempty"`
	ContentHash  string `json:"content_hash,omitempty"`
	ManifestBody string `json:"manifest_body,omitempty"`
	Frontmatter  string `json:"frontmatter,omitempty"`
	// Resources carries every bundled resource as a presigned reference
	// per the §7.6.2 wire example {path, presigned_url, content_hash}.
	// The batch path keeps the response body small by delivering all
	// resources via URL; the SDK fetches them concurrently afterward.
	Resources          []BatchResource `json:"resources,omitempty"`
	Deprecated         bool            `json:"deprecated,omitempty"`
	ReplacedBy         string          `json:"replaced_by,omitempty"`
	DeprecationWarning string          `json:"deprecation_warning,omitempty"`
	Error              *ErrorResponse  `json:"error,omitempty"`
}

// BatchResource is one §7.6.2 bundled-resource reference in a batch
// envelope. The field names match the spec example exactly.
type BatchResource struct {
	Path         string `json:"path"`
	PresignedURL string `json:"presigned_url"`
	ContentHash  string `json:"content_hash"`
}

// handleBatchLoad answers POST /v1/artifacts:batchLoad per
// §7.6.2. Partial failure is the rule: items the caller cannot
// see come back as status=error with the §6.10 envelope; the
// batch HTTP status stays 200.
func (s *Server) handleBatchLoad(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
		return
	}
	var req BatchLoadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", err.Error())
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument",
			"ids is required and must be non-empty")
		return
	}
	if len(req.IDs) > BatchLoadCap {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument",
			"ids exceeds the batch cap of 50")
		return
	}
	id := s.identity(r)
	out := make([]BatchLoadEnvelope, 0, len(req.IDs))
	for _, artifactID := range req.IDs {
		out = append(out, s.loadOneForBatch(r.Context(), id, artifactID, req.VersionPins[artifactID], req.SessionID))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) loadOneForBatch(ctx context.Context, id layer.Identity, artifactID, version, sessionID string) BatchLoadEnvelope {
	res, err := s.core.LoadArtifact(ctx, id, artifactID, core.LoadArtifactOptions{
		Version:   version,
		SessionID: sessionID,
	})
	if err != nil {
		return BatchLoadEnvelope{
			ID:     artifactID,
			Status: "error",
			Error:  batchLoadError(err),
		}
	}
	env := BatchLoadEnvelope{
		ID:                 res.ID,
		Status:             "ok",
		Type:               res.Type,
		Version:            res.Version,
		ContentHash:        res.ContentHash,
		ManifestBody:       res.ManifestBody,
		Frontmatter:        string(res.Frontmatter),
		Deprecated:         res.Deprecated,
		ReplacedBy:         res.ReplacedBy,
		DeprecationWarning: res.DeprecationWarning,
	}
	// §7.6.2: bundled resources travel as presigned URLs so the batch
	// response body stays small. When no object store is configured the
	// references are omitted (the data plane is unavailable); the manifest
	// still loads.
	if s.objectStore != nil {
		for _, ref := range res.Resources {
			url, err := s.objectStore.Presign(ctx, resourceKey(ref), s.presignTTL)
			if err != nil {
				return BatchLoadEnvelope{
					ID:     artifactID,
					Status: "error",
					Error:  errorEnvelopeFor(err),
				}
			}
			env.Resources = append(env.Resources, BatchResource{
				Path:         ref.Path,
				PresignedURL: url,
				ContentHash:  ref.ContentHash,
			})
		}
	}
	return env
}

// batchLoadError maps a per-item load failure to the §7.6.2 envelope. A
// not-found or visibility-filtered artifact both surface as
// visibility.denied (the spec's documented per-item code) so the caller
// cannot tell whether the artifact exists in some hidden layer; §7.6.2
// forbids that existence leak. Other errors pass through errorEnvelopeFor.
// spec: §7.6.2.
func batchLoadError(err error) *ErrorResponse {
	if errors.Is(err, core.ErrNotFound) {
		e := &ErrorResponse{Code: "visibility.denied", Message: "artifact not visible to caller"}
		enrichEnvelope(e)
		return e
	}
	return errorEnvelopeFor(err)
}

// errorEnvelopeFor maps a core error to the §6.10 envelope. The
// retryable flag and suggested_action are assigned by enrichEnvelope from
// the per-code registry so per-item batch errors carry the same envelope
// fields as the top-level writeError path (F-6.10.4).
func errorEnvelopeFor(err error) *ErrorResponse {
	var e *ErrorResponse
	switch {
	case errors.Is(err, core.ErrNotFound):
		e = &ErrorResponse{Code: "registry.not_found", Message: err.Error()}
	case errors.Is(err, core.ErrUnavailable):
		e = &ErrorResponse{Code: "registry.unavailable", Message: err.Error()}
	case errors.Is(err, core.ErrInvalidArgument):
		e = &ErrorResponse{Code: "registry.invalid_argument", Message: err.Error()}
	default:
		e = &ErrorResponse{Code: "registry.unknown", Message: err.Error()}
	}
	enrichEnvelope(e)
	return e
}
