package e2e

// End-to-end tests for README.md (D-readme). These drive the real binaries
// for the build, hello-world, CLI-surface, server, MCP, and setup claims the
// README makes. Tooling that the sandbox does not provision (Docker, Homebrew,
// the Python/Node SDKs, a live Postgres) is skipped with a clear reason;
// behaviors blocked by a known BUILD-GAPS finding are skipped citing that id.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readmeGreet stages the README hello-world skill at personal/hello/greet.
func readmeGreet(t *testing.T) string {
	return writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": greetSkillArtifact,
		"personal/hello/greet/SKILL.md":    greetSkillBody,
	})
}

// T-D-readme-1 — go build ./cmd/podium produces a working binary.
func TestReadme_BuildCmdPodium(t *testing.T) {
	out := filepath.Join(t.TempDir(), "podium")
	build, ok := runExternal(t, repoRoot(t), 180*time.Second, "go", "build", "-o", out, "./cmd/podium")
	if !ok {
		t.Skip("go toolchain not on PATH")
	}
	if build.Exit != 0 {
		t.Fatalf("go build ./cmd/podium exit=%d stderr=%s", build.Exit, build.Stderr)
	}
	ver := runBin(t, out, "", []string{"PODIUM_NO_AUTOSTANDALONE=1"}, nil, 30*time.Second, "version")
	if ver.Exit != 0 || !strings.Contains(ver.Stdout, "podium") {
		t.Errorf("built binary version exit=%d stdout=%q", ver.Exit, ver.Stdout)
	}
}

// T-D-readme-2 — go build ./... builds every binary.
func TestReadme_BuildAll(t *testing.T) {
	build, ok := runExternal(t, repoRoot(t), 240*time.Second, "go", "build", "./...")
	if !ok {
		t.Skip("go toolchain not on PATH")
	}
	if build.Exit != 0 {
		t.Fatalf("go build ./... exit=%d stderr=%s", build.Exit, build.Stderr)
	}
}

// T-D-readme-3 — make test runs the full suite.
func TestReadme_MakeTest(t *testing.T) {
	t.Skip("running `make test` from within the test suite is recursive and unbounded; the suite itself is the coverage for this claim")
}

// T-D-readme-4 — make help lists the documented targets.
func TestReadme_MakeHelp(t *testing.T) {
	res, ok := runExternal(t, repoRoot(t), 30*time.Second, "make", "help")
	if !ok {
		t.Skip("make not on PATH")
	}
	if res.Exit != 0 {
		t.Fatalf("make help exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	for _, target := range []string{"test", "test-live", "bench", "lint", "coverage", "build"} {
		if !strings.Contains(res.Stdout, target) {
			t.Errorf("make help missing target %q:\n%s", target, res.Stdout)
		}
	}
}

// T-D-readme-5 — Python SDK installs and imports.
func TestReadme_PythonSDKInstall(t *testing.T) {
	t.Skip("requires a provisioned Python 3.10+ toolchain and pip install -e sdks/podium-py; not available in the test sandbox")
}

// T-D-readme-6 — Python SDK pytest suite passes.
func TestReadme_PythonSDKPytest(t *testing.T) {
	t.Skip("requires Python + pytest in sdks/podium-py; not available in the test sandbox")
}

// T-D-readme-7 — TypeScript SDK installs and tests.
func TestReadme_TypeScriptSDK(t *testing.T) {
	t.Skip("requires Node.js 20+ and npm in sdks/podium-ts; not available in the test sandbox")
}

// T-D-readme-8 — hello world: create the documented skill files.
func TestReadme_HelloWorldFiles(t *testing.T) {
	t.Parallel()
	reg := readmeGreet(t)
	art := readFile(t, filepath.Join(reg, "personal/hello/greet/ARTIFACT.md"))
	if !strings.Contains(art, "type: skill") || !strings.Contains(art, "version: 1.0.0") {
		t.Errorf("ARTIFACT.md missing type/version:\n%s", art)
	}
	skill := readFile(t, filepath.Join(reg, "personal/hello/greet/SKILL.md"))
	if !strings.Contains(skill, "name: greet") {
		t.Errorf("SKILL.md missing name: greet:\n%s", skill)
	}
}

// T-D-readme-9 — hello world: podium lint passes.
func TestReadme_HelloWorldLint(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "lint", "--registry", readmeGreet(t))
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q stderr=%q", res.Exit, res.Stdout, res.Stderr)
	}
}

// T-D-readme-10 — hello world: podium init writes sync.yaml with registry and
// harness plus gitignore entries.
func TestReadme_HelloWorldInit(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	reg := readmeGreet(t)
	res := runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "init", "--registry", reg, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("init exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	sync := readFile(t, filepath.Join(ws, ".podium", "sync.yaml"))
	if !strings.Contains(sync, "registry: "+reg) || !strings.Contains(sync, "harness: claude-code") {
		t.Errorf("sync.yaml missing registry/harness:\n%s", sync)
	}
	gi := readFile(t, filepath.Join(ws, ".gitignore"))
	if !strings.Contains(gi, ".podium/sync.local.yaml") || !strings.Contains(gi, ".podium/overlay/") {
		t.Errorf(".gitignore missing entries:\n%s", gi)
	}
}

// T-D-readme-11 — init --registry without --harness omits the harness line.
func TestReadme_InitNoHarness(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	reg := readmeGreet(t)
	runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "init", "--registry", reg)
	sync := readFile(t, filepath.Join(ws, ".podium", "sync.yaml"))
	if !strings.Contains(sync, "registry: "+reg) {
		t.Errorf("sync.yaml missing registry:\n%s", sync)
	}
	if strings.Contains(sync, "harness:") {
		t.Errorf("sync.yaml unexpectedly contains a harness line:\n%s", sync)
	}
}

