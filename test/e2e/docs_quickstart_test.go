package e2e

// End-to-end tests for docs/getting-started/quickstart.md (D-quickstart).
// Each test drives the real podium / podium-mcp binaries and asserts the
// behavior the quickstart documents. Doc-accuracy gaps (where the prose
// describes output the implementation does not produce) are asserted
// against actual behavior and noted in comments.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// greetRegistry stages the quickstart's hello-world skill at
// personal/hello/greet and returns the registry root.
func greetRegistry(t *testing.T) string {
	return writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": greetSkillArtifact,
		"personal/hello/greet/SKILL.md":    greetSkillBody,
	})
}

// T-D-quickstart-1 — podium version exits zero and prints a version string.
// Doc: quickstart § 1. Install the CLI.
func TestQuickstart_Version(t *testing.T) {
	t.Parallel()
	for _, arg := range []string{"version", "--version", "-v"} {
		res := runPodium(t, "", nil, arg)
		if res.Exit != 0 {
			t.Errorf("podium %s exit=%d stderr=%s", arg, res.Exit, res.Stderr)
		}
		if !strings.HasPrefix(res.Stdout, "podium ") {
			t.Errorf("podium %s stdout=%q, want prefix %q", arg, res.Stdout, "podium ")
		}
		if strings.Contains(strings.ToLower(res.Stdout), "error") {
			t.Errorf("podium %s stdout contains error: %q", arg, res.Stdout)
		}
	}
}

