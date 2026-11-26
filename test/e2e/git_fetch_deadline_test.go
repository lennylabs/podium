package e2e

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newHangingGitRemote starts an HTTP git remote that accepts each request and
// never answers it, and returns the repository URL.
func newHangingGitRemote(t *testing.T) string {
	t.Helper()
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	// Cleanups run last-in first-out: the handlers return before Close waits
	// for them.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(stop) })
	return srv.URL + "/acme/layer.git"
}

// Spec: §7.3.1 (Source unreachable), §13.12 — through the binary, a reingest
// of a git layer whose remote never answers ends once PODIUM_GIT_FETCH_TIMEOUT
// passes, with ingest.source_unreachable naming the setting, and the registry
// keeps serving: the layer list still answers afterwards.
// Matrix: §6.10 (ingest.source_unreachable)
func TestGitFetchDeadline_ReingestFailsAsSourceUnreachable(t *testing.T) {
	t.Parallel()
	repo := newHangingGitRemote(t)
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_SIGN=none",
		"PODIUM_GIT_FETCH_TIMEOUT=500ms",
	}, "serve", "--standalone")
	reg := runPodium(t, "", nil, "layer", "register", "--registry", srv.BaseURL,
		"--id", "slow", "--repo", repo, "--ref", "main")
	cliWantExit(t, reg, 0, "register the git layer")

	start := time.Now()
	res := runPodium(t, "", nil, "layer", "reingest", "--registry", srv.BaseURL, "slow")
	elapsed := time.Since(start)
	cliWantExit(t, res, 1, "reingest against a remote that never answers")
	for _, want := range []string{"ingest.source_unreachable", "PODIUM_GIT_FETCH_TIMEOUT (500ms)"} {
		cliContains(t, res.Stderr, want, "reingest refusal")
	}
	if elapsed > 30*time.Second {
		t.Errorf("reingest returned after %v, want shortly after the 500ms deadline", elapsed)
	}
	cliWantExit(t, runPodium(t, "", nil, "layer", "list", "--registry", srv.BaseURL), 0,
		"layer list after the timed-out reingest")
}

// Spec: §13.12 — through the binary, `podium config show --server` reports a
// PODIUM_GIT_FETCH_TIMEOUT that the registry refuses to start with as the
// value set, marked invalid, and warns with the config.invalid refusal.
func TestGitFetchDeadline_ConfigShowMarksInvalidValue(t *testing.T) {
	t.Parallel()
	out := runPodium(t, "", []string{"HOME=" + t.TempDir(), "PODIUM_GIT_FETCH_TIMEOUT=600"},
		"config", "show", "--server")
	cliWantExit(t, out, 0, "config show --server with an invalid PODIUM_GIT_FETCH_TIMEOUT")
	cliContains(t, out.Stderr, "warning: config.invalid: PODIUM_GIT_FETCH_TIMEOUT must be a duration above zero", "config show warning")
	cliContains(t, out.Stdout, "600 (invalid)", "config show settings")
}

// Spec: §13.12 — a PODIUM_GIT_FETCH_TIMEOUT that is not a duration above zero
// refuses startup with config.invalid naming the variable.
// Matrix: §6.10 (config.invalid)
func TestGitFetchDeadline_StartupRefusesInvalidValue(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"0s", "-1m", "600"} {
		out := runPodium(t, "", []string{"HOME=" + t.TempDir(), "PODIUM_GIT_FETCH_TIMEOUT=" + value},
			"serve", "--standalone", "--bind", "127.0.0.1:0")
		combined := out.Stderr + out.Stdout
		if out.Exit == 0 || !strings.Contains(combined, "config.invalid: PODIUM_GIT_FETCH_TIMEOUT must be a duration above zero") {
			t.Errorf("serve with PODIUM_GIT_FETCH_TIMEOUT=%s: exit=%d\n%s", value, out.Exit, combined)
		}
	}
}
