package e2e

// End-to-end tests for docs/authoring/signing-with-github-actions.md (D-attest-signing).
// They drive `podium layer register --trust-preset github-actions` and
// `podium attest init` through the binary that the harness builds with the
// podiumtest tag, whose hidden --test-github-api flag points the one GitHub
// API request at an in-process server, so no test reaches the network. A
// binary built without the tag refuses that flag.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// e2eGitHub is an in-process GitHub API that answers repository records by
// path and counts requests.
type e2eGitHub struct {
	*httptest.Server
	requests atomic.Int32
	auth     atomic.Value
}

func newE2EGitHub(t *testing.T, records map[string]string) *e2eGitHub {
	t.Helper()
	g := &e2eGitHub{}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.requests.Add(1)
		g.auth.Store(r.Header.Get("Authorization"))
		body, ok := records[r.URL.Path]
		switch {
		case !ok:
			w.WriteHeader(http.StatusNotFound)
		case body == "slow":
			select {
			case <-r.Context().Done():
			case <-time.After(10 * time.Second):
			}
		default:
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(g.Close)
	return g
}

func e2eRecord(fullName, branch, visibility string) string {
	return `{"id": 123456, "full_name": "` + fullName + `", "default_branch": "` + branch + `", "visibility": "` + visibility + `", "owner": {"id": 7890}}`
}

// Spec: §7.3.1 (Trust preset) — the preset registers the layer at
// podium/signed with mode require and a signer bound to the repository's
// URL, identifiers, and default branch, sends the token as a bearer token,
// and prints the resolved policy.
func TestTrustPreset_RegistersTheResolvedPolicy(t *testing.T) {
	t.Parallel()
	api := newE2EGitHub(t, map[string]string{"/repos/acme/finance-skills": e2eRecord("acme/finance-skills", "main", "public")})
	srv := startServer(t, "")
	env := append(brEnv(srv.BaseURL), "GH_TOKEN=tok-e2e", "GITHUB_TOKEN=")
	res := runPodium(t, "", env, "layer", "register", "--id", "finance", "--repo", "https://github.com/acme/finance-skills",
		"--root", "skills", "--trust-preset", "github-actions", "--test-github-api", api.URL)
	cliWantExit(t, res, 0, "register with the preset")
	cliContains(t, res.Stderr, "trust preset github-actions resolved for acme/finance-skills:", "resolved policy")
	if api.auth.Load() != "Bearer tok-e2e" {
		t.Errorf("Authorization = %v", api.auth.Load())
	}
	obj := shownLayer(t, srv.BaseURL, "finance")
	if obj["ref"] != "podium/signed" || obj["trust_mode"] != "require" {
		t.Errorf("layer = %v", obj)
	}
	signer := obj["trust_requirements"].([]any)[0].(map[string]any)["signers"].([]any)[0].(map[string]any)
	want := map[string]string{
		"issuer":                     "https://token.actions.githubusercontent.com",
		"identity":                   "https://github.com/acme/finance-skills/.github/workflows/podium-attest.yml@refs/heads/main",
		"source_repository":          "https://github.com/acme/finance-skills",
		"source_repository_id":       "123456",
		"source_repository_owner_id": "7890",
		"source_repository_ref":      "refs/heads/main",
	}
	for k, v := range want {
		if signer[k] != v {
			t.Errorf("signer %s = %v, want %s", k, signer[k], v)
		}
	}

	// A shared workflow's identity as an explicit signer keeps the binding.
	res = runPodium(t, "", env, "layer", "register", "--id", "finance-shared", "--repo", "https://github.com/acme/finance-skills",
		"--trust-preset", "github-actions", "--trust", "warn", "--test-github-api", api.URL,
		"--trust-signer", "https://token.actions.githubusercontent.com=https://github.com/acme/.github/.github/workflows/podium-attest.yml@refs/heads/main")
	cliWantExit(t, res, 0, "register with the preset and a shared workflow")
	obj = shownLayer(t, srv.BaseURL, "finance-shared")
	signer = obj["trust_requirements"].([]any)[0].(map[string]any)["signers"].([]any)[0].(map[string]any)
	if obj["trust_mode"] != "warn" || signer["source_repository_id"] != "123456" ||
		!strings.HasPrefix(signer["identity"].(string), "https://github.com/acme/.github/") {
		t.Errorf("shared-workflow layer = %v", obj)
	}
}

// Spec: §7.3.1 (Trust preset), §13.12 (PODIUM_GITHUB_API_TIMEOUT) — a
// --repo outside github.com sends no request even with GH_TOKEN set, and a
// failed request, a deadline, a renamed repository, and a private one each
// register nothing and name the explicit flags.
func TestTrustPreset_RefusalsRegisterNothing(t *testing.T) {
	t.Parallel()
	api := newE2EGitHub(t, map[string]string{
		"/repos/acme/old":    e2eRecord("acme/new", "main", "public"),
		"/repos/acme/secret": e2eRecord("acme/secret", "main", "private"),
		"/repos/acme/slow":   "slow",
	})
	srv := startServer(t, "")
	env := append(brEnv(srv.BaseURL), "GH_TOKEN=tok-e2e")
	register := func(repo string, extra ...string) cliResult {
		return runPodium(t, "", append(append([]string{}, env...), extra...), "layer", "register", "--id", "x", "--repo", repo,
			"--trust-preset", "github-actions", "--test-github-api", api.URL)
	}
	res := register("https://gitlab.com/acme/finance")
	cliWantExit(t, res, 1, "other host")
	cliContains(t, res.Stderr, "so no request was sent", "other host")
	if n := api.requests.Load(); n != 0 {
		t.Fatalf("the other host's registration sent %d requests", n)
	}
	cases := []struct {
		repo  string
		extra []string
		want  string
	}{
		{"https://github.com/acme/old", nil, "Register with --repo https://github.com/acme/new"},
		{"https://github.com/acme/secret", nil, "--trust-instance github"},
		{"https://github.com/acme/missing", nil, "HTTP 404"},
		{"https://github.com/acme/slow", []string{"PODIUM_GITHUB_API_TIMEOUT=300ms"}, "no answer within 300ms (PODIUM_GITHUB_API_TIMEOUT)"},
	}
	for _, c := range cases {
		res := register(c.repo, c.extra...)
		cliWantExit(t, res, 1, c.repo)
		cliContains(t, res.Stderr, c.want, c.repo)
		cliContains(t, res.Stderr, "othing was registered", c.repo)
	}
	res = register("https://github.com/acme/old", "PODIUM_GITHUB_API_TIMEOUT=0")
	cliWantExit(t, res, 2, "invalid timeout")

	list := runPodium(t, "", env, "layer", "list")
	cliWantExit(t, list, 0, "layer list")
	cliNotContains(t, list.Stdout, `"id":"x"`, "layer list")
	cliNotContains(t, list.Stdout, `"id": "x"`, "layer list")
}

// Spec: §7.3.1 (Trust preset), §7.3.5 (Writing the signing workflow) — a
// binary built without the podiumtest tag, as the release builds it,
// refuses the flag that redirects the GitHub API.
func TestReleaseBuild_RefusesTheGitHubAPITestFlag(t *testing.T) {
	t.Parallel()
	bin := cmdharness.ReleaseBin(t, "podium")
	for _, args := range [][]string{
		{"layer", "register", "--id", "x", "--repo", "https://github.com/acme/x", "--trust-preset", "github-actions", "--test-github-api", "http://127.0.0.1:1"},
		{"attest", "init", "--test-github-api", "http://127.0.0.1:1"},
	} {
		res := runBin(t, bin, t.TempDir(), []string{"PODIUM_REGISTRY=http://127.0.0.1:1", "PODIUM_NO_AUTOSTANDALONE=1"}, nil, 90*time.Second, args...)
		cliWantExit(t, res, 2, strings.Join(args[:2], " "))
		cliContains(t, res.Stderr, "flag provided but not defined: -test-github-api", "release build")
	}
}

// e2eRepo creates a working tree with an origin remote of url and
// refs/remotes/origin/HEAD naming branch.
func e2eRepo(t *testing.T, url, branch string) string {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "skills", "greet"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skills", "greet", "ARTIFACT.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.AddGlob("."); err != nil {
		t.Fatal(err)
	}
	sig := &object.Signature{Name: "alice", Email: "alice@acme.com", When: time.Unix(0, 0).UTC()}
	if _, err := wt.Commit("c", &git.CommitOptions{Author: sig, Committer: sig}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{url}}); err != nil {
		t.Fatal(err)
	}
	ref := plumbing.NewSymbolicReference(plumbing.NewRemoteHEADReferenceName("origin"), plumbing.NewRemoteReferenceName("origin", branch))
	if err := repo.Storer.SetReference(ref); err != nil {
		t.Fatal(err)
	}
	return dir
}

