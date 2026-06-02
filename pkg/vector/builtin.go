package vector

import "fmt"

// BackendConfig carries the resolved configuration for the built-in §13.12
// vector backends. It is the typed input to OpenBuiltin so the registry
// bootstrap and the MCP workspace-overlay index construct the same backends
// from their respective env vars without duplicating the per-backend
// constructor wiring.
type BackendConfig struct {
	// pgvector
	PgVectorDSN string
	// sqlite-vec
	SQLitePath string
	// pinecone
	PineconeKey   string
	PineconeHost  string
	PineconeIndex string
	PineconeNS    string
	// weaviate-cloud
	WeaviateURL  string
	WeaviateKey  string
	WeaviateColl string
	// qdrant-cloud
	QdrantURL  string
	QdrantKey  string
	QdrantColl string
	// InferenceModel is the §13.12 self-embedding model shared by the
	// managed backends (Pinecone Integrated Inference, Weaviate vectorizer,
	// Qdrant Cloud Inference). Empty leaves storage-only mode.
	InferenceModel string
}

// OpenBuiltin constructs one of the built-in §13.12 vector backends from the
// resolved configuration and the embedding dimension. It is the single
// factory behind both the registry bootstrap (internal/serverboot) and the
// MCP workspace-overlay index (cmd/podium-mcp, spec §6.4.1), so an operator
// selects sqlite-vec, pgvector, pinecone, weaviate-cloud, or qdrant-cloud the
// same way in either deployment.
//
// It returns (nil, nil) for "" and "none" so the caller can leave the layer
// disabled, and an error for an unknown id. The "memory" backend is a
// non-durable test affordance; callers that want to warn about its
// volatility do so before calling OpenBuiltin.
func OpenBuiltin(id string, cfg BackendConfig, dim int) (Provider, error) {
	switch id {
	case "", "none":
		return nil, nil
	case "memory":
		return NewMemory(dim), nil
	case "pgvector":
		if cfg.PgVectorDSN == "" {
			return nil, fmt.Errorf("PODIUM_PGVECTOR_DSN or PODIUM_POSTGRES_DSN required for pgvector")
		}
		return OpenPgVector(PgVectorConfig{DSN: cfg.PgVectorDSN, Dimensions: dim})
	case "sqlite-vec":
		path := cfg.SQLitePath
		if path == "" {
			path = ":memory:"
		}
		return OpenSQLiteVec(SQLiteVecConfig{Path: path, Dimensions: dim})
	case "pinecone":
		host := cfg.PineconeHost
		if host == "" && cfg.PineconeIndex != "" {
			// §13.12: PODIUM_PINECONE_INDEX is auto-resolved to a host for
			// serverless. Point the operator at the host URL rather than
			// dialing the Pinecone control plane here.
			return nil, fmt.Errorf(
				"PODIUM_PINECONE_INDEX=%q set but PODIUM_PINECONE_HOST is required for serverless; supply the index host URL", cfg.PineconeIndex)
		}
		return NewPinecone(PineconeConfig{
			APIKey: cfg.PineconeKey, Host: host,
			Namespace: cfg.PineconeNS, Dimensions: dim,
			InferenceModel: cfg.InferenceModel,
		})
	case "weaviate-cloud":
		return NewWeaviate(WeaviateConfig{
			URL: cfg.WeaviateURL, APIKey: cfg.WeaviateKey,
			Collection: cfg.WeaviateColl, Dimensions: dim,
			Vectorizer: cfg.InferenceModel,
		})
	case "qdrant-cloud":
		return NewQdrant(QdrantConfig{
			URL: cfg.QdrantURL, APIKey: cfg.QdrantKey,
			Collection: cfg.QdrantColl, Dimensions: dim,
			InferenceModel: cfg.InferenceModel,
		})
	}
	return nil, fmt.Errorf("unknown vector backend: %s", id)
}
