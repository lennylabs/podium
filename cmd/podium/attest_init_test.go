package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/lennylabs/podium/internal/buildinfo"
	"github.com/lennylabs/podium/pkg/attest/ghactions"
)

// initRepo creates a working tree with one commit, an origin remote of url
// (none when empty), and refs/remotes/origin/HEAD naming branch (none when
// empty), and returns its path.
func initRepo(t *testing.T, url, branch string) string {
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
	if url != "" {
		if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{url}}); err != nil {
			t.Fatal(err)
		}
	}
	if branch != "" {
		ref := plumbing.NewSymbolicReference(plumbing.NewRemoteHEADReferenceName("origin"), plumbing.NewRemoteReferenceName("origin", branch))
		if err := repo.Storer.SetReference(ref); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// runInit runs `podium attest init` with args against api and env.
func runInit(t *testing.T, api string, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runAttestInit(context.Background(), args, attestInitEnv{
		getenv: func(k string) string { return env[k] }, apiBase: api, stdout: &stdout, stderr: &stderr,
	})
	return code, stdout.String(), stderr.String()
}

func readWorkflow(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".github", "workflows", "podium-attest.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func wantRendered(t *testing.T, w ghactions.Workflow) string {
	t.Helper()
	w.Version = buildinfo.Version
	b, err := ghactions.Render(w)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Spec: §7.3.5 (Writing the signing workflow) — for a public repository the
// command writes the workflow for the record's default branch and each
// root, and prints one preset registration per root.
func TestAttestInit_Public(t *testing.T) {
	api := newFakeGitHub(t, map[string]string{"/repos/acme/finance": ghRecord("acme/finance", "main", "public")})
	dir := initRepo(t, "git@github.com:acme/finance.git", "main")
	code, stdout, stderr := runInit(t, api.URL, nil, dir, "--root", "skills", "--root", "/team/tools/")
	if code != 0 {
		t.Fatalf("code %d\n%s", code, stderr)
	}
	if got := readWorkflow(t, dir); got != wantRendered(t, ghactions.Workflow{Kind: ghactions.KindRepository, Branch: "main", Roots: []string{"skills", "team/tools"}}) {
		t.Errorf("workflow differs from the rendered recipe:\n%s", got)
	}
	for _, want := range []string{
		"Wrote " + filepath.Join(dir, ".github", "workflows", "podium-attest.yml"),
		"Commit the file and push it to main. Then register each layer:",
		"podium layer register --id <id> --repo https://github.com/acme/finance --root skills --trust-preset github-actions",
		"--root team/tools --trust-preset github-actions",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}

	// An existing file is refused without --force and before any request.
	before := api.requests.Load()
	code, _, stderr = runInit(t, api.URL, nil, dir)
	if code != 1 || !strings.Contains(stderr, "exists; --force replaces it") || api.requests.Load() != before {
		t.Errorf("existing file: code %d, stderr %q, requests %d", code, stderr, api.requests.Load()-before)
	}
	code, stdout, _ = runInit(t, api.URL, nil, dir, "--force")
	if code != 0 || !strings.Contains(stdout, "Then register the layer:") || !strings.Contains(stdout, "finance --trust-preset github-actions") {
		t.Errorf("--force: code %d\n%s", code, stdout)
	}
	if got := readWorkflow(t, dir); got != wantRendered(t, ghactions.Workflow{Kind: ghactions.KindRepository, Branch: "main"}) {
		t.Error("--force did not replace the file")
	}
}

// Spec: §7.3.5 (Writing the signing workflow) — --shared-workflow writes a
// caller and prints the registration with the shared workflow as an
// explicit --trust-signer; --reusable writes the shared workflow with no
// request.
func TestAttestInit_SharedAndReusable(t *testing.T) {
	api := newFakeGitHub(t, map[string]string{"/repos/acme/finance": ghRecord("acme/finance", "trunk", "public")})
	dir := initRepo(t, "https://github.com/acme/finance", "")
	code, stdout, stderr := runInit(t, api.URL, nil, dir, "--shared-workflow", "acme/.github")
	if code != 0 {
		t.Fatalf("code %d\n%s", code, stderr)
	}
	if got := readWorkflow(t, dir); got != wantRendered(t, ghactions.Workflow{Kind: ghactions.KindCaller, Branch: "trunk", Shared: "acme/.github"}) {
		t.Errorf("caller workflow:\n%s", got)
	}
	if !strings.Contains(stdout, "--trust-preset github-actions --trust-signer 'https://token.actions.githubusercontent.com=https://github.com/acme/.github/.github/workflows/podium-attest.yml@refs/heads/main'") ||
		!strings.Contains(stdout, "push it to trunk") {
		t.Errorf("stdout:\n%s", stdout)
	}

	shared := initRepo(t, "https://github.com/acme/.github", "")
	before := api.requests.Load()
	code, stdout, stderr = runInit(t, api.URL, nil, shared, "--reusable")
	if code != 0 || api.requests.Load() != before {
		t.Fatalf("reusable: code %d, requests %d\n%s", code, api.requests.Load()-before, stderr)
	}
	if got := readWorkflow(t, shared); got != wantRendered(t, ghactions.Workflow{Kind: ghactions.KindReusable}) {
		t.Errorf("reusable workflow:\n%s", got)
	}
	if !strings.Contains(stdout, "podium attest init --shared-workflow acme/.github") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// Spec: §7.3.5 (Writing the signing workflow) — a private repository gets
// the workflow, the GitHub Enterprise Cloud note, the trusted-root step, and
// the explicit flags with the github instance; a renamed repository's
// commands use the current name; a failed request still writes the file
// and says so.
func TestAttestInit_RecordStates(t *testing.T) {
	api := newFakeGitHub(t, map[string]string{
		"/repos/acme/secret": ghRecord("acme/secret", "main", "private"),
		"/repos/acme/old":    ghRecord("acme/new", "main", "public"),
	})
	dir := initRepo(t, "https://github.com/acme/secret.git", "main")
	code, stdout, _ := runInit(t, api.URL, nil, dir, "--root", "skills")
	for _, want := range []string{"as private", "GitHub Enterprise Cloud", "gh attestation trusted-root | grep fulcio.githubapp.com",
		"--root skills --ref podium/signed", "--trust-source-repository-id 123456", "--trust-instance github"} {
		if code != 0 || !strings.Contains(stdout, want) {
			t.Errorf("private: code %d, stdout lacks %q:\n%s", code, want, stdout)
		}
	}

	dir = initRepo(t, "https://github.com/acme/old", "main")
	code, stdout, _ = runInit(t, api.URL, nil, dir)
	if code != 0 || !strings.Contains(stdout, "the GitHub API names the repository acme/new") || !strings.Contains(stdout, "--repo https://github.com/acme/new --trust-preset") {
		t.Errorf("renamed: code %d\n%s", code, stdout)
	}

	dir = initRepo(t, "https://github.com/acme/gone", "dev")
	code, stdout, _ = runInit(t, api.URL, nil, dir)
	if code != 0 || !strings.Contains(stdout, "Could not read the repository's visibility") || !strings.Contains(stdout, "push it to dev") {
		t.Errorf("failed request: code %d\n%s", code, stdout)
	}

	// With no record and no refs/remotes/origin/HEAD the branch is unknown.
	dir = initRepo(t, "https://github.com/acme/gone", "")
	code, _, stderr := runInit(t, api.URL, nil, dir)
	if code != 1 || !strings.Contains(stderr, "git remote set-head origin --auto") {
		t.Errorf("unknown branch: code %d %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".github")); !os.IsNotExist(err) {
		t.Error("a refused run wrote .github")
	}

	// The record's default branch wins over a stale origin HEAD.
	api2 := newFakeGitHub(t, map[string]string{"/repos/acme/x": ghRecord("acme/x", "trunk", "public")})
	dir = initRepo(t, "https://github.com/acme/x", "master")
	code, stdout, stderr = runInit(t, api2.URL, nil, dir)
	if code != 0 || !strings.Contains(stderr, "the workflow uses trunk") || !strings.Contains(stdout, "push it to trunk") {
		t.Errorf("stale origin HEAD: code %d\n%s\n%s", code, stdout, stderr)
	}
}

// Spec: §7.3.5 (Writing the signing workflow) — a remote outside GitHub, a
// repository with no origin, and a link at the workflow paths are refused
// with no file and no request; usage errors exit 2.
func TestAttestInit_Refusals(t *testing.T) {
	api := newFakeGitHub(t, map[string]string{"/repos/acme/x": ghRecord("acme/x", "main", "public")})
	dir := initRepo(t, "https://gitlab.com/acme/x", "main")
	code, _, stderr := runInit(t, api.URL, map[string]string{"GH_TOKEN": "tok"}, dir)
	if code != 1 || !strings.Contains(stderr, "Other environments") || api.requests.Load() != 0 {
		t.Errorf("gitlab: code %d %q requests %d", code, stderr, api.requests.Load())
	}
	if _, err := os.Stat(filepath.Join(dir, ".github")); !os.IsNotExist(err) {
		t.Error("the refused run wrote .github")
	}
	if code, _, stderr := runInit(t, api.URL, nil, initRepo(t, "", "")); code != 1 || !strings.Contains(stderr, "no origin remote") {
		t.Errorf("no origin: %d %q", code, stderr)
	}

	usage := [][]string{
		{"a", "b"},
		{"--reusable", "--root", "skills"},
		{"--reusable", "--shared-workflow", "acme/.github"},
		{"--root", "a b"},
		{"--root", "sk$ills"},
		{"--root", "skills", "--root", "/skills/"},
		{"--shared-workflow", "acme"},
		{"--no-such-flag"},
	}
	for _, args := range usage {
		if code, _, stderr := runInit(t, api.URL, nil, args...); code != 2 {
			t.Errorf("%v: code %d %q", args, code, stderr)
		}
	}
	if code, _, stderr := runInit(t, api.URL, map[string]string{"PODIUM_GITHUB_API_TIMEOUT": "never"}, dir); code != 2 || !strings.Contains(stderr, "PODIUM_GITHUB_API_TIMEOUT") {
		t.Errorf("timeout: %d %q", code, stderr)
	}
	if code, _, _ := runInit(t, api.URL, nil, "--help"); code != 0 {
		t.Errorf("--help: %d", code)
	}
}

// Spec: §7.3.1 (Trust preset) — a release build defines no flag that
// redirects the GitHub API, and the base defaults to api.github.com.
func TestGitHubAPIBase(t *testing.T) {
	if addGitHubAPITestFlag(nil) != nil {
		t.Error("a build without the podiumtest tag defines the test flag")
	}
	other := "http://127.0.0.1:1"
	if githubAPIBase("", nil) != ghactions.APIBase || githubAPIBase("x", nil) != "x" || githubAPIBase("x", &other) != other {
		t.Error("githubAPIBase")
	}
	empty := ""
	if githubAPIBase("x", &empty) != "x" {
		t.Error("an empty test flag replaced the base")
	}
}
