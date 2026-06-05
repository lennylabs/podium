package e2e

// End-to-end hybrid-search journeys that isolate the semantic (vector)
// contribution through the standalone server's HTTP surface. They cover a
// query-time embedder failure that degrades to BM25 and recovers, and RRF
// ranking a paraphrased query whose words are absent from the target.
//
// The existing vector_semantic_search_test.go proves a lexically-matching
// artifact ranks first whether the vector path contributes or silently
// degrades. These tests go further: the semantic target shares no words with
// the query, while a separate distractor shares one neutral word with it. When
// the topic-routed embedder is live the vector half lifts the semantic target
// above the lexical distractor through reciprocal rank fusion; when the embedder
// errors or is unset the ranking falls back to BM25, the distractor ranks first,
// and the zero-overlap target demotes. Search keeps returning the distractor
// throughout, so the degrade is observable without a silent empty set, and the
// change in which artifact ranks first is the proof that the vector path, and
// not lexical overlap, produced the semantic hit.
//
// Both tests boot pgvector against the CI Postgres container (PODIUM_POSTGRES_DSN)
// on an isolated ephemeral schema. pgvector is collocated storage-only, so the
// registry computes vectors through an embedding provider; a topic-routed mock
// embedder keeps ingest and query embedding off the real OpenAI API while
// producing vectors that group a concept's paraphrases together regardless of
// shared words.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	_ "github.com/lib/pq"
)

// topicVector maps a text to a deterministic semanticDim-element vector by the
// concept it belongs to rather than by its words. Each concept owns one
// coordinate bucket; every text routed to that concept points at the same
// bucket, so two phrasings of the same concept that share no words still land at
// cosine distance ~0. Texts that match no concept fall back to a per-word hash
// projection so a lexical distractor stays far from every concept bucket. This
// models what a real embedder does semantically (paraphrases cluster) while
// staying deterministic and offline.
//
// Concept A's triggers are split so an artifact and a query route to the same
// concept through different substrings: the target says "invoice reconciliation
// / vendor payments"; the concept-A query says "remit settlement ... vendor" and
// shares no content word with the target except the topic itself.
func topicVector(text string) []float64 {
	lower := strings.ToLower(text)
	concepts := []struct {
		triggers []string
		bucket   int
	}{
		// Concept A: settling money owed between a buyer and a seller.
		{triggers: []string{"invoice reconciliation", "vendor payments", "remit settlement", "amounts owed", "accounts payable"}, bucket: 100},
		// Concept B: bringing a new employee on board.
		{triggers: []string{"employee onboarding", "new hire orientation", "welcoming staff"}, bucket: 500},
		// Concept C: running containerized workloads.
		{triggers: []string{"kubernetes deployment", "pod autoscaling", "container orchestration"}, bucket: 900},
	}
	v := make([]float64, semanticDim)
	matched := false
	for _, c := range concepts {
		for _, tr := range c.triggers {
			if strings.Contains(lower, tr) {
				v[c.bucket] += 1
				matched = true
			}
		}
	}
	if matched {
		return v
	}
	// No concept matched: fall back to the bag-of-words projection so a lexical
	// distractor is far from every concept bucket and never accidentally ranks as
	// a concept member.
	return semanticVector(text)
}

// controllableEmbedder is a topic-routed mock OpenAI embedder whose responses can
// be flipped to HTTP 429 at runtime via an atomic flag, modeling a transient
// provider outage during live queries. When healthy it returns one
// topicVector per input text in input order.
type controllableEmbedder struct {
	srv  *httptest.Server
	fail atomic.Bool // when true, every /v1/embeddings call returns 429
}

