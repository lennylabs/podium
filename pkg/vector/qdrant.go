package vector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
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
	// InferenceModel enables Qdrant Cloud Inference (§13.12
	// PODIUM_QDRANT_INFERENCE_MODEL). When set, points carry text + model
	// and Qdrant embeds them server-side; Dim is unused (0).
	InferenceModel string
	Client         *http.Client

	// tenantIndexMu guards tenantIndexDone, a per-process latch for the
	// best-effort creation of the tenant_id payload index. §4.7.1 isolation
	// filters every read by tenant_id, and Qdrant requires a keyword payload
	// index on a filtered field for the Cloud Inference query path; without it a
	// self-embedding query returns "Index required but not found for tenant_id".
	// The latch is set only after a successful create (which is idempotent), so a
	// transient failure on the first write retries on the next one rather than
	// permanently disabling indexing.
	tenantIndexMu   sync.Mutex
	tenantIndexDone bool
}

// QdrantConfig is the constructor input.
type QdrantConfig struct {
	URL        string
	APIKey     string
	Collection string
	Dimensions int
	// InferenceModel, when set, enables self-embedding (§13.12). The hosted
	// model determines the dimension, so Dimensions may be 0.
	InferenceModel string
}

// NewQdrant returns a configured Qdrant backend.
func NewQdrant(cfg QdrantConfig) (*Qdrant, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("vector.qdrant: URL is required")
	}
	if cfg.Collection == "" {
		return nil, fmt.Errorf("vector.qdrant: Collection is required")
	}
	// §13.12: with Cloud Inference the hosted model fixes the dimension, so
	// a self-embedding backend needs no local dimension.
	if cfg.Dimensions <= 0 && cfg.InferenceModel == "" {
		return nil, fmt.Errorf("vector.qdrant: Dimensions must be > 0")
	}
	return &Qdrant{
		URL:            strings.TrimRight(cfg.URL, "/"),
		APIKey:         cfg.APIKey,
		Collection:     cfg.Collection,
		Dim:            cfg.Dimensions,
		InferenceModel: cfg.InferenceModel,
	}, nil
}

// ID returns "qdrant-cloud".
func (*Qdrant) ID() string { return "qdrant-cloud" }

// Dimensions returns the configured dimension (0 in self-embedding mode).
func (q *Qdrant) Dimensions() int { return q.Dim }

// SelfEmbeds reports whether Cloud Inference is configured.
func (q *Qdrant) SelfEmbeds() bool { return q.InferenceModel != "" }

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

// ensureTenantIndex creates the tenant_id keyword payload index once per
// process. Qdrant rejects a filtered Cloud Inference query when the filtered
// field has no payload index ("Index required but not found for tenant_id"),
// and the index also speeds the storage-only tenant filter at scale. The PUT is
// idempotent: Qdrant treats a re-create of an existing index as a completed
// no-op. The success latch is set only after a nil-error create, so a transient
// outage during the first write retries on the next write rather than
// permanently disabling indexing. It runs on the write path (Put/PutText) so a
// read-only constructor stays network-free.
func (q *Qdrant) ensureTenantIndex(ctx context.Context) {
	q.tenantIndexMu.Lock()
	defer q.tenantIndexMu.Unlock()
	if q.tenantIndexDone {
		return
	}
	body := map[string]any{"field_name": "tenant_id", "field_schema": "keyword"}
	if _, err := q.doJSON(ctx, http.MethodPut,
		fmt.Sprintf("/collections/%s/index?wait=true", q.Collection), body); err == nil {
		q.tenantIndexDone = true
	}
}

// Put upserts via PUT /collections/<col>/points with a single point.
func (q *Qdrant) Put(ctx context.Context, tenantID, artifactID, version string, vec []float32) error {
	if tenantID == "" || artifactID == "" || version == "" {
		return ErrInvalidArgument
	}
	if err := validateDim(vec, q.Dim); err != nil {
		return err
	}
	q.ensureTenantIndex(ctx)
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

// PutText upserts a point whose vector is a {text, model} document, so
// Qdrant Cloud Inference embeds it server-side. spec: §13.12
// PODIUM_QDRANT_INFERENCE_MODEL.
func (q *Qdrant) PutText(ctx context.Context, tenantID, artifactID, version, text string) error {
	if tenantID == "" || artifactID == "" || version == "" {
		return ErrInvalidArgument
	}
	q.ensureTenantIndex(ctx)
	body := map[string]any{
		"points": []map[string]any{{
			"id":     q.pointID(tenantID, artifactID, version),
			"vector": map[string]any{"text": text, "model": q.InferenceModel},
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

// QueryText runs the Query API with a {text, model} document so Qdrant
// embeds the query server-side. spec: §13.12.
func (q *Qdrant) QueryText(ctx context.Context, tenantID, text string, topK int) ([]Match, error) {
	if tenantID == "" || topK < 1 {
		return nil, ErrInvalidArgument
	}
	body := map[string]any{
		"query": map[string]any{"text": text, "model": q.InferenceModel},
		"limit": topK,
		"filter": map[string]any{
			"must": []map[string]any{{
				"key":   "tenant_id",
				"match": map[string]any{"value": tenantID},
			}},
		},
		"with_payload": true,
	}
	respBody, err := q.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/collections/%s/points/query", q.Collection), body)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Result struct {
			Points []struct {
				Score   float64 `json:"score"`
				Payload struct {
					ArtifactID string `json:"artifact_id"`
					Version    string `json:"version"`
				} `json:"payload"`
			} `json:"points"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("qdrant: decode: %w", err)
	}
	out := make([]Match, 0, len(parsed.Result.Points))
	for _, r := range parsed.Result.Points {
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
