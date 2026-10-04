package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// BatchLoadCap is the §7.6.2 hard cap on batch size. Larger
// requests fail with registry.invalid_argument.
const BatchLoadCap = 50

// BatchLoadRequest is the §7.6.2 request body of POST
// /v1/artifacts:batchLoad. The request selects no harness: the
// registry runs no harness adapter (§2.2). Like the other JSON
// handlers, the decoder ignores keys outside this struct, so a body
// from an older SDK that still sends `harness` loads normally.
//
// Spec: §7.6.2
type BatchLoadRequest struct {
	IDs         []string          `json:"ids"`
	SessionID   string            `json:"session_id,omitempty"`
	VersionPins map[string]string `json:"version_pins,omitempty"`
}

// BatchLoadEnvelope is one per-item response. Status is "ok" or
// "error"; on error the Error field carries the §6.10 envelope. An "ok"
// entry carries every §4.7.10 record field the single-load response serves,
// including sensitivity, so a client rebuilds the delivery record from the
// entry alone.
type BatchLoadEnvelope struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Type         string `json:"type,omitempty"`
	Version      string `json:"version,omitempty"`
	ContentHash  string `json:"content_hash,omitempty"`
	Sensitivity  string `json:"sensitivity,omitempty"` // Spec: §4.7.10, §7.6.2
	ManifestBody string `json:"manifest_body,omitempty"`
	Frontmatter  string `json:"frontmatter,omitempty"`
	// SkillRaw is the verbatim SKILL.md for a type: skill artifact (§4.3.4),
	// delivered so the SDK materializes the authored skill file byte-for-byte
	// rather than reconstructing it from frontmatter plus body. Empty for
	// non-skills.
	SkillRaw string `json:"skill_raw,omitempty"`
	// Resources carries every bundled resource as a §7.6.2 reference. A
	// resource the registry holds inline on the manifest record travels
	// inline, whatever its size and whether or not an object store is
	// configured. Every other resource travels as a presigned_url the SDK
	// fetches concurrently afterward, per the §7.6.2 wire example
	// {path, presigned_url, content_hash}.
	Resources          []BatchResource `json:"resources,omitempty"`
	Deprecated         bool            `json:"deprecated,omitempty"`
	ReplacedBy         string          `json:"replaced_by,omitempty"`
	DeprecationWarning string          `json:"deprecation_warning,omitempty"`
	// DeliveryHash and DeliverySignature are the §4.7.10 attestation of the
	// record this envelope delivers, composed and signed by the same code as
	// the single-load response, so both paths serve one digest per artifact.
	DeliveryHash      string `json:"delivery_hash,omitempty"`
	DeliverySignature string `json:"delivery_signature,omitempty"`
	// ArtifactRevision is the §4.7.10 ingest time of the served version,
	// framed into DeliveryHash. An error entry carries no record and omits it.
	ArtifactRevision string         `json:"artifact_revision,omitempty"`
	Error            *ErrorResponse `json:"error,omitempty"`
}

// BatchResource is one §7.6.2 bundled-resource reference in a batch
// envelope. A resource the registry holds inline on the manifest record
// travels in Inline (base64-encoded, with InlineBase64 set, when the payload
// is not valid UTF-8), including when a copy of it also exists in object
// storage. Every other resource travels as a presigned_url naming the object
// the §13.4 stored-row admission read.
type BatchResource struct {
	Path         string `json:"path"`
	PresignedURL string `json:"presigned_url,omitempty"`
	ContentHash  string `json:"content_hash"`
	Inline       string `json:"inline,omitempty"`
	InlineBase64 bool   `json:"inline_base64,omitempty"`
}

// handleBatchLoad answers POST /v1/artifacts:batchLoad per
// §7.6.2. Partial failure is the rule: items the caller cannot
// see come back as status=error with the §6.10 envelope; the
// batch HTTP status stays 200. Each item of a valid request is
// charged against the tenant's §4.7.8 materialization rate before
// any item loads. The items admitPrefix refuses come back as
// per-item quota.materialize_rate_exceeded errors, and a request
// rejected as a whole charges nothing.
//
// Spec: §7.6.2, §4.7.8
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
	admitted := admitPrefix(len(req.IDs), func() bool { return s.allowMaterialize(r.Context()) })
	out := make([]BatchLoadEnvelope, 0, len(req.IDs))
	for i, artifactID := range req.IDs {
		if i >= admitted {
			out = append(out, BatchLoadEnvelope{ID: artifactID, Status: "error", Error: materializeQuotaEnvelope()})
			continue
		}
		out = append(out, s.loadOneForBatch(r.Context(), id, artifactID, req.VersionPins[artifactID], req.SessionID))
	}
	writeJSON(w, http.StatusOK, out)
}

