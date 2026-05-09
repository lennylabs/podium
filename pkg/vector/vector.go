// Package vector implements the §4.7 RegistrySearchProvider SPI.
// Implementations persist (tenant_id, artifact_id, version,
// embedding) tuples and answer top-K cosine-similarity queries
// against a per-call query vector.
//
// Six built-in backends ship in this package:
//
//   - Memory:   in-process, used by tests and small standalone
//     deployments without a SQLite/Postgres-backed metadata store.
//   - PgVector: Postgres + pgvector extension. The default for the
//     standard deployment; collocated with the metadata store.
//   - SQLiteVec: SQLite + sqlite-vec extension. The default for
//     standalone; collocated with the metadata store.
//   - Pinecone:  managed cloud (REST API).
//   - Weaviate:  managed cloud (REST API).
//   - Qdrant:    managed cloud (REST API).
//
// Selection happens at the operator level via PODIUM_VECTOR_BACKEND.
// Switching backends requires `podium admin reembed --all` because
// vectors are not portable across stores (cosine distance assumes
// the same embedding model + dimension on both ends).
package vector

import (
	"context"
	"errors"
)

// Errors returned by Provider implementations.
var (
	// ErrUnreachable wraps a transient backend failure.
	ErrUnreachable = errors.New("vector: backend unreachable")
	// ErrDimensionMismatch signals that the supplied vector
	// dimension does not match the backend's configured dimension.
	// Callers re-embed via the configured EmbeddingProvider when
	// this happens.
	ErrDimensionMismatch = errors.New("vector: dimension mismatch")
	// ErrInvalidArgument wraps malformed input (empty tenant id,
	// negative top_k, etc).
	ErrInvalidArgument = errors.New("vector: invalid argument")
)

// Match is one (artifact_id, version, distance) result returned by
// Query. Lower Distance is more similar; the conventional metric is
// cosine distance (1 - cosine similarity), bounded to [0, 2].
type Match struct {
	ArtifactID string
	Version    string
	Distance   float32
}

// Provider is the SPI implementations satisfy. All methods take a
// context first per §9.3 and are safe for concurrent use.
type Provider interface {
	// ID returns the backend identifier ("memory" | "pgvector" |
	// "sqlite-vec" | "pinecone" | "weaviate-cloud" | "qdrant-cloud").
	ID() string
	// Dimensions returns the configured vector dimension. Put and
	// Query reject vectors that don't match.
	Dimensions() int
	// Put inserts or updates the embedding for the (tenant, id,
	// version) tuple. Re-Putting the same tuple with a new vector
	// is atomic per row (search continues to return the prior
	// vector until the upsert lands).
	Put(ctx context.Context, tenantID, artifactID, version string, embedding []float32) error
	// Query returns the top-K nearest neighbours for vec, scoped to
	// the tenant. Results are ordered by ascending Distance.
	Query(ctx context.Context, tenantID string, vec []float32, topK int) ([]Match, error)
	// Delete removes the vector for the (tenant, id, version)
	// tuple. Missing rows are a no-op.
	Delete(ctx context.Context, tenantID, artifactID, version string) error
	// Close releases backend resources (DB pool, HTTP keep-alives,
	// etc). Safe to call more than once.
	Close() error
}

// validateDim returns ErrDimensionMismatch when v's length doesn't
// match the configured dimension. Backends call this on every Put /
// Query so the error surface is consistent.
func validateDim(v []float32, dim int) error {
	if len(v) != dim {
		return ErrDimensionMismatch
	}
	return nil
}
