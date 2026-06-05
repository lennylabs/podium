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
	// Vectorizer enables Weaviate self-embedding (§13.12
	// PODIUM_WEAVIATE_VECTORIZER). When set, objects are inserted without a
	// vector and the module embeds the text property; Dim is unused (0).
	Vectorizer string
	Client     *http.Client
}

// WeaviateConfig is the constructor input.
type WeaviateConfig struct {
	URL        string
	APIKey     string
	Collection string
	Dimensions int
	// Vectorizer, when set, enables self-embedding (§13.12). The module
	// determines the dimension, so Dimensions may be 0.
	Vectorizer string
}

// NewWeaviate returns a configured Weaviate backend.
func NewWeaviate(cfg WeaviateConfig) (*Weaviate, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("vector.weaviate: URL is required")
	}
	if cfg.Collection == "" {
		return nil, fmt.Errorf("vector.weaviate: Collection is required")
	}
	// §13.12: with a vectorizer module the dimension is fixed server-side,
	// so a self-embedding backend needs no local dimension.
	if cfg.Dimensions <= 0 && cfg.Vectorizer == "" {
		return nil, fmt.Errorf("vector.weaviate: Dimensions must be > 0")
	}
	return &Weaviate{
		URL:        strings.TrimRight(cfg.URL, "/"),
		APIKey:     cfg.APIKey,
		Collection: cfg.Collection,
		Dim:        cfg.Dimensions,
		Vectorizer: cfg.Vectorizer,
	}, nil
}

// ID returns "weaviate-cloud".
func (*Weaviate) ID() string { return "weaviate-cloud" }

// Dimensions returns the configured dimension (0 in self-embedding mode).
func (w *Weaviate) Dimensions() int { return w.Dim }

// SelfEmbeds reports whether a vectorizer module is configured.
func (w *Weaviate) SelfEmbeds() bool { return w.Vectorizer != "" }

func (w *Weaviate) client() *http.Client {
	if w.Client != nil {
		return w.Client
	}
	return http.DefaultClient
}

func (w *Weaviate) doJSON(ctx context.Context, method, path string, body any) ([]byte, error) {
	_, respBody, err := w.doStatus(ctx, method, path, body)
	return respBody, err
}

// doStatus performs the request and returns the HTTP status code alongside the
// body and a non-nil error on any non-2xx. The status is exposed so callers
// (the upsert path) can branch on a specific code such as 422 "already exists"
// without parsing the error string.
func (w *Weaviate) doStatus(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, w.URL+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	if w.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+w.APIKey)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// §13.12 self-embedding: the hosted text2vec-weaviate vectorizer routes the
	// embedding call back through the caller's Weaviate Cloud cluster and rejects
	// a write or nearText query that omits the cluster URL ("no cluster URL found
	// in request header: X-Weaviate-Cluster-Url"). The header is the cluster base
	// URL. Other vectorizer modules and the storage-only path ignore it.
	if w.Vectorizer == "text2vec-weaviate" {
		req.Header.Set("X-Weaviate-Cluster-Url", w.URL)
	}
	resp, err := w.client().Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, respBody, fmt.Errorf("%w: HTTP %d: %s", ErrUnreachable, resp.StatusCode, string(respBody))
	}
	return resp.StatusCode, respBody, nil
}

// objectID returns a deterministic UUID-like identifier from the
// (tenant, id, version) tuple. Weaviate REST treats object IDs as
// opaque UUIDs; we synthesize one so upserts target the right row.
func (w *Weaviate) objectID(tenantID, artifactID, version string) string {
	return weaviateUUID(tenantID + "/" + artifactID + "@" + version)
}

// Put upserts the (tenant, id, version) object with a precomputed vector.
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
	return w.upsertObject(ctx, id, body)
}

// upsertObject replaces an object via PUT /v1/objects/<class>/<id> and, when the
// object does not yet exist, creates it via POST /v1/objects. Weaviate's PUT is
// update-only and reports the object missing ("no object with id", surfaced as
// 404 or a 500 naming the id) on the first write to an empty collection; the
// POST fallback makes the write a true upsert on both the create and the replace
// path. PUT is attempted first so a re-ingest replaces the prior object in one
// round-trip (the common steady-state path), matching §4.7 (a re-ingest replaces
// the prior embedding; search returns the prior vector until the replace lands).
func (w *Weaviate) upsertObject(ctx context.Context, id string, body map[string]any) error {
	status, respBody, err := w.doStatus(ctx, http.MethodPut,
		fmt.Sprintf("/v1/objects/%s/%s", w.Collection, id), body)
	if err == nil {
		return nil
	}
	if !weaviateObjectMissing(status, respBody) {
		return err
	}
	// The object does not exist yet: create it.
	_, _, perr := w.doStatus(ctx, http.MethodPost, "/v1/objects", body)
	return perr
}

// weaviateObjectMissing reports whether a PUT-update response indicates the
// target object does not exist, so the upsert should fall back to a create.
// Weaviate returns 404 for a missing object on some versions and a 500 whose
// body names the id ("no object with id '<uuid>'") on others, so both are
// treated as missing.
func weaviateObjectMissing(status int, body []byte) bool {
	if status == http.StatusNotFound {
		return true
	}
	return status == http.StatusInternalServerError && bytes.Contains(body, []byte("no object with id"))
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
	near := fmt.Sprintf("nearVector: { vector: %s }", string(vecJSON))
	return w.graphQLGet(ctx, near, tenantID, topK)
}

// graphQLGet builds and runs a GraphQL Get over the collection with the
// supplied near-operator clause (nearVector for the precomputed-vector path,
// nearText for the self-embedding path), scoped to the tenant, and unpacks
// the rows into Match records.
func (w *Weaviate) graphQLGet(ctx context.Context, nearClause, tenantID string, topK int) ([]Match, error) {
	query := fmt.Sprintf(`{
		Get {
			%s(
				%s
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
	}`, w.Collection, nearClause, topK, tenantID)

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

// PutText upserts an object carrying the text in the vectorized `content`
// property and no explicit vector, so the configured vectorizer module
// embeds it server-side. spec: §13.12 PODIUM_WEAVIATE_VECTORIZER.
func (w *Weaviate) PutText(ctx context.Context, tenantID, artifactID, version, text string) error {
	if tenantID == "" || artifactID == "" || version == "" {
		return ErrInvalidArgument
	}
	id := w.objectID(tenantID, artifactID, version)
	body := map[string]any{
		"class": w.Collection,
		"id":    id,
		"properties": map[string]any{
			"content":    text,
			"tenantId":   tenantID,
			"artifactId": artifactID,
			"version":    version,
		},
	}
	return w.upsertObject(ctx, id, body)
}

// QueryText runs a nearText GraphQL search so the vectorizer module embeds
// the query server-side. spec: §13.12.
func (w *Weaviate) QueryText(ctx context.Context, tenantID, text string, topK int) ([]Match, error) {
	if tenantID == "" || topK < 1 {
		return nil, ErrInvalidArgument
	}
	conceptJSON, _ := json.Marshal([]string{text})
	near := fmt.Sprintf("nearText: { concepts: %s }", string(conceptJSON))
	return w.graphQLGet(ctx, near, tenantID, topK)
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
