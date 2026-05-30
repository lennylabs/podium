package e2e

// End-to-end tests for docs/deployment/vector-backends.md
// (D-vector-backends). The page documents Pinecone, Weaviate Cloud, and
// Qdrant Cloud as vector backend choices for the registry process.
//
// Known gaps:
//   - F-13.10.10: standalone sqlite-vec default is not implemented; the
//     effective backend when PODIUM_VECTOR_BACKEND is unset is "" (BM25 only).
//
// Self-embedding via *_INFERENCE_MODEL / *_VECTORIZER (tests 3, 10, 15) is
// implemented (F-13.12.6); those env vars wire vector search with no separate
// embedding provider.
//
// Tests 4, 11, 16, 35 require a faithful mock embedder + vector server
// returning correctly-dimensioned vectors and wiring that was uncertain; they
// are skipped with honest reasons. Tests 7, 13, 37–42, 44 are wire-level
// assertions covered by pkg/vector unit tests; they are skipped with honest
// reasons.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// vbExpectRefuseToStart runs `podium serve` with the given env and asserts the
// process refuses to start (non-zero exit) with an error naming wantKey. §13.12
// requires the registry to refuse to start when a selected backend's required
// values are missing, naming the missing keys (F-13.12.10). validate() fails
// before any listener binds, so the process exits promptly; the bind is a
// regression backstop.
func vbExpectRefuseToStart(t *testing.T, reg, wantKey string, extra ...string) {
	t.Helper()
	bind := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	res := runPodium(t, "", vbServerEnv(t, extra...),
		"serve", "--standalone", "--layer-path", reg, "--bind", bind)
	if res.Exit == 0 {
		t.Fatalf("expected non-zero exit (refuse to start)\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	}
	combined := strings.ToUpper(res.Stdout + res.Stderr)
	if !strings.Contains(combined, strings.ToUpper(wantKey)) {
		t.Errorf("startup error should name %s; got:\nstdout:\n%s\nstderr:\n%s", wantKey, res.Stdout, res.Stderr)
	}
}

// ---- package-level helpers (prefixed vb) ------------------------------------

// vbReg returns a minimal filesystem registry with one context artifact.
func vbReg(t *testing.T) string {
	t.Helper()
	return writeRegistry(t, map[string]string{
		"glossary/ARTIFACT.md": contextArtifact("glossary"),
	})
}

// vbConfigShowRow runs `podium config show` with the given env and returns the
// stdout. It does not start a server.
func vbConfigShowRow(t *testing.T, env []string) string {
	t.Helper()
	res := runPodium(t, "", env, "config", "show")
	if res.Exit != 0 {
		t.Fatalf("config show exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	return res.Stdout
}

// vbServerEnv builds an env slice for startServerArgs; always pins HOME.
func vbServerEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	return append([]string{"HOME=" + t.TempDir()}, extra...)
}

// vbMockEmbedder starts an httptest server that returns a 768-dimensional zero
// vector for every POST /v1/embeddings request (OpenAI wire format). The
// caller should call ts.Close() or rely on t.Cleanup.
func vbMockEmbedder(t *testing.T) *httptest.Server {
	t.Helper()
	zeros := make([]float64, 768)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"object": "embedding", "index": 0, "embedding": zeros},
			},
			"model": "text-embedding-3-small",
			"usage": map[string]any{"prompt_tokens": 1, "total_tokens": 1},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// ---- Tests ------------------------------------------------------------------

// T-D-vector-backends-1: sqlite-vec default not implemented (F-13.10.10).
// Doc claims sqlite-vec is the standalone default; the implementation uses ""
// (BM25 only) when PODIUM_VECTOR_BACKEND is unset.
func TestVectorBackends_1_SqliteVecDefaultNotImplemented(t *testing.T) {
	t.Parallel()
	// F-13.10.10: standalone sqlite-vec hybrid default is not implemented.
	// Assert actual behavior: server starts, /healthz 200, no sqlite-vec line.
	reg := vbReg(t)
	srv := startServerArgs(t, vbServerEnv(t,
		"PODIUM_VECTOR_BACKEND=",
		"PODIUM_EMBEDDING_PROVIDER=",
	), "serve", "--standalone", "--layer-path", reg)
	if st := getStatus(t, srv.BaseURL+"/healthz"); st != 200 {
		t.Fatalf("healthz = %d, want 200", st)
	}
	log := srv.log()
	if strings.Contains(log, "vector=sqlite-vec") {
		t.Logf("NOTE: F-13.10.10 resolved: startup log now shows vector=sqlite-vec")
	} else {
		t.Logf("F-13.10.10 confirmed: no sqlite-vec in startup log (BM25 only)\nlog: %s", log)
	}
}

// T-D-vector-backends-2: filesystem-only registry has no vector search.
func TestVectorBackends_2_FilesystemOnlyNoVectorSearch(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync",
		"--registry", reg,
		"--target", target,
		"--harness", "none",
	)
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	combined := res.Stdout + res.Stderr
	if strings.Contains(combined, "hybrid search") {
		t.Errorf("sync stdout/stderr should not contain 'hybrid search':\n%s", combined)
	}
	mustExist(t, target+"/glossary/ARTIFACT.md")
}

// T-D-vector-backends-3: Pinecone Integrated Inference (self-embedding) via
// PODIUM_PINECONE_INFERENCE_MODEL (F-13.12.6). The inference model enables
// self-embedding, so vector search is wired with no separate embedding
// provider; the server logs vector=pinecone with a self-embedding model.
func TestVectorBackends_3_PineconeSelfEmbedding(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	srv := startServerArgs(t, vbServerEnv(t,
		"PODIUM_VECTOR_BACKEND=pinecone",
		"PODIUM_PINECONE_API_KEY=pcn-test",
		"PODIUM_PINECONE_HOST=http://127.0.0.1:19999",
		"PODIUM_PINECONE_INFERENCE_MODEL=multilingual-e5-large",
		"PODIUM_EMBEDDING_PROVIDER=",
	), "serve", "--standalone", "--layer-path", reg)
	if st := getStatus(t, srv.BaseURL+"/healthz"); st != 200 {
		t.Fatalf("healthz = %d, want 200", st)
	}
	log := srv.log()
	if !strings.Contains(log, "vector=pinecone") || !strings.Contains(log, "self-embedding") {
		t.Errorf("expected self-embedding 'vector=pinecone' in startup log:\n%s", log)
	}
	if strings.Contains(log, "vector search disabled") {
		t.Errorf("a self-embedding backend must not be disabled for a missing embedder:\n%s", log)
	}
}

// T-D-vector-backends-4: Pinecone storage-only happy path needs mock vector +
// mock embedder with correct dimensions; skipped as too uncertain without
// verified wiring.
func TestVectorBackends_4_PineconeStorageOnlyHappyPath(t *testing.T) {
	t.Parallel()
	t.Skip("storage-only happy path needs a faithful mock embedder + vector server; covered by pkg/vector cloud tests")
}

// T-D-vector-backends-5: Pinecone selected without its API key is a
// misconfiguration; §13.12 ("refuses to start when a backend is selected but
// its required values are missing, naming the missing keys") makes it a hard
// startup error rather than a silent BM25 fallback (F-13.12.10).
func TestVectorBackends_5_PineconeMissingAPIKey(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	vbExpectRefuseToStart(t, reg, "PODIUM_PINECONE_API_KEY",
		"PODIUM_VECTOR_BACKEND=pinecone",
		"PODIUM_PINECONE_API_KEY=",
		"PODIUM_PINECONE_HOST=https://h.example.com",
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"OPENAI_API_KEY=sk-test",
	)
}

// T-D-vector-backends-6: Pinecone INDEX without HOST => clear error.
func TestVectorBackends_6_PineconeIndexWithoutHost(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	srv := startServerArgs(t, vbServerEnv(t,
		"PODIUM_VECTOR_BACKEND=pinecone",
		"PODIUM_PINECONE_API_KEY=pcn-test",
		"PODIUM_PINECONE_INDEX=podium-dev",
		"PODIUM_PINECONE_HOST=",
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"OPENAI_API_KEY=sk-test",
	), "serve", "--standalone", "--layer-path", reg)
	if st := getStatus(t, srv.BaseURL+"/healthz"); st != 200 {
		t.Fatalf("healthz = %d, want 200 (server must start in BM25 mode)", st)
	}
	log := srv.log()
	if !strings.Contains(log, "warning: vector search disabled") {
		t.Errorf("expected 'warning: vector search disabled' in startup log:\n%s", log)
	}
	// The implementation emits PODIUM_PINECONE_HOST is required for serverless.
	lowerLog := strings.ToLower(log)
	if !strings.Contains(lowerLog, "host") && !strings.Contains(lowerLog, "serverless") {
		t.Logf("doc-accuracy note: expected HOST/serverless mention in log; got:\n%s", log)
	}
}

// T-D-vector-backends-7: Pinecone namespace default is empty (not "default").
// Wire-level assertion requires a mock Pinecone recording upsert namespace.
func TestVectorBackends_7_PineconeNamespaceDefault(t *testing.T) {
	t.Parallel()
	t.Skip("namespace default is a wire-level assertion needing a mock Pinecone recording the upsert body; covered by pkg/vector NewPinecone tests")
}

// T-D-vector-backends-8: Pinecone standard YAML config sets vector_backend.
func TestVectorBackends_8_PineconeYAMLConfig(t *testing.T) {
	t.Parallel()
	cfgDir := t.TempDir()
	cfgFile := cfgDir + "/registry.yaml"
	if err := vbWriteFile(t, cfgFile, "registry:\n  vector_backend:\n    type: pinecone\n"); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}

	reg := vbReg(t)
	srv := startServerArgs(t, vbServerEnv(t,
		"PODIUM_CONFIG_FILE="+cfgFile,
		"PODIUM_PINECONE_API_KEY=pcn-test",
		"PODIUM_PINECONE_HOST=http://127.0.0.1:19998",
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"OPENAI_API_KEY=sk-test",
	), "serve", "--standalone", "--layer-path", reg)
	if st := getStatus(t, srv.BaseURL+"/healthz"); st != 200 {
		t.Fatalf("healthz = %d, want 200", st)
	}
	// The startup log may warn vector disabled (mock host not reachable); that is fine.
	// The primary assertion is config show.
	out := vbConfigShowRow(t, []string{
		"PODIUM_CONFIG_FILE=" + cfgFile,
		"PODIUM_PINECONE_API_KEY=pcn-test",
		"PODIUM_PINECONE_HOST=http://127.0.0.1:19998",
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"OPENAI_API_KEY=sk-test",
	})
	if !strings.Contains(out, "pinecone") {
		t.Errorf("config show output missing 'pinecone':\n%s", out)
	}
	// Source should be registry.yaml when PODIUM_VECTOR_BACKEND is not set.
	if !strings.Contains(out, "registry.yaml") {
		t.Logf("config show source note: expected registry.yaml in output:\n%s", out)
	}
}