// newControllableEmbedder starts the mock and registers cleanup.
func newControllableEmbedder(t *testing.T) *controllableEmbedder {
	t.Helper()
	ce := &controllableEmbedder{}
	ce.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ce.fail.Load() {
			// 429 Too Many Requests, the canonical transient embedding-provider
			// rate-limit signal. The OpenAI provider classifies a non-2xx as an
			// error, which the search path treats as a vector-half failure and
			// degrades to BM25.
			http.Error(w, `{"error":{"message":"rate limited","type":"rate_limit"}}`, http.StatusTooManyRequests)
			return
		}
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
			data[i] = map[string]any{"object": "embedding", "index": i, "embedding": topicVector(text)}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   data,
			"model":  req.Model,
			"usage":  map[string]any{"prompt_tokens": 1, "total_tokens": 1},
		})
	}))
	t.Cleanup(ce.srv.Close)
	return ce
}

func (ce *controllableEmbedder) url() string { return ce.srv.URL }
func (ce *controllableEmbedder) breakIt()    { ce.fail.Store(true) }
func (ce *controllableEmbedder) restore()    { ce.fail.Store(false) }

// hybridQuery is the shared query: it routes to concept A through "remit
// settlement" / "vendor" (so the vector half ranks the concept-A target) and
// shares the neutral word "quarterly" with the lexical distractor (so BM25
// always returns at least the distractor). It shares no content word with the
// concept-A target description.
const hybridQuery = "remit settlement to the quarterly vendor"

// hybridTarget is the concept-A artifact the query targets semantically.
const hybridTarget = "finance/reconcile"

// hybridLexicalAnchor is the distractor that shares the neutral word "quarterly"
// with the query, so a BM25-only ranking surfaces it (no silent empty set) and
// ranks it first once the vector half is gone.
const hybridLexicalAnchor = "ops/quarterly"

// paraphraseRegistry stages a concept-A target whose description shares no word
// with the query, a lexical-anchor distractor that shares the neutral word
// "quarterly" with the query, and two off-topic artifacts anchoring other
// semantic regions. The query's only lexical match is the anchor; its only
// strong semantic match is the target.
func paraphraseRegistry(t *testing.T) string {
	t.Helper()
	return writeRegistry(t, map[string]string{
		hybridTarget + "/ARTIFACT.md":        contextArtifact("invoice reconciliation matching vendor payments against purchase orders"),
		hybridLexicalAnchor + "/ARTIFACT.md": contextArtifact("quarterly compliance audit schedule and signoff"),
		"hr/onboarding/ARTIFACT.md":          contextArtifact("employee onboarding checklist for new hire orientation"),
		"infra/deploy/ARTIFACT.md":           contextArtifact("kubernetes deployment rollout and pod autoscaling"),
	})
}

// searchIDs drives search_artifacts over HTTP for a query and returns the
// ordered result ids plus the raw decoded response, so a test can compare rank
// order across embedder states and assert on the envelope.
func searchIDs(t *testing.T, baseURL, query string) ([]string, semanticSearchResponse) {
	t.Helper()
	var resp semanticSearchResponse
	getJSON(t, baseURL+"/v1/search_artifacts?query="+queryEscape(query)+"&top_k=10", &resp)
	ids := make([]string, len(resp.Results))
	for i, r := range resp.Results {
		ids[i] = r.ID
	}
	return ids, resp
}

// indexOf returns the position of id in ids, or -1.
func indexOf(ids []string, id string) int {
	for i, v := range ids {
		if v == id {
			return i
		}
	}
	return -1
}

// startHybridPgVector boots a standalone server with pgvector on an isolated
// schema and the given embedder, ingesting the paraphrase registry. It asserts
// the vector backend is wired (a silent degrade to BM25 would mask the path).
func startHybridPgVector(t *testing.T, scopedDSN, embURL, reg string) *serverProc {
	t.Helper()
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_VECTOR_BACKEND=pgvector",
		"PODIUM_PGVECTOR_DSN=" + scopedDSN,
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"OPENAI_API_KEY=sk-test",
		"PODIUM_OPENAI_BASE_URL=" + embURL,
	}, "serve", "--standalone", "--layer-path", reg)
	log := srv.log()
	if !strings.Contains(log, "vector=pgvector") {
		t.Fatalf("startup log missing 'vector=pgvector' (vector backend not wired):\n%s", log)
	}
	if strings.Contains(log, "vector search disabled") {
		t.Fatalf("vector search was disabled at startup:\n%s", log)
	}
	return srv
}

