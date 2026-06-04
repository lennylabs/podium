package e2e

// End-to-end semantic-search tests that wire a real vector backend through
// the standalone server's HTTP surface, closing G-VEC-3 (TEST-GAPS.md). The
// existing discovery_search e2e runs against in-memory BM25, and the
// vector_backend_config e2e skips every storage happy-path; neither boots a
// server with a live vector backend, ingests artifacts, and asserts that a
// semantic query returns the expected ranked result through the registry.
//
// Two forms run here:
//
//   - TestVectorSemanticSearch_PgVectorThroughServer (PR lane) boots the
//     server with PODIUM_VECTOR_BACKEND=pgvector against the CI Postgres
//     container (PODIUM_POSTGRES_DSN). pgvector is collocated storage-only, so
//     the registry computes vectors through an embedding provider; a mock
//     OpenAI-format embedder keeps ingest off the real API while producing
//     deterministic, query-discriminating vectors.
//
//   - TestVectorSemanticSearch_ManagedThroughServer (Release lane) boots the
//     server with the first managed backend whose credentials are present
//     (Pinecone, Weaviate Cloud, or Qdrant Cloud) and asserts the same
//     end-to-end query. It gates on PODIUM_LIVE_EXTERNAL=1 plus that backend's
//     credentials and skips cleanly otherwise.
//
// Both forms assert the same observable behavior: search_artifacts over HTTP
// returns the artifact whose description matches the query at rank 1. The
// hybrid retriever (BM25 + embeddings, fused via RRF) and the BM25-only
// fallback both rank a lexically-matching artifact first, so the assertion
// holds whether the vector path contributes or silently degrades; the test
// additionally requires the startup log to show the vector backend wired, so
// a silent degrade to BM25 does not pass as a vector-backed result.

import (
	"crypto/rand"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	_ "github.com/lib/pq"
)

// semanticDim is the embedding dimension the mock OpenAI embedder emits. It
// matches text-embedding-3-small (the standard default model, §13.12), which is
// the dimension serverboot opens pgvector at when no Dim override is set.
const semanticDim = 1536

// semanticVector projects text into a deterministic semanticDim-element vector.
// Each lowercase word increments one bucket chosen by a stable hash, so texts
// that share words land near each other under cosine distance. The same text
// produces the same vector at ingest time and at query time, which is what
// makes the ranked result deterministic across the embed-store-search path. The
// math mirrors the in-process bagEmbedder used by the integration suite.
func semanticVector(text string) []float64 {
	v := make([]float64, semanticDim)
	for _, word := range strings.Fields(strings.ToLower(text)) {
		sum := sha1.Sum([]byte(word))
		idx := (int(sum[0])<<8 | int(sum[1])) % semanticDim
		v[idx]++
	}
	return v
}

// semanticMockEmbedder starts an httptest server speaking the OpenAI
// /v1/embeddings wire format. It decodes the batch input, emits one
// deterministic semanticVector per text in input order, and preserves the index
// field the provider scatters on. Wiring it via PODIUM_OPENAI_BASE_URL keeps
// ingest and query embedding off the real OpenAI API while still producing
// vectors that rank meaningfully.
func semanticMockEmbedder(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		data := make([]map[string]any, len(req.Input))
		for i, text := range req.Input {
			data[i] = map[string]any{
				"object":    "embedding",
				"index":     i,
				"embedding": semanticVector(text),
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   data,
			"model":  req.Model,
			"usage":  map[string]any{"prompt_tokens": 1, "total_tokens": 1},
		})
	}))
	t.Cleanup(ts.Close)
	return ts
}

// semanticSearchResponse mirrors the §7.6.1 search_artifacts JSON envelope on
// the wire: {query, total_matched, results:[{id, type, version, score}]}.
type semanticSearchResponse struct {
	Query        string `json:"query"`
	TotalMatched int    `json:"total_matched"`
	Results      []struct {
		ID      string  `json:"id"`
		Type    string  `json:"type"`
		Version string  `json:"version"`
		Score   float64 `json:"score"`
	} `json:"results"`
}

// semanticRegistry stages three context artifacts whose descriptions are
// lexically and semantically distinct, so a query matching one of them ranks
// that artifact first. The "invoice reconciliation" artifact is the search
// target; the other two are distractors.
func semanticRegistry(t *testing.T) string {
	t.Helper()
	return writeRegistry(t, map[string]string{
		"finance/reconcile/ARTIFACT.md": contextArtifact("invoice reconciliation matching vendor payments against purchase orders"),
		"hr/onboarding/ARTIFACT.md":     contextArtifact("employee onboarding checklist for new hire orientation"),
		"infra/deploy/ARTIFACT.md":      contextArtifact("kubernetes deployment rollout and pod autoscaling"),
	})
}