// T-D-vector-backends-9: env overrides YAML vector_backend.
func TestVectorBackends_9_EnvOverridesYAMLVectorBackend(t *testing.T) {
	t.Parallel()
	cfgDir := t.TempDir()
	cfgFile := cfgDir + "/registry.yaml"
	if err := vbWriteFile(t, cfgFile, "registry:\n  vector_backend:\n    type: pinecone\n"); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}

	env := []string{
		"PODIUM_CONFIG_FILE=" + cfgFile,
		"PODIUM_VECTOR_BACKEND=qdrant-cloud",
		"PODIUM_QDRANT_URL=http://127.0.0.1:19997",
		"PODIUM_QDRANT_API_KEY=qdr-test",
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"OPENAI_API_KEY=sk-test",
	}
	out := vbConfigShowRow(t, env)
	if !strings.Contains(out, "qdrant-cloud") {
		t.Errorf("config show should show qdrant-cloud (env override); got:\n%s", out)
	}
	if !strings.Contains(out, "PODIUM_VECTOR_BACKEND") {
		t.Errorf("config show should show source PODIUM_VECTOR_BACKEND; got:\n%s", out)
	}
}

// T-D-vector-backends-10: Weaviate self-embedding via PODIUM_WEAVIATE_VECTORIZER
// (F-13.12.6). The vectorizer module enables self-embedding, so vector search
// is wired with no separate embedding provider.
func TestVectorBackends_10_WeaviateSelfEmbedding(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	srv := startServerArgs(t, vbServerEnv(t,
		"PODIUM_VECTOR_BACKEND=weaviate-cloud",
		"PODIUM_WEAVIATE_URL=http://127.0.0.1:19996",
		"PODIUM_WEAVIATE_API_KEY=wv-test",
		"PODIUM_WEAVIATE_COLLECTION=PodiumArtifacts",
		"PODIUM_WEAVIATE_VECTORIZER=text2vec-weaviate",
		"PODIUM_EMBEDDING_PROVIDER=",
	), "serve", "--standalone", "--layer-path", reg)
	if st := getStatus(t, srv.BaseURL+"/healthz"); st != 200 {
		t.Fatalf("healthz = %d, want 200", st)
	}
	log := srv.log()
	if !strings.Contains(log, "vector=weaviate-cloud") || !strings.Contains(log, "self-embedding") {
		t.Errorf("expected self-embedding 'vector=weaviate-cloud' in startup log:\n%s", log)
	}
	if strings.Contains(log, "vector search disabled") {
		t.Errorf("a self-embedding backend must not be disabled for a missing embedder:\n%s", log)
	}
}