// TestVectorHybrid_DegradesToBM25AndRecovers. It boots pgvector
// with a controllable topic-routed embedder, runs a baseline query whose only
// lexical match is a distractor and whose semantic target shares no query word
// so only the vector half can rank it first, flips the embedder to 429 and
// asserts search keeps returning ranked BM25 results (the distractor) with the
// target demoted off rank 1, then restores the embedder and asserts semantic
// fusion resumes and the target returns to rank 1 (the ranking changes on the
// same query).
//
// Gating: PODIUM_POSTGRES_DSN[_VECTOR]. With no DSN the test skips cleanly.
//
// Spec: §4.7 (hybrid retrieval; a failed vector half degrades to BM25 without
// erroring and the artifact stays discoverable via BM25).
func TestVectorHybrid_DegradesToBM25AndRecovers(t *testing.T) {
	t.Parallel()
	dsn := firstEnv("PODIUM_POSTGRES_DSN_VECTOR", "PODIUM_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN[_VECTOR] unset; skipping pgvector degrade-recover e2e")
	}
	scopedDSN := pgVectorIsolatedSchema(t, dsn)
	emb := newControllableEmbedder(t)
	srv := startHybridPgVector(t, scopedDSN, emb.url(), paraphraseRegistry(t))

	// Baseline: embedder healthy, vector fusion lifts the semantic target above
	// the lexical distractor.
	baseline, baseResp := searchIDs(t, srv.BaseURL, hybridQuery)
	if len(baseline) == 0 || baseline[0] != hybridTarget {
		t.Fatalf("baseline top result = %v, want %q first (vector fusion should rank the semantic target above the lexical distractor)\nresults: %+v",
			baseline, hybridTarget, baseResp.Results)
	}

	// Break the embedder mid-flight (transient 429). Search must keep serving:
	// the lexical distractor still matches "quarterly", so results are non-empty
	// (no silent empty set). With no lexical overlap the semantic target can no
	// longer be pinned to rank 1 by fusion, so it demotes off the top.
	emb.breakIt()
	st, body := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query="+queryEscape(hybridQuery)+"&top_k=10")
	if st != 200 {
		t.Fatalf("search during embedder 429 = %d, want 200 (BM25 fallback must keep serving)\nbody: %s", st, body)
	}
	var degradedResp semanticSearchResponse
	if err := json.Unmarshal(body, &degradedResp); err != nil {
		t.Fatalf("degraded search response not valid JSON: %v\n%s", err, body)
	}
	if len(degradedResp.Results) == 0 {
		t.Fatalf("search during embedder 429 returned a silent empty set; the lexical distractor should keep BM25 results non-empty\nbody: %s", body)
	}
	degradedIDs := make([]string, len(degradedResp.Results))
	for i, r := range degradedResp.Results {
		degradedIDs[i] = r.ID
	}
	if degradedIDs[0] == hybridTarget {
		t.Errorf("semantic target %q still ranked first during the embedder outage; BM25 cannot rank a zero-overlap query first, so the vector half did not actually degrade\nresults: %v",
			hybridTarget, degradedIDs)
	}
	if indexOf(degradedIDs, hybridLexicalAnchor) != 0 {
		t.Errorf("during the outage the lexical distractor %q should rank first (it is the only lexical match); got order %v",
			hybridLexicalAnchor, degradedIDs)
	}

	// Restore the embedder. Semantic fusion resumes and the ranking changes back:
	// the target returns to rank 1 on the same query.
	emb.restore()
	recovered, recResp := searchIDs(t, srv.BaseURL, hybridQuery)
	if len(recovered) == 0 || recovered[0] != hybridTarget {
		t.Fatalf("after embedder recovery top result = %v, want %q (semantic fusion should resume)\nresults: %+v",
			recovered, hybridTarget, recResp.Results)
	}
	// The rank order during the outage differs from the recovered order, proving
	// the vector half (not lexical overlap) supplied the top result.
	if degradedIDs[0] == recovered[0] {
		t.Errorf("rank order did not change across the outage; degraded and recovered both rank %q first", recovered[0])
	}
}