// T-D-quickstart-2 — podium init writes .podium/sync.yaml with registry and
// harness defaults plus gitignore entries. Doc: quickstart § 2.
func TestQuickstart_InitWritesSyncYAML(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	reg := t.TempDir()
	res := runPodium(t, ws, []string{"HOME=" + t.TempDir()},
		"init", "--registry", reg, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("init exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "Wrote .podium/sync.yaml") {
		t.Errorf("stdout missing 'Wrote .podium/sync.yaml': %q", res.Stdout)
	}
	sync := readFile(t, filepath.Join(ws, ".podium", "sync.yaml"))
	if !strings.Contains(sync, "registry: "+reg) {
		t.Errorf("sync.yaml missing registry %q:\n%s", reg, sync)
	}
	if !strings.Contains(sync, "harness: claude-code") {
		t.Errorf("sync.yaml missing harness: claude-code:\n%s", sync)
	}
	gi := readFile(t, filepath.Join(ws, ".gitignore"))
	for _, want := range []string{".podium/sync.local.yaml", ".podium/overlay/"} {
		if !strings.Contains(gi, want) {
			t.Errorf(".gitignore missing %q:\n%s", want, gi)
		}
	}
}

// T-D-quickstart-3 — podium config show prints a name/value/source table.
// Doc: quickstart § 2 ("Verify: podium config show"). Doc-accuracy: per
// F-7.7.1 config show prints the server config, not the sync.yaml registry
// and harness; this test asserts the actual table content.
func TestQuickstart_ConfigShowTable(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	reg := t.TempDir()
	runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "init", "--registry", reg, "--harness", "claude-code")

	res := runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "config", "show")
	if res.Exit != 0 {
		t.Fatalf("config show exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	// spec §7.7 (F-7.7.1): config show prints the merged sync.yaml with
	// per-key provenance, so the registry and harness from `init` appear
	// alongside the file scope each came from.
	cliContains(t, res.Stdout, reg, "merged registry value")
	cliContains(t, res.Stdout, "claude-code", "merged harness value")
	cliContains(t, res.Stdout, "from", "per-key provenance")
}

// T-D-quickstart-4 — podium init --global writes ~/.podium/sync.yaml and
// leaves the workspace untouched. Doc: quickstart § 2.
func TestQuickstart_InitGlobal(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	home := t.TempDir()
	reg := t.TempDir()
	res := runPodium(t, ws, []string{"HOME=" + home}, "init", "--global", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("init --global exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	global := readFile(t, filepath.Join(home, ".podium", "sync.yaml"))
	if !strings.Contains(global, "registry: "+reg) {
		t.Errorf("global sync.yaml missing registry %q:\n%s", reg, global)
	}
	if _, err := os.Stat(filepath.Join(ws, ".podium", "sync.yaml")); err == nil {
		t.Errorf("workspace gained a .podium/sync.yaml under --global")
	}
	if _, err := os.Stat(filepath.Join(ws, ".gitignore")); err == nil {
		t.Errorf("workspace .gitignore modified under --global scope")
	}
}

// T-D-quickstart-5 — podium init refuses to overwrite sync.yaml without
// --force. Doc: quickstart § 2.
func TestQuickstart_InitRefusesOverwrite(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	home := t.TempDir()
	runPodium(t, ws, []string{"HOME=" + home}, "init", "--registry", "first")
	res := runPodium(t, ws, []string{"HOME=" + home}, "init", "--registry", "second")
	if res.Exit != 2 {
		t.Fatalf("second init exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "already exists") || !strings.Contains(res.Stderr, "--force") {
		t.Errorf("stderr missing 'already exists'/'--force': %q", res.Stderr)
	}
	if got := readFile(t, filepath.Join(ws, ".podium", "sync.yaml")); !strings.Contains(got, "registry: first") {
		t.Errorf("sync.yaml was overwritten:\n%s", got)
	}
}

// T-D-quickstart-6 / T-D-quickstart-22 — the documented skill (ARTIFACT.md +
// SKILL.md) lints cleanly. Doc: quickstart § 3.
func TestQuickstart_LintCleanSkill(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "lint", "--registry", greetRegistry(t))
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Errorf("stdout=%q, want 'lint: no issues.'", res.Stdout)
	}
}

// T-D-quickstart-7 — a type:skill artifact missing SKILL.md fails lint with
// an error diagnostic. Doc: quickstart § Troubleshooting.
func TestQuickstart_LintMissingSkillBody(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": greetSkillArtifact,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s\nstderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
	out := res.Stdout + res.Stderr
	// The implementation emits "error: personal/hello/greet: type: skill
	// missing SKILL.md" (the doc speaks of an "[error]" diagnostic).
	if !strings.Contains(out, "error") || !strings.Contains(out, "personal/hello/greet") {
		t.Errorf("diagnostic missing error/artifact id:\n%s", out)
	}
	if !strings.Contains(strings.ToUpper(out), "SKILL.MD") {
		t.Errorf("diagnostic does not reference SKILL.md:\n%s", out)
	}
}

// T-D-quickstart-8 / T-D-quickstart-30 — sync with claude-code materializes a
// type:skill to .claude/skills/<name>/SKILL.md (NOT .claude/agents/greet.md
// as § 4 / § 5 of the doc claim). Doc-accuracy gap asserted here.
func TestQuickstart_SyncClaudeCodeSkillLayout(t *testing.T) {
	t.Parallel()
	reg := greetRegistry(t)
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "adapter: claude-code") {
		t.Errorf("stdout missing 'adapter: claude-code':\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "target:") || !strings.Contains(res.Stdout, tgt) {
		t.Errorf("stdout missing resolved target %q:\n%s", tgt, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "personal/hello/greet") {
		t.Errorf("stdout missing artifact id:\n%s", res.Stdout)
	}
	skill := readFile(t, filepath.Join(tgt, ".claude", "skills", "greet", "SKILL.md"))
	if !strings.Contains(skill, "name: greet") {
		t.Errorf("materialized SKILL.md missing name: greet:\n%s", skill)
	}
	if _, err := os.Stat(filepath.Join(tgt, ".claude", "agents", "greet.md")); err == nil {
		t.Errorf(".claude/agents/greet.md exists; doc claims this path but skills go to .claude/skills/")
	}
}

// T-D-quickstart-9 — sync output uses adapter/target/artifacts, not the
// doc's "Materialized N artifact" / arrow notation. Doc-accuracy gap.
func TestQuickstart_SyncOutputFormat(t *testing.T) {
	t.Parallel()
	reg := greetRegistry(t)
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if !strings.HasPrefix(res.Stdout, "adapter: none") {
		t.Errorf("stdout does not start with 'adapter: none':\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "target:") {
		t.Errorf("stdout missing target line:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "artifacts:") || !strings.Contains(res.Stdout, "personal/hello/greet") {
		t.Errorf("stdout missing artifacts listing:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "Materialized") {
		t.Errorf("stdout contains doc's 'Materialized N artifact' phrasing:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "→") {
		t.Errorf("stdout contains the doc's arrow notation:\n%s", res.Stdout)
	}
}

// T-D-quickstart-10 — sync with no --registry reads defaults.registry from
// .podium/sync.yaml. Doc: quickstart § 4.
func TestQuickstart_SyncReadsConfigRegistry(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "init", "--registry", reg)
	tgt := t.TempDir()
	res := runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "sync", "--target", tgt)
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if _, err := os.Stat(filepath.Join(tgt, "x", "ARTIFACT.md")); err != nil {
		t.Errorf("expected x/ARTIFACT.md materialized from config registry: %v", err)
	}
}

// T-D-quickstart-11 / T-D-quickstart-24 ish — sync with no registry and no
// sync.yaml exits 2. Doc: quickstart § Troubleshooting (config.no_registry).
func TestQuickstart_SyncNoRegistryExits2(t *testing.T) {
	t.Parallel()
	res := runPodium(t, t.TempDir(), []string{"HOME=" + t.TempDir(), "PODIUM_REGISTRY="}, "sync", "--target", t.TempDir())
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "registry is required") {
		t.Errorf("stderr missing 'registry is required': %q", res.Stderr)
	}
}

// T-D-quickstart-12 — sync --harness none writes the canonical layout.
// Doc: quickstart § 4.
func TestQuickstart_SyncNoneCanonical(t *testing.T) {
	t.Parallel()
	reg := greetRegistry(t)
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, "personal/hello/greet/ARTIFACT.md"))
	mustExist(t, filepath.Join(tgt, "personal/hello/greet/SKILL.md"))
	if _, err := os.Stat(filepath.Join(tgt, ".claude")); err == nil {
		t.Errorf(".claude/ created under --harness none")
	}
}

// T-D-quickstart-13 — sync --dry-run resolves artifacts and writes nothing.
// Doc: quickstart § 4.
func TestQuickstart_SyncDryRun(t *testing.T) {
	t.Parallel()
	reg := greetRegistry(t)
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none", "--dry-run")
	if res.Exit != 0 {
		t.Fatalf("dry-run exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "dry-run") {
		t.Errorf("stdout missing dry-run notice:\n%s", res.Stdout)
	}
	if files := readTreeFiltered(t, tgt); len(files) != 0 {
		t.Errorf("dry-run wrote %d files, want 0: %v", len(files), files)
	}
}

// T-D-quickstart-14 — sync claude-code maps type:agent to
// .claude/agents/<name>.md. Doc: quickstart § What's next.
func TestQuickstart_SyncClaudeCodeAgent(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/agent-demo/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: demo\n---\n\nagent body\n",
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	mustExist(t, filepath.Join(tgt, ".claude/agents/agent-demo.md"))
	if _, err := os.Stat(filepath.Join(tgt, ".claude/skills/agent-demo")); err == nil {
		t.Errorf("agent should not land under .claude/skills/")
	}
}

// T-D-quickstart-15 — sync claude-code maps type:rule to .claude/rules/<name>.md.
func TestQuickstart_SyncClaudeCodeRule(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/code-style/ARTIFACT.md": "---\ntype: rule\nversion: 1.0.0\ndescription: style\nrule_mode: always\n---\n\nrules\n",
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	mustExist(t, filepath.Join(tgt, ".claude/rules/code-style.md"))
}

// T-D-quickstart-16 — sync claude-code places context/command/hook/mcp-server
// under .claude/podium/<id>/. Doc: quickstart § What's next.
func TestQuickstart_SyncClaudeCodeFallbackPath(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/ctx-demo/ARTIFACT.md": contextArtifact("ctx"),
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	mustExist(t, filepath.Join(tgt, ".claude/podium/personal/hello/ctx-demo/ARTIFACT.md"))
}

// T-D-quickstart-17 — sync --watch performs an initial sync and exits 0 on
// SIGINT. Doc: quickstart § Watch mode.
func TestQuickstart_WatchInitialSyncThenSIGINT(t *testing.T) {
	t.Parallel()
	reg := greetRegistry(t)
	tgt := t.TempDir()
	w := startWatch(t, reg, tgt, "none")
	if !pollFile(filepath.Join(tgt, "personal/hello/greet/ARTIFACT.md"), 5*time.Second) {
		t.Fatalf("initial sync did not materialize within 5s\nlog:\n%s", w.log())
	}
	if code := w.stop(t); code != 0 {
		t.Errorf("watch exit after SIGINT = %d, want 0\nlog:\n%s", code, w.log())
	}
	if !strings.Contains(w.log(), "adapter:") || !strings.Contains(w.log(), "artifacts:") {
		t.Errorf("watch log missing initial sync block:\n%s", w.log())
	}
}

// T-D-quickstart-18 — sync --watch re-materializes after a registry edit.
// Doc: quickstart § Watch mode.
func TestQuickstart_WatchRematerializesOnEdit(t *testing.T) {
	t.Parallel()
	reg := greetRegistry(t)
	tgt := t.TempDir()
	skillPath := filepath.Join(tgt, "personal/hello/greet/SKILL.md")
	w := startWatch(t, reg, tgt, "none")
	if !pollFile(skillPath, 5*time.Second) {
		t.Fatalf("initial sync missing\nlog:\n%s", w.log())
	}
	// Append a sentinel to the source SKILL.md.
	srcSkill := filepath.Join(reg, "personal/hello/greet/SKILL.md")
	appendLine(t, srcSkill, "\nSENTINEL-EDIT\n")
	deadline := time.Now().Add(6 * time.Second)
	updated := false
	for time.Now().Before(deadline) {
		if strings.Contains(readFile(t, skillPath), "SENTINEL-EDIT") {
			updated = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	w.stop(t)
	if !updated {
		t.Errorf("watcher did not re-materialize the edit within 6s\nlog:\n%s", w.log())
	}
}

// T-D-quickstart-19 — sync is idempotent across two runs. Doc: quickstart § 4.
func TestQuickstart_SyncIdempotent(t *testing.T) {
	t.Parallel()
	reg := greetRegistry(t)
	tgt := t.TempDir()
	first := syncAndSnapshot(t, reg, tgt)
	second := syncAndSnapshot(t, reg, tgt)
	if len(first) != len(second) {
		t.Fatalf("file count changed: %d -> %d", len(first), len(second))
	}
	for path, content := range first {
		if second[path] != content {
			t.Errorf("content changed for %q across runs", path)
		}
	}
}

// T-D-quickstart-20 — sync with an unknown harness exits non-zero. Doc:
// quickstart § 4 (negative).
func TestQuickstart_SyncUnknownHarness(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", t.TempDir(), "--harness", "not-a-real-harness")
	if res.Exit == 0 {
		t.Fatalf("expected non-zero exit\nstdout=%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "config.unknown_harness") && !strings.Contains(res.Stderr, "not-a-real-harness") {
		t.Errorf("stderr missing unknown-harness signal: %q", res.Stderr)
	}
}

// T-D-quickstart-21 — intermediate domain directories without ARTIFACT.md are
// not treated as artifacts. Doc: quickstart § 3.
func TestQuickstart_IntermediateDirsNotArtifacts(t *testing.T) {
	t.Parallel()
	reg := greetRegistry(t) // personal/ and personal/hello/ are bare dirs
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if c := strings.Count(res.Stdout, "  - "); c != 1 {
		t.Errorf("expected exactly 1 artifact listed, saw %d:\n%s", c, res.Stdout)
	}
	if _, err := os.Stat(filepath.Join(tgt, "personal/ARTIFACT.md")); err == nil {
		t.Errorf("personal/ARTIFACT.md should not exist")
	}
	if _, err := os.Stat(filepath.Join(tgt, "personal/hello/ARTIFACT.md")); err == nil {
		t.Errorf("personal/hello/ARTIFACT.md should not exist")
	}
	mustExist(t, filepath.Join(tgt, "personal/hello/greet/ARTIFACT.md"))
}

// T-D-quickstart-23 — lint on a non-existent registry path exits 1. Doc:
// quickstart § Troubleshooting.
func TestQuickstart_LintBadRegistryPath(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "does-not-exist-xyz")
	res := runPodium(t, "", nil, "lint", "--registry", missing)
	if res.Exit != 1 {
		t.Fatalf("exit=%d, want 1\nstdout=%s\nstderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
	if res.Stderr == "" {
		t.Errorf("expected an error message on stderr")
	}
}

// T-D-quickstart-24 — lint without --registry exits 2. Doc: quickstart § 3.
func TestQuickstart_LintNoRegistry(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "lint")
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--registry is required") {
		t.Errorf("stderr missing '--registry is required': %q", res.Stderr)
	}
}

// T-D-quickstart-25 — sync.yaml written by init carries no credentials.
// Doc: quickstart § 2 ("commit .podium/sync.yaml").
func TestQuickstart_SyncYAMLNoCredentials(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	reg := t.TempDir()
	runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "init", "--registry", reg, "--harness", "claude-code")
	// Scan the field keys only; the registry value is an absolute temp
	// path whose name can incidentally contain a secret-like substring.
	var keyScan []string
	for _, line := range strings.Split(readFile(t, filepath.Join(ws, ".podium", "sync.yaml")), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "registry:") {
			continue
		}
		keyScan = append(keyScan, strings.ToLower(line))
	}
	scanned := strings.Join(keyScan, "\n")
	for _, secret := range []string{"token", "password", "secret", "credential"} {
		if strings.Contains(scanned, secret) {
			t.Errorf("sync.yaml contains %q:\n%s", secret, scanned)
		}
	}
	if strings.Contains(scanned, "sync.local.yaml") {
		t.Errorf("sync.yaml references sync.local.yaml:\n%s", scanned)
	}
}

// T-D-quickstart-26 — init --standalone sets registry to http://127.0.0.1:8080.
func TestQuickstart_InitStandalone(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	res := runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "init", "--standalone")
	if res.Exit != 0 {
		t.Fatalf("init --standalone exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if got := readFile(t, filepath.Join(ws, ".podium", "sync.yaml")); !strings.Contains(got, "http://127.0.0.1:8080") {
		t.Errorf("sync.yaml missing standalone URL:\n%s", got)
	}
}

// T-D-quickstart-27 — init without --registry or --standalone exits 2.
func TestQuickstart_InitNoFlags(t *testing.T) {
	t.Parallel()
	res := runPodium(t, t.TempDir(), []string{"HOME=" + t.TempDir()}, "init")
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "registry") {
		t.Errorf("stderr does not mention registry: %q", res.Stderr)
	}
}

// T-D-quickstart-28 — init --global and --local are mutually exclusive.
func TestQuickstart_InitGlobalLocalMutuallyExclusive(t *testing.T) {
	t.Parallel()
	res := runPodium(t, t.TempDir(), []string{"HOME=" + t.TempDir()}, "init", "--global", "--local", "--registry", "r")
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "mutually exclusive") {
		t.Errorf("stderr missing 'mutually exclusive': %q", res.Stderr)
	}
}

// T-D-quickstart-29 — init --local writes .podium/sync.local.yaml.
func TestQuickstart_InitLocal(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	reg := t.TempDir()
	res := runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "init", "--local", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("init --local exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if got := readFile(t, filepath.Join(ws, ".podium", "sync.local.yaml")); !strings.Contains(got, "registry: "+reg) {
		t.Errorf("sync.local.yaml missing registry:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(ws, ".podium", "sync.yaml")); err == nil {
		t.Errorf(".podium/sync.yaml should not exist under --local")
	}
}

// T-D-quickstart-31 — the materialized SKILL.md has valid frontmatter and a
// non-empty body. Doc: quickstart § 5.
func TestQuickstart_MaterializedSkillContent(t *testing.T) {
	t.Parallel()
	reg := greetRegistry(t)
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	content := readFile(t, filepath.Join(tgt, ".claude/skills/greet/SKILL.md"))
	if !strings.HasPrefix(content, "---") {
		t.Errorf("SKILL.md does not start with frontmatter delimiter:\n%s", content)
	}
	if !strings.Contains(content, "name: greet") {
		t.Errorf("SKILL.md missing name: greet")
	}
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 || strings.TrimSpace(parts[2]) == "" {
		t.Errorf("SKILL.md body is empty:\n%s", content)
	}
}

// T-D-quickstart-32 — sync against an empty registry exits 0 with zero
// artifacts and writes nothing. Doc: quickstart § Troubleshooting.
func TestQuickstart_SyncEmptyRegistry(t *testing.T) {
	t.Parallel()
	reg := t.TempDir()
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "artifacts:") {
		t.Errorf("stdout missing artifacts header:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "  - ") {
		t.Errorf("expected no artifact entries:\n%s", res.Stdout)
	}
	if files := readTreeFiltered(t, tgt); len(files) != 0 {
		t.Errorf("empty registry wrote %d files: %v", len(files), files)
	}
}

// T-D-quickstart-33 — sync --json emits structured output with adapter,
// target, and artifacts keys. Doc: quickstart § 4.
func TestQuickstart_SyncJSON(t *testing.T) {
	t.Parallel()
	reg := greetRegistry(t)
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none", "--json")
	if res.Exit != 0 {
		t.Fatalf("exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	var env struct {
		Adapter   string `json:"adapter"`
		Target    string `json:"target"`
		Artifacts []struct {
			ID string `json:"id"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, res.Stdout)
	}
	if env.Adapter == "" || env.Target == "" {
		t.Errorf("missing adapter/target: %+v", env)
	}
	found := false
	for _, a := range env.Artifacts {
		if a.ID == "personal/hello/greet" {
			found = true
		}
	}
	if !found {
		t.Errorf("artifacts missing personal/hello/greet: %+v", env.Artifacts)
	}
}

// T-D-quickstart-34 — multi-layer registry strips the layer prefix from
// artifact IDs. Doc: quickstart § What's next.
func TestQuickstart_MultiLayerStripsPrefix(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":                        "multi_layer: true\n",
		"team-shared/greetings/hello/ARTIFACT.md": greetSkillArtifact,
		"team-shared/greetings/hello/SKILL.md":    greetSkillBody,
		"personal/notes/ctx/ARTIFACT.md":          contextArtifact("note"),
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "greetings/hello") || !strings.Contains(res.Stdout, "notes/ctx") {
		t.Errorf("stdout missing layer-stripped ids:\n%s", res.Stdout)
	}
	mustExist(t, filepath.Join(tgt, "greetings/hello/ARTIFACT.md"))
	mustExist(t, filepath.Join(tgt, "notes/ctx/ARTIFACT.md"))
}

// T-D-quickstart-35 — a manifest at the top level of a multi-layer registry
// is rejected as ambiguous. Doc: quickstart § What's next (negative).
func TestQuickstart_MultiLayerTopLevelManifestRejected(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config": "multi_layer: true\n",
		"ARTIFACT.md":      contextArtifact("top"),
	})
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", t.TempDir())
	if res.Exit == 0 {
		t.Fatalf("expected non-zero exit\nstdout=%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "ambiguous") {
		t.Errorf("stderr missing 'ambiguous': %q", res.Stderr)
	}
}

// T-D-quickstart-36 — podium serve --standalone serves /healthz and /readyz.
// Doc: quickstart § What's next.
func TestQuickstart_ServeStandaloneHealth(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	srv := startServer(t, reg)
	if st := getStatus(t, srv.BaseURL+"/readyz"); st != 200 {
		t.Errorf("/readyz = %d, want 200", st)
	}
	if st := getStatus(t, srv.BaseURL+"/healthz"); st != 200 {
		t.Errorf("/healthz = %d, want 200", st)
	}
}

// T-D-quickstart-37 — podium help lists the core subcommands. Doc: § 1.
func TestQuickstart_Help(t *testing.T) {
	t.Parallel()
	for _, arg := range []string{"help", "-h", "--help"} {
		res := runPodium(t, "", nil, arg)
		if res.Exit != 0 {
			t.Errorf("podium %s exit=%d", arg, res.Exit)
		}
		for _, want := range []string{"sync", "init", "config show", "lint", "version"} {
			if !strings.Contains(res.Stdout, want) {
				t.Errorf("podium %s missing %q", arg, want)
			}
		}
	}
}

// T-D-quickstart-38 — the binary built from source via go build is
// functional. Doc: quickstart § 1 ("From source"). cmdharness compiles
// ./cmd/podium with `go build`; this asserts that binary runs `version`.
func TestQuickstart_BuildFromSource(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "version")
	if res.Exit != 0 || !strings.HasPrefix(res.Stdout, "podium ") {
		t.Errorf("go-built binary version failed: exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// T-D-quickstart-39 — watch mode is poll-based, not fsnotify. Doc-accuracy:
// the quickstart says fsnotify; pkg/sync/watch.go is poll-based.
func TestQuickstart_WatchIsPollBased(t *testing.T) {
	t.Parallel()
	src := readFile(t, filepath.Join("..", "..", "pkg", "sync", "watch.go"))
	if strings.Contains(src, "fsnotify/fsnotify") {
		t.Errorf("watch.go imports fsnotify; doc-accuracy gap would be resolved")
	}
	if !strings.Contains(src, "poll-based") {
		t.Errorf("watch.go does not document poll-based detection")
	}
	// Behavioral confirmation: an edit is picked up by polling.
	reg := writeRegistry(t, map[string]string{"a/ARTIFACT.md": contextArtifact("a")})
	tgt := t.TempDir()
	w := startWatch(t, reg, tgt, "none")
	if !pollFile(filepath.Join(tgt, "a/ARTIFACT.md"), 5*time.Second) {
		t.Fatalf("initial sync missing\nlog:\n%s", w.log())
	}
	mkArtifact(t, filepath.Join(reg, "b"), contextArtifact("b"))
	got := pollFile(filepath.Join(tgt, "b/ARTIFACT.md"), 5*time.Second)
	w.stop(t)
	if !got {
		t.Errorf("poll-based watcher did not pick up the new artifact\nlog:\n%s", w.log())
	}
}