// T-D-readme-12 — hello world: sync materializes the skill under .claude/skills/.
func TestReadme_HelloWorldSyncClaudeCode(t *testing.T) {
	t.Parallel()
	reg := readmeGreet(t)
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	skill := filepath.Join(tgt, ".claude/skills/greet/SKILL.md")
	if !strings.Contains(readFile(t, skill), "name: greet") {
		t.Errorf("materialized SKILL.md missing name: greet")
	}
	if _, err := os.Stat(filepath.Join(tgt, ".claude/skills/greet/ARTIFACT.md")); err == nil {
		t.Errorf("claude-code adapter should not write ARTIFACT.md beside SKILL.md")
	}
}

// T-D-readme-13 — hello world: sync --harness none writes the canonical layout.
func TestReadme_HelloWorldSyncNone(t *testing.T) {
	t.Parallel()
	reg := readmeGreet(t)
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	mustExist(t, filepath.Join(tgt, "personal/hello/greet/ARTIFACT.md"))
	mustExist(t, filepath.Join(tgt, "personal/hello/greet/SKILL.md"))
}

// T-D-readme-14 — init --standalone points sync.yaml at http://127.0.0.1:8080.
func TestReadme_InitStandalone(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "init", "--standalone")
	if got := readFile(t, filepath.Join(ws, ".podium", "sync.yaml")); !strings.Contains(got, "registry: http://127.0.0.1:8080") {
		t.Errorf("sync.yaml missing standalone URL:\n%s", got)
	}
}

// T-D-readme-15 — init --global writes ~/.podium/sync.yaml.
func TestReadme_InitGlobal(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	home := t.TempDir()
	runPodium(t, ws, []string{"HOME=" + home}, "init", "--global", "--registry", "https://registry.example.com/")
	if got := readFile(t, filepath.Join(home, ".podium", "sync.yaml")); !strings.Contains(got, "registry.example.com") {
		t.Errorf("global sync.yaml missing registry URL:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(ws, ".podium", "sync.yaml")); err == nil {
		t.Errorf("workspace sync.yaml written under --global")
	}
}

// T-D-readme-16 — init refuses to overwrite without --force.
func TestReadme_InitRefuseOverwrite(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	home := t.TempDir()
	runPodium(t, ws, []string{"HOME=" + home}, "init", "--registry", "https://old.example.com/")
	res := runPodium(t, ws, []string{"HOME=" + home}, "init", "--registry", "https://new.example.com/")
	if res.Exit != 2 || !strings.Contains(res.Stderr, "already exists") {
		t.Errorf("exit=%d stderr=%q, want 2 + already exists", res.Exit, res.Stderr)
	}
	if got := readFile(t, filepath.Join(ws, ".podium", "sync.yaml")); !strings.Contains(got, "old.example.com") {
		t.Errorf("existing sync.yaml was changed:\n%s", got)
	}
}

// T-D-readme-17 — init --force overwrites.
func TestReadme_InitForce(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	home := t.TempDir()
	runPodium(t, ws, []string{"HOME=" + home}, "init", "--registry", "https://old.example.com/")
	res := runPodium(t, ws, []string{"HOME=" + home}, "init", "--registry", "https://new.example.com/", "--force")
	if res.Exit != 0 {
		t.Fatalf("init --force exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if got := readFile(t, filepath.Join(ws, ".podium", "sync.yaml")); !strings.Contains(got, "new.example.com") {
		t.Errorf("sync.yaml not overwritten:\n%s", got)
	}
}

// T-D-readme-18 — sync without --registry falls back to sync.yaml.
func TestReadme_SyncConfigFallback(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "init", "--registry", reg)
	tgt := t.TempDir()
	res := runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "sync", "--target", tgt)
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, "x/ARTIFACT.md"))
}

// T-D-readme-19 — sync --dry-run writes nothing.
func TestReadme_SyncDryRun(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--dry-run")
	if res.Exit != 0 || !strings.Contains(res.Stdout, "dry-run") {
		t.Fatalf("dry-run exit=%d stdout=%q", res.Exit, res.Stdout)
	}
	if files := readTreeFiltered(t, tgt); len(files) != 0 {
		t.Errorf("dry-run wrote files")
	}
}