// TestVectorHybrid_RRFRanksParaphraseThenChangesWithoutEmbedder.
// It ingests a corpus whose semantic target shares no word with the query,
// asserts the semantically-correct artifact ranks first with the topic-routed
// embedder live, then re-runs against a second server booted with no embedding
// provider (PODIUM_EMBEDDING_PROVIDER unset) over the same corpus and asserts
// the rank order changes, proving reciprocal rank fusion supplied the top result
// rather than lexical overlap.
//
// Gating: PODIUM_POSTGRES_DSN[_VECTOR]. With no DSN the test skips cleanly.
//
// Spec: §4.7 (reciprocal rank fusion of BM25 and vector ranks; semantic recall
// on a query whose terms are absent from the artifact).
func TestVectorHybrid_RRFRanksParaphraseThenChangesWithoutEmbedder(t *testing.T) {
	t.Parallel()
	dsn := firstEnv("PODIUM_POSTGRES_DSN_VECTOR", "PODIUM_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN[_VECTOR] unset; skipping pgvector paraphrase RRF e2e")
	}
	reg := paraphraseRegistry(t)

	// Hybrid server: pgvector + topic-routed embedder. The vector half groups the
	// query with the target, so RRF lifts finance/reconcile to rank 1 even though
	// the query shares no word with its description; the only lexical match is the
	// "quarterly" distractor.
	hybridDSN := pgVectorIsolatedSchema(t, dsn)
	emb := newControllableEmbedder(t)
	hybrid := startHybridPgVector(t, hybridDSN, emb.url(), reg)

	hybridIDs, hybridResp := searchIDs(t, hybrid.BaseURL, hybridQuery)
	if len(hybridIDs) == 0 || hybridIDs[0] != hybridTarget {
		t.Fatalf("hybrid top result = %v, want %q (RRF should rank the semantic target first)\nresults: %+v",
			hybridIDs, hybridTarget, hybridResp.Results)
	}

	// BM25-only server: same corpus, no embedding provider, so the vector half is
	// off and ranking is purely lexical. The query's only lexical match is the
	// "quarterly" distractor, so it ranks first and the zero-overlap target is not
	// rank 1; the order must differ from the hybrid order.
	bm25 := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_VECTOR_BACKEND=",
		"PODIUM_EMBEDDING_PROVIDER=",
	}, "serve", "--standalone", "--layer-path", reg)

	bm25IDs, bm25Resp := searchIDs(t, bm25.BaseURL, hybridQuery)
	if len(bm25IDs) == 0 {
		t.Fatalf("BM25-only search returned no results; the lexical distractor should match %q", hybridQuery)
	}
	if bm25IDs[0] == hybridTarget {
		t.Fatalf("BM25-only top result = %q, but the query shares no word with that target; lexical ranking cannot put it first, so the hybrid run did not prove a semantic contribution\nresults: %+v",
			hybridTarget, bm25Resp.Results)
	}
	// The two rank orders differ: the only variable is the embedding provider, so
	// reciprocal rank fusion over the vector half supplied the hybrid top result.
	if hybridIDs[0] == bm25IDs[0] {
		t.Errorf("hybrid and BM25-only rank the same artifact %q first; the vector contribution is not isolated", hybridIDs[0])
	}
}