// assertSemanticTopHit drives search_artifacts over HTTP for the given query
// and asserts the expected artifact id returns at rank 1 through the registry
// surface. It returns the decoded response for any further per-test checks.
func assertSemanticTopHit(t *testing.T, baseURL, query, wantID string) semanticSearchResponse {
	t.Helper()
	var resp semanticSearchResponse
	getJSON(t, baseURL+"/v1/search_artifacts?query="+queryEscape(query), &resp)
	if len(resp.Results) == 0 {
		t.Fatalf("search_artifacts(%q) returned no results", query)
	}
	if resp.Results[0].ID != wantID {
		t.Fatalf("search_artifacts(%q) top result = %q, want %q\nresults: %+v",
			query, resp.Results[0].ID, wantID, resp.Results)
	}
	return resp
}

// queryEscape percent-encodes spaces in a query string for the URL.
func queryEscape(q string) string {
	return strings.ReplaceAll(q, " ", "+")
}

// TestVectorSemanticSearch_PgVectorThroughServer is the G-VEC-3 PR-lane form
// (pgvector). It boots the standalone server with PODIUM_VECTOR_BACKEND=pgvector
// against the CI Postgres container, ingests three context artifacts, and
// asserts a semantic query returns the matching artifact at rank 1 through the
// HTTP search surface. The mock embedder keeps ingest off the real OpenAI API.
//
// Gating: PODIUM_POSTGRES_DSN (the existing pgvector PR-lane pattern). With no
// DSN the test skips cleanly.
//
// Spec: §4.7 (Registry as a Service; hybrid retrieval, embedding generation,
// pgvector as the collocated storage-only backend)
func TestVectorSemanticSearch_PgVectorThroughServer(t *testing.T) {
	t.Parallel()
	dsn := firstEnv("PODIUM_POSTGRES_DSN_VECTOR", "PODIUM_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN[_VECTOR] unset; skipping pgvector semantic-search e2e")
	}
	// vec_artifacts is created at a fixed vector(dim) per schema. The pkg/vector
	// conformance suite owns public.vec_artifacts at dim=8; this test needs it at
	// semanticDim. Pointing the server at the public schema would race that suite
	// under `go test ./...` (both packages share one Postgres), so the server is
	// pointed at a unique ephemeral schema via the DSN search_path, dropped in
	// cleanup. A skip on an unreachable DB keeps the no-DSN path clean. This
	// mirrors the openPgSchema idiom in pkg/vector/pgvector_depth_test.go.
	scopedDSN := pgVectorIsolatedSchema(t, dsn)
	emb := semanticMockEmbedder(t)

	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_VECTOR_BACKEND=pgvector",
		"PODIUM_PGVECTOR_DSN=" + scopedDSN,
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"OPENAI_API_KEY=sk-test",
		"PODIUM_OPENAI_BASE_URL=" + emb.URL,
	}, "serve", "--standalone", "--layer-path", semanticRegistry(t))

	// The vector backend must be wired; a silent degrade to BM25 would still
	// rank the lexical match first and mask a broken vector path, so require the
	// hybrid-search wiring in the startup log before asserting the ranked hit.
	log := srv.log()
	if !strings.Contains(log, "vector=pgvector") {
		t.Fatalf("startup log missing 'vector=pgvector' (vector backend not wired):\n%s", log)
	}
	if strings.Contains(log, "vector search disabled") {
		t.Fatalf("vector search was disabled at startup:\n%s", log)
	}

	resp := assertSemanticTopHit(t, srv.BaseURL,
		"invoice reconciliation vendor payments", "finance/reconcile")
	if resp.TotalMatched == 0 {
		t.Errorf("total_matched = 0, want > 0")
	}
}

// managedBackend names a managed vector backend, the server env that selects
// it, and whether its credentials are present. The e2e iterates every backend so
// a live run exercises Pinecone, Weaviate Cloud, and Qdrant Cloud independently
// and skips only the ones whose variables are absent. Self-embedding is left off
// (storage-only) so the registry computes vectors through the configured
// embedder, matching the pgvector form and keeping the assertion identical
// across backends.
type managedBackend struct {
	name    string
	env     []string
	present bool
}

