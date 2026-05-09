package vector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	Client    *http.Client
}

// PineconeConfig is the constructor input.
type PineconeConfig struct {
	APIKey     string
	Host       string
	Namespace  string
	Dimensions int
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
	if cfg.Dimensions <= 0 {
		return nil, fmt.Errorf("vector.pinecone: Dimensions must be > 0")
	}
	return &Pinecone{
		APIKey:    cfg.APIKey,
		Host:      strings.TrimRight(cfg.Host, "/"),
		Namespace: cfg.Namespace,
		Dim:       cfg.Dimensions,
	}, nil
}

// ID returns "pinecone".
func (*Pinecone) ID() string { return "pinecone" }

// Dimensions returns the configured dimension.
func (p *Pinecone) Dimensions() int { return p.Dim }

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
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.Host+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Api-Key", p.APIKey)
	req.Header.Set("Content-Type", "application/json")
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
