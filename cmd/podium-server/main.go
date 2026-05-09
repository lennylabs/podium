// Command podium-server runs the Podium registry as a long-lived
// HTTP server. The standalone deployment (§13.10) bundles all
// dependencies (SQLite + filesystem object store + ONNX embeddings);
// the standard deployment (§13.1) wires Postgres + S3-compatible
// object storage + an OIDC IdP via env vars per §13.12.
//
// The binary reads its configuration from PODIUM_REGISTRY_STORE,
// PODIUM_OBJECT_STORE, PODIUM_VECTOR_BACKEND, PODIUM_EMBEDDING_PROVIDER,
// PODIUM_IDENTITY_PROVIDER, plus the per-backend env vars from §13.12.
// Default behavior matches §13.10's zero-flag standalone bootstrap:
// SQLite + filesystem object store + embedded-onnx + no auth bound on
// 127.0.0.1:8080.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/embedding"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// envFirst returns the value of the first non-empty env var.
func envFirst(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg := loadConfig()
	if err := cfg.validate(); err != nil {
		return err
	}

	st, err := openStore(cfg)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}

	// Standalone bootstrap: ensure the default tenant exists so
	// initial requests don't fail on missing-tenant lookups (§13.10
	// auto-bootstrap).
	const tenantID = "default"
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: tenantID, Name: tenantID})

	registry := core.New(st, tenantID, []layer.Layer{})
	if v, e, err := openVectorAndEmbedder(cfg); err != nil {
		log.Printf("warning: vector search disabled: %v", err)
	} else if v != nil && e != nil {
		registry = registry.WithVectorSearch(v, e)
		log.Printf("hybrid search: vector=%s embedder=%s dim=%d", v.ID(), e.ID(), e.Dimensions())
	}
	srv := server.New(registry,
		bootstrapOptions(cfg)...,
	)

	mux := http.NewServeMux()
	mux.Handle("/", srv.Handler())

	httpServer := &http.Server{
		Addr:              cfg.bind,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("podium-server listening on %s (mode=%s)", cfg.bind, cfg.modeBanner())
	return httpServer.ListenAndServe()
}

type config struct {
	bind             string
	publicMode       bool
	identityProvider string
	storeType        string
	sqlitePath       string
	postgresDSN      string
	objectStore      string
	filesystemRoot   string
	publicURL        string
	presignTTL       time.Duration
	s3Endpoint       string
	s3Region         string
	s3Bucket         string
	s3AccessKey      string
	s3SecretKey      string
	s3UseSSL         bool
	// Vector + embedding (§4.7).
	vectorBackend    string
	embeddingProvider string
	embeddingModel   string
	openaiAPIKey     string
	voyageAPIKey     string
	cohereAPIKey     string
	ollamaURL        string
	pgvectorDSN      string
	pineconeKey      string
	pineconeHost     string
	pineconeNS       string
	weaviateURL      string
	weaviateKey      string
	weaviateColl     string
	qdrantURL        string
	qdrantKey        string
	qdrantColl       string
}

