package vector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// The managed backends implement VectorReader for their storage-only mode, so
// the §7.3.1 semantic overlap check queries them with an artifact's stored
// vector rather than embedding its projection a second time. They record no
// model per row (§4.7), so the model they report is empty.

// StoredVector fetches the (tenant, id, version) vector through
// GET /vectors/fetch in the tenant's namespace.
func (p *Pinecone) StoredVector(ctx context.Context, tenantID, artifactID, version string) ([]float32, string, error) {
	id := artifactID + "@" + version
	q := url.Values{}
	q.Set("ids", id)
	q.Set("namespace", p.namespaceFor(tenantID))
	body, err := p.doRaw(ctx, http.MethodGet, "/vectors/fetch?"+q.Encode(), "", nil)
	if err != nil {
		return nil, "", err
	}
	var parsed struct {
		Vectors map[string]struct {
			Values []float32 `json:"values"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, "", fmt.Errorf("pinecone: decode fetch: %w", err)
	}
	row, ok := parsed.Vectors[id]
	if !ok || len(row.Values) == 0 {
		return nil, "", ErrNotFound
	}
	return row.Values, "", nil
}

// StoredVector retrieves the (tenant, id, version) point with its vector
// through POST /collections/<col>/points.
func (q *Qdrant) StoredVector(ctx context.Context, tenantID, artifactID, version string) ([]float32, string, error) {
	body := map[string]any{
		"ids":          []uint64{q.pointID(tenantID, artifactID, version)},
		"with_vector":  true,
		"with_payload": false,
	}
	respBody, err := q.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/collections/%s/points", q.Collection), body)
	if err != nil {
		return nil, "", err
	}
	var parsed struct {
		Result []struct {
			Vector []float32 `json:"vector"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, "", fmt.Errorf("qdrant: decode retrieve: %w", err)
	}
	if len(parsed.Result) == 0 || len(parsed.Result[0].Vector) == 0 {
		return nil, "", ErrNotFound
	}
	return parsed.Result[0].Vector, "", nil
}

// StoredVector reads the (tenant, id, version) object with its vector through
// GET /v1/objects/<class>/<id>?include=vector. A missing object answers 404.
func (w *Weaviate) StoredVector(ctx context.Context, tenantID, artifactID, version string) ([]float32, string, error) {
	id := w.objectID(tenantID, artifactID, version)
	status, respBody, err := w.doStatus(ctx, http.MethodGet,
		fmt.Sprintf("/v1/objects/%s/%s?include=vector", w.Collection, id), nil)
	if err != nil {
		if weaviateObjectMissing(status, respBody) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	var parsed struct {
		Vector []float32 `json:"vector"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, "", fmt.Errorf("weaviate: decode object: %w", err)
	}
	if len(parsed.Vector) == 0 {
		return nil, "", ErrNotFound
	}
	return parsed.Vector, "", nil
}

// HostedModel returns the Pinecone Integrated Inference model, or "" when the
// index is storage-only.
func (p *Pinecone) HostedModel() string { return p.InferenceModel }

// HostedModel returns the Qdrant Cloud Inference model, or "" when the
// collection is storage-only.
func (q *Qdrant) HostedModel() string { return q.InferenceModel }

// HostedModel returns the Weaviate vectorizer module, or "" when the class is
// storage-only.
func (w *Weaviate) HostedModel() string { return w.Vectorizer }
