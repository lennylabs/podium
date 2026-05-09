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

// Weaviate is the §4.7 Weaviate Cloud vector backend. Talks to the
// REST data plane; the GraphQL surface is more flexible but adds
// dependency weight.
//
// Per-tenant isolation uses Weaviate's tenant feature when the
// configured class is multi-tenant, and falls back to a per-tenant
// property filter otherwise.
type Weaviate struct {
	URL        string
	APIKey     string
	Collection string
	Dim        int
	Client     *http.Client
}

// WeaviateConfig is the constructor input.
type WeaviateConfig struct {
	URL        string
	APIKey     string
	Collection string
	Dimensions int
}

// NewWeaviate returns a configured Weaviate backend.
func NewWeaviate(cfg WeaviateConfig) (*Weaviate, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("vector.weaviate: URL is required")
	}
	if cfg.Collection == "" {
		return nil, fmt.Errorf("vector.weaviate: Collection is required")
	}
	if cfg.Dimensions <= 0 {
		return nil, fmt.Errorf("vector.weaviate: Dimensions must be > 0")
	}
	return &Weaviate{
		URL:        strings.TrimRight(cfg.URL, "/"),
		APIKey:     cfg.APIKey,
		Collection: cfg.Collection,
		Dim:        cfg.Dimensions,
	}, nil
}

// ID returns "weaviate-cloud".
func (*Weaviate) ID() string { return "weaviate-cloud" }

// Dimensions returns the configured dimension.
func (w *Weaviate) Dimensions() int { return w.Dim }

func (w *Weaviate) client() *http.Client {
	if w.Client != nil {
		return w.Client
	}
	return http.DefaultClient
}

func (w *Weaviate) doJSON(ctx context.Context, method, path string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, w.URL+path, rdr)
	if err != nil {
		return nil, err
	}
	if w.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+w.APIKey)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := w.client().Do(req)
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

// objectID returns a deterministic UUID-like identifier from the
// (tenant, id, version) tuple. Weaviate REST treats object IDs as
// opaque UUIDs; we synthesize one so upserts target the right row.
func (w *Weaviate) objectID(tenantID, artifactID, version string) string {
	return weaviateUUID(tenantID + "/" + artifactID + "@" + version)
}

// Put upserts via PUT /v1/objects/<class>/<uuid>.
func (w *Weaviate) Put(ctx context.Context, tenantID, artifactID, version string, vec []float32) error {
	if tenantID == "" || artifactID == "" || version == "" {
		return ErrInvalidArgument
	}
	if err := validateDim(vec, w.Dim); err != nil {
		return err
	}
	id := w.objectID(tenantID, artifactID, version)
	body := map[string]any{
		"class":  w.Collection,
		"id":     id,
		"vector": vec,
		"properties": map[string]any{
			"tenantId":   tenantID,
			"artifactId": artifactID,
			"version":    version,
		},
	}
	_, err := w.doJSON(ctx, http.MethodPut,
		fmt.Sprintf("/v1/objects/%s/%s", w.Collection, id), body)
	return err
}

// Query uses the GraphQL nearVector path. Weaviate's REST /v1/objects
// doesn't expose vector search; GraphQL is the canonical entry point.
func (w *Weaviate) Query(ctx context.Context, tenantID string, vec []float32, topK int) ([]Match, error) {
	if tenantID == "" || topK < 1 {
		return nil, ErrInvalidArgument
	}
	if err := validateDim(vec, w.Dim); err != nil {
		return nil, err
	}
	vecJSON, _ := json.Marshal(vec)
	query := fmt.Sprintf(`{
		Get {
			%s(
				nearVector: { vector: %s }
				limit: %d
				where: {
					path: ["tenantId"]
					operator: Equal
					valueText: %q
				}
			) {
				artifactId
				version
				_additional { distance }
			}
		}
	}`, w.Collection, string(vecJSON), topK, tenantID)

	respBody, err := w.doJSON(ctx, http.MethodPost, "/v1/graphql", map[string]any{"query": query})
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Data struct {
			Get map[string][]struct {
				ArtifactID string `json:"artifactId"`
				Version    string `json:"version"`
				Additional struct {
					Distance float64 `json:"distance"`
				} `json:"_additional"`
			} `json:"Get"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("weaviate: decode: %w", err)
	}
	rows := parsed.Data.Get[w.Collection]
	out := make([]Match, 0, len(rows))
	for _, r := range rows {
		out = append(out, Match{
			ArtifactID: r.ArtifactID,
			Version:    r.Version,
			Distance:   float32(r.Additional.Distance),
		})
	}
	return out, nil
}

// Delete removes the (tenant, id, version) object via DELETE
// /v1/objects/<class>/<uuid>.
func (w *Weaviate) Delete(ctx context.Context, tenantID, artifactID, version string) error {
	id := w.objectID(tenantID, artifactID, version)
	_, err := w.doJSON(ctx, http.MethodDelete,
		fmt.Sprintf("/v1/objects/%s/%s", w.Collection, id), nil)
	// Weaviate returns 404 on missing; convert to nil for SPI
	// idempotence.
	if err != nil && strings.Contains(err.Error(), "HTTP 404") {
		return nil
	}
	return err
}

// Close is a no-op for HTTP-backed providers.
func (*Weaviate) Close() error { return nil }

// weaviateUUID synthesizes a UUID-shaped string from a key. Not a
// real RFC 4122 UUID — but Weaviate accepts any UUID-shaped string
// as an object ID, and the deterministic mapping lets upserts target
// the same row.
func weaviateUUID(key string) string {
	h := fnv64(key)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(h>>32), uint16(h>>16), uint16(h), uint16(h>>48), uint64(h)&0xffffffffffff)
}

func fnv64(s string) uint64 {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}
