package e2e

// Live end-to-end comparison of the two Pinecone embedding routes through the
// standalone server. TestVectorBackend_*
// SelfEmbedding asserts only the chosen route from the startup log against a
// refused host, and TestVectorSemanticSearch_ManagedThroughServer runs a live
// managed backend storage-only with a mock embedder. No test ingests and queries
// through Pinecone self-embedding live nor compares the self-embedding and
// external-embedder routes end to end.
//
// This test boots the server against live Pinecone twice over the same fixture
// set: once with PODIUM_PINECONE_INFERENCE_MODEL set (Integrated Inference, the
// backend embeds server-side) and once with an external OpenAI embedder
// (storage-only). Each run uses a unique namespace so the two runs and any prior
// state never mix. Both routes must return the same expected artifact at rank 1
// through search_artifacts, and each run's startup log must record its route.
//
// Gating: PODIUM_LIVE_EXTERNAL=1 plus PODIUM_PINECONE_API_KEY. The self-embedding
// run additionally needs PODIUM_PINECONE_SELFEMBED_INDEX and
// PODIUM_PINECONE_INFERENCE_MODEL; the external run needs PODIUM_PINECONE_INDEX
// and a real OPENAI_API_KEY. Any missing piece skips that run.

import (
	"os"
	"strings"
	"testing"
	"time"
)

// pineconeRouteRegistry stages a corpus whose target is semantically
// unambiguous so either a hosted inference model or OpenAI ranks it first for
// the matching query. The descriptions are lexically distinct, so the assertion
// holds across two different embedding models.
func pineconeRouteRegistry(t *testing.T) string {
	t.Helper()
	return writeRegistry(t, map[string]string{
		"weather/forecast/ARTIFACT.md": contextArtifact("weather forecasting rainfall temperature and wind predictions for tomorrow"),
		"finance/payroll/ARTIFACT.md":  contextArtifact("employee payroll salary tax withholding and direct deposit"),
		"cooking/recipes/ARTIFACT.md":  contextArtifact("recipe ingredients cooking instructions and meal preparation steps"),
	})
}

// uniquePineconeNS returns a per-run namespace prefix so concurrent or repeated
// runs of this test never read each other's vectors out of the shared index.
func uniquePineconeNS(tag string) string {
	return "podium_e2e_" + tag + "_" + randHex(8)
}

// pollSemanticTopHit polls search_artifacts until wantID ranks first or the
// deadline passes, because a managed backend's write is asynchronous (the §4.7.2
// outbox drains in the background) and Pinecone is eventually consistent. It
// fails if the deadline passes without the expected top hit.
func pollSemanticTopHit(t *testing.T, baseURL, query, wantID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last semanticSearchResponse
	for time.Now().Before(deadline) {
		var resp semanticSearchResponse
		getJSON(t, baseURL+"/v1/search_artifacts?query="+queryEscape(query)+"&top_k=5", &resp)
		last = resp
		if len(resp.Results) > 0 && resp.Results[0].ID == wantID {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("search_artifacts(%q) never ranked %q first within %s; last results: %+v", query, wantID, timeout, last.Results)
}

// TestVectorPineconeRoutes_SelfEmbedVsExternalLive. It boots the
// server against live Pinecone twice over the same fixtures, once self-embedding
// and once with an external OpenAI embedder, and asserts both rank the same
// artifact first while each startup log records the chosen route.
//
// Spec: §4.7 / §13.12 (self-embedding via PODIUM_PINECONE_INFERENCE_MODEL versus
// an external EmbeddingProvider; both produce correct recall).
func TestVectorPineconeRoutes_SelfEmbedVsExternalLive(t *testing.T) {
	t.Parallel()
	if os.Getenv("PODIUM_LIVE_EXTERNAL") != "1" {
		t.Skip("PODIUM_LIVE_EXTERNAL != 1; skipping live Pinecone route-comparison e2e")
	}
	apiKey := os.Getenv("PODIUM_PINECONE_API_KEY")
	if apiKey == "" {
		t.Skip("PODIUM_PINECONE_API_KEY unset; skipping live Pinecone route-comparison e2e")
	}
	reg := pineconeRouteRegistry(t)
	const query = "what will the weather be like"
	const target = "weather/forecast"

	// Self-embedding route: Integrated Inference index plus inference model, no
	// external embedder. The backend embeds the text projection server-side.
	t.Run("self-embedding", func(t *testing.T) {
		t.Parallel()
		selfIndex := os.Getenv("PODIUM_PINECONE_SELFEMBED_INDEX")
		model := os.Getenv("PODIUM_PINECONE_INFERENCE_MODEL")
		if selfIndex == "" || model == "" {
			t.Skip("PODIUM_PINECONE_SELFEMBED_INDEX/INFERENCE_MODEL unset; skipping self-embedding route")
		}
		ns := uniquePineconeNS("selfembed")
		srv := startServerArgs(t, []string{
			"HOME=" + t.TempDir(),
			"PODIUM_VECTOR_BACKEND=pinecone",
			"PODIUM_PINECONE_API_KEY=" + apiKey,
			"PODIUM_PINECONE_INDEX=" + selfIndex,
			"PODIUM_PINECONE_INFERENCE_MODEL=" + model,
			"PODIUM_PINECONE_NAMESPACE=" + ns,
			"PODIUM_EMBEDDING_PROVIDER=",
		}, "serve", "--standalone", "--layer-path", reg)
		log := srv.log()
		if !strings.Contains(log, "vector=pinecone") || !strings.Contains(log, "self-embedding") {
			t.Fatalf("self-embedding route not recorded in startup log:\n%s", log)
		}
		pollSemanticTopHit(t, srv.BaseURL, query, target, 90*time.Second)
	})

	// External-embedder route: storage-only index plus a real OpenAI embedder.
	t.Run("external-embedder", func(t *testing.T) {
		t.Parallel()
		storeIndex := os.Getenv("PODIUM_PINECONE_INDEX")
		openaiKey := os.Getenv("OPENAI_API_KEY")
		if storeIndex == "" || openaiKey == "" {
			t.Skip("PODIUM_PINECONE_INDEX/OPENAI_API_KEY unset; skipping external-embedder route")
		}
		ns := uniquePineconeNS("external")
		srv := startServerArgs(t, []string{
			"HOME=" + t.TempDir(),
			"PODIUM_VECTOR_BACKEND=pinecone",
			"PODIUM_PINECONE_API_KEY=" + apiKey,
			"PODIUM_PINECONE_INDEX=" + storeIndex,
			"PODIUM_PINECONE_NAMESPACE=" + ns,
			"PODIUM_EMBEDDING_PROVIDER=openai",
			"OPENAI_API_KEY=" + openaiKey,
		}, "serve", "--standalone", "--layer-path", reg)
		log := srv.log()
		if !strings.Contains(log, "vector=pinecone") || !strings.Contains(log, "embedder=openai") {
			t.Fatalf("external-embedder route not recorded in startup log:\n%s", log)
		}
		if strings.Contains(log, "self-embedding") {
			t.Fatalf("external-embedder run took the self-embedding branch:\n%s", log)
		}
		pollSemanticTopHit(t, srv.BaseURL, query, target, 90*time.Second)
	})
}