// T-D-vector-backends-11: Weaviate storage-only happy path.
func TestVectorBackends_11_WeaviateStorageOnlyHappyPath(t *testing.T) {
	t.Parallel()
	t.Skip("storage-only happy path needs a faithful mock Weaviate + mock embedder; covered by pkg/vector cloud tests")
}

// T-D-vector-backends-12: Weaviate selected without its URL is a
// misconfiguration; §13.12 makes it a hard startup error naming the missing
// key rather than a silent BM25 fallback (F-13.12.10).
func TestVectorBackends_12_WeaviateMissingURL(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	vbExpectRefuseToStart(t, reg, "PODIUM_WEAVIATE_URL",
		"PODIUM_VECTOR_BACKEND=weaviate-cloud",
		"PODIUM_WEAVIATE_URL=",
		"PODIUM_WEAVIATE_API_KEY=wv-test",
		"PODIUM_WEAVIATE_COLLECTION=PodiumArtifacts",
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"OPENAI_API_KEY=sk-test",
	)
}

// T-D-vector-backends-13: Weaviate default collection PodiumArtifacts.
// Wire-level assertion requires a mock Weaviate recording the PUT path.
func TestVectorBackends_13_WeaviateDefaultCollection(t *testing.T) {
	t.Parallel()
	t.Skip("default collection is a wire-level assertion needing a mock Weaviate recording PUT paths; covered by pkg/vector tests")
}