// docWorkflow returns the YAML block of the signing guide that shows the
// workflow `podium attest init --root skills` writes.
func docWorkflow(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "authoring", "signing-with-github-actions.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(b), "## The workflow")
	if !ok {
		t.Fatal("the guide has no workflow section")
	}
	_, block, ok := strings.Cut(after, "```yaml\n")
	if !ok {
		t.Fatal("the workflow section has no yaml block")
	}
	block, _, _ = strings.Cut(block, "```\n")
	return block
}

// versionLines matches the two lines that carry the podium version, which
// the guide shows for a release and the test binary reports for its build.
var versionLines = regexp.MustCompile(`(?m)^(# Written by .podium attest init. \(podium |  PODIUM_VERSION: )v[^\s)]+`)

// Spec: §7.3.5 (Writing the signing workflow) — the file that `podium
// attest init --root skills` writes is the workflow the signing guide
// shows, apart from the podium version, and the command prints the
// registration command with the preset.
func TestAttestInit_WritesTheDocumentedWorkflow(t *testing.T) {
	t.Parallel()
	api := newE2EGitHub(t, map[string]string{"/repos/acme/finance-skills": e2eRecord("acme/finance-skills", "main", "public")})
	dir := e2eRepo(t, "git@github.com:acme/finance-skills.git", "main")
	res := runPodium(t, dir, []string{"GH_TOKEN=", "GITHUB_TOKEN="}, "attest", "init", "--root", "skills", "--test-github-api", api.URL)
	cliWantExit(t, res, 0, "attest init")
	cliContains(t, res.Stdout, "Wrote .github/workflows/podium-attest.yml", "attest init")
	cliContains(t, res.Stdout, "podium layer register --id <id> --repo https://github.com/acme/finance-skills --root skills --trust-preset github-actions", "attest init")
	got, err := os.ReadFile(filepath.Join(dir, ".github", "workflows", "podium-attest.yml"))
	if err != nil {
		t.Fatal(err)
	}
	norm := func(s string) string { return versionLines.ReplaceAllString(s, "${1}vX") }
	if norm(string(got)) != norm(docWorkflow(t)) {
		t.Errorf("the written workflow differs from the guide's:\n%s", got)
	}

	res = runPodium(t, dir, nil, "attest", "init", "--test-github-api", api.URL)
	cliWantExit(t, res, 1, "existing file")
	cliContains(t, res.Stderr, "exists; --force replaces it", "existing file")
}

