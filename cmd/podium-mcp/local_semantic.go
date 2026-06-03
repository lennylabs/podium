package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/podium/pkg/embedding"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/vector"
)

// localSemanticTimeout bounds the per-call build + query so a slow or
// unreachable embedding provider degrades the workspace-overlay search
// to BM25-only instead of hanging the host's search_artifacts call.
const localSemanticTimeout = 10 * time.Second

// localTenant is the synthetic tenant the overlay semantic index stores
// its vectors under. The workspace overlay is single-tenant by
// construction, so a constant key suffices.
const localTenant = "local-overlay"

// localSemanticIndex is the §9.1 LocalSearchProvider semantic backing
// for the workspace-overlay index (§6.4.1). It mirrors the registry-side
// RegistrySearchProvider path: an embedding.Provider projects overlay
// manifest text into vectors held by a vector.Provider, and a query
// embeds and retrieves the nearest overlay records. The index builds
// lazily on first use from the overlay snapshot and is reused for the
// bridge lifetime. Any embedding or vector-backend error degrades the
// stream to empty, leaving the BM25 default in place.
type localSemanticIndex struct {
	emb embedding.Provider
	vec vector.Provider

	mu       sync.Mutex
	built    bool
	buildErr error
	byID     map[string]filesystem.ArtifactRecord
}

func newLocalSemanticIndex(emb embedding.Provider, vec vector.Provider) *localSemanticIndex {
	return &localSemanticIndex{emb: emb, vec: vec, byID: map[string]filesystem.ArtifactRecord{}}
}

// search embeds query and returns the nearest overlay records as local
// search results, building the index on first call. Returns nil on any
// backend error so the caller keeps the BM25 stream. build and query
// share ctx so a slow provider cannot hang the call.
func (x *localSemanticIndex) search(ctx context.Context, records []filesystem.ArtifactRecord, query, typeFilter, scope string, tags []string, topK int) []localSearchResult {
	if x == nil || strings.TrimSpace(query) == "" {
		return nil
	}
	if err := x.build(ctx, records); err != nil {
		return nil
	}
	if topK <= 0 {
		topK = 10
	}
	qv, err := x.emb.Embed(ctx, []string{query})
	if err != nil || len(qv) == 0 {
		return nil
	}
	matches, err := x.vec.Query(ctx, localTenant, qv[0], topK*4)
	if err != nil {
		return nil
	}
	out := make([]localSearchResult, 0, len(matches))
	for _, m := range matches {
		rec, ok := x.recordFor(m.ArtifactID)
		if !ok || !overlayRecordMatches(rec, typeFilter, scope, tags) {
			continue
		}
		res := descriptorFor(rec)
		// Cosine distance is bounded to [0, 2]; convert to a positive
		// similarity score so a nearer match ranks higher.
		res.Score = float64(2 - m.Distance)
		out = append(out, res)
		if len(out) >= topK {
			break
		}
	}
	return out
}

func (x *localSemanticIndex) recordFor(id string) (filesystem.ArtifactRecord, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	rec, ok := x.byID[id]
	return rec, ok
}

// build embeds every overlay record once and stores its vector. The
// result (including any error) is memoized so the index is built at most
// once per bridge process.
func (x *localSemanticIndex) build(ctx context.Context, records []filesystem.ArtifactRecord) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.built {
		return x.buildErr
	}
	x.built = true

	texts := make([]string, 0, len(records))
	kept := make([]filesystem.ArtifactRecord, 0, len(records))
	for _, rec := range records {
		if rec.Artifact == nil {
			continue
		}
		text := strings.Join(overlayTokens(rec), " ")
		if strings.TrimSpace(text) == "" {
			continue
		}
		texts = append(texts, text)
		kept = append(kept, rec)
	}
	if len(texts) == 0 {
		return nil
	}
	vecs, err := x.emb.Embed(ctx, texts)
	if err != nil {
		x.buildErr = err
		return err
	}
	if len(vecs) != len(kept) {
		x.buildErr = fmt.Errorf("local-semantic: embedder returned %d vectors for %d records", len(vecs), len(kept))
		return x.buildErr
	}
	for i, rec := range kept {
		if err := x.vec.Put(ctx, localTenant, rec.ID, rec.Artifact.Version, vecs[i]); err != nil {
			x.buildErr = err
			return err
		}
		x.byID[rec.ID] = rec
	}
	return nil
}

// buildLocalSemantic constructs the §9.1 LocalSearchProvider semantic
// index from the bridge config. Returns (nil, nil) when no overlay
// vector backend or embedding provider is configured, leaving the
// overlay BM25-only (the row's default). The vector backend and
// embedding provider are selected by the same env vars the registry-side
// path uses (PODIUM_VECTOR_BACKEND, PODIUM_EMBEDDING_PROVIDER), so an
// operator points the overlay index at sqlite-vec, an in-memory index,
// or a managed backend the same way.
func buildLocalSemantic(cfg *config) (*localSemanticIndex, error) {
	if isNoneBackend(cfg.localEmbeddingProvider) || isNoneBackend(cfg.localVectorBackend) {
		return nil, nil
	}
	emb, err := mcpOverlayEmbedder(cfg.localEmbeddingProvider)
	if err != nil {
		return nil, err
	}
	if emb == nil {
		return nil, nil
	}
	vec, err := mcpOverlayVectorBackend(cfg.localVectorBackend, emb.Dimensions())
	if err != nil {
		return nil, err
	}
	if vec == nil {
		return nil, nil
	}
	return newLocalSemanticIndex(emb, vec), nil
}

func isNoneBackend(id string) bool { return id == "" || id == "none" }