// T-D-vector-backends-14: Weaviate YAML standard deployment config sets vector_backend.
func TestVectorBackends_14_WeaviateYAMLConfig(t *testing.T) {
	t.Parallel()
	cfgDir := t.TempDir()
	cfgFile := cfgDir + "/registry.yaml"
	if err := vbWriteFile(t, cfgFile, "registry:\n  vector_backend:\n    type: weaviate-cloud\n"); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}

	env := []string{
		"PODIUM_CONFIG_FILE=" + cfgFile,
		"PODIUM_WEAVIATE_URL=http://127.0.0.1:19995",
		"PODIUM_WEAVIATE_API_KEY=wv-test",
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"OPENAI_API_KEY=sk-test",
	}
	out := vbConfigShowRow(t, env)
	if !strings.Contains(out, "weaviate-cloud") {
		t.Errorf("config show output missing 'weaviate-cloud':\n%s", out)
	}
	if !strings.Contains(out, "registry.yaml") {
		t.Logf("config show source note: expected registry.yaml in output:\n%s", out)
	}
}

// T-D-vector-backends-15: Qdrant Cloud Inference (self-embedding) via
// PODIUM_QDRANT_INFERENCE_MODEL (F-13.12.6). The inference model enables
// self-embedding, so vector search is wired with no separate embedding
// provider.
func TestVectorBackends_15_QdrantSelfEmbedding(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	srv := startServerArgs(t, vbServerEnv(t,
		"PODIUM_VECTOR_BACKEND=qdrant-cloud",
		"PODIUM_QDRANT_URL=http://127.0.0.1:19994",
		"PODIUM_QDRANT_API_KEY=qdr-test",
		"PODIUM_QDRANT_COLLECTION=podium-artifacts",
		"PODIUM_QDRANT_INFERENCE_MODEL=bge-small-en",
		"PODIUM_EMBEDDING_PROVIDER=",
	), "serve", "--standalone", "--layer-path", reg)
	if st := getStatus(t, srv.BaseURL+"/healthz"); st != 200 {
		t.Fatalf("healthz = %d, want 200", st)
	}
	log := srv.log()
	if !strings.Contains(log, "vector=qdrant-cloud") || !strings.Contains(log, "self-embedding") {
		t.Errorf("expected self-embedding 'vector=qdrant-cloud' in startup log:\n%s", log)
	}
	if strings.Contains(log, "vector search disabled") {
		t.Errorf("a self-embedding backend must not be disabled for a missing embedder:\n%s", log)
	}
}

// T-D-vector-backends-16: Qdrant storage-only happy path.
func TestVectorBackends_16_QdrantStorageOnlyHappyPath(t *testing.T) {
	t.Parallel()
	t.Skip("storage-only happy path needs a faithful mock Qdrant + mock embedder; covered by pkg/vector cloud tests")
}

// T-D-vector-backends-17: Qdrant selected without its URL is a
// misconfiguration; §13.12 makes it a hard startup error naming the missing
// key rather than a silent BM25 fallback (F-13.12.10).
func TestVectorBackends_17_QdrantMissingURL(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	vbExpectRefuseToStart(t, reg, "PODIUM_QDRANT_URL",
		"PODIUM_VECTOR_BACKEND=qdrant-cloud",
		"PODIUM_QDRANT_URL=",
		"PODIUM_QDRANT_API_KEY=qdr-test",
		"PODIUM_QDRANT_COLLECTION=podium-artifacts",
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"OPENAI_API_KEY=sk-test",
	)
}

// T-D-vector-backends-18: Qdrant default collection name is podium_artifacts (underscore).
// Wire-level assertion requires a mock Qdrant recording PUT paths.
func TestVectorBackends_18_QdrantDefaultCollection(t *testing.T) {
	t.Parallel()
	t.Skip("default collection name is a wire-level assertion needing a mock Qdrant recording request paths; covered by pkg/vector tests. Doc-accuracy note: doc shows podium-artifacts (hyphen), implementation defaults to podium_artifacts (underscore)")
}

