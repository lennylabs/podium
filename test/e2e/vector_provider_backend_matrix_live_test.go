package e2e

// Live Release-lane matrix that boots a standalone server for each reachable
// (embedding provider, managed vector backend) pair and each self-embedding
// backend, ingests one fixture set, and asserts semantic search returns the
// expected artifact at rank 1 through the server, closing G-VEC-12 (TEST-GAPS.md).
// TestVectorSemanticSearch_ManagedThroughServer iterates the managed backends
// but only with a mock OpenAI-format embedder for storage-only vectors, and the
// pkg/embedding live tests exercise each provider with no managed backend, so no
// test boots a server with a real embedding provider and a managed backend
// together; this matrix runs every reachable combination.
//
// Cells:
//   - storage-only: each embedding provider whose key is present, paired with
//     each managed backend whose credentials are present (provider computes the
//     vector, backend stores it).
//   - self-embedding: each managed backend with an inference model / vectorizer
//     and no external provider (the backend computes the vector).
//
// Gating: PODIUM_LIVE_EXTERNAL=1. Each cell skips when its provider key or its
// backend credentials are absent, so the matrix runs every reachable cell and
// skips the rest cleanly. Only OpenAI is exercised as the external provider
// because it is the embedding-provider key present in the standard live config;
// adding VOYAGE_API_KEY / COHERE_API_KEY activates those rows with no code
// change.

import (
	"os"
	"strings"
	"testing"
	"time"
)

// matrixRegistry stages a corpus with ids distinct from the other vector e2e
// fixtures so concurrent or repeated runs never clobber each other's vectors in
// the shared Qdrant/Weaviate collections (which isolate by tenant, not by a
// per-run namespace). The target is semantically unambiguous so any real
// embedding model ranks it first for the matching query.
func matrixRegistry(t *testing.T) string {
	t.Helper()
	return writeRegistry(t, map[string]string{
		"matrix/astronomy/ARTIFACT.md": contextArtifact("telescope astronomy observing distant galaxies nebulae and star clusters at night"),
		"matrix/gardening/ARTIFACT.md": contextArtifact("gardening planting vegetables pruning shrubs and composting soil"),
		"matrix/taxes/ARTIFACT.md":     contextArtifact("income tax filing deductions credits and quarterly estimated payments"),
	})
}

const (
	matrixQuery  = "stargazing through a telescope at the cosmos"
	matrixTarget = "matrix/astronomy"
)

// embeddingProviderCell names an external embedding provider, the env that
// selects it, and whether its key is present.
type embeddingProviderCell struct {
	name    string
	env     []string
	present bool
}

// externalEmbeddingProviders returns the external embedding providers with their
// wiring and key presence. Only providers whose key is set are marked present;
// the matrix skips absent ones.
func externalEmbeddingProviders() []embeddingProviderCell {
	return []embeddingProviderCell{
		{
			name:    "openai",
			present: os.Getenv("OPENAI_API_KEY") != "",
			env:     []string{"PODIUM_EMBEDDING_PROVIDER=openai", "OPENAI_API_KEY=" + os.Getenv("OPENAI_API_KEY")},
		},
		{
			name:    "voyage",
			present: os.Getenv("VOYAGE_API_KEY") != "",
			env:     []string{"PODIUM_EMBEDDING_PROVIDER=voyage", "VOYAGE_API_KEY=" + os.Getenv("VOYAGE_API_KEY")},
		},
		{
			name:    "cohere",
			present: os.Getenv("COHERE_API_KEY") != "",
			env:     []string{"PODIUM_EMBEDDING_PROVIDER=cohere", "COHERE_API_KEY=" + os.Getenv("COHERE_API_KEY")},
		},
	}
}

// storageBackendCell names a managed backend in storage-only mode (no inference
// model), the env that selects its storage index/collection, and whether its
// credentials are present.
type storageBackendCell struct {
	name    string
	env     []string
	present bool
}

