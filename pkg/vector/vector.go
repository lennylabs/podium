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

// TextVectorizer is the optional capability a vector backend implements
// when it embeds raw text server-side: Pinecone Integrated Inference, a
// Weaviate vectorizer module, or Qdrant Cloud Inference (§13.12). When a
// backend self-embeds, the registry sends text through PutText / QueryText
// and no separate embedding.Provider is required; the hosted model
// determines the vector dimension, so Dimensions reports 0 and the
// dimension checks are skipped on the text path.
type TextVectorizer interface {
	// SelfEmbeds reports whether server-side embedding is active (an
	// inference-model / vectorizer name is configured). A backend that
	// implements this interface but is configured storage-only returns
	// false, and the registry uses the precomputed-vector path instead.
	SelfEmbeds() bool
	// PutText upserts the (tenant, id, version) row from raw text; the
	// backend embeds it with the configured hosted model.
	PutText(ctx context.Context, tenantID, artifactID, version, text string) error
	// QueryText embeds text server-side and returns the top-K nearest rows
	// scoped to the tenant, ordered by ascending Distance.
	QueryText(ctx context.Context, tenantID, text string, topK int) ([]Match, error)
}

// SelfEmbeds reports whether v is a TextVectorizer with server-side
// embedding active. Nil-safe: a nil provider or a backend that does not
// self-embed returns false.
func SelfEmbeds(v Provider) bool {
	tv, ok := v.(TextVectorizer)
	return ok && tv.SelfEmbeds()
}

// ModelVersioned is the optional §4.7 "Model versioning and re-embedding"
// capability a backend implements to record the embedding model per row and
// restrict queries to the currently-configured model. The collocated backends
// (memory, sqlite-vec, pgvector) implement it so a model switch runs as an
// online re-embed: PutModel tags each new row with the current model id,
// QueryModel returns only current-model rows (plus legacy untagged rows, so an
// upgrade with no model change keeps serving), and PurgeModelExcept drops the
// stale model's rows once the re-embed completes. A managed backend that
// re-indexes per model need not implement it; the registry falls back to the
// plain Put / Query.
type ModelVersioned interface {
	// PutModel upserts the (tenant, id, version) row and tags it with modelID.
	PutModel(ctx context.Context, tenantID, artifactID, version string, embedding []float32, modelID string) error
	// QueryModel returns the top-K nearest rows whose model_id is modelID or is
	// empty (a legacy row ingested before model versioning), so a query during
	// re-embed never scores a stale model's vectors. An empty modelID matches
	// every row (no restriction).
	QueryModel(ctx context.Context, tenantID string, vec []float32, topK int, modelID string) ([]Match, error)
	// PurgeModelExcept removes rows whose model_id differs from modelID,
	// returning the count purged. Called after a full re-embed completes to
	// drop the stale model's vectors. An empty modelID purges nothing.
	PurgeModelExcept(ctx context.Context, tenantID, modelID string) (int, error)
}

// ModelVersionedOf returns v as a ModelVersioned backend when it implements the
// capability. Nil-safe.
func ModelVersionedOf(v Provider) (ModelVersioned, bool) {
	mv, ok := v.(ModelVersioned)
	return mv, ok
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