func loadConfig() *config {
	c := &config{
		bind:             envDefault("PODIUM_BIND", "127.0.0.1:8080"),
		publicMode:       isTrue(os.Getenv("PODIUM_PUBLIC_MODE")),
		identityProvider: os.Getenv("PODIUM_IDENTITY_PROVIDER"),
		storeType:        envDefault("PODIUM_REGISTRY_STORE", "sqlite"),
		sqlitePath:       os.Getenv("PODIUM_SQLITE_PATH"),
		postgresDSN:      os.Getenv("PODIUM_POSTGRES_DSN"),
		objectStore:      envDefault("PODIUM_OBJECT_STORE", "filesystem"),
		filesystemRoot:   os.Getenv("PODIUM_FILESYSTEM_ROOT"),
		publicURL:        os.Getenv("PODIUM_PUBLIC_URL"),
		s3Endpoint:       os.Getenv("PODIUM_S3_ENDPOINT"),
		s3Region:         envDefault("PODIUM_S3_REGION", "us-east-1"),
		s3Bucket:         os.Getenv("PODIUM_S3_BUCKET"),
		s3AccessKey:      os.Getenv("PODIUM_S3_ACCESS_KEY_ID"),
		s3SecretKey:      os.Getenv("PODIUM_S3_SECRET_ACCESS_KEY"),
		s3UseSSL:         os.Getenv("PODIUM_S3_USE_SSL") != "false",
		// §4.7 vector + embedding.
		vectorBackend:     os.Getenv("PODIUM_VECTOR_BACKEND"),
		embeddingProvider: os.Getenv("PODIUM_EMBEDDING_PROVIDER"),
		embeddingModel:    os.Getenv("PODIUM_EMBEDDING_MODEL"),
		openaiAPIKey:      os.Getenv("OPENAI_API_KEY"),
		voyageAPIKey:      os.Getenv("VOYAGE_API_KEY"),
		cohereAPIKey:      os.Getenv("COHERE_API_KEY"),
		ollamaURL:         envDefault("PODIUM_OLLAMA_URL", "http://localhost:11434"),
		pgvectorDSN:       envFirst("PODIUM_PGVECTOR_DSN", "PODIUM_POSTGRES_DSN"),
		pineconeKey:       os.Getenv("PODIUM_PINECONE_API_KEY"),
		pineconeHost:      os.Getenv("PODIUM_PINECONE_HOST"),
		pineconeNS:        os.Getenv("PODIUM_PINECONE_NAMESPACE"),
		weaviateURL:       os.Getenv("PODIUM_WEAVIATE_URL"),
		weaviateKey:       os.Getenv("PODIUM_WEAVIATE_API_KEY"),
		weaviateColl:      envDefault("PODIUM_WEAVIATE_COLLECTION", "PodiumArtifacts"),
		qdrantURL:         os.Getenv("PODIUM_QDRANT_URL"),
		qdrantKey:         os.Getenv("PODIUM_QDRANT_API_KEY"),
		qdrantColl:        envDefault("PODIUM_QDRANT_COLLECTION", "podium_artifacts"),
	}
	if c.sqlitePath == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			c.sqlitePath = filepath.Join(home, ".podium", "standalone", "podium.db")
		}
	}
	if c.filesystemRoot == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			c.filesystemRoot = filepath.Join(home, ".podium", "standalone", "objects")
		}
	}
	if v := os.Getenv("PODIUM_PRESIGN_TTL_SECONDS"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			c.presignTTL = time.Duration(secs) * time.Second
		}
	}
	if c.presignTTL <= 0 {
		c.presignTTL = objectstore.DefaultPresignTTL
	}
	if c.publicURL == "" {
		c.publicURL = "http://" + c.bind
	}
	return c
}

func (c *config) validate() error {
	startup := server.StartupConfig{
		PublicMode:       c.publicMode,
		IdentityProvider: c.identityProvider,
	}
	if err := startup.Validate(); err != nil {
		return err
	}
	if c.storeType == "postgres" && c.postgresDSN == "" {
		return fmt.Errorf("PODIUM_POSTGRES_DSN is required when PODIUM_REGISTRY_STORE=postgres")
	}
	return nil
}

func (c *config) modeBanner() string {
	if c.publicMode {
		return "public"
	}
	if c.identityProvider != "" {
		return c.identityProvider
	}
	return "standalone"
}

func openStore(c *config) (store.Store, error) {
	switch c.storeType {
	case "sqlite":
		dir := filepath.Dir(c.sqlitePath)
		_ = os.MkdirAll(dir, 0o755)
		return store.OpenSQLite(c.sqlitePath)
	case "memory":
		return store.NewMemory(), nil
	case "postgres":
		return store.OpenPostgres(c.postgresDSN)
	}
	return nil, fmt.Errorf("unknown PODIUM_REGISTRY_STORE: %s", c.storeType)
}

func bootstrapOptions(c *config) []server.Option {
	out := []server.Option{}
	if c.publicMode {
		out = append(out, server.WithPublicMode())
	}
	if store, err := openObjectStore(c); err != nil {
		log.Printf("warning: object store disabled: %v", err)
	} else if store != nil {
		out = append(out, server.WithObjectStore(store, c.publicURL, c.presignTTL))
	}
	return out
}

