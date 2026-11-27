package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec: §13.12 — a PODIUM_GIT_FETCH_DEPTH, or a registry.yaml
// git_fetch_depth, that is not an integer from 1 to 2147483646 refuses
// startup with config.invalid naming the setting. 2147483647, which Git reads
// as a request for the complete history, is refused.
// Matrix: §6.10 (config.invalid)
func TestGitFetchDepth_StartupRefusesInvalidValue(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"0", "-1", "2147483647", "deep"} {
		out := runPodium(t, "", []string{"HOME=" + t.TempDir(), "PODIUM_GIT_FETCH_DEPTH=" + value},
			"serve", "--standalone", "--bind", "127.0.0.1:0")
		combined := out.Stderr + out.Stdout
		if out.Exit == 0 || !strings.Contains(combined, "config.invalid: PODIUM_GIT_FETCH_DEPTH must be an integer from 1 to 2147483646") {
			t.Errorf("serve with PODIUM_GIT_FETCH_DEPTH=%s: exit=%d\n%s", value, out.Exit, combined)
		}
	}

	cfgPath := filepath.Join(t.TempDir(), "registry.yaml")
	if err := os.WriteFile(cfgPath, []byte("registry:\n  git_fetch_depth: 0\n"), 0o600); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}
	out := runPodium(t, "", []string{"HOME=" + t.TempDir(), "PODIUM_CONFIG_FILE=" + cfgPath},
		"serve", "--standalone", "--bind", "127.0.0.1:0")
	combined := out.Stderr + out.Stdout
	if out.Exit == 0 || !strings.Contains(combined, "config.invalid: registry.yaml git_fetch_depth must be an integer") {
		t.Errorf("serve with registry.yaml git_fetch_depth: 0: exit=%d\n%s", out.Exit, combined)
	}
}

// Spec: §13.12 — through the binary, `podium config show --server` reports a
// PODIUM_GIT_FETCH_DEPTH that the registry refuses to start with as the value
// set, marked invalid, and warns with the config.invalid refusal.
func TestGitFetchDepth_ConfigShowMarksInvalidValue(t *testing.T) {
	t.Parallel()
	out := runPodium(t, "", []string{"HOME=" + t.TempDir(), "PODIUM_GIT_FETCH_DEPTH=0"},
		"config", "show", "--server")
	cliWantExit(t, out, 0, "config show --server with an invalid PODIUM_GIT_FETCH_DEPTH")
	cliContains(t, out.Stderr, "warning: config.invalid: PODIUM_GIT_FETCH_DEPTH must be an integer from 1 to 2147483646", "config show warning")
	cliContains(t, out.Stdout, "0 (invalid)", "config show settings")
}

// Spec: §7.3.1 (Git fetch: Snapshot, Deepening), §13.12 — through the binary,
// a registry started with PODIUM_GIT_FETCH_DEPTH=1 clones one commit and
// deepens while the force-push answer is undecided. A layer under the strict
// force-push policy whose last ingested commit lies several commits behind
// the new tip ingests the new commit, because the deepened history decides
// that the history was not rewritten.
func TestGitFetchDepth_ReingestDeepensTheBoundedClone(t *testing.T) {
	t.Parallel()
	repo := newGitJourneyRepo(t)
	repo.commitAgentArtifact(t, "ops/runbook", "1.0.0", "Runbook for the ingest worker", "v1")
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_SIGN=none",
		"PODIUM_GIT_FETCH_DEPTH=1",
	}, "serve", "--standalone")
	st, body := apiDo(t, "POST", srv.BaseURL+"/v1/layers", map[string]any{
		"id": "deep", "source_type": "git", "repo": repo.url(), "ref": "master", "force_push_policy": "strict",
	})
	apiWantStatus(t, st, 201, "register the git layer", body)

	st, body = apiDo(t, "POST", srv.BaseURL+"/v1/layers/reingest?id=deep", nil)
	apiWantStatus(t, st, 200, "first reingest", body)
	for i := 2; i <= 7; i++ {
		repo.commitAgentArtifact(t, "ops/runbook", fmt.Sprintf("1.0.%d", i), "Runbook for the ingest worker", fmt.Sprintf("v%d", i))
	}
	st, body = apiDo(t, "POST", srv.BaseURL+"/v1/layers/reingest?id=deep", nil)
	apiWantStatus(t, st, 200, "reingest with the last ingested commit six commits behind the tip", body)

	st, body = getRaw(t, srv.BaseURL+"/v1/load_artifact?id=ops/runbook&version=1.0.7")
	apiWantStatus(t, st, 200, "load the newest version", body)
}

// Spec: §7.3.1 (Git fetch: Deadline), §13.12 — through the binary, the
// reingest response of a git layer whose remote never answers carries the
// fetch_deadline details: the setting, the operation, and the depth of the
// clone, which PODIUM_GIT_FETCH_DEPTH sets.
// Matrix: §6.10 (ingest.source_unreachable)
func TestGitFetchDepth_DeadlineDetailsInTheReingestResponse(t *testing.T) {
	t.Parallel()
	repo := newHangingGitRemote(t)
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_SIGN=none",
		"PODIUM_GIT_FETCH_TIMEOUT=500ms",
		"PODIUM_GIT_FETCH_DEPTH=8",
	}, "serve", "--standalone")
	reg := runPodium(t, "", nil, "layer", "register", "--registry", srv.BaseURL,
		"--id", "slow", "--repo", repo, "--ref", "main")
	cliWantExit(t, reg, 0, "register the git layer")

	st, body := apiDo(t, "POST", srv.BaseURL+"/v1/layers/reingest?id=slow", nil)
	apiWantStatus(t, st, 502, "reingest against a remote that never answers", body)
	var env struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, body)
	}
	want := map[string]any{"constraint": "fetch_deadline", "setting": "PODIUM_GIT_FETCH_TIMEOUT", "operation": "snapshot", "depth": float64(8)}
	if env.Code != "ingest.source_unreachable" || fmt.Sprint(env.Details) != fmt.Sprint(want) {
		t.Errorf("envelope = %s, want ingest.source_unreachable with details %v", body, want)
	}
}
