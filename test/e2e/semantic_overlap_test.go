package e2e

// Semantic overlap, end to end.
//
// Spec §7.3.1 stores semantic overlap records, reports them at ingest as
// lint.semantic_overlap advisories, and serves them through GET /v1/overlap,
// `podium overlap`, and `podium admin rebuild-overlap`. These tests boot the
// standalone server with PODIUM_LINT_SEMANTIC_OVERLAP=on, the collocated
// sqlite-vec backend, and the deterministic topic-routed OpenAI-format mock
// embedder the hybrid-search e2e uses, so two descriptions that share a
// concept and no words have a cosine similarity of 1 and two that share half a
// concept have about 0.71.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Spec: §7.3.1, §13.12 — a standalone registry started with a layer path and
// the check on serves before its startup ingest's artifacts are computed: the
// startup ingest computes no overlap, and the startup pass computes them in
// the background with the periodic sweep off. The server becomes ready, and
// podium overlap --json eventually reports computed equal to total and the
// paraphrase pair as peers, with no reingest and no rebuild.
func TestSemanticOverlap_StartupPassComputesLayerPath(t *testing.T) {
	t.Parallel()
	emb := newControllableEmbedder(t)
	reg := writeRegistry(t, map[string]string{
		"finance/settle/ARTIFACT.md": agentArtifact("invoice reconciliation against vendor payments"),
		"finance/remit/ARTIFACT.md":  agentArtifact("remit settlement of amounts owed"),
		"hr/welcome/ARTIFACT.md":     agentArtifact("welcoming staff with new hire orientation"),
	})
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_VECTOR_BACKEND=sqlite-vec",
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"OPENAI_API_KEY=sk-test",
		"PODIUM_OPENAI_BASE_URL=" + emb.url(),
		"PODIUM_LINT_SEMANTIC_OVERLAP=on",
		"PODIUM_LINT_SEMANTIC_REFRESH_INTERVAL=0",
		"PODIUM_INGEST_OFFLINE=true",
	}, "serve", "--standalone", "--layer-path", reg)

	readyDeadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := http.Get(srv.BaseURL + "/readyz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(readyDeadline) {
			t.Fatalf("/readyz never answered 200\n%s", srv.log())
		}
		time.Sleep(50 * time.Millisecond)
	}

	type report struct {
		SemanticOverlap struct {
			State    string `json:"state"`
			Computed *int   `json:"computed"`
			Total    *int   `json:"total"`
		} `json:"semantic_overlap"`
		Diagnostics []struct {
			ArtifactID string `json:"artifact_id"`
			Code       string `json:"code"`
			Peers      []struct {
				ArtifactID string `json:"artifact_id"`
			} `json:"peers"`
		} `json:"diagnostics"`
	}
	var got report
	var last cliResult
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		last = runPodium(t, "", srv.cliEnv(), "overlap", "--registry", srv.BaseURL, "--json")
		got = report{}
		if err := json.Unmarshal([]byte(last.Stdout), &got); err != nil {
			t.Fatalf("--json stdout is not one object: %v\n%s", err, last.Stdout)
		}
		st := got.SemanticOverlap
		if st.Computed != nil && st.Total != nil && *st.Computed == *st.Total {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	st := got.SemanticOverlap
	if st.State != "on" || st.Computed == nil || st.Total == nil || *st.Total != 3 || *st.Computed != 3 {
		t.Fatalf("semantic_overlap = %+v, want state on and computed == total == 3\nstderr=%s\n%s", st, last.Stderr, srv.log())
	}
	if strings.Contains(last.Stderr, "warning: semantic overlap is computed for") {
		t.Errorf("a complete report printed the coverage warning: %s", last.Stderr)
	}
	peersOf := map[string]string{}
	for _, d := range got.Diagnostics {
		if d.Code != "lint.semantic_overlap" {
			continue
		}
		ids := make([]string, 0, len(d.Peers))
		for _, p := range d.Peers {
			ids = append(ids, p.ArtifactID)
		}
		peersOf[d.ArtifactID] = strings.Join(ids, ",")
	}
	if peersOf["finance/settle"] != "finance/remit" || peersOf["finance/remit"] != "finance/settle" || peersOf["hr/welcome"] != "" {
		t.Errorf("semantic peers = %v, want finance/settle and finance/remit as each other's only peer", peersOf)
	}
	if !strings.Contains(srv.log(), "lint.semantic_overlap: startup pass of tenant") {
		t.Errorf("the server log does not record the startup pass\n%s", srv.log())
	}
}