// T-D-readme-20 — the sync --json envelope (spec §7.5) carries harness, target,
// and artifacts:[{id, version, type, layer}]; the artifacts actually
// materialize under the target.
func TestReadme_SyncJSON(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--json")
	var env struct {
		Harness   string `json:"harness"`
		Target    string `json:"target"`
		Artifacts []struct {
			ID    string `json:"id"`
			Layer string `json:"layer"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, res.Stdout)
	}
	if env.Harness == "" || env.Target == "" || len(env.Artifacts) == 0 {
		t.Fatalf("incomplete envelope: %+v", env)
	}
	if a := env.Artifacts[0]; a.ID == "" || a.Layer == "" {
		t.Errorf("artifact entry missing id/layer: %+v", a)
	}
	// The default (none) harness writes the canonical <id>/ARTIFACT.md layout.
	mustExist(t, filepath.Join(tgt, "x", "ARTIFACT.md"))
}

// T-D-readme-21 — sync is idempotent.
func TestReadme_SyncIdempotent(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"a/ARTIFACT.md": contextArtifact("a"),
		"b/ARTIFACT.md": contextArtifact("b"),
	})
	tgt := t.TempDir()
	first := syncAndSnapshot(t, reg, tgt)
	second := syncAndSnapshot(t, reg, tgt)
	if len(first) != len(second) {
		t.Fatalf("file count changed: %d -> %d", len(first), len(second))
	}
	for p, c := range first {
		if second[p] != c {
			t.Errorf("content changed for %q", p)
		}
	}
}

// T-D-readme-22 — sync with an unknown harness exits non-zero with
// config.unknown_harness.
func TestReadme_SyncUnknownHarness(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", t.TempDir(), "--harness", "not-a-real-harness")
	if res.Exit == 0 || !strings.Contains(res.Stderr, "config.unknown_harness") {
		t.Errorf("exit=%d stderr=%q", res.Exit, res.Stderr)
	}
}

// T-D-readme-23 — version prints a version string.
func TestReadme_Version(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "version")
	if res.Exit != 0 || !strings.HasPrefix(res.Stdout, "podium") {
		t.Errorf("version exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// T-D-readme-24 — help prints the full command list.
func TestReadme_HelpFullList(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "help")
	if res.Exit != 0 {
		t.Fatalf("help exit=%d", res.Exit)
	}
	for _, cmd := range []string{
		"serve", "config", "cache", "import", "sync", "lint", "search", "domain",
		"artifact", "init", "profile", "layer", "impact", "admin", "login", "logout",
		"status", "sign", "verify", "quota", "version",
	} {
		if !strings.Contains(res.Stdout, cmd) {
			t.Errorf("help missing subcommand %q", cmd)
		}
	}
}

// T-D-readme-25 — running podium with no subcommand exits 2 with usage.
func TestReadme_NoSubcommand(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil)
	if res.Exit != 2 {
		t.Fatalf("no-subcommand exit=%d, want 2", res.Exit)
	}
	if !strings.Contains(res.Stdout+res.Stderr, "usage: podium") {
		t.Errorf("missing usage message: %q / %q", res.Stdout, res.Stderr)
	}
}

// T-D-readme-26 — status prints the documented diagnostic labels.
func TestReadme_Status(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", []string{"HOME=" + t.TempDir(), "PODIUM_REGISTRY="}, "status")
	if res.Exit != 0 {
		t.Fatalf("status exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	for _, label := range []string{
		"registry:", "harness:", "cache dir:", "cache mode:", "overlay path:",
		"identity provider:", "session token:", "tenant:",
	} {
		if !strings.Contains(res.Stdout, label) {
			t.Errorf("status missing label %q:\n%s", label, res.Stdout)
		}
	}
	if !strings.Contains(res.Stdout, "unset; set PODIUM_REGISTRY") {
		t.Errorf("status should note unset registry:\n%s", res.Stdout)
	}
}

// T-D-readme-27 — status against a reachable registry shows OK and the mode.
func TestReadme_StatusReachable(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	res := runPodium(t, "", nil, "status", "--registry", srv.BaseURL)
	if !strings.Contains(res.Stdout, "reachability:") || !strings.Contains(res.Stdout, "OK") {
		t.Errorf("status missing reachability OK:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "registry mode:") {
		t.Errorf("status missing registry mode:\n%s", res.Stdout)
	}
}

// T-D-readme-28 — status against an unreachable registry shows UNREACHABLE and
// still exits 0.
func TestReadme_StatusUnreachable(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "status", "--registry", "http://127.0.0.1:19999")
	if res.Exit != 0 {
		t.Errorf("status exit=%d, want 0 (diagnostic, not a gate)", res.Exit)
	}
	if !strings.Contains(res.Stdout, "UNREACHABLE") {
		t.Errorf("status missing UNREACHABLE:\n%s", res.Stdout)
	}
}

// T-D-readme-29 — lint with a missing registry path exits 1.
func TestReadme_LintMissingPath(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "lint", "--registry", filepath.Join(t.TempDir(), "nope"))
	if res.Exit != 1 || res.Stderr == "" {
		t.Errorf("exit=%d stderr=%q, want 1 + error", res.Exit, res.Stderr)
	}
}

// T-D-readme-30 — lint reports errors for a malformed ARTIFACT.md (no type).
func TestReadme_LintMalformed(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"myartifact/ARTIFACT.md": "---\nversion: 1.0.0\n---\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("exit=%d, want 1\nstdout=%s stderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout+res.Stderr, "type") {
		t.Errorf("diagnostic does not name the missing type field")
	}
}

// T-D-readme-31 — artifact scaffold creates a skill directory.
func TestReadme_ScaffoldSkill(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "personal/hello/greet")
	res := runPodium(t, "", nil, "artifact", "scaffold", "--type", "skill", "--description", "Greet the user by name.", "--yes", out)
	if res.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(readFile(t, filepath.Join(out, "ARTIFACT.md")), "type: skill") {
		t.Errorf("ARTIFACT.md missing type: skill")
	}
	if !strings.Contains(readFile(t, filepath.Join(out, "SKILL.md")), "name: greet") {
		t.Errorf("SKILL.md missing name: greet")
	}
	if !strings.Contains(res.Stdout, "Scaffolded skill at") {
		t.Errorf("stdout missing 'Scaffolded skill at':\n%s", res.Stdout)
	}
}

// T-D-readme-32 — scaffold with a non-first-class type warns but succeeds.
func TestReadme_ScaffoldExtensionType(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "my-artifact")
	res := runPodium(t, "", nil, "artifact", "scaffold", "--type", "custom-ext", "--description", "An extension type.", "--yes", out)
	if res.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "not a first-class type") {
		t.Errorf("stderr missing first-class warning:\n%s", res.Stderr)
	}
	mustExist(t, filepath.Join(out, "ARTIFACT.md"))
}

// T-D-readme-33 — scaffold refuses to overwrite an existing directory.
func TestReadme_ScaffoldRefuseOverwrite(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "my-artifact")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	res := runPodium(t, "", nil, "artifact", "scaffold", "--type", "context", "--description", "A context.", "--yes", out)
	if res.Exit == 0 {
		t.Fatalf("expected non-zero exit")
	}
	if !strings.Contains(res.Stderr, "already exists") || !strings.Contains(res.Stderr, "--force") {
		t.Errorf("stderr missing overwrite guard:\n%s", res.Stderr)
	}
}

// T-D-readme-34 — filesystem catalog: sync materializes without a server.
func TestReadme_FilesystemCatalog(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"a/ARTIFACT.md": contextArtifact("a"),
		"b/ARTIFACT.md": contextArtifact("b"),
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, "a/ARTIFACT.md"))
	mustExist(t, filepath.Join(tgt, "b/ARTIFACT.md"))
}

// T-D-readme-35 — /healthz returns 200 with a mode/status field.
func TestReadme_Healthz(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	var body map[string]any
	getJSON(t, srv.BaseURL+"/healthz", &body)
	if _, ok := body["mode"]; !ok {
		t.Errorf("/healthz body missing mode: %v", body)
	}
}

// T-D-readme-36 — /readyz returns 200.
func TestReadme_Readyz(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	if st := getStatus(t, srv.BaseURL+"/readyz"); st != 200 {
		t.Errorf("/readyz = HTTP %d, want 200", st)
	}
}

// T-D-readme-37 — MCP initialize returns capabilities with tools.
func TestReadme_MCPInitialize(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	res := mcpExec(t, []string{"PODIUM_REGISTRY=" + srv.BaseURL}, rpcReq{ID: 1, Method: "initialize"})
	caps, _ := rpcResult(t, res.Stdout, 1)["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Errorf("initialize capabilities missing tools: %v", caps)
	}
}

// T-D-readme-38 — MCP tools/list returns the four meta-tools.
func TestReadme_MCPToolsList(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	res := mcpExec(t, []string{"PODIUM_REGISTRY=" + srv.BaseURL}, rpcReq{ID: 1, Method: "tools/list"})
	for _, tool := range []string{"load_domain", "search_domains", "search_artifacts", "load_artifact"} {
		if !strings.Contains(res.Stdout, tool) {
			t.Errorf("tools/list missing %q", tool)
		}
	}
}

// T-D-readme-39 — MCP search_artifacts proxies to the registry.
func TestReadme_MCPSearchArtifacts(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/close-reporting/run-variance-analysis/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n---\n\n",
		"finance/close-reporting/run-variance-analysis/SKILL.md":    skillBodyDesc("run-variance-analysis", "Flag unusual variance vs forecast."),
	}))
	res := mcpExec(t, []string{"PODIUM_REGISTRY=" + srv.BaseURL},
		toolCall(1, "search_artifacts", map[string]any{"query": "variance"}))
	if !strings.Contains(res.Stdout, "run-variance-analysis") {
		t.Errorf("search did not return the variance artifact:\n%s", res.Stdout)
	}
}

// T-D-readme-40 — MCP load_artifact materializes a skill to disk.
func TestReadme_MCPLoadArtifact(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"greetings/hello/ARTIFACT.md": greetSkillArtifact,
		"greetings/hello/SKILL.md":    skillBody("hello"),
	}))
	mat := t.TempDir()
	res := mcpExec(t, []string{
		"PODIUM_REGISTRY=" + srv.BaseURL,
		"PODIUM_HARNESS=claude-code",
		"PODIUM_MATERIALIZE_ROOT=" + mat,
		"PODIUM_CACHE_DIR=" + t.TempDir(),
	}, toolCall(1, "load_artifact", map[string]any{"id": "greetings/hello"}))
	result := rpcResult(t, res.Stdout, 1)
	if paths, _ := result["materialized_at"].([]any); len(paths) == 0 {
		t.Errorf("missing materialized_at: %v", result)
	}
	mustExist(t, filepath.Join(mat, ".claude/skills/hello/SKILL.md"))
}

// T-D-readme-41 — MCP load_domain returns the domain map. Doc-accuracy
// (F-0.0.2): the wire keys are subdomains/notable, not domains/artifacts.
func TestReadme_MCPLoadDomain(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/close-reporting/run/ARTIFACT.md": contextArtifact("run"),
	}))
	res := mcpExec(t, []string{"PODIUM_REGISTRY=" + srv.BaseURL},
		toolCall(1, "load_domain", map[string]any{"path": "finance/close-reporting"}))
	result := rpcResult(t, res.Stdout, 1)
	if _, ok := result["subdomains"]; !ok {
		t.Errorf("load_domain missing subdomains: %v", result)
	}
	if _, ok := result["notable"]; !ok {
		t.Errorf("load_domain missing notable: %v", result)
	}
}

// T-D-readme-42 — /v1/search_artifacts returns total_matched and results.
func TestReadme_HTTPSearch(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"greet/ARTIFACT.md": contextArtifact("greet the user"),
	}))
	var resp struct {
		TotalMatched int   `json:"total_matched"`
		Results      []any `json:"results"`
	}
	getJSON(t, srv.BaseURL+"/v1/search_artifacts?query=greet", &resp)
	if resp.TotalMatched < 1 || len(resp.Results) == 0 {
		t.Errorf("expected total_matched>=1 and results: %+v", resp)
	}
}

// T-D-readme-43 — /v1/load_artifact returns the manifest.
func TestReadme_HTTPLoadArtifact(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"a/b/ARTIFACT.md": contextArtifact("ab")}))
	var resp struct {
		ID           string `json:"id"`
		Type         string `json:"type"`
		Version      string `json:"version"`
		ManifestBody string `json:"manifest_body"`
		Frontmatter  string `json:"frontmatter"`
	}
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id=a/b", &resp)
	if resp.ID != "a/b" || resp.Type != "context" || resp.Version != "1.0.0" || resp.ManifestBody == "" || resp.Frontmatter == "" {
		t.Errorf("incomplete load_artifact response: %+v", resp)
	}
}

// T-D-readme-44 — /v1/load_domain returns the domain map (subdomains/notable).
func TestReadme_HTTPLoadDomain(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("x")}))
	var resp map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance", &resp)
	_, hasSub := resp["subdomains"]
	_, hasNotable := resp["notable"]
	if !hasSub && !hasNotable {
		t.Errorf("load_domain missing subdomains/notable: %v", resp)
	}
}

// T-D-readme-45 — /v1/layers lists registered layers, each with an ID field.
func TestReadme_HTTPLayers(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	var resp struct {
		Layers []map[string]any `json:"layers"`
	}
	getJSON(t, srv.BaseURL+"/v1/layers", &resp)
	if len(resp.Layers) == 0 {
		t.Fatalf("no layers returned")
	}
	if _, ok := resp.Layers[0]["ID"]; !ok {
		t.Errorf("layer entry missing ID field: %v", resp.Layers[0])
	}
}

// T-D-readme-46 — layer reingest triggers a fresh ingest.
func TestReadme_LayerReingest(t *testing.T) {
	t.Parallel()
	// §0 quickstart: `podium layer reingest <id>` prints a per-artifact
	// confirmation line `artifact: <id>@<version>   layer: <layer>`.
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	extra := writeRegistry(t, map[string]string{"docs/note/ARTIFACT.md": contextArtifact("note")})
	reg := runPodium(t, "", nil, "layer", "register", "--id", "reingest-layer", "--local", extra, "--registry", srv.BaseURL)
	if reg.Exit != 0 {
		t.Fatalf("layer register exit=%d stderr=%s", reg.Exit, reg.Stderr)
	}
	res := runPodium(t, "", nil, "layer", "reingest", "--registry", srv.BaseURL, "reingest-layer")
	if res.Exit != 0 {
		t.Fatalf("layer reingest exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "artifact: docs/note@1.0.0") || !strings.Contains(res.Stdout, "layer: reingest-layer") {
		t.Errorf("reingest output missing per-artifact confirmation line\nwant %q and %q\ngot:\n%s",
			"artifact: docs/note@1.0.0", "layer: reingest-layer", res.Stdout)
	}
}

// T-D-readme-47 — layer register registers a local layer; list shows it.
func TestReadme_LayerRegister(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	extra := writeRegistry(t, map[string]string{"extra/note/ARTIFACT.md": contextArtifact("note")})
	reg := runPodium(t, "", nil, "layer", "register", "--id", "test-layer", "--local", extra, "--registry", srv.BaseURL)
	if reg.Exit != 0 {
		t.Fatalf("layer register exit=%d stderr=%s", reg.Exit, reg.Stderr)
	}
	list := runPodium(t, "", nil, "layer", "list", "--registry", srv.BaseURL)
	if !strings.Contains(list.Stdout, "test-layer") {
		t.Errorf("layer list missing test-layer:\n%s", list.Stdout)
	}
}

// T-D-readme-48 — podium search returns human-readable results.
func TestReadme_SearchCLI(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"greet/ARTIFACT.md": contextArtifact("greet the user")}))
	res := runPodium(t, "", []string{"PODIUM_REGISTRY=" + srv.BaseURL}, "search", "greet")
	if res.Exit != 0 {
		t.Fatalf("search exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "results") || !strings.Contains(res.Stdout, "greet") {
		t.Errorf("search output missing results/artifact:\n%s", res.Stdout)
	}
}

// T-D-readme-49 — podium search --json emits a JSON envelope.
func TestReadme_SearchJSON(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"greet/ARTIFACT.md": contextArtifact("greet the user")}))
	res := runPodium(t, "", nil, "search", "--json", "--registry", srv.BaseURL, "greet")
	var env struct {
		TotalMatched int   `json:"total_matched"`
		Results      []any `json:"results"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, res.Stdout)
	}
}