// storageBackends returns the managed backends in storage-only mode.
func storageBackends() []storageBackendCell {
	return []storageBackendCell{
		{
			name: "pinecone",
			present: os.Getenv("PODIUM_PINECONE_API_KEY") != "" &&
				(os.Getenv("PODIUM_PINECONE_HOST") != "" || os.Getenv("PODIUM_PINECONE_INDEX") != ""),
			env: []string{
				"PODIUM_VECTOR_BACKEND=pinecone",
				"PODIUM_PINECONE_API_KEY=" + os.Getenv("PODIUM_PINECONE_API_KEY"),
				"PODIUM_PINECONE_INDEX=" + os.Getenv("PODIUM_PINECONE_INDEX"),
				"PODIUM_PINECONE_HOST=" + os.Getenv("PODIUM_PINECONE_HOST"),
			},
		},
		{
			name: "weaviate-cloud",
			present: os.Getenv("PODIUM_WEAVIATE_URL") != "" &&
				os.Getenv("PODIUM_WEAVIATE_API_KEY") != "" &&
				os.Getenv("PODIUM_WEAVIATE_COLLECTION") != "",
			env: []string{
				"PODIUM_VECTOR_BACKEND=weaviate-cloud",
				"PODIUM_WEAVIATE_URL=" + os.Getenv("PODIUM_WEAVIATE_URL"),
				"PODIUM_WEAVIATE_API_KEY=" + os.Getenv("PODIUM_WEAVIATE_API_KEY"),
				"PODIUM_WEAVIATE_COLLECTION=" + os.Getenv("PODIUM_WEAVIATE_COLLECTION"),
			},
		},
		{
			name: "qdrant-cloud",
			present: os.Getenv("PODIUM_QDRANT_URL") != "" &&
				os.Getenv("PODIUM_QDRANT_API_KEY") != "" &&
				os.Getenv("PODIUM_QDRANT_COLLECTION") != "",
			env: []string{
				"PODIUM_VECTOR_BACKEND=qdrant-cloud",
				"PODIUM_QDRANT_URL=" + os.Getenv("PODIUM_QDRANT_URL"),
				"PODIUM_QDRANT_API_KEY=" + os.Getenv("PODIUM_QDRANT_API_KEY"),
				"PODIUM_QDRANT_COLLECTION=" + os.Getenv("PODIUM_QDRANT_COLLECTION"),
			},
		},
	}
}

// selfEmbeddingBackends returns the managed backends in self-embedding mode,
// each with its own inference model / vectorizer and dedicated self-embedding
// index/collection, and no external provider.
func selfEmbeddingBackends() []storageBackendCell {
	return []storageBackendCell{
		{
			name: "pinecone-selfembed",
			present: os.Getenv("PODIUM_PINECONE_API_KEY") != "" &&
				os.Getenv("PODIUM_PINECONE_SELFEMBED_INDEX") != "" &&
				os.Getenv("PODIUM_PINECONE_INFERENCE_MODEL") != "",
			env: []string{
				"PODIUM_VECTOR_BACKEND=pinecone",
				"PODIUM_PINECONE_API_KEY=" + os.Getenv("PODIUM_PINECONE_API_KEY"),
				"PODIUM_PINECONE_INDEX=" + os.Getenv("PODIUM_PINECONE_SELFEMBED_INDEX"),
				"PODIUM_PINECONE_INFERENCE_MODEL=" + os.Getenv("PODIUM_PINECONE_INFERENCE_MODEL"),
				"PODIUM_EMBEDDING_PROVIDER=",
			},
		},
		{
			name: "weaviate-selfembed",
			present: os.Getenv("PODIUM_WEAVIATE_URL") != "" &&
				os.Getenv("PODIUM_WEAVIATE_API_KEY") != "" &&
				os.Getenv("PODIUM_WEAVIATE_SELFEMBED_COLLECTION") != "" &&
				os.Getenv("PODIUM_WEAVIATE_VECTORIZER") != "",
			env: []string{
				"PODIUM_VECTOR_BACKEND=weaviate-cloud",
				"PODIUM_WEAVIATE_URL=" + os.Getenv("PODIUM_WEAVIATE_URL"),
				"PODIUM_WEAVIATE_API_KEY=" + os.Getenv("PODIUM_WEAVIATE_API_KEY"),
				"PODIUM_WEAVIATE_COLLECTION=" + os.Getenv("PODIUM_WEAVIATE_SELFEMBED_COLLECTION"),
				"PODIUM_WEAVIATE_VECTORIZER=" + os.Getenv("PODIUM_WEAVIATE_VECTORIZER"),
				"PODIUM_EMBEDDING_PROVIDER=",
			},
		},
		{
			name: "qdrant-selfembed",
			present: os.Getenv("PODIUM_QDRANT_URL") != "" &&
				os.Getenv("PODIUM_QDRANT_API_KEY") != "" &&
				os.Getenv("PODIUM_QDRANT_SELFEMBED_COLLECTION") != "" &&
				os.Getenv("PODIUM_QDRANT_INFERENCE_MODEL") != "",
			env: []string{
				"PODIUM_VECTOR_BACKEND=qdrant-cloud",
				"PODIUM_QDRANT_URL=" + os.Getenv("PODIUM_QDRANT_URL"),
				"PODIUM_QDRANT_API_KEY=" + os.Getenv("PODIUM_QDRANT_API_KEY"),
				"PODIUM_QDRANT_COLLECTION=" + os.Getenv("PODIUM_QDRANT_SELFEMBED_COLLECTION"),
				"PODIUM_QDRANT_INFERENCE_MODEL=" + os.Getenv("PODIUM_QDRANT_INFERENCE_MODEL"),
				"PODIUM_EMBEDDING_PROVIDER=",
			},
		},
	}
}