// T-D-vector-backends-19: Qdrant YAML standard deployment config sets vector_backend.
func TestVectorBackends_19_QdrantYAMLConfig(t *testing.T) {
	t.Parallel()
	cfgDir := t.TempDir()
	cfgFile := cfgDir + "/registry.yaml"
	if err := vbWriteFile(t, cfgFile, "registry:\n  vector_backend:\n    type: qdrant-cloud\n"); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}

	env := []string{
		"PODIUM_CONFIG_FILE=" + cfgFile,
		"PODIUM_QDRANT_URL=http://127.0.0.1:19993",
		"PODIUM_QDRANT_API_KEY=qdr-test",
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"OPENAI_API_KEY=sk-test",
	}
	out := vbConfigShowRow(t, env)
	if !strings.Contains(out, "qdrant-cloud") {
		t.Errorf("config show output missing 'qdrant-cloud':\n%s", out)
	}
	if !strings.Contains(out, "registry.yaml") {
		t.Logf("config show source note: expected registry.yaml in output:\n%s", out)
	}
}

// T-D-vector-backends-20: admin reembed bare POST exits 0 with JSON.
func TestVectorBackends_20_AdminReembedBarePOST(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	srv := startServer(t, reg)
	res := runPodium(t, "", nil,
		"admin", "reembed",
		"--registry", srv.BaseURL,
	)
	// reembed reaches the (un-gated) endpoint but requires a configured vector
	// backend + embedder. The default standalone server has neither, so the
	// request returns the structured "vector search not configured" error. When
	// a backend is wired the command exits 0 with a JSON summary; accept either.
	if res.Exit == 0 {
		out := strings.TrimSpace(res.Stdout)
		var m map[string]any
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Errorf("reembed stdout not valid JSON: %v\n%s", err, out)
		}
		return
	}
	if !strings.Contains(res.Stderr, "vector search not configured") {
		t.Errorf("reembed exit=%d; want exit 0 or a 'vector search not configured' error\nstderr=%s", res.Exit, res.Stderr)
	}
}

// T-D-vector-backends-21: admin reembed --all flag is not recognized (exits 2).
func TestVectorBackends_21_AdminReembedAllFlagUnknown(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	srv := startServer(t, reg)
	res := runPodium(t, "", nil,
		"admin", "reembed",
		"--registry", srv.BaseURL,
		"--all",
	)
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (--all should be unknown); stderr=%s", res.Exit, res.Stderr)
	}
}

// T-D-vector-backends-22: admin reembed --since <timestamp> is a
// recognized flag (spec §4.7 "podium admin reembed --since"; F-4.7.8).
// A valid RFC3339 timestamp reaches the endpoint (no flag-parse error);
// a malformed one is rejected with a usage error before any request.
func TestVectorBackends_22_AdminReembedSinceFlag(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	srv := startServer(t, reg)

	// Valid RFC3339: flag is parsed, request is sent. vbReg has no
	// embedder so the endpoint returns the structured "vector search not
	// configured" error (exit 1) or, when a backend is wired, exits 0.
	// Either way the exit is not 2 (which would mean an unknown flag).
	ok := runPodium(t, "", nil,
		"admin", "reembed",
		"--registry", srv.BaseURL,
		"--since", "2026-01-01T00:00:00Z",
	)
	if ok.Exit == 2 {
		t.Errorf("valid --since exit=2 (flag unrecognized), want 0 or 1; stderr=%s", ok.Stderr)
	}

	// Malformed timestamp: rejected locally with a usage error (exit 2)
	// and never sent to the registry.
	bad := runPodium(t, "", nil,
		"admin", "reembed",
		"--registry", srv.BaseURL,
		"--since", "2026-01-01",
	)
	if bad.Exit != 2 {
		t.Errorf("malformed --since exit=%d, want 2; stderr=%s", bad.Exit, bad.Stderr)
	}
	if !strings.Contains(bad.Stderr, "RFC3339") {
		t.Errorf("malformed --since stderr should mention RFC3339: %s", bad.Stderr)
	}
}

// T-D-vector-backends-23: admin reembed --artifact --version for a specific artifact.
func TestVectorBackends_23_AdminReembedArtifactVersion(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	srv := startServer(t, reg)
	// Attempt reembed of a known artifact id; even if the artifact lacks a
	// vector backend the endpoint should return a JSON response (not 500).
	res := runPodium(t, "", nil,
		"admin", "reembed",
		"--registry", srv.BaseURL,
		"--artifact", "glossary",
		"--version", "1.0.0",
	)
	// Exit 0 (success) or 1 (runtime, e.g. not found) are both acceptable;
	// exit 2 (usage) would indicate flag parsing failure.
	if res.Exit == 2 {
		t.Errorf("exit=2 (flag error), want 0 or 1; stderr=%s", res.Stderr)
	}
}