// T-D-readme-50 — podium search with no query exits 2.
func TestReadme_SearchNoQuery(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", []string{"PODIUM_REGISTRY=http://127.0.0.1:8080"}, "search")
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2", res.Exit)
	}
	if !strings.Contains(res.Stderr, "usage") {
		t.Errorf("missing usage on stderr: %q", res.Stderr)
	}
}

// T-D-readme-51 — Python SDK search_artifacts.
func TestReadme_PythonSDKSearch(t *testing.T) {
	t.Skip("requires the installed Python SDK and a Python runtime; not available in the test sandbox")
}

// T-D-readme-52 — TypeScript SDK searchArtifacts.
func TestReadme_TypeScriptSDKSearch(t *testing.T) {
	t.Skip("requires the built TypeScript SDK and a Node runtime; not available in the test sandbox")
}

// T-D-readme-53 — Docker container image healthz.
func TestReadme_DockerImage(t *testing.T) {
	t.Skip("requires Docker and the published ghcr.io/lennylabs/podium-server image; not available in the test sandbox")
}

// T-D-readme-54 — multi-layer filesystem registry resolves the hierarchy.
func TestReadme_MultiLayerFilesystem(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":                        "multi_layer: true\n",
		"team-shared/greetings/hello/ARTIFACT.md": greetSkillArtifact,
		"team-shared/greetings/hello/SKILL.md":    skillBody("hello"),
		"personal/my-stuff/note/ARTIFACT.md":      contextArtifact("note"),
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, "greetings/hello/ARTIFACT.md"))
	mustExist(t, filepath.Join(tgt, "greetings/hello/SKILL.md"))
	mustExist(t, filepath.Join(tgt, "my-stuff/note/ARTIFACT.md"))
}

