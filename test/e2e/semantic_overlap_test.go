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
	"strings"
	"testing"
)

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
// object under --json, applies --threshold with no recomputation, and exits as
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
	cliContains(t, ri.Stdout, "advisory: finance/remit [warning] description is semantically similar to finance/settle (", "ingest advisory names the paraphrase")
	cliContains(t, ri.Stdout, "(lint.semantic_overlap)", "ingest advisory carries the code")
	if strings.Contains(ri.Stdout, "advisory: hr/welcome [warning] description is semantically") {
		t.Errorf("an unrelated artifact drew a semantic advisory:\n%s", ri.Stdout)
	}

	text := runPodium(t, "", srv.cliEnv(), "overlap", "--registry", srv.BaseURL)
	if text.Exit != 0 {
		t.Fatalf("podium overlap exit=%d, want 0 under --fail-on error\nstdout=%s\nstderr=%s", text.Exit, text.Stdout, text.Stderr)
	}
	cliContains(t, text.Stdout, "[warning] finance/settle: description is semantically similar to finance/remit (", "text line in the lint format")
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
			ArtifactID string `json:"artifact_id"`
			Code       string `json:"code"`
			Peers      []struct {
				ArtifactID string  `json:"artifact_id"`
				Similarity float64 `json:"similarity"`
				ComputedAt string  `json:"computed_at"`
			} `json:"peers"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(low.Stdout), &report); err != nil {
		t.Fatalf("--json stdout is not one object: %v\n%s", err, low.Stdout)
	}
	mixedPeer := false
	for _, d := range report.Diagnostics {
		if d.ArtifactID != "finance/settle" || d.Code != "lint.semantic_overlap" {
			continue
		}
		for _, p := range d.Peers {
			if p.ArtifactID == "ops/mixed" && p.Similarity > 0.69 && p.Similarity < 0.72 && p.ComputedAt != "" {
				mixedPeer = true
			}
		}
	}
	if report.SemanticOverlap.State != "on" || report.SemanticOverlap.Threshold != 0.7 || !mixedPeer {
		t.Errorf("at --threshold 0.7 the report = %+v, want finance/settle to list ops/mixed near 0.71", report)
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