// mcpOverlayEmbedder selects the §9.1 EmbeddingProvider for the overlay
// index. Embedding-provider selection follows the same rules as the
// registry-side path: a custom provider registered via
// embedding.Default.Register is consulted first, then the built-ins.
func mcpOverlayEmbedder(id string) (embedding.Provider, error) {
	settings := map[string]string{
		"openai_key": os.Getenv("OPENAI_API_KEY"),
		"voyage_key": os.Getenv("VOYAGE_API_KEY"),
		"cohere_key": os.Getenv("COHERE_API_KEY"),
		"ollama_url": envDefault("PODIUM_OLLAMA_URL", "http://localhost:11434"),
		"model":      os.Getenv("PODIUM_EMBEDDING_MODEL"),
	}
	if p, ok, err := embedding.Default.New(id, settings); err != nil {
		return nil, err
	} else if ok {
		return p, nil
	}
	switch id {
	case "openai":
		key := os.Getenv("OPENAI_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY required for the openai overlay embedder")
		}
		return embedding.OpenAI{APIKey: key, Model_: os.Getenv("PODIUM_EMBEDDING_MODEL"), BaseURL: os.Getenv("PODIUM_OPENAI_BASE_URL"), Org: os.Getenv("PODIUM_OPENAI_ORG")}, nil
	case "voyage":
		key := os.Getenv("VOYAGE_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("VOYAGE_API_KEY required for the voyage overlay embedder")
		}
		return embedding.Voyage{APIKey: key, Model_: os.Getenv("PODIUM_EMBEDDING_MODEL")}, nil
	case "cohere":
		key := os.Getenv("COHERE_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("COHERE_API_KEY required for the cohere overlay embedder")
		}
		return embedding.Cohere{APIKey: key, Model_: os.Getenv("PODIUM_EMBEDDING_MODEL")}, nil
	case "ollama":
		return embedding.Ollama{BaseURL: envDefault("PODIUM_OLLAMA_URL", "http://localhost:11434"), Model_: os.Getenv("PODIUM_EMBEDDING_MODEL")}, nil
	}
	return nil, fmt.Errorf("unknown overlay embedding provider: %s", id)
}

// mcpOverlayVectorBackend selects the §6.4.1 LocalSearchProvider vector
// backend for the workspace overlay. The "memory" backend is an in-process
// index that needs no external service; sqlite-vec collocates with a local
// file; and per §6.4.1 the overlay also reaches a local pgvector instance or
// a managed service (Pinecone, Weaviate Cloud, Qdrant Cloud). Custom backends
// register through vector.Default.Register. The built-in backends are
// constructed by the shared vector.OpenBuiltin factory, so the overlay
// selects the same set the registry bootstrap does, configured by the same
// env vars (PODIUM_PGVECTOR_DSN/PODIUM_POSTGRES_DSN, PODIUM_PINECONE_*,
// PODIUM_WEAVIATE_*, PODIUM_QDRANT_*).
func mcpOverlayVectorBackend(id string, dim int) (vector.Provider, error) {
	cfg := vector.BackendConfig{
		PgVectorDSN:   envFirst("PODIUM_PGVECTOR_DSN", "PODIUM_POSTGRES_DSN"),
		SQLitePath:    envFirst("PODIUM_LOCAL_SQLITE_VEC_PATH", "PODIUM_SQLITE_PATH"),
		PineconeKey:   os.Getenv("PODIUM_PINECONE_API_KEY"),
		PineconeHost:  os.Getenv("PODIUM_PINECONE_HOST"),
		PineconeIndex: os.Getenv("PODIUM_PINECONE_INDEX"),
		PineconeNS:    envDefault("PODIUM_PINECONE_NAMESPACE", "default"),
		// §13.12 (F-13.12.3): the same control-plane override applies on the MCP
		// server, so an index-only Pinecone overlay resolves its host the same way.
		PineconeControlPlane: os.Getenv("PODIUM_PINECONE_CONTROL_PLANE"),
		WeaviateURL:          os.Getenv("PODIUM_WEAVIATE_URL"),
		WeaviateKey:          os.Getenv("PODIUM_WEAVIATE_API_KEY"),
		WeaviateColl:         os.Getenv("PODIUM_WEAVIATE_COLLECTION"),
		QdrantURL:            os.Getenv("PODIUM_QDRANT_URL"),
		QdrantKey:            os.Getenv("PODIUM_QDRANT_API_KEY"),
		QdrantColl:           os.Getenv("PODIUM_QDRANT_COLLECTION"),
		InferenceModel:       envFirst("PODIUM_PINECONE_INFERENCE_MODEL", "PODIUM_WEAVIATE_VECTORIZER", "PODIUM_QDRANT_INFERENCE_MODEL"),
	}
	// The Default registry (custom backends) consumes the wire-serializable
	// settings map so a future out-of-process provider receives the same
	// inputs.
	settings := map[string]string{
		"pgvector_dsn":    cfg.PgVectorDSN,
		"sqlite_path":     cfg.SQLitePath,
		"pinecone_key":    cfg.PineconeKey,
		"pinecone_host":   cfg.PineconeHost,
		"pinecone_index":  cfg.PineconeIndex,
		"namespace":       cfg.PineconeNS,
		"weaviate_url":    cfg.WeaviateURL,
		"weaviate_key":    cfg.WeaviateKey,
		"collection":      cfg.WeaviateColl,
		"qdrant_url":      cfg.QdrantURL,
		"qdrant_key":      cfg.QdrantKey,
		"inference_model": cfg.InferenceModel,
	}
	if p, ok, err := vector.Default.New(id, settings, dim); err != nil {
		return nil, err
	} else if ok {
		return p, nil
	}
	return vector.OpenBuiltin(id, cfg, dim)
}