// T-D-readme-55 — multi-layer with a top-level ARTIFACT.md is ambiguous.
func TestReadme_MultiLayerAmbiguous(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config": "multi_layer: true\n",
		"ARTIFACT.md":      contextArtifact("top"),
	})
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", t.TempDir())
	if res.Exit == 0 || !strings.Contains(res.Stderr, "ambiguous") {
		t.Errorf("exit=%d stderr=%q, want non-zero + ambiguous", res.Exit, res.Stderr)
	}
}

// T-D-readme-56 — make coverage.
func TestReadme_MakeCoverage(t *testing.T) {
	t.Skip("make coverage runs the full Go suite with -coverprofile; running it from within the suite is recursive and slow")
}

// T-D-readme-57 — cross-harness: skill materializes under .cursor/.
func TestReadme_CursorSkill(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/formatter/ARTIFACT.md": greetSkillArtifact,
		"tools/formatter/SKILL.md":    skillBody("formatter"),
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "cursor")
	if res.Exit != 0 {
		t.Fatalf("cursor sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if files := readTreeFiltered(t, tgt); !hasPrefixKey(files, ".cursor/") {
		t.Errorf("no files under .cursor/: %v", keysOf(files))
	}
}

// T-D-readme-58 — login with missing --registry exits 2.
func TestReadme_LoginNoRegistry(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", []string{"PODIUM_REGISTRY="}, "login")
	if res.Exit != 2 || !strings.Contains(res.Stderr, "no registry configured") {
		t.Errorf("exit=%d stderr=%q", res.Exit, res.Stderr)
	}
}

