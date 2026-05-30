package vector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Pinecone is the §4.7 Pinecone vector backend. Uses Pinecone's
// REST data plane (the gRPC plane is faster but adds a heavy dep;
// REST suffices for Podium's QPS).
//
// Per-tenant isolation uses Pinecone namespaces: each tenant gets
// its own namespace within a single Pinecone index. Operators size
// the index for their fleet (Pinecone's serverless tier scales
// transparently).
type Pinecone struct {
	APIKey    string
	Host      string // e.g. https://my-index-xxxxxxx.svc.aped-4627-b74a.pinecone.io
	Namespace string // optional global prefix; combined with tenant
	Dim       int
	// InferenceModel enables Pinecone Integrated Inference (§13.12
	// PODIUM_PINECONE_INFERENCE_MODEL). When set, the backend embeds raw
	// text server-side via the records API and Dim is unused (0).
	InferenceModel string
	Client         *http.Client
}

// PineconeConfig is the constructor input.
type PineconeConfig struct {
	APIKey     string
	Host       string
	Namespace  string
	Dimensions int
	// InferenceModel, when set, enables self-embedding (§13.12). The hosted
	// model determines the dimension, so Dimensions may be 0.
	InferenceModel string
}

// NewPinecone returns a configured Pinecone backend. Validation
// happens lazily on first call so a misconfigured Host fails on the
// first Put rather than at startup.
func NewPinecone(cfg PineconeConfig) (*Pinecone, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("vector.pinecone: APIKey is required")
	}
	if cfg.Host == "" {
		return nil, fmt.Errorf("vector.pinecone: Host is required")
	}
	// §13.12: with Integrated Inference the hosted model fixes the
	// dimension, so a self-embedding backend needs no local dimension.
	if cfg.Dimensions <= 0 && cfg.InferenceModel == "" {
		return nil, fmt.Errorf("vector.pinecone: Dimensions must be > 0")
	}
	return &Pinecone{
		APIKey:         cfg.APIKey,
		Host:           strings.TrimRight(cfg.Host, "/"),
		Namespace:      cfg.Namespace,
		Dim:            cfg.Dimensions,
		InferenceModel: cfg.InferenceModel,
	}, nil
}

// ID returns "pinecone".
func (*Pinecone) ID() string { return "pinecone" }

// Dimensions returns the configured dimension (0 in self-embedding mode).
func (p *Pinecone) Dimensions() int { return p.Dim }

// SelfEmbeds reports whether Integrated Inference is configured.
func (p *Pinecone) SelfEmbeds() bool { return p.InferenceModel != "" }

// namespaceFor combines the optional global Namespace prefix with
// the per-tenant tag. Pinecone treats namespaces as opaque strings.
func (p *Pinecone) namespaceFor(tenantID string) string {
	if p.Namespace == "" {
		return tenantID
	}
	return p.Namespace + "_" + tenantID
}

func (p *Pinecone) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}

func (p *Pinecone) doJSON(ctx context.Context, method, path string, body any) ([]byte, error) {
	var raw []byte
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		raw = buf
	}
	return p.doRaw(ctx, method, path, "application/json", raw)
}