// runMatrixCell boots a server with the combined env over the matrix registry,
// asserts the vector backend is wired, and polls search_artifacts until the
// target ranks first. A per-run Pinecone namespace isolates Pinecone cells; the
// Weaviate/Qdrant cells isolate by the default tenant and distinct fixture ids.
func runMatrixCell(t *testing.T, cellEnv []string, isPinecone bool) {
	t.Helper()
	env := append([]string{"HOME=" + t.TempDir()}, cellEnv...)
	if isPinecone {
		env = append(env, "PODIUM_PINECONE_NAMESPACE="+uniquePineconeNS("matrix"))
	}
	srv := startServerArgs(t, env, "serve", "--standalone", "--layer-path", matrixRegistry(t))
	log := srv.log()
	if !strings.Contains(log, "hybrid search:") {
		t.Fatalf("vector backend not wired in startup log:\n%s", log)
	}
	if strings.Contains(log, "vector search disabled") {
		t.Fatalf("vector search disabled at startup:\n%s", log)
	}
	pollSemanticTopHit(t, srv.BaseURL, matrixQuery, matrixTarget, 120*time.Second)
}

// TestVectorMatrix_ProviderByBackendLive closes G-VEC-12. It runs one subtest
// per reachable (embedding provider, managed backend) storage-only cell and one
// per reachable self-embedding backend, each booting a server, ingesting the
// fixtures, and asserting the expected artifact ranks first through
// search_artifacts.
//
// Spec: §4.7 / §13.12 (storage-only backends compute the vector through a
// configured EmbeddingProvider; self-embedding backends compute it server-side;
// both produce correct recall through the registry search surface).
func TestVectorMatrix_ProviderByBackendLive(t *testing.T) {
	t.Parallel()
	if os.Getenv("PODIUM_LIVE_EXTERNAL") != "1" {
		t.Skip("PODIUM_LIVE_EXTERNAL != 1; skipping live provider-by-backend matrix")
	}

	// Storage-only cross-product: provider × backend.
	for _, prov := range externalEmbeddingProviders() {
		for _, backend := range storageBackends() {
			prov, backend := prov, backend
			t.Run("storage/"+prov.name+"+"+backend.name, func(t *testing.T) {
				t.Parallel()
				if !prov.present {
					t.Skipf("%s key absent; skipping", prov.name)
				}
				if !backend.present {
					t.Skipf("%s credentials absent; skipping", backend.name)
				}
				cellEnv := append(append([]string{}, backend.env...), prov.env...)
				runMatrixCell(t, cellEnv, backend.name == "pinecone")
			})
		}
	}

	// Self-embedding cells: backend computes the vector, no external provider.
	for _, backend := range selfEmbeddingBackends() {
		backend := backend
		t.Run("selfembed/"+backend.name, func(t *testing.T) {
			t.Parallel()
			if !backend.present {
				t.Skipf("%s self-embedding credentials absent; skipping", backend.name)
			}
			runMatrixCell(t, backend.env, strings.HasPrefix(backend.name, "pinecone"))
		})
	}
}