// T-D-readme-59 — login with missing --issuer exits non-zero.
func TestReadme_LoginNoIssuer(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", []string{"PODIUM_OAUTH_AUTHORIZATION_ENDPOINT="}, "login", "--registry", "https://registry.example.com/")
	if res.Exit == 0 {
		t.Fatalf("expected non-zero exit")
	}
	if !strings.Contains(res.Stderr, "issuer") && !strings.Contains(res.Stderr, "PODIUM_OAUTH_AUTHORIZATION_ENDPOINT") {
		t.Errorf("stderr does not mention issuer:\n%s", res.Stderr)
	}
}

// T-D-readme-60 — logout with missing --registry exits 2.
func TestReadme_LogoutNoRegistry(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", []string{"PODIUM_REGISTRY="}, "logout")
	if res.Exit != 2 || !strings.Contains(res.Stderr, "no registry configured") {
		t.Errorf("exit=%d stderr=%q", res.Exit, res.Stderr)
	}
}

// T-D-readme-61 — quota prints tenant quota information.
func TestReadme_Quota(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	res := runPodium(t, "", nil, "quota", "--registry", srv.BaseURL)
	if res.Exit != 0 {
		t.Fatalf("quota exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "limits") {
		t.Errorf("quota output missing limits:\n%s", res.Stdout)
	}
}

// T-D-readme-62 — domain show returns the domain map via HTTP.
func TestReadme_DomainShow(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("x")}))
	res := runPodium(t, "", nil, "domain", "show", "--registry", srv.BaseURL, "finance")
	if res.Exit != 0 {
		t.Fatalf("domain show exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "finance") {
		t.Errorf("domain show missing finance:\n%s", res.Stdout)
	}
}

