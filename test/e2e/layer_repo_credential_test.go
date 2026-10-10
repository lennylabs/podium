package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// basicAuthPair is the credential one request to the git remote carried.
type basicAuthPair struct {
	user, password string
}

// failingGitRemote is an HTTP remote that answers every request with 500 and
// records the basic-auth pair each request carried. The pair comes from the
// Authorization header: a request line never carries URL userinfo, so the
// request URL says nothing about the credential a clone sent.
type failingGitRemote struct {
	*httptest.Server

	// mu guards pairs, which the handler appends to from the server's request
	// goroutines.
	mu    sync.Mutex
	pairs []basicAuthPair
}

func newFailingGitRemote(t *testing.T) *failingGitRemote {
	t.Helper()
	remote := &failingGitRemote{}
	remote.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, password, ok := r.BasicAuth(); ok {
			remote.mu.Lock()
			remote.pairs = append(remote.pairs, basicAuthPair{user, password})
			remote.mu.Unlock()
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(remote.Close)
	return remote
}

// recorded returns a copy of the pairs the remote has received.
func (f *failingGitRemote) recorded() []basicAuthPair {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]basicAuthPair(nil), f.pairs...)
}

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

// Spec: §7.3.1 (Repository credentials) / §7.3.1 (Re-registration with a
// reported repo) — a git layer registered with a credential in its URL
// userinfo keeps that credential across a restart of the binary on the same
// SQLite file. After the restart the list reports the repo without userinfo,
// a registration with the reported value is refused, a reingest sends the
// registered credential to the remote, and the log of neither process names a
// credential part.
func TestLayerRepoCredential_SurvivesRestart_CLI(t *testing.T) {
	t.Parallel()
	const (
		user     = "alice-user"
		password = "s3cr3tpw"
	)
	remote := newFailingGitRemote(t)
	reportedWant := remote.URL + "/acme/private.git"
	full := "http://" + user + ":" + password + "@" + strings.TrimPrefix(reportedWant, "http://")

	home := t.TempDir()
	env := []string{
		"HOME=" + home,
		"PODIUM_SQLITE_PATH=" + filepath.Join(home, "podium.db"),
		"PODIUM_SIGN=none",
	}
	srv := startServerArgs(t, env, "serve", "--standalone")
	registered := runPodium(t, "", nil, "layer", "register", "--registry", srv.BaseURL,
		"--id", "private", "--repo", full, "--ref", "main")
	cliWantExit(t, registered, 0, "register a layer whose repo carries a credential")
	stopProc(srv.cmd)

	// The second process reads the layer from the file the first one wrote.
	srv2 := startServerArgs(t, env, "serve", "--standalone")
	reported := reportedLayerRepo(t, srv2.BaseURL, "private")
	if reported != reportedWant {
		t.Fatalf("reported repo after the restart = %q, want %q", reported, reportedWant)
	}

	refused := runPodium(t, "", nil, "layer", "register", "--registry", srv2.BaseURL,
		"--id", "private", "--repo", reported, "--ref", "main")
	if refused.Exit == 0 {
		t.Fatalf("re-registration with the reported repo exited 0:\nstdout: %s\nstderr: %s", refused.Stdout, refused.Stderr)
	}
	cliContains(t, refused.Stderr, "redacted_repo", "re-registration refusal after the restart")

	// Only requests the second process makes count, so the pairs recorded
	// before this point are skipped.
	before := len(remote.recorded())
	reingest := runPodium(t, "", nil, "layer", "reingest", "--registry", srv2.BaseURL, "private")
	if reingest.Exit == 0 {
		t.Fatalf("reingest against a remote that answers 500 exited 0:\nstdout: %s", reingest.Stdout)
	}
	sent := remote.recorded()[before:]
	if len(sent) == 0 {
		t.Fatalf("the remote recorded no basic-auth request after the restart; the clone sent no credential\nreingest stderr: %s\nserver log:\n%s",
			reingest.Stderr, srv2.log())
	}
	for i, pair := range sent {
		if pair.user != user || pair.password != password {
			t.Errorf("remote request %d carried basic auth %q:%q, want the registered credential", i, pair.user, pair.password)
		}
	}

	outputs := map[string]string{
		"log of the first process":  srv.log(),
		"log of the second process": srv2.log(),
		"register output":           registered.Stdout + registered.Stderr,
		"refusal output":            refused.Stdout + refused.Stderr,
		"reingest output":           reingest.Stdout + reingest.Stderr,
	}
	for what, text := range outputs {
		for _, part := range []string{user, password} {
			if strings.Contains(text, part) {
				t.Errorf("%s carries the credential part %q:\n%s", what, part, text)
			}
		}
	}
}