// admitPrefix charges n items in request order through allow and returns how
// many were admitted. Admission is a prefix: the first refusal ends charging,
// so allow is never called after it returns false, and that item and every
// later one are refused. Stopping at the first refusal keeps a token that
// refills mid-batch from admitting a later item after an earlier one was
// refused. n == 0 makes no call.
//
// The handler charges every item before it loads the first one, whatever each
// item's outcome turns out to be, so a visibility.denied item still costs a
// token and no charge is refunded. Charging ahead of the loads is safe because
// core.LoadArtifact draws no tokens, so the admitted count does not depend on
// the loads.
//
// Spec: §7.6.2, §4.7.8
func admitPrefix(n int, allow func() bool) int {
	for i := 0; i < n; i++ {
		if !allow() {
			return i
		}
	}
	return n
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
		Sensitivity:        res.Sensitivity,
		ManifestBody:       res.ManifestBody,
		Frontmatter:        string(res.Frontmatter),
		SkillRaw:           string(res.SkillRaw),
		Deprecated:         res.Deprecated,
		ReplacedBy:         res.ReplacedBy,
		DeprecationWarning: res.DeprecationWarning,
		ArtifactRevision:   res.ArtifactRevision,
	}
	// Spec: §4.7.10 — the batch entry carries the attestation the single-load
	// response carries for the same admitted result.
	deliveryHash, deliverySig, err := s.attestDelivery(ctx, res)
	if err != nil {
		return BatchLoadEnvelope{
			ID:     artifactID,
			Status: "error",
			Error:  errorEnvelopeFor(err),
		}
	}
	env.DeliveryHash, env.DeliverySignature = deliveryHash, deliverySig
	// Spec: §7.6.2, §13.4 — a resource the admitted row holds inline travels
	// inline from the bytes admission hashed, and is never presigned, because
	// admission read no object under its key. Every other resource travels as
	// a presigned link to the object admission read.
	for _, ref := range res.Resources {
		br, err := s.batchResource(ctx, ref)
		if err != nil {
			return BatchLoadEnvelope{
				ID:     artifactID,
				Status: "error",
				Error:  errorEnvelopeFor(err),
			}
		}
		env.Resources = append(env.Resources, br)
	}
	return env
}

// batchResource builds one admitted ref's §7.6.2 reference. A server with no
// object store answers an object-held ref with an error rather than serving
// bytes.
func (s *Server) batchResource(ctx context.Context, ref store.ResourceRef) (BatchResource, error) {
	br := BatchResource{Path: ref.Path, ContentHash: ref.ContentHash}
	if ref.Inline == nil {
		link, err := s.presignResource(ctx, ref)
		if err != nil {
			return BatchResource{}, err
		}
		br.PresignedURL = link.URL
		return br, nil
	}
	// §4.1/§7.2: a binary resource is base64-encoded so encoding/json does
	// not replace its non-UTF-8 bytes with U+FFFD.
	if utf8.Valid(ref.Inline) {
		br.Inline = string(ref.Inline)
	} else {
		br.Inline = base64.StdEncoding.EncodeToString(ref.Inline)
		br.InlineBase64 = true
	}
	return br, nil
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
// fields as the top-level writeError path.
func errorEnvelopeFor(err error) *ErrorResponse {
	var e *ErrorResponse
	switch {
	case errors.Is(err, core.ErrNotFound):
		e = &ErrorResponse{Code: "registry.not_found", Message: err.Error()}
	case errors.Is(err, core.ErrUnavailable):
		e = &ErrorResponse{Code: "registry.unavailable", Message: err.Error()}
	case errors.Is(err, core.ErrInvalidArgument):
		e = &ErrorResponse{Code: "registry.invalid_argument", Message: err.Error()}
	case errors.Is(err, core.ErrContentHashMismatch),
		errors.Is(err, core.ErrStoredSignatureMissing),
		errors.Is(err, core.ErrStoredSignatureInvalid):
		e = &ErrorResponse{Code: admissionCode(err), Message: err.Error()}
	default:
		e = &ErrorResponse{Code: "registry.unknown", Message: err.Error()}
	}
	enrichEnvelope(e)
	return e
}
