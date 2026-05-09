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

// Qdrant is the §4.7 Qdrant Cloud vector backend. Uses Qdrant's
// REST API; the gRPC variant is faster but adds dependency weight.
//
// Per-tenant isolation uses a `tenant_id` payload filter combined
// with a single shared collection. Qdrant supports proper
// multi-tenant collections too; switching to the dedicated mode is
// a deployment-side configuration change.
type Qdrant struct {
	URL        string
	APIKey     string
	Collection string
	Dim        int
	Client     *http.Client
}

// QdrantConfig is the constructor input.
type QdrantConfig struct {
	URL        string
	APIKey     string
	Collection string
	Dimensions int
}

// NewQdrant returns a configured Qdrant backend.
func NewQdrant(cfg QdrantConfig) (*Qdrant, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("vector.qdrant: URL is required")
	}
	if cfg.Collection == "" {
		return nil, fmt.Errorf("vector.qdrant: Collection is required")
	}
	if cfg.Dimensions <= 0 {
		return nil, fmt.Errorf("vector.qdrant: Dimensions must be > 0")
	}
	return &Qdrant{
		URL:        strings.TrimRight(cfg.URL, "/"),
		APIKey:     cfg.APIKey,
		Collection: cfg.Collection,
		Dim:        cfg.Dimensions,
	}, nil
}

// ID returns "qdrant-cloud".
func (*Qdrant) ID() string { return "qdrant-cloud" }

// Dimensions returns the configured dimension.
func (q *Qdrant) Dimensions() int { return q.Dim }

func (q *Qdrant) client() *http.Client {
	if q.Client != nil {
		return q.Client
	}
	return http.DefaultClient
}

func (q *Qdrant) doJSON(ctx context.Context, method, path string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, q.URL+path, rdr)
	if err != nil {
		return nil, err
	}
	if q.APIKey != "" {
		req.Header.Set("api-key", q.APIKey)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := q.client().Do(req)
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

// pointID returns a deterministic numeric ID for the (tenant, id,
// version) tuple. Qdrant accepts unsigned 64-bit integers or
// UUID strings; we use the integer form since fnv64 is cheap.
func (q *Qdrant) pointID(tenantID, artifactID, version string) uint64 {
	return fnv64(tenantID + "/" + artifactID + "@" + version)
}

// Put upserts via PUT /collections/<col>/points with a single point.
func (q *Qdrant) Put(ctx context.Context, tenantID, artifactID, version string, vec []float32) error {
	if tenantID == "" || artifactID == "" || version == "" {
		return ErrInvalidArgument
	}
	if err := validateDim(vec, q.Dim); err != nil {
		return err
	}
	body := map[string]any{
		"points": []map[string]any{{
			"id":     q.pointID(tenantID, artifactID, version),
			"vector": vec,
			"payload": map[string]string{
				"tenant_id":   tenantID,
				"artifact_id": artifactID,
				"version":     version,
			},
		}},
	}
	_, err := q.doJSON(ctx, http.MethodPut,
		fmt.Sprintf("/collections/%s/points?wait=true", q.Collection), body)
	return err
}

// Query runs /points/search with a tenant_id filter.
func (q *Qdrant) Query(ctx context.Context, tenantID string, vec []float32, topK int) ([]Match, error) {
	if tenantID == "" || topK < 1 {
		return nil, ErrInvalidArgument
	}
	if err := validateDim(vec, q.Dim); err != nil {
		return nil, err
	}
	body := map[string]any{
		"vector": vec,
		"limit":  topK,
		"filter": map[string]any{
			"must": []map[string]any{{
				"key":   "tenant_id",
				"match": map[string]any{"value": tenantID},
			}},
		},
		"with_payload": true,
	}
	respBody, err := q.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/collections/%s/points/search", q.Collection), body)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Result []struct {
			Score   float64 `json:"score"`
			Payload struct {
				ArtifactID string `json:"artifact_id"`
				Version    string `json:"version"`
			} `json:"payload"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("qdrant: decode: %w", err)
	}
	out := make([]Match, 0, len(parsed.Result))
	for _, r := range parsed.Result {
		// Qdrant returns cosine *similarity* in [-1, 1]; convert
		// to cosine distance for SPI consistency.
		out = append(out, Match{
			ArtifactID: r.Payload.ArtifactID,
			Version:    r.Payload.Version,
			Distance:   float32(1 - r.Score),
		})
	}
	return out, nil
}

// Delete removes the point.
func (q *Qdrant) Delete(ctx context.Context, tenantID, artifactID, version string) error {
	body := map[string]any{
		"points": []uint64{q.pointID(tenantID, artifactID, version)},
	}
	_, err := q.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/collections/%s/points/delete?wait=true", q.Collection), body)
	return err
}

// Close is a no-op for HTTP-backed providers.
func (*Qdrant) Close() error { return nil }