// managedBackends returns all three managed vector backends with their wiring
// and credential presence, read from the environment.
func managedBackends() []managedBackend {
	return []managedBackend{
		{
			name: "pinecone",
			present: os.Getenv("PODIUM_PINECONE_API_KEY") != "" &&
				(os.Getenv("PODIUM_PINECONE_HOST") != "" || os.Getenv("PODIUM_PINECONE_INDEX") != ""),
			env: []string{
				"PODIUM_VECTOR_BACKEND=pinecone",
				"PODIUM_PINECONE_API_KEY=" + os.Getenv("PODIUM_PINECONE_API_KEY"),
				"PODIUM_PINECONE_HOST=" + os.Getenv("PODIUM_PINECONE_HOST"),
				"PODIUM_PINECONE_INDEX=" + os.Getenv("PODIUM_PINECONE_INDEX"),
				"PODIUM_PINECONE_NAMESPACE=" + os.Getenv("PODIUM_PINECONE_NAMESPACE"),
				"PODIUM_PINECONE_CONTROL_PLANE=" + os.Getenv("PODIUM_PINECONE_CONTROL_PLANE"),
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

// TestVectorSemanticSearch_ManagedThroughServer is the G-VEC-3 Release-lane form
// (managed backends). It iterates Pinecone, Weaviate Cloud, and Qdrant Cloud and
// runs one subtest per backend: each boots the standalone server wired to that
// backend, ingests the same artifacts, and asserts the same end-to-end semantic
// query returns the matching artifact at rank 1. The mock embedder supplies
// storage-only vectors so each assertion is identical to the pgvector form.
//
// Gating: PODIUM_LIVE_EXTERNAL=1 arms the suite; within it each backend subtest
// skips only when its own credentials are absent, so credentials for two of the
// three backends exercise those two and skip the third. A plain `go test ./...`
// with no credentials skips the parent cleanly. This mirrors the live-suite
// idiom in pkg/objectstore/s3_live_test.go and pkg/sign/sigstore_live_test.go.
//
// Spec: §9.1 (RegistrySearchProvider: managed built-ins via
// PODIUM_VECTOR_BACKEND)
func TestVectorSemanticSearch_ManagedThroughServer(t *testing.T) {
	t.Parallel()
	if os.Getenv("PODIUM_LIVE_EXTERNAL") != "1" {
		t.Skip("PODIUM_LIVE_EXTERNAL != 1; skipping managed-backend semantic-search e2e")
	}
	for _, b := range managedBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()
			if !b.present {
				t.Skipf("%s credentials absent; skipping (set its PODIUM_* variables to run it)", b.name)
			}
			emb := semanticMockEmbedder(t)
			env := append([]string{
				"HOME=" + t.TempDir(),
				"PODIUM_EMBEDDING_PROVIDER=openai",
				"OPENAI_API_KEY=sk-test",
				"PODIUM_OPENAI_BASE_URL=" + emb.URL,
			}, b.env...)

			srv := startServerArgs(t, env, "serve", "--standalone", "--layer-path", semanticRegistry(t))

			log := srv.log()
			if !strings.Contains(log, "vector="+b.name) {
				t.Fatalf("startup log missing 'vector=%s' (managed backend not wired):\n%s", b.name, log)
			}
			if strings.Contains(log, "vector search disabled") {
				t.Fatalf("vector search was disabled at startup:\n%s", log)
			}

			assertSemanticTopHit(t, srv.BaseURL,
				"invoice reconciliation vendor payments", "finance/reconcile")
		})
	}
}

// firstEnv returns the value of the first non-empty environment variable among
// keys, or "" when none is set.
func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// pgVectorIsolatedSchema creates a uniquely-named Postgres schema and returns a
// DSN whose search_path points the server's pgvector connection at it, so the
// server creates and queries vec_artifacts inside that schema rather than the
// shared public.vec_artifacts. The schema is dropped in cleanup. It skips the
// test when the database is unreachable, so the no-DSN and offline paths never
// fail. Isolating per test removes the cross-package race with the pkg/vector
// conformance suite, which owns public.vec_artifacts at dim=8 and TRUNCATEs it
// between sub-tests.
func pgVectorIsolatedSchema(t *testing.T, dsn string) string {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("pgvector DSN unusable (%v); skipping pgvector semantic-search e2e", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Skipf("Postgres unreachable (%v); skipping pgvector semantic-search e2e", err)
	}
	// Identifier is a fixed prefix plus crypto/rand hex, so direct interpolation
	// is safe (no user input, no quoting hazard).
	schema := "podium_e2e_vec_" + randHex(8)
	if _, err := db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		_ = db.Close()
		t.Skipf("create schema (%v); skipping pgvector semantic-search e2e", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
		_ = db.Close()
	})
	return pgWithSearchPath(dsn, schema)
}

// pgWithSearchPath appends a search_path connection option to a DSN so the
// server's pgvector connection creates and queries vec_artifacts inside the
// named schema. public stays on the path so the vector type (installed into
// public by CREATE EXTENSION) resolves. Mirrors withSearchPath in
// pgvector_depth_test.go; duplicated because that helper is in another package.
func pgWithSearchPath(dsn, schema string) string {
	opt := "-csearch_path=" + schema + ",public"
	if strings.Contains(dsn, "://") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		encoded := strings.ReplaceAll(opt, "=", "%3D")
		encoded = strings.ReplaceAll(encoded, ",", "%2C")
		return dsn + sep + "options=" + encoded
	}
	return dsn + " options='" + opt + "'"
}

// randHex returns n hex characters from crypto/rand for a unique schema name.
func randHex(n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		return "fallback0"
	}
	return hex.EncodeToString(b)[:n]
}