// overlapServer boots a standalone server with the semantic overlap check on
// over sqlite-vec and the topic-routed mock embedder.
func overlapServer(t *testing.T) *serverProc {
	t.Helper()
	emb := newControllableEmbedder(t)
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_VECTOR_BACKEND=sqlite-vec",
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"OPENAI_API_KEY=sk-test",
		"PODIUM_OPENAI_BASE_URL=" + emb.url(),
		"PODIUM_LINT_SEMANTIC_OVERLAP=on",
	}, "serve", "--standalone")
	if log := srv.log(); !strings.Contains(log, "vector=sqlite-vec") || strings.Contains(log, "vector search disabled") {
		t.Fatalf("server did not wire sqlite-vec:\n%s", log)
	}
	return srv
}

// Spec: §7.3.1 — a reingest prints the lint.semantic_overlap advisory for two
// artifacts that say the same thing in different words, and podium overlap
// prints the stored overlap in the podium lint format, writes the response
// object under --json with each diagnostic's and each peer's description,
// type, and layer, applies --threshold with no recomputation, and exits as
// --fail-on directs; podium admin rebuild-overlap prints the counts.
func TestSemanticOverlap_EndToEnd(t *testing.T) {
	t.Parallel()
	srv := overlapServer(t)
	layer := newRepublishLayer(t, srv, "overlap-team")
	ri := reingestArtifacts(t, layer, map[string]string{
		"finance/settle/ARTIFACT.md": agentArtifact("invoice reconciliation against vendor payments"),
		"finance/remit/ARTIFACT.md":  agentArtifact("remit settlement of amounts owed"),
		"ops/mixed/ARTIFACT.md":      agentArtifact("invoice reconciliation for employee onboarding"),
		"hr/welcome/ARTIFACT.md":     agentArtifact("welcoming staff with new hire orientation"),
	})
	cliContains(t, ri.Stdout, "advisory: finance/remit [warning] name, description, when_to_use, and tags are semantically similar to finance/settle (", "ingest advisory names the paraphrase")
	cliContains(t, ri.Stdout, "(lint.semantic_overlap)", "ingest advisory carries the code")
	if strings.Contains(ri.Stdout, "advisory: hr/welcome [warning] description is semantically") {
		t.Errorf("an unrelated artifact drew a semantic advisory:\n%s", ri.Stdout)
	}

	text := runPodium(t, "", srv.cliEnv(), "overlap", "--registry", srv.BaseURL)
	if text.Exit != 0 {
		t.Fatalf("podium overlap exit=%d, want 0 under --fail-on error\nstdout=%s\nstderr=%s", text.Exit, text.Stdout, text.Stderr)
	}
	cliContains(t, text.Stdout, "[warning] finance/settle: name, description, when_to_use, and tags are semantically similar to finance/remit (", "text line in the lint format")
	if strings.Contains(text.Stdout, "ops/mixed (7") {
		t.Errorf("the 0.71 pair is reported at the default threshold:\n%s", text.Stdout)
	}

	low := runPodium(t, "", srv.cliEnv(), "overlap", "--registry", srv.BaseURL, "--threshold", "0.7", "--json")
	if low.Exit != 0 {
		t.Fatalf("podium overlap --threshold 0.7 --json exit=%d\nstderr=%s", low.Exit, low.Stderr)
	}
	var report struct {
		SemanticOverlap struct {
			State     string  `json:"state"`
			Threshold float64 `json:"threshold"`
		} `json:"semantic_overlap"`
		Diagnostics []struct {
			ArtifactID  string `json:"artifact_id"`
			Description string `json:"description"`
			Type        string `json:"type"`
			Layer       string `json:"layer"`
			Code        string `json:"code"`
			Peers       []struct {
				ArtifactID  string  `json:"artifact_id"`
				Description string  `json:"description"`
				Type        string  `json:"type"`
				Layer       string  `json:"layer"`
				Similarity  float64 `json:"similarity"`
				ComputedAt  string  `json:"computed_at"`
			} `json:"peers"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(low.Stdout), &report); err != nil {
		t.Fatalf("--json stdout is not one object: %v\n%s", err, low.Stdout)
	}
	mixedPeer, settleFields := false, false
	for _, d := range report.Diagnostics {
		if d.ArtifactID != "finance/settle" || d.Code != "lint.semantic_overlap" {
			continue
		}
		settleFields = d.Description == "invoice reconciliation against vendor payments" && d.Type == "agent" && d.Layer == "overlap-team"
		for _, p := range d.Peers {
			if p.ArtifactID == "ops/mixed" && p.Similarity > 0.69 && p.Similarity < 0.72 && p.ComputedAt != "" &&
				p.Description == "invoice reconciliation for employee onboarding" && p.Type == "agent" && p.Layer == "overlap-team" {
				mixedPeer = true
			}
		}
	}
	if report.SemanticOverlap.State != "on" || report.SemanticOverlap.Threshold != 0.7 || !mixedPeer || !settleFields {
		t.Errorf("at --threshold 0.7 the report = %+v, want finance/settle and its peer ops/mixed near 0.71 with their descriptions, type agent, and layer overlap-team", report)
	}

	if strict := runPodium(t, "", srv.cliEnv(), "overlap", "--registry", srv.BaseURL, "--fail-on", "warning"); strict.Exit != 1 {
		t.Errorf("podium overlap --fail-on warning exit=%d, want 1", strict.Exit)
	}
	if bad := runPodium(t, "", srv.cliEnv(), "overlap", "--registry", srv.BaseURL, "--threshold", "0.3"); bad.Exit != 2 {
		t.Errorf("podium overlap --threshold 0.3 exit=%d, want 2", bad.Exit)
	}

	rebuild := runPodium(t, "", srv.cliEnv(), "admin", "rebuild-overlap", "--registry", srv.BaseURL)
	if rebuild.Exit != 0 || !strings.Contains(rebuild.Stdout, "computed: 4") || !strings.Contains(rebuild.Stdout, "failed: 0") {
		t.Errorf("admin rebuild-overlap exit=%d stdout=%q stderr=%q, want 4 computed and 0 failed", rebuild.Exit, rebuild.Stdout, rebuild.Stderr)
	}
}

// Spec: §7.3.1, §13.12 — a registry started with the check on and no
// embedding source refuses to start with config.invalid naming
// PODIUM_LINT_SEMANTIC_OVERLAP.
// Matrix: §6.10 (config.invalid)
func TestSemanticOverlap_StartupRefusedWithoutEmbeddingSource(t *testing.T) {
	t.Parallel()
	out := runPodium(t, "", []string{
		"HOME=" + t.TempDir(),
		"PODIUM_LINT_SEMANTIC_OVERLAP=on",
		"PODIUM_EMBEDDING_PROVIDER=",
		"PODIUM_VECTOR_BACKEND=",
	}, "serve", "--standalone")
	combined := out.Stderr + out.Stdout
	if out.Exit == 0 || !strings.Contains(combined, "config.invalid") || !strings.Contains(combined, "PODIUM_LINT_SEMANTIC_OVERLAP") {
		t.Fatalf("serve with the check on and no embedding source: exit=%d\n%s", out.Exit, combined)
	}
}
