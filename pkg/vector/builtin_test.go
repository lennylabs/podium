package vector

import (
	"strings"
	"testing"
)

// spec: §6.4.1 / §13.12 — OpenBuiltin is the shared factory behind the
// registry bootstrap and the MCP workspace-overlay index. It must construct
// every documented backend (sqlite-vec, pgvector, pinecone, weaviate-cloud,
// qdrant-cloud) from resolved config, leave the layer disabled for ""/none,
// and reject an unknown id.
func TestOpenBuiltin_DispatchesByBackend(t *testing.T) {
	cases := []struct {
		name       string
		id         string
		cfg        BackendConfig
		dim        int
		wantNil    bool
		wantID     string
		wantErrSub string
	}{
		{name: "empty-disabled", id: "", dim: 4, wantNil: true},
		{name: "none-disabled", id: "none", dim: 4, wantNil: true},
		{name: "memory", id: "memory", dim: 4, wantID: "memory"},
		{name: "sqlite-vec-default-path", id: "sqlite-vec", dim: 4, wantID: "sqlite-vec"},
		{name: "pgvector-missing-dsn", id: "pgvector", dim: 4, wantErrSub: "DSN"},
		{name: "pinecone-missing-key", id: "pinecone", cfg: BackendConfig{PineconeHost: "https://h"}, dim: 4, wantErrSub: "APIKey"},
		{
			name: "pinecone-ok", id: "pinecone",
			cfg: BackendConfig{PineconeKey: "k", PineconeHost: "https://h"}, dim: 4,
			wantID: "pinecone",
		},
		{
			name: "weaviate-ok", id: "weaviate-cloud",
			cfg: BackendConfig{WeaviateURL: "https://w", WeaviateColl: "Podium"}, dim: 4,
			wantID: "weaviate-cloud",
		},
		{
			name: "qdrant-ok", id: "qdrant-cloud",
			cfg: BackendConfig{QdrantURL: "https://q", QdrantColl: "podium"}, dim: 4,
			wantID: "qdrant-cloud",
		},
		{name: "unknown", id: "bogus", dim: 4, wantErrSub: "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := OpenBuiltin(c.id, c.cfg, c.dim)
			if c.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErrSub) {
					t.Fatalf("err = %v, want substring %q", err, c.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err = %v", err)
			}
			if c.wantNil {
				if got != nil {
					t.Fatalf("got %v, want nil provider", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("got nil provider, want id %q", c.wantID)
			}
			if c.wantID != "" && got.ID() != c.wantID {
				t.Errorf("ID = %q, want %q", got.ID(), c.wantID)
			}
		})
	}
}

// spec: §13.12 — the pgvector DSN error names both accepted env vars so an
// operator who set neither knows what to supply.
func TestOpenBuiltin_PgVectorDSNErrorNamesEnvVars(t *testing.T) {
	_, err := OpenBuiltin("pgvector", BackendConfig{}, 4)
	if err == nil {
		t.Fatal("want error for pgvector without a DSN")
	}
	for _, want := range []string{"PODIUM_PGVECTOR_DSN", "PODIUM_POSTGRES_DSN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}