// doRaw issues a request with a caller-supplied content type and body, used
// for both the JSON data plane and the NDJSON records (Integrated Inference)
// API. A nil body sends no content.
func (p *Pinecone) doRaw(ctx context.Context, method, path, contentType string, raw []byte) ([]byte, error) {
	var rdr io.Reader
	if raw != nil {
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.Host+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Api-Key", p.APIKey)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%w: HTTP %d: %s", ErrUnreachable, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// Put upserts a single vector under the (tenant, id, version) key.
// Pinecone supports batch upsert via /vectors/upsert; we send one
// vector per call for simplicity. The vector ID is the canonical
// "<artifactID>@<version>" form.
func (p *Pinecone) Put(ctx context.Context, tenantID, artifactID, version string, vec []float32) error {
	if tenantID == "" || artifactID == "" || version == "" {
		return ErrInvalidArgument
	}
	if err := validateDim(vec, p.Dim); err != nil {
		return err
	}
	body := map[string]any{
		"namespace": p.namespaceFor(tenantID),
		"vectors": []map[string]any{{
			"id":     artifactID + "@" + version,
			"values": vec,
			"metadata": map[string]string{
				"artifact_id": artifactID,
				"version":     version,
			},
		}},
	}
	_, err := p.doJSON(ctx, http.MethodPost, "/vectors/upsert", body)
	return err
}

// Query runs a /query against the tenant's namespace and unpacks the
// metadata back into Match records.
func (p *Pinecone) Query(ctx context.Context, tenantID string, vec []float32, topK int) ([]Match, error) {
	if tenantID == "" || topK < 1 {
		return nil, ErrInvalidArgument
	}
	if err := validateDim(vec, p.Dim); err != nil {
		return nil, err
	}
	body := map[string]any{
		"namespace":       p.namespaceFor(tenantID),
		"vector":          vec,
		"topK":            topK,
		"includeMetadata": true,
	}
	respBody, err := p.doJSON(ctx, http.MethodPost, "/query", body)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Matches []struct {
			ID       string  `json:"id"`
			Score    float64 `json:"score"`
			Metadata struct {
				ArtifactID string `json:"artifact_id"`
				Version    string `json:"version"`
			} `json:"metadata"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("pinecone: decode: %w", err)
	}
	out := make([]Match, 0, len(parsed.Matches))
	for _, m := range parsed.Matches {
		// Pinecone returns cosine *similarity* in [-1, 1]; convert
		// to cosine distance for SPI consistency.
		out = append(out, Match{
			ArtifactID: m.Metadata.ArtifactID,
			Version:    m.Metadata.Version,
			Distance:   float32(1 - m.Score),
		})
	}
	return out, nil
}

// PutText upserts raw text via the Integrated Inference records API
// (POST /records/namespaces/{ns}/upsert, NDJSON body). Pinecone embeds the
// `chunk_text` field with the index's hosted model. spec: §13.12
// PODIUM_PINECONE_INFERENCE_MODEL.
func (p *Pinecone) PutText(ctx context.Context, tenantID, artifactID, version, text string) error {
	if tenantID == "" || artifactID == "" || version == "" {
		return ErrInvalidArgument
	}
	rec := map[string]any{
		"_id":         artifactID + "@" + version,
		"chunk_text":  text,
		"artifact_id": artifactID,
		"version":     version,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	path := "/records/namespaces/" + url.PathEscape(p.namespaceFor(tenantID)) + "/upsert"
	_, err = p.doRaw(ctx, http.MethodPost, path, "application/x-ndjson", append(line, '\n'))
	return err
}

// QueryText runs a server-side embedded search via the records API
// (POST /records/namespaces/{ns}/search). spec: §13.12.
func (p *Pinecone) QueryText(ctx context.Context, tenantID, text string, topK int) ([]Match, error) {
	if tenantID == "" || topK < 1 {
		return nil, ErrInvalidArgument
	}
	body := map[string]any{
		"query":  map[string]any{"top_k": topK, "inputs": map[string]any{"text": text}},
		"fields": []string{"artifact_id", "version"},
	}
	path := "/records/namespaces/" + url.PathEscape(p.namespaceFor(tenantID)) + "/search"
	respBody, err := p.doJSON(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Result struct {
			Hits []struct {
				ID     string  `json:"_id"`
				Score  float64 `json:"_score"`
				Fields struct {
					ArtifactID string `json:"artifact_id"`
					Version    string `json:"version"`
				} `json:"fields"`
			} `json:"hits"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("pinecone: decode: %w", err)
	}
	out := make([]Match, 0, len(parsed.Result.Hits))
	for _, h := range parsed.Result.Hits {
		out = append(out, Match{
			ArtifactID: h.Fields.ArtifactID,
			Version:    h.Fields.Version,
			Distance:   float32(1 - h.Score),
		})
	}
	return out, nil
}

// Delete removes the (tenant, id, version) vector.
func (p *Pinecone) Delete(ctx context.Context, tenantID, artifactID, version string) error {
	body := map[string]any{
		"namespace": p.namespaceFor(tenantID),
		"ids":       []string{artifactID + "@" + version},
	}
	_, err := p.doJSON(ctx, http.MethodPost, "/vectors/delete", body)
	return err
}

// Close is a no-op for HTTP-backed providers.
func (*Pinecone) Close() error { return nil }