// openVectorAndEmbedder returns the configured §4.7 hybrid-search
// pieces. Returns (nil, nil, nil) when vector search is disabled
// (operator left PODIUM_VECTOR_BACKEND unset / set to "none").
func openVectorAndEmbedder(c *config) (vector.Provider, embedding.Provider, error) {
	emb, err := openEmbedder(c)
	if err != nil {
		return nil, nil, err
	}
	if emb == nil {
		return nil, nil, nil
	}
	v, err := openVectorBackend(c, emb.Dimensions())
	if err != nil {
		return nil, nil, err
	}
	return v, emb, nil
}

func openEmbedder(c *config) (embedding.Provider, error) {
	switch c.embeddingProvider {
	case "", "none":
		return nil, nil
	case "openai":
		if c.openaiAPIKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY required for openai embedder")
		}
		return embedding.OpenAI{APIKey: c.openaiAPIKey, Model_: c.embeddingModel}, nil
	case "voyage":
		if c.voyageAPIKey == "" {
			return nil, fmt.Errorf("VOYAGE_API_KEY required for voyage embedder")
		}
		return embedding.Voyage{APIKey: c.voyageAPIKey, Model_: c.embeddingModel}, nil
	case "cohere":
		if c.cohereAPIKey == "" {
			return nil, fmt.Errorf("COHERE_API_KEY required for cohere embedder")
		}
		return embedding.Cohere{APIKey: c.cohereAPIKey, Model_: c.embeddingModel}, nil
	case "ollama":
		return embedding.Ollama{BaseURL: c.ollamaURL, Model_: c.embeddingModel}, nil
	}
	return nil, fmt.Errorf("unknown PODIUM_EMBEDDING_PROVIDER: %s", c.embeddingProvider)
}

func openVectorBackend(c *config, dim int) (vector.Provider, error) {
	switch c.vectorBackend {
	case "", "none":
		return nil, nil
	case "memory":
		return vector.NewMemory(dim), nil
	case "pgvector":
		if c.pgvectorDSN == "" {
			return nil, fmt.Errorf("PODIUM_PGVECTOR_DSN or PODIUM_POSTGRES_DSN required for pgvector")
		}
		return vector.OpenPgVector(vector.PgVectorConfig{DSN: c.pgvectorDSN, Dimensions: dim})
	case "sqlite-vec":
		path := c.sqlitePath
		if path == "" {
			path = ":memory:"
		}
		return vector.OpenSQLiteVec(vector.SQLiteVecConfig{Path: path, Dimensions: dim})
	case "pinecone":
		return vector.NewPinecone(vector.PineconeConfig{
			APIKey: c.pineconeKey, Host: c.pineconeHost,
			Namespace: c.pineconeNS, Dimensions: dim,
		})
	case "weaviate-cloud":
		return vector.NewWeaviate(vector.WeaviateConfig{
			URL: c.weaviateURL, APIKey: c.weaviateKey,
			Collection: c.weaviateColl, Dimensions: dim,
		})
	case "qdrant-cloud":
		return vector.NewQdrant(vector.QdrantConfig{
			URL: c.qdrantURL, APIKey: c.qdrantKey,
			Collection: c.qdrantColl, Dimensions: dim,
		})
	}
	return nil, fmt.Errorf("unknown PODIUM_VECTOR_BACKEND: %s", c.vectorBackend)
}

// openObjectStore returns the configured §13.10 object-storage
// backend, or (nil, nil) when the standalone deployment runs without
// one (resources stay inline regardless of size).
func openObjectStore(c *config) (objectstore.Provider, error) {
	switch c.objectStore {
	case "", "filesystem":
		_ = os.MkdirAll(c.filesystemRoot, 0o755)
		return objectstore.Open(c.filesystemRoot)
	case "s3":
		if c.s3Bucket == "" {
			return nil, fmt.Errorf("PODIUM_S3_BUCKET is required when PODIUM_OBJECT_STORE=s3")
		}
		if c.s3Endpoint == "" {
			c.s3Endpoint = "s3.amazonaws.com"
		}
		return objectstore.NewS3(objectstore.S3Config{
			Endpoint:        c.s3Endpoint,
			Bucket:          c.s3Bucket,
			Region:          c.s3Region,
			AccessKeyID:     c.s3AccessKey,
			SecretAccessKey: c.s3SecretKey,
			UseSSL:          c.s3UseSSL,
		})
	case "none":
		return nil, nil
	}
	return nil, fmt.Errorf("unknown PODIUM_OBJECT_STORE: %s", c.objectStore)
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func isTrue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}