// Spec: §7.3.5 (Writing the signing workflow) — several roots print one
// registration each, a shared workflow prints its identity as the signer, a
// private repository prints the explicit flags, and a remote outside GitHub
// writes no file and sends no request.
func TestAttestInit_Variants(t *testing.T) {
	t.Parallel()
	api := newE2EGitHub(t, map[string]string{
		"/repos/acme/finance": e2eRecord("acme/finance", "trunk", "public"),
		"/repos/acme/secret":  e2eRecord("acme/secret", "main", "private"),
	})
	dir := e2eRepo(t, "https://github.com/acme/finance", "trunk")
	res := runPodium(t, dir, nil, "attest", "init", "--root", "skills", "--root", "tools", "--shared-workflow", "acme/.github", "--test-github-api", api.URL)
	cliWantExit(t, res, 0, "shared, two roots")
	cliContains(t, res.Stdout, "Then register each layer:", "two roots")
	cliContains(t, res.Stdout, "--root tools --trust-preset github-actions --trust-signer 'https://token.actions.githubusercontent.com=https://github.com/acme/.github/.github/workflows/podium-attest.yml@refs/heads/main'", "shared")
	wf, err := os.ReadFile(filepath.Join(dir, ".github", "workflows", "podium-attest.yml"))
	if err != nil || !strings.Contains(string(wf), "uses: acme/.github/.github/workflows/podium-attest.yml@main") || !strings.Contains(string(wf), "roots: skills tools") {
		t.Errorf("caller workflow: %v\n%s", err, wf)
	}

	secret := e2eRepo(t, "https://github.com/acme/secret", "main")
	res = runPodium(t, secret, nil, "attest", "init", "--test-github-api", api.URL)
	cliWantExit(t, res, 0, "private")
	cliContains(t, res.Stdout, "GitHub Enterprise Cloud", "private")
	cliContains(t, res.Stdout, "--trust-instance github", "private")

	before := api.requests.Load()
	other := e2eRepo(t, "https://gitlab.com/acme/finance", "main")
	res = runPodium(t, other, []string{"GH_TOKEN=tok"}, "attest", "init", "--test-github-api", api.URL)
	cliWantExit(t, res, 1, "gitlab remote")
	cliContains(t, res.Stderr, "Other environments", "gitlab remote")
	if api.requests.Load() != before {
		t.Error("a remote outside GitHub sent a request")
	}
	if _, err := os.Stat(filepath.Join(other, ".github")); !os.IsNotExist(err) {
		t.Error("a remote outside GitHub got a workflow")
	}
}