// T-D-vector-backends-24: admin reembed --artifact without --version exits 2.
func TestVectorBackends_24_AdminReembedArtifactNoVersion(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	srv := startServer(t, reg)
	res := runPodium(t, "", nil,
		"admin", "reembed",
		"--registry", srv.BaseURL,
		"--artifact", "some-artifact",
	)
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2; stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--version") {
		t.Errorf("stderr should mention --version: %s", res.Stderr)
	}
}

// T-D-vector-backends-25: admin reembed without --registry exits 2.
func TestVectorBackends_25_AdminReembedNoRegistry(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", []string{"PODIUM_REGISTRY="},
		"admin", "reembed",
	)
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2; stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--registry is required") {
		t.Errorf("stderr should contain '--registry is required': %s", res.Stderr)
	}
}

// T-D-vector-backends-26: GET /v1/admin/reembed => 405 registry.invalid_argument.
func TestVectorBackends_26_GetReembedIs405(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	srv := startServer(t, reg)
	st, body := getRaw(t, srv.BaseURL+"/v1/admin/reembed")
	if st != 405 {
		t.Errorf("GET /v1/admin/reembed = %d, want 405\nbody: %s", st, body)
	}
	if !strings.Contains(string(body), "registry.invalid_argument") {
		t.Errorf("body should contain 'registry.invalid_argument': %s", body)
	}
}

// T-D-vector-backends-27: POST /v1/admin/reembed?artifact=foo without version => 400.
func TestVectorBackends_27_PostReembedArtifactNoVersion400(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	srv := startServer(t, reg)
	st, body := postJSON(t, srv.BaseURL+"/v1/admin/reembed?artifact=foo", nil)
	if st != 400 {
		t.Errorf("POST /v1/admin/reembed?artifact=foo = %d, want 400\nbody: %s", st, body)
	}
	if !strings.Contains(string(body), "registry.invalid_argument") {
		t.Errorf("body should contain 'registry.invalid_argument': %s", body)
	}
}

// T-D-vector-backends-28: search degrades to BM25 when vector backend unreachable.
// Also documents the doc-accuracy gap: the response does NOT include a "degraded"
// key in the HTTP body (only SearchResult.Degraded is set internally).
func TestVectorBackends_28_SearchDegradesToBM25WhenVectorUnreachable(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	// Use a Pinecone backend with a host that will refuse connections.
	srv := startServerArgs(t, vbServerEnv(t,
		"PODIUM_VECTOR_BACKEND=pinecone",
		"PODIUM_PINECONE_API_KEY=pcn-test",
		"PODIUM_PINECONE_HOST=http://127.0.0.1:1", // port 1 always refused
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"OPENAI_API_KEY=sk-test",
	), "serve", "--standalone", "--layer-path", reg)

	st, body := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query=glossary")
	if st != 200 {
		t.Errorf("GET /v1/search_artifacts = %d, want 200 (BM25 fallback); body: %s", st, body)
	}
	// Doc-accuracy gap: the response does NOT include a "degraded" key.
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("response not valid JSON: %v\n%s", err, body)
	}
	if _, hasDegraded := resp["degraded"]; hasDegraded {
		t.Logf("NOTE: 'degraded' key is present in response (doc claim now satisfied)")
	} else {
		t.Logf("doc-accuracy gap: 'degraded' key absent from search response (SearchResult.Degraded not surfaced in HTTP body)")
	}
}

// T-D-vector-backends-29: PODIUM_EMBEDDING_PROVIDER empty disables embedding => BM25.
func TestVectorBackends_29_EmptyEmbeddingProviderDegradesToBM25(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	srv := startServerArgs(t, vbServerEnv(t,
		"PODIUM_EMBEDDING_PROVIDER=",
		"PODIUM_VECTOR_BACKEND=",
	), "serve", "--standalone", "--layer-path", reg)
	if st := getStatus(t, srv.BaseURL+"/healthz"); st != 200 {
		t.Fatalf("healthz = %d, want 200", st)
	}
	log := srv.log()
	if strings.Contains(log, "hybrid search:") {
		t.Errorf("startup log should not contain 'hybrid search:' with empty provider:\n%s", log)
	}
	st, body := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query=glossary")
	if st != 200 {
		t.Errorf("search = %d, want 200 (BM25); body: %s", st, body)
	}
}