// T-D-readme-63 — domain analyze returns discovery metrics.
func TestReadme_DomainAnalyze(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("x")}))
	res := runPodium(t, "", nil, "domain", "analyze", "--registry", srv.BaseURL, "--path", "finance")
	if res.Exit != 0 {
		t.Fatalf("domain analyze exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	var metrics map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &metrics); err != nil {
		t.Fatalf("domain analyze output not JSON: %v\n%s", err, res.Stdout)
	}
	if _, ok := metrics["artifact_count"]; !ok {
		t.Errorf("metrics missing artifact_count: %v", metrics)
	}
}

// T-D-readme-64 — impact lists dependents. A child extends a parent, so the
// reverse-dependency index records the edge and `podium impact` reports it.
func TestReadme_Impact(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":               "multi_layer: true\nlayer_order:\n  - org\n  - team\n",
		"org/shared/parent/ARTIFACT.md":  "---\ntype: context\nversion: 1.0.0\ndescription: parent\n---\n\nbody\n",
		"team/finance/child/ARTIFACT.md": "---\ntype: context\nversion: 2.0.0\ndescription: child\nextends: shared/parent@1.x\n---\n\nbody\n",
	})
	srv := startServer(t, reg)
	res := runPodium(t, "", nil, "impact", "--registry", srv.BaseURL, "shared/parent")
	if res.Exit != 0 {
		t.Fatalf("impact exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "finance/child") {
		t.Errorf("impact should list the extends dependent finance/child:\n%s", res.Stdout)
	}
}

// T-D-readme-65 — sign signs a content hash.
func TestReadme_Sign(t *testing.T) {
	t.Skip("requires a configured signing provider (Sigstore or equivalent); not available in the test sandbox")
}

// T-D-readme-66 — admin erase removes audit entries.
func TestReadme_AdminErase(t *testing.T) {
	t.Skip("requires tenant admin credentials and a seeded audit log; the documented --subject flag is also not the implemented flag, so this is not exercisable in the standalone sandbox")
}

// T-D-readme-67 — admin grant / revoke.
func TestReadme_AdminGrantRevoke(t *testing.T) {
	t.Skip("requires tenant admin credentials; the documented --user flag is not the implemented flag, so grant/revoke is not exercisable in the standalone sandbox")
}

// T-D-readme-68 — serve --layer-path starts a standalone server.
func TestReadme_ServeLayerPath(t *testing.T) {
	t.Parallel()
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir()}, "serve", "--standalone", "--layer-path",
		writeRegistry(t, map[string]string{"greet/ARTIFACT.md": contextArtifact("greet the user")}))
	if st := getStatus(t, srv.BaseURL+"/healthz"); st != 200 {
		t.Errorf("/healthz = HTTP %d", st)
	}
	var resp struct {
		TotalMatched int   `json:"total_matched"`
		Results      []any `json:"results"`
	}
	getJSON(t, srv.BaseURL+"/v1/search_artifacts?query=greet", &resp)
	if resp.TotalMatched < 1 {
		t.Errorf("search returned no matches: %+v", resp)
	}
}

// T-D-readme-69 — sync override --add reports the toggle.
func TestReadme_SyncOverrideAdd(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	reg := writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("x")})
	runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "init", "--registry", reg)
	res := runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "sync", "override", "--add", "finance/x", "--target", t.TempDir())
	if res.Exit != 0 {
		t.Fatalf("sync override exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "toggles.add:") || !strings.Contains(res.Stdout, "finance/x") {
		t.Errorf("stdout missing toggles.add: finance/x:\n%s", res.Stdout)
	}
}

