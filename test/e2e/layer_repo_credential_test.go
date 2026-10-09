package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// reportedLayerRepo reads GET /v1/layers and returns the repo the response
// carries for id.
func reportedLayerRepo(t *testing.T, baseURL, id string) string {
	t.Helper()
	resp, err := http.Get(baseURL + "/v1/layers")
	if err != nil {
		t.Fatalf("GET /v1/layers: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /v1/layers: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/layers = %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Layers []struct {
			ID   string `json:"id"`
			Repo string `json:"repo"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode /v1/layers: %v: %s", err, body)
	}
	for _, l := range out.Layers {
		if l.ID == id {
			return l.Repo
		}
	}
	t.Fatalf("GET /v1/layers does not carry layer %s: %s", id, body)
	return ""
}

// Spec: §7.3.1 — the re-registration rule through the CLI. `podium layer
// register` with the repo a read reports for a layer whose stored URL carries
// a credential exits non-zero with the 400 registry.invalid_argument refusal
// and its redacted_repo constraint on stderr, and the same command with
// --force-repo-overwrite stores the value as given.
func TestLayerRegister_RedactedRepoGuard_CLI(t *testing.T) {
	t.Parallel()
	// Multi-character fixtures: a single-letter credential part matches
	// unrelated output text.
	const (
		user     = "alice-user"
		password = "s3cr3tpw"
		full     = "https://" + user + ":" + password + "@git.acme.com/acme/private.git"
	)
	srv := startServer(t, "")
	register := func(repo string, extra ...string) cliResult {
		args := append([]string{"layer", "register", "--registry", srv.BaseURL,
			"--id", "private", "--repo", repo, "--ref", "main"}, extra...)
		return runPodium(t, "", nil, args...)
	}

	cliWantExit(t, register(full), 0, "register a layer whose repo carries a credential")

	reported := reportedLayerRepo(t, srv.BaseURL, "private")
	if reported != "https://git.acme.com/acme/private.git" {
		t.Fatalf("reported repo = %q, want the URL without its userinfo", reported)
	}

	refused := register(reported)
	if refused.Exit == 0 {
		t.Fatalf("re-registration with the reported repo exited 0:\nstdout: %s\nstderr: %s", refused.Stdout, refused.Stderr)
	}
	for _, want := range []string{"HTTP 400", "registry.invalid_argument", "redacted_repo"} {
		cliContains(t, refused.Stderr, want, "re-registration refusal")
	}
	for _, part := range []string{user, password} {
		if strings.Contains(refused.Stdout+refused.Stderr, part) {
			t.Errorf("refusal output carries the credential part %q:\nstdout: %s\nstderr: %s", part, refused.Stdout, refused.Stderr)
		}
	}
	// The refusal stored nothing, so the layer still reports the same remote
	// and the forced registration below is what replaces the stored value.
	if again := reportedLayerRepo(t, srv.BaseURL, "private"); again != reported {
		t.Errorf("reported repo after the refusal = %q, want %q", again, reported)
	}

	cliWantExit(t, register(reported, "--force-repo-overwrite"), 0, "forced re-registration with the reported repo")
	// With the credential gone the stored value has no userinfo, so the guard
	// no longer matches and the same registration needs no flag.
	cliWantExit(t, register(reported), 0, "re-registration after the forced overwrite")
}