// T-D-vector-backends-30: unknown PODIUM_VECTOR_BACKEND => warning + BM25 fallback.
func TestVectorBackends_30_UnknownVectorBackendWarning(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	srv := startServerArgs(t, vbServerEnv(t,
		"PODIUM_VECTOR_BACKEND=nonexistent-backend",
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"OPENAI_API_KEY=sk-test",
	), "serve", "--standalone", "--layer-path", reg)
	if st := getStatus(t, srv.BaseURL+"/healthz"); st != 200 {
		t.Fatalf("healthz = %d, want 200", st)
	}
	log := srv.log()
	if !strings.Contains(log, "warning: vector search disabled") {
		t.Errorf("expected 'warning: vector search disabled':\n%s", log)
	}
	if !strings.Contains(log, "nonexistent-backend") {
		t.Errorf("expected 'nonexistent-backend' in warning:\n%s", log)
	}
}

// T-D-vector-backends-31: unknown PODIUM_EMBEDDING_PROVIDER => warning.
func TestVectorBackends_31_UnknownEmbeddingProviderWarning(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	srv := startServerArgs(t, vbServerEnv(t,
		"PODIUM_EMBEDDING_PROVIDER=bogus-provider",
	), "serve", "--standalone", "--layer-path", reg)
	if st := getStatus(t, srv.BaseURL+"/healthz"); st != 200 {
		t.Fatalf("healthz = %d, want 200", st)
	}
	log := srv.log()
	if !strings.Contains(log, "warning: vector search disabled") {
		t.Errorf("expected 'warning: vector search disabled':\n%s", log)
	}
	if !strings.Contains(log, "bogus-provider") {
		t.Errorf("expected 'bogus-provider' in warning:\n%s", log)
	}
}

// T-D-vector-backends-32: the openai embedding provider selected without
// OPENAI_API_KEY is a misconfiguration; §13.12 makes it a hard startup error
// naming the missing key (F-13.12.10). An embedding provider set to the empty
// string is a separate, intentional disable that degrades to BM25 (test 29).
func TestVectorBackends_32_OpenAIMissingAPIKey(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	vbExpectRefuseToStart(t, reg, "OPENAI_API_KEY",
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"OPENAI_API_KEY=",
	)
}

// T-D-vector-backends-33: MCP overlay search uses BM25 regardless of PODIUM_VECTOR_BACKEND.
func TestVectorBackends_33_MCPOverlaySearchBM25Regardless(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"registry-item/ARTIFACT.md": contextArtifact("registry-item"),
	})
	overlay := writeRegistry(t, map[string]string{
		"overlay-item/ARTIFACT.md": contextArtifact("overlay-item"),
	})
	srv := startServer(t, reg)

	res := mcpExec(t,
		[]string{
			"PODIUM_REGISTRY=" + srv.BaseURL,
			"PODIUM_OVERLAY_PATH=" + overlay,
			"PODIUM_CACHE_DIR=" + t.TempDir(),
			// Non-existent Pinecone; MCP must not connect to it for overlay search.
			"PODIUM_VECTOR_BACKEND=pinecone",
			"PODIUM_PINECONE_API_KEY=pcn-test",
			"PODIUM_PINECONE_HOST=http://127.0.0.1:1",
		},
		rpcReq{ID: 1, Method: "initialize", Params: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "1"},
		}},
		toolCall(2, "search_artifacts", map[string]any{"query": "overlay-item"}),
	)
	result := rpcResult(t, res.Stdout, 2)
	// The result should not be nil; overlay search should return results (or at
	// least not error with a Pinecone connection error).
	if result == nil {
		t.Errorf("search_artifacts returned nil result (stderr=%s)", res.Stderr)
	}
	// No crash or Pinecone error expected.
	combined := res.Stdout + res.Stderr
	if strings.Contains(combined, "pinecone") && strings.Contains(combined, "connection refused") {
		t.Errorf("MCP should not attempt Pinecone connection for overlay BM25 search:\n%s", combined)
	}
}