// T-D-readme-70 — sync save-as captures a named profile.
// §7.5.6: a real sync records the resolved scope in the target lock, and
// save-as renders that scope into a sync.yaml profile (F-7.5.8, fixed: the
// lock's scope is populated, so the captured profile reproduces --include).
func TestReadme_SyncSaveAs(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	tgt := t.TempDir()
	env := []string{"HOME=" + t.TempDir()}
	reg := writeRegistry(t, map[string]string{
		"finance/invoice/ARTIFACT.md": contextArtifact("Vendor invoice variance reference for the finance team here."),
		"personal/note/ARTIFACT.md":   contextArtifact("Personal note about variance tracking and reminders for later."),
	})
	// Seed: a real sync scoped to finance/** writes the lock's scope.include.
	seed := runPodium(t, ws, env, "sync", "--registry", reg, "--target", tgt, "--harness", "none", "--include", "finance/**")
	if seed.Exit != 0 {
		t.Fatalf("seed sync exit=%d stderr=%s", seed.Exit, seed.Stderr)
	}
	// save-as captures the lock's scope as a named profile.
	res := runPodium(t, ws, env, "sync", "save-as", "--profile", "dev-focus", "--target", tgt)
	if res.Exit != 0 {
		t.Fatalf("sync save-as exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "profile: dev-focus") {
		t.Errorf("stdout missing `profile: dev-focus`:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "finance/**") {
		t.Errorf("save-as did not reproduce the lock's include scope (F-7.5.8):\n%s", res.Stdout)
	}
}

// T-D-readme-71 — cache prune removes content-cache buckets older than N days.
// Doc-accuracy: --days 0 is the boundary "older than now" and is accepted (it
// prunes every bucket last accessed in the past); only a negative --days is
// rejected, so the documented capability is exercised with an aged bucket.
func TestReadme_CachePrune(t *testing.T) {
	t.Parallel()
	cache := t.TempDir()
	bucket := filepath.Join(cache, "aged-bucket")
	mkdirOld(t, bucket, 10)
	res := runPodium(t, "", nil, "cache", "prune", "--dir", cache, "--days", "1")
	if res.Exit != 0 {
		t.Fatalf("cache prune exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if _, err := os.Stat(bucket); err == nil {
		t.Errorf("aged bucket was not pruned")
	}
	// --days 0 is accepted: the cutoff is the present moment, so a bucket last
	// accessed before now is prunable.
	zeroCache := t.TempDir()
	zeroBucket := filepath.Join(zeroCache, "now-bucket")
	mkdirOld(t, zeroBucket, 1)
	zero := runPodium(t, "", nil, "cache", "prune", "--dir", zeroCache, "--days", "0")
	if zero.Exit != 0 {
		t.Fatalf("--days 0 should be accepted, exit=%d stderr=%q", zero.Exit, zero.Stderr)
	}
	if _, err := os.Stat(zeroBucket); err == nil {
		t.Errorf("--days 0 did not prune a bucket aged before now")
	}
	// A negative --days would push the cutoff into the future; it is rejected.
	neg := runPodium(t, "", nil, "cache", "prune", "--dir", t.TempDir(), "--days", "-1")
	if neg.Exit != 2 || !strings.Contains(neg.Stderr, "negative") {
		t.Errorf("--days -1 should be rejected as negative, exit=%d stderr=%q", neg.Exit, neg.Stderr)
	}
}

// T-D-readme-72 — import converts a skills tree into a Podium layer.
// Doc-accuracy: the implemented flag is --target (the README shows --output).
func TestReadme_Import(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	skillDir := filepath.Join(src, "skills", "greet")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: greet\ndescription: greet the user. Use when greeted.\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	res := runPodium(t, "", nil, "import", "--source", filepath.Join(src, "skills"), "--target", out)
	if res.Exit != 0 {
		t.Fatalf("import exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	tree := readTreeAll(t, out)
	if !hasSuffixKey(tree, "ARTIFACT.md") || !hasSuffixKey(tree, "SKILL.md") {
		t.Errorf("import did not produce ARTIFACT.md + SKILL.md: %v", keysOf(tree))
	}
}

// T-D-readme-73 — profile edit adds an include pattern.
func TestReadme_ProfileEdit(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	reg := writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("x")})
	runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "init", "--registry", reg)
	res := runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "profile", "edit", "dev-focus", "--add-include", "finance/**", "--target", t.TempDir())
	if res.Exit != 0 {
		t.Fatalf("profile edit exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "profile: dev-focus") || !strings.Contains(res.Stdout, "finance/**") {
		t.Errorf("stdout missing profile/include:\n%s", res.Stdout)
	}
}

// T-D-readme-74 — layer watch polls a source and re-ingests.
//
// spec §7.3.1 / §14.10 (F-14.10.2): `podium layer watch` polls the reingest
// endpoint on its interval, and that endpoint now runs the real ingest
// pipeline (wired in 0f8db6f), so the watcher drives periodic ingestion. The
// watcher loops forever, so the test backgrounds it and owns teardown.
func TestReadme_LayerWatch(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	extra := writeRegistry(t, map[string]string{"extra/note/ARTIFACT.md": contextArtifact("note")})
	reg := runPodium(t, "", nil, "layer", "register", "--id", "watch-layer", "--local", extra, "--registry", srv.BaseURL)
	if reg.Exit != 0 {
		t.Fatalf("layer register exit=%d stderr=%s", reg.Exit, reg.Stderr)
	}
	w := cliStartWatchLayer(t, srv.BaseURL, "watch-layer", time.Second)
	if !cliPollLog(w, "[reingest watch-layer]", 8*time.Second) {
		t.Errorf("layer watch did not poll reingest within 8s\nlog:\n%s", w.log())
	}
}

// T-D-readme-75 — admin migrate-to-standard into a live standard deployment.
func TestReadme_MigrateToStandardLive(t *testing.T) {
	t.Skip("requires both a standalone source and a live standard deployment (Postgres + S3) running simultaneously; the SQLite-target migration is covered by TestHIW_MigrateToStandard")
}

// T-D-readme-76 — Homebrew install ships all three binaries.
func TestReadme_Homebrew(t *testing.T) {
	t.Skip("requires macOS Homebrew and the lennylabs/tap; not available in the test sandbox")
}

// T-D-readme-77 — Docker Compose starts Postgres and MinIO for live tests.
func TestReadme_DockerCompose(t *testing.T) {
	t.Skip("requires Docker Compose (make services-up / make test-live against Postgres + MinIO); not available in the test sandbox")
}

// hasPrefixKey reports whether any key in m starts with prefix.
func hasPrefixKey(m map[string]string, prefix string) bool {
	for k := range m {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// hasSuffixKey reports whether any key in m ends with suffix.
func hasSuffixKey(m map[string]string, suffix string) bool {
	for k := range m {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}
	return false
}

// keysOf returns the keys of a map for diagnostic output.
func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
