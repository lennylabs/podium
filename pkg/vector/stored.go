package vector

import (
	"context"
	"errors"
	"math"
)

// ErrNotFound is returned by VectorReader.StoredVector when the tenant holds
// no row for the requested (artifact, version).
var ErrNotFound = errors.New("vector: row not found")

// RowKey names one stored row of a tenant by artifact identifier and version.
type RowKey struct {
	ArtifactID string
	Version    string
}

// VectorReader is the optional capability a storage-only backend implements
// to return the vector it stores for one row. The §7.3.1 semantic overlap
// check queries the index with an artifact's stored vector, so it adds no
// second embedding of the catalog. A self-embedding backend computes the
// vector from text server-side and the check queries it with the projection
// text instead, so the capability is not consulted for one.
type VectorReader interface {
	// StoredVector returns the stored vector of the (tenant, id, version) row
	// and the embedding model the row is tagged with. modelID is empty on a
	// backend that records no model per row (§4.7 model versioning). A
	// missing row returns ErrNotFound.
	StoredVector(ctx context.Context, tenantID, artifactID, version string) (vec []float32, modelID string, err error)
}

// CosineScorer is the optional capability of a collocated backend (memory,
// pgvector, sqlite-vec) that computes the exact cosine similarity between a
// vector and stored rows. The §7.3.1 semantic overlap similarity is the
// cosine similarity of two projection vectors whatever distance the backend's
// nearest-neighbour index orders by: sqlite-vec's vec0 table orders by L2
// distance, so one minus its reported distance is not a cosine similarity.
type CosineScorer interface {
	// CosineSimilarities returns the cosine similarity between vec and the
	// stored vector of each row the tenant holds. A row the tenant does not
	// hold, or whose stored dimension differs from vec, is absent from the
	// result.
	CosineSimilarities(ctx context.Context, tenantID string, vec []float32, rows []RowKey) (map[RowKey]float64, error)
}

// HostedModeler is the optional capability of a self-embedding backend that
// names the hosted model it embeds text with. The §7.3.1 overlap records carry
// the embedding model identifier, and on a self-embedding backend with no
// local embedder that identifier is the hosted model (§13.12).
type HostedModeler interface {
	// HostedModel returns the configured inference model or vectorizer
	// module, or the empty string when the backend is storage-only.
	HostedModel() string
}

// cosineSimilarity returns the cosine similarity of a and b, computed in
// float64. Vectors of different length, empty vectors, and zero-magnitude
// vectors report 0.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		af, bf := float64(a[i]), float64(b[i])
		dot += af * bf
		na += af * af
		nb += bf * bf
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// StoredVector returns a copy of the stored vector for (tenant, id, version).
func (m *Memory) StoredVector(_ context.Context, tenantID, artifactID, version string) ([]float32, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	row, ok := m.rows[tenantID][memKey(artifactID, version)]
	if !ok {
		return nil, "", ErrNotFound
	}
	cp := make([]float32, len(row.vec))
	copy(cp, row.vec)
	return cp, row.model, nil
}

// CosineSimilarities computes the cosine similarity of vec against each named
// row's stored vector.
func (m *Memory) CosineSimilarities(_ context.Context, tenantID string, vec []float32, rows []RowKey) (map[RowKey]float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	bucket := m.rows[tenantID]
	out := make(map[RowKey]float64, len(rows))
	for _, k := range rows {
		row, ok := bucket[memKey(k.ArtifactID, k.Version)]
		if !ok || len(row.vec) != len(vec) {
			continue
		}
		out[k] = cosineSimilarity(vec, row.vec)
	}
	return out, nil
}