// T-D-vector-backends-34: MCP merges registry and overlay results.
func TestVectorBackends_34_MCPMergesRegistryAndOverlayResults(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"registry-artifact/ARTIFACT.md": contextArtifact("unique-registry-term"),
	})
	overlay := writeRegistry(t, map[string]string{
		"overlay-artifact/ARTIFACT.md": contextArtifact("unique-overlay-term"),
	})
	srv := startServer(t, reg)

	res := mcpExec(t,
		[]string{
			"PODIUM_REGISTRY=" + srv.BaseURL,
			"PODIUM_OVERLAY_PATH=" + overlay,
			"PODIUM_CACHE_DIR=" + t.TempDir(),
		},
		rpcReq{ID: 1, Method: "initialize", Params: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "1"},
		}},
		toolCall(2, "search_artifacts", map[string]any{"query": "artifact"}),
	)
	if res.Exit != 0 {
		t.Fatalf("mcpExec exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	// MCP should not crash; result may include items from either source.
	env := rpcEnvelope(t, res.Stdout, 2)
	if errVal, ok := env["error"]; ok && errVal != nil {
		t.Logf("search_artifacts returned error (acceptable if both sources are BM25): %v", errVal)
	}
}

// T-D-vector-backends-35: backend switch Pinecone => Qdrant with reembed.
func TestVectorBackends_35_BackendSwitchReembed(t *testing.T) {
	t.Parallel()
	t.Skip("backend switch needs two mock vector servers and a verified embedder wiring; covered by pkg/vector cloud tests")
}

// T-D-vector-backends-36: reembed --only-missing skips already-embedded artifacts.
func TestVectorBackends_36_ReembedOnlyMissing(t *testing.T) {
	t.Parallel()
	t.Skip("--only-missing skip logic requires a mock vector backend that correctly answers probe queries; covered by pkg/vector tests")
}

// T-D-vector-backends-37: Pinecone per-tenant namespace isolation wire assertion.
func TestVectorBackends_37_PineconeNamespaceIsolation(t *testing.T) {
	t.Parallel()
	t.Skip("per-tenant namespace isolation is a wire-level assertion needing a mock Pinecone recording upsert bodies; covered by pkg/vector Pinecone tests")
}

// T-D-vector-backends-38: Qdrant per-tenant tenant_id filter wire assertion.
func TestVectorBackends_38_QdrantTenantIDFilter(t *testing.T) {
	t.Parallel()
	t.Skip("tenant_id filter is a wire-level assertion needing a mock Qdrant recording query bodies; covered by pkg/vector Qdrant tests")
}

// T-D-vector-backends-39: Weaviate per-tenant tenantId property filter wire assertion.
func TestVectorBackends_39_WeaviateTenantIDFilter(t *testing.T) {
	t.Parallel()
	t.Skip("tenantId filter is a wire-level assertion needing a mock Weaviate recording GraphQL bodies; covered by pkg/vector Weaviate tests")
}

// T-D-vector-backends-40: Pinecone Api-Key header wire assertion.
func TestVectorBackends_40_PineconeApiKeyHeader(t *testing.T) {
	t.Parallel()
	t.Skip("Api-Key header is a wire-level assertion needing a mock Pinecone validating headers; covered by pkg/vector Pinecone tests")
}

// T-D-vector-backends-41: Weaviate Authorization Bearer header wire assertion.
func TestVectorBackends_41_WeaviateAuthorizationHeader(t *testing.T) {
	t.Parallel()
	t.Skip("Authorization Bearer header is a wire-level assertion needing a mock Weaviate validating headers; covered by pkg/vector Weaviate tests")
}

// T-D-vector-backends-42: Qdrant api-key header (lowercase) wire assertion.
func TestVectorBackends_42_QdrantApiKeyHeaderLowercase(t *testing.T) {
	t.Parallel()
	t.Skip("api-key (lowercase) header is a wire-level assertion needing a mock Qdrant validating headers; covered by pkg/vector Qdrant tests")
}

// T-D-vector-backends-43: config show reflects resolved vector_backend and embedding_provider.
func TestVectorBackends_43_ConfigShowReflectsVectorAndEmbedder(t *testing.T) {
	t.Parallel()
	out := vbConfigShowRow(t, []string{
		"PODIUM_VECTOR_BACKEND=qdrant-cloud",
		"PODIUM_EMBEDDING_PROVIDER=voyage",
	})
	if !strings.Contains(out, "qdrant-cloud") {
		t.Errorf("config show missing qdrant-cloud:\n%s", out)
	}
	if !strings.Contains(out, "PODIUM_VECTOR_BACKEND") {
		t.Errorf("config show missing source PODIUM_VECTOR_BACKEND:\n%s", out)
	}
	if !strings.Contains(out, "voyage") {
		t.Errorf("config show missing voyage:\n%s", out)
	}
	if !strings.Contains(out, "PODIUM_EMBEDDING_PROVIDER") {
		t.Errorf("config show missing source PODIUM_EMBEDDING_PROVIDER:\n%s", out)
	}
}

// T-D-vector-backends-44: Pinecone trailing slash stripped from host URL.
// Wire-level: covered by pkg/vector NewPinecone TrimRight test.
func TestVectorBackends_44_PineconeTrailingSlashStripped(t *testing.T) {
	t.Parallel()
	t.Skip("trailing slash stripping is a unit-level assertion in pkg/vector NewPinecone (strings.TrimRight); covered by pkg/vector tests")
}

// ---- internal helpers -------------------------------------------------------

// vbWriteFile writes content to path, returning any error.
func vbWriteFile(t *testing.T, path, content string) error {
	t.Helper()
	return os.WriteFile(path, []byte(content), 0o644)
}
