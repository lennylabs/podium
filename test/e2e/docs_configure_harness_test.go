package e2e

// End-to-end tests for docs/consuming/configure-your-harness.md
// (D-configure-harness). The page documents per-harness setup via
// `podium sync` (filesystem materialization) and the `podium-mcp` server
// (runtime discovery). Tests drive the real CLI and the podium-mcp stdio
// bridge against filesystem-source registries.
//
// Several documented behaviors are known gaps:
//   - F-7.5.13: `podium sync` ignores PODIUM_HARNESS and the sync.yaml
//     harness, always defaulting the adapter to `none`. Tests 3, 4, and 24
//     assert the actual (none-default) behavior.
//   - F-14.11.1: `podium sync` has no server/URL registry source, so the
//     standalone-against-a-server path (test 63) is skipped.
//   - F-6.7.1 / F-6.7.2: the ingest-time capability-matrix lint and the
//     target_harnesses opt-out are absent, so the lint-warning / lint-error
//     expectations (21, 37, 38, 42, 43, 46, 50) are skipped.
//
// Several adapter destination paths in the doc tables are doc-accuracy gaps;
// these tests assert the implementation's actual on-disk layout.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- fixtures + helpers -----------------------------------------------------

func chRule(mode, extra string) string {
	s := "---\ntype: rule\nversion: 1.0.0\nrule_mode: " + mode + "\n"
	if extra != "" {
		s += extra + "\n"
	}
	return s + "---\n\nRule body for " + mode + ".\n"
}

// chMCPEnv points podium-mcp at a filesystem-source registry (the bridge
// accepts a filesystem path for PODIUM_REGISTRY, see how-it-works tests).
func chMCPEnv(t *testing.T, reg string, extra ...string) []string {
	return append([]string{"PODIUM_REGISTRY=" + reg, "PODIUM_CACHE_DIR=" + t.TempDir()}, extra...)
}

// chInitParams is the documented JSON-RPC initialize params.
var chInitParams = map[string]any{
	"protocolVersion": "2024-11-05",
	"capabilities":    map[string]any{},
	"clientInfo":      map[string]any{"name": "test", "version": "1"},
}

// chAssertInitOK spawns podium-mcp with env, sends initialize, and asserts a
// valid JSON-RPC 2.0 response with the documented protocol version and a
// capabilities object carrying `tools`.
func chAssertInitOK(t *testing.T, env []string) {
	t.Helper()
	res := mcpExec(t, env, rpcReq{ID: 1, Method: "initialize", Params: chInitParams})
	result := rpcResult(t, res.Stdout, 1)
	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocolVersion=%v, want 2024-11-05 (stderr=%s)", result["protocolVersion"], res.Stderr)
	}
	caps, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("missing capabilities object: %v", result)
	}
	if _, ok := caps["tools"]; !ok {
		t.Errorf("capabilities missing tools key: %v", caps)
	}
}

// chWriteSyncYAML writes <ws>/.podium/sync.yaml with the given defaults body.
func chWriteSyncYAML(t *testing.T, ws, defaultsBody string) {
	t.Helper()
	dir := filepath.Join(ws, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .podium: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sync.yaml"), []byte(defaultsBody), 0o644); err != nil {
		t.Fatalf("write sync.yaml: %v", err)
	}
}

// chSync runs one-shot sync and fails on a non-zero exit.
func chSync(t *testing.T, reg, target, harness string) cliResult {
	t.Helper()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", harness)
	if res.Exit != 0 {
		t.Fatalf("sync(%s) exit=%d stderr=%s", harness, res.Exit, res.Stderr)
	}
	return res
}

// ---- Common pieces: init + sync config --------------------------------------

// T-D-configure-harness-1 — podium init writes sync.yaml with registry and harness.
func TestConfigureHarness_InitWritesSyncYAML(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	reg := writeRegistry(t, map[string]string{"glossary/ARTIFACT.md": contextArtifact("glossary")})
	res := runPodium(t, ws, nil, "init", "--registry", reg, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("init exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	yaml := readFile(t, filepath.Join(ws, ".podium", "sync.yaml"))
	if !strings.Contains(yaml, "registry: "+reg) {
		t.Errorf("sync.yaml missing registry %q:\n%s", reg, yaml)
	}
	if !strings.Contains(yaml, "harness: claude-code") {
		t.Errorf("sync.yaml missing harness: claude-code:\n%s", yaml)
	}
	if !strings.Contains(res.Stdout, "Wrote") || !strings.Contains(res.Stdout, "sync.yaml") {
		t.Errorf("init stdout missing Wrote ...sync.yaml: %q", res.Stdout)
	}
}

// T-D-configure-harness-2 — podium sync reads registry from sync.yaml when --registry omitted.
func TestConfigureHarness_SyncReadsRegistryFromConfig(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"glossary/ARTIFACT.md": contextArtifact("glossary")})
	ws := t.TempDir()
	chWriteSyncYAML(t, ws, "defaults:\n  registry: "+reg+"\n")
	res := runPodium(t, ws, []string{"PODIUM_REGISTRY="}, "sync")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(ws, "glossary", "ARTIFACT.md"))
}

// T-D-configure-harness-3 — podium sync ignores the sync.yaml harness field and
// defaults the adapter to none (F-7.5.13). Documents the actual behavior.
func TestConfigureHarness_SyncIgnoresConfigHarness(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"greet/ARTIFACT.md": greetSkillArtifact,
		"greet/SKILL.md":    skillBody("greet"),
	})
	ws := t.TempDir()
	chWriteSyncYAML(t, ws, "defaults:\n  registry: "+reg+"\n  harness: claude-code\n")
	res := runPodium(t, ws, []string{"PODIUM_REGISTRY=", "PODIUM_HARNESS="}, "sync")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	// Actual: none-layout, not .claude/skills.
	mustExist(t, filepath.Join(ws, "greet", "ARTIFACT.md"))
	if _, err := os.Stat(filepath.Join(ws, ".claude")); err == nil {
		t.Errorf("F-7.5.13: sync should not honor sync.yaml harness, but a .claude/ dir appeared")
	}
}

// T-D-configure-harness-4 — podium sync ignores PODIUM_HARNESS (F-7.5.13).
func TestConfigureHarness_SyncIgnoresEnvHarness(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"greet/ARTIFACT.md": greetSkillArtifact,
		"greet/SKILL.md":    skillBody("greet"),
	})
	target := t.TempDir()
	res := runPodium(t, "", []string{"PODIUM_HARNESS=claude-code"},
		"sync", "--registry", reg, "--target", target)
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(target, "greet", "ARTIFACT.md"))
	if _, err := os.Stat(filepath.Join(target, ".claude")); err == nil {
		t.Errorf("F-7.5.13: PODIUM_HARNESS must not be read by sync, but a .claude/ dir appeared")
	}
}

// T-D-configure-harness-5 — sync with no registry and no sync.yaml exits 2.
func TestConfigureHarness_SyncNoRegistryFails(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	res := runPodium(t, ws, []string{"PODIUM_REGISTRY="}, "sync")
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--registry is required") {
		t.Errorf("stderr missing '--registry is required':\n%s", res.Stderr)
	}
}

// T-D-configure-harness-6 — sync one-shot completes without --watch.
func TestConfigureHarness_SyncOneShot(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"glossary/ARTIFACT.md": contextArtifact("glossary")})
	target := t.TempDir()
	res := chSync(t, reg, target, "none")
	mustExist(t, filepath.Join(target, "glossary", "ARTIFACT.md"))
	if strings.TrimSpace(res.Stdout) == "" {
		t.Errorf("expected a materialization summary on stdout")
	}
}

// T-D-configure-harness-7 — sync --watch exits cleanly on SIGINT.
func TestConfigureHarness_WatchSIGINT(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"glossary/ARTIFACT.md": contextArtifact("glossary")})
	target := t.TempDir()
	w := startWatch(t, reg, target, "none")
	if !pollFile(filepath.Join(target, "glossary", "ARTIFACT.md"), 10*time.Second) {
		t.Fatalf("initial sync did not materialize\nlog:\n%s", w.log())
	}
	if code := w.stop(t); code != 0 {
		t.Errorf("watch exit=%d on SIGINT, want 0\nlog:\n%s", code, w.log())
	}
}

// T-D-configure-harness-8 — sync --watch picks up a new artifact.
func TestConfigureHarness_WatchPicksUpNewArtifact(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"seed/ARTIFACT.md": contextArtifact("seed")})
	target := t.TempDir()
	w := startWatch(t, reg, target, "none")
	if !pollFile(filepath.Join(target, "seed", "ARTIFACT.md"), 10*time.Second) {
		t.Fatalf("initial sync did not materialize\nlog:\n%s", w.log())
	}
	mkArtifact(t, filepath.Join(reg, "my-rule"), "---\ntype: rule\nversion: 1.0.0\n---\n\nRules body.\n")
	if !pollFile(filepath.Join(target, "my-rule", "ARTIFACT.md"), 10*time.Second) {
		t.Errorf("watcher did not materialize the new artifact\nlog:\n%s", w.log())
	}
	w.stop(t)
}

// T-D-configure-harness-9 — podium-mcp exits 2 when PODIUM_REGISTRY is unset.
func TestConfigureHarness_MCPRequiresRegistry(t *testing.T) {
	t.Parallel()
	res := mcpExec(t, []string{"PODIUM_REGISTRY="}, rpcReq{ID: 1, Method: "initialize", Params: chInitParams})
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "PODIUM_REGISTRY is required") {
		t.Errorf("stderr missing 'PODIUM_REGISTRY is required':\n%s", res.Stderr)
	}
}

// T-D-configure-harness-10 — podium-mcp accepts PODIUM_HARNESS=claude-code and
// responds to initialize.
func TestConfigureHarness_MCPClaudeCodeInitialize(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_HARNESS=claude-code"))
}

// T-D-configure-harness-11 — podium-mcp accepts PODIUM_OVERLAY_PATH.
func TestConfigureHarness_MCPOverlayPathAccepted(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	overlay := t.TempDir()
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_HARNESS=claude-code", "PODIUM_OVERLAY_PATH="+overlay))
}

// T-D-configure-harness-12 — podium-mcp exposes the four meta-tools.
func TestConfigureHarness_MCPFourTools(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	res := mcpExec(t, chMCPEnv(t, reg), rpcReq{ID: 2, Method: "tools/list", Params: map[string]any{}})
	for _, tool := range []string{"load_domain", "search_domains", "search_artifacts", "load_artifact"} {
		if !strings.Contains(res.Stdout, tool) {
			t.Errorf("tools/list missing %q:\n%s", tool, res.Stdout)
		}
	}
}

// ---- Claude Code adapter ----------------------------------------------------

// T-D-configure-harness-13 — claude-code skill writes .claude/skills/<name>/SKILL.md.
func TestConfigureHarness_ClaudeCodeSkill(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"greetings/hello/ARTIFACT.md": greetSkillArtifact,
		"greetings/hello/SKILL.md":    "---\nname: hello-world\ndescription: Say hello.\n---\n\nBody.\n",
	})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	got := readFile(t, filepath.Join(target, ".claude", "skills", "hello", "SKILL.md"))
	if !strings.Contains(got, "Say hello.") {
		t.Errorf("skill SKILL.md missing description:\n%s", got)
	}
}

// T-D-configure-harness-14 — claude-code agent writes .claude/agents/<name>.md.
func TestConfigureHarness_ClaudeCodeAgent(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"assistants/qa-agent/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: QA.\n---\n\nQA agent body\n",
	})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	got := readFile(t, filepath.Join(target, ".claude", "agents", "qa-agent.md"))
	if !strings.Contains(got, "QA agent body") {
		t.Errorf("agent file missing body:\n%s", got)
	}
}

// T-D-configure-harness-15 — claude-code rule writes .claude/rules/<name>.md.
func TestConfigureHarness_ClaudeCodeRule(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"rules/code-style/ARTIFACT.md": chRule("always", "")})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	got := readFile(t, filepath.Join(target, ".claude", "rules", "code-style.md"))
	if !strings.Contains(got, "Rule body") {
		t.Errorf("rule file missing content:\n%s", got)
	}
}

// T-D-configure-harness-16 — claude-code context lands under .claude/podium/<id>/
// (doc says .claude/context/). Doc-accuracy gap; assert actual.
func TestConfigureHarness_ClaudeCodeContextDefaultCase(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"glossary/company/ARTIFACT.md": contextArtifact("company glossary")})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	mustExist(t, filepath.Join(target, ".claude", "podium", "glossary", "company", "ARTIFACT.md"))
}

// T-D-configure-harness-17 — claude-code command lands under .claude/podium/<id>/.
func TestConfigureHarness_ClaudeCodeCommandDefaultCase(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/deploy/ARTIFACT.md": "---\ntype: command\nversion: 1.0.0\ndescription: Deploy.\n---\n\n$ARGUMENTS\n",
	})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	mustExist(t, filepath.Join(target, ".claude", "podium", "tools", "deploy", "ARTIFACT.md"))
}

// T-D-configure-harness-18 — claude-code hook lands under .claude/podium/<id>/.
func TestConfigureHarness_ClaudeCodeHookDefaultCase(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/pre-commit/ARTIFACT.md": "---\ntype: hook\nversion: 1.0.0\ndescription: A hook.\nhook_event: stop\nhook_action: echo done\n---\n\nbody\n",
	})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	mustExist(t, filepath.Join(target, ".claude", "podium", "hooks", "pre-commit", "ARTIFACT.md"))
}

// T-D-configure-harness-19 — claude-code skill bundled resources co-locate under
// .claude/skills/<name>/.
func TestConfigureHarness_ClaudeCodeSkillResources(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/analyzer/ARTIFACT.md":    greetSkillArtifact,
		"tools/analyzer/SKILL.md":       skillBody("analyzer"),
		"tools/analyzer/scripts/run.py": "print('hi')\n",
	})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	mustExist(t, filepath.Join(target, ".claude", "skills", "analyzer", "SKILL.md"))
	mustExist(t, filepath.Join(target, ".claude", "skills", "analyzer", "scripts", "run.py"))
}

// T-D-configure-harness-20 — claude-code non-skill bundled resources land under
// .claude/podium/<id>/.
func TestConfigureHarness_ClaudeCodeNonSkillResources(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/deploy/ARTIFACT.md":       "---\ntype: command\nversion: 1.0.0\ndescription: Deploy.\n---\n\n$ARGUMENTS\n",
		"tools/deploy/scripts/deploy.sh": "#!/bin/sh\necho deploy\n",
	})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	mustExist(t, filepath.Join(target, ".claude", "podium", "tools", "deploy", "ARTIFACT.md"))
	mustExist(t, filepath.Join(target, ".claude", "podium", "tools", "deploy", "scripts", "deploy.sh"))
}

// T-D-configure-harness-21 — claude-code rule_mode glob fallback warning.
func TestConfigureHarness_ClaudeCodeGlobLintWarning(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-6.7.1: the ingest-time capability-matrix lint is absent, so claude-code/glob emits no fallback warning")
}

// T-D-configure-harness-22 — claude-code rule_mode auto carries the description
// field through to the materialized rule file.
func TestConfigureHarness_ClaudeCodeAutoDescription(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/py-style/ARTIFACT.md": chRule("auto", "description: Use when working with Python files"),
	})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	got := readFile(t, filepath.Join(target, ".claude", "rules", "py-style.md"))
	if !strings.Contains(got, "Use when working with Python files") {
		t.Errorf("rule file missing description value:\n%s", got)
	}
}

// T-D-configure-harness-23 — claude-code always and explicit are fully supported.
func TestConfigureHarness_ClaudeCodeAlwaysExplicit(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/always-rule/ARTIFACT.md":   chRule("always", ""),
		"rules/explicit-rule/ARTIFACT.md": chRule("explicit", ""),
	})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	mustExist(t, filepath.Join(target, ".claude", "rules", "always-rule.md"))
	mustExist(t, filepath.Join(target, ".claude", "rules", "explicit-rule.md"))
	if lint := runPodium(t, "", nil, "lint", "--registry", reg); lint.Exit != 0 {
		t.Errorf("lint exit=%d, want 0 (stderr=%s, stdout=%s)", lint.Exit, lint.Stderr, lint.Stdout)
	}
}

// T-D-configure-harness-24 — claude-code init+sync two-step (explicit --harness
// required on sync per F-7.5.13).
func TestConfigureHarness_ClaudeCodeInitSync(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"greetings/hello/ARTIFACT.md": greetSkillArtifact,
		"greetings/hello/SKILL.md":    "---\nname: hello\ndescription: Say hi.\n---\n\nBody.\n",
	})
	ws := t.TempDir()
	if r := runPodium(t, ws, nil, "init", "--registry", reg, "--harness", "claude-code"); r.Exit != 0 {
		t.Fatalf("init exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if r := runPodium(t, ws, nil, "sync", "--harness", "claude-code"); r.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	yaml := readFile(t, filepath.Join(ws, ".podium", "sync.yaml"))
	if !strings.Contains(yaml, "harness: claude-code") || !strings.Contains(yaml, "registry: "+reg) {
		t.Errorf("sync.yaml missing registry/harness:\n%s", yaml)
	}
	mustExist(t, filepath.Join(ws, ".claude", "skills", "hello", "SKILL.md"))
}

// ---- Claude Desktop ---------------------------------------------------------

// T-D-configure-harness-25 — claude-desktop writes .claude-desktop/extensions/<id>/.
func TestConfigureHarness_ClaudeDesktopExtensionLayout(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"greet/ARTIFACT.md": greetSkillArtifact,
		"greet/SKILL.md":    skillBody("greet"),
	})
	target := t.TempDir()
	chSync(t, reg, target, "claude-desktop")
	mustExist(t, filepath.Join(target, ".claude-desktop", "extensions", "greet", "ARTIFACT.md"))
	mustExist(t, filepath.Join(target, ".claude-desktop", "extensions", "greet", "SKILL.md"))
}

// T-D-configure-harness-26 — claude-desktop MCP startup.
func TestConfigureHarness_MCPClaudeDesktop(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_HARNESS=claude-desktop"))
}

// ---- Claude Cowork ----------------------------------------------------------

// T-D-configure-harness-27 — claude-cowork writes .claude-cowork/plugins/<id>/.
func TestConfigureHarness_ClaudeCoworkPluginLayout(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"greet/ARTIFACT.md": greetSkillArtifact,
		"greet/SKILL.md":    skillBody("greet"),
	})
	cowork := t.TempDir()
	chSync(t, reg, cowork, "claude-cowork")
	mustExist(t, filepath.Join(cowork, ".claude-cowork", "plugins", "greet", "ARTIFACT.md"))
}

// T-D-configure-harness-28 — claude-cowork sync, git add, git commit sequence.
func TestConfigureHarness_ClaudeCoworkGitCommit(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"greet/ARTIFACT.md": greetSkillArtifact,
		"greet/SKILL.md":    skillBody("greet"),
	})
	cowork := t.TempDir()
	if _, ok := runExternal(t, cowork, 30*time.Second, "git", "init"); !ok {
		t.Skip("git not installed")
	}
	chSync(t, reg, cowork, "claude-cowork")
	if r, _ := runExternal(t, cowork, 30*time.Second, "git", "add", "."); r.Exit != 0 {
		t.Fatalf("git add exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if r, _ := runExternal(t, cowork, 30*time.Second, "git",
		"-c", "user.email=alice@acme.com", "-c", "user.name=alice",
		"commit", "-m", "Sync from Podium"); r.Exit != 0 {
		t.Fatalf("git commit exit=%d stderr=%s stdout=%s", r.Exit, r.Stderr, r.Stdout)
	}
	if r, _ := runExternal(t, cowork, 30*time.Second, "git", "show", "--stat", "HEAD"); !strings.Contains(r.Stdout, ".claude-cowork") {
		t.Errorf("commit does not include the plugin layout:\n%s", r.Stdout)
	}
}

// T-D-configure-harness-29 — claude-cowork MCP startup is not refused.
func TestConfigureHarness_MCPClaudeCowork(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_HARNESS=claude-cowork"))
}

// ---- Cursor -----------------------------------------------------------------

// T-D-configure-harness-30 — cursor rule writes .cursor/rules/<name>.mdc.
func TestConfigureHarness_CursorRule(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"style/naming/ARTIFACT.md": chRule("always", "")})
	target := t.TempDir()
	chSync(t, reg, target, "cursor")
	got := readFile(t, filepath.Join(target, ".cursor", "rules", "naming.mdc"))
	if !strings.HasPrefix(got, "---") {
		t.Errorf(".mdc does not begin with frontmatter:\n%s", got)
	}
	if !strings.Contains(got, "Rule body") {
		t.Errorf(".mdc missing the original rule content:\n%s", got)
	}
}

// T-D-configure-harness-31 — cursor non-rule lands under .cursor/extensions/<id>/.
func TestConfigureHarness_CursorNonRuleDefaultCase(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/greet/ARTIFACT.md": greetSkillArtifact,
		"tools/greet/SKILL.md":    skillBody("greet"),
	})
	target := t.TempDir()
	chSync(t, reg, target, "cursor")
	mustExist(t, filepath.Join(target, ".cursor", "extensions", "tools", "greet", "ARTIFACT.md"))
}

// T-D-configure-harness-32 — cursor: all four rule_mode values produce .mdc with no lint errors.
func TestConfigureHarness_CursorAllRuleModes(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"r/always/ARTIFACT.md":   chRule("always", ""),
		"r/glob/ARTIFACT.md":     chRule("glob", `rule_globs: "src/**/*.ts"`),
		"r/auto/ARTIFACT.md":     chRule("auto", "rule_description: Use for TS"),
		"r/explicit/ARTIFACT.md": chRule("explicit", ""),
	})
	target := t.TempDir()
	chSync(t, reg, target, "cursor")
	if lint := runPodium(t, "", nil, "lint", "--registry", reg); lint.Exit != 0 {
		t.Errorf("lint exit=%d, want 0 (stdout=%s)", lint.Exit, lint.Stdout)
	}
	for _, n := range []string{"always", "glob", "auto", "explicit"} {
		mustExist(t, filepath.Join(target, ".cursor", "rules", n+".mdc"))
	}
}

// T-D-configure-harness-33 — cursor init+sync two-step.
func TestConfigureHarness_CursorInitSync(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"style/naming/ARTIFACT.md": chRule("always", "")})
	ws := t.TempDir()
	if r := runPodium(t, ws, nil, "init", "--registry", reg, "--harness", "cursor"); r.Exit != 0 {
		t.Fatalf("init exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if r := runPodium(t, ws, nil, "sync", "--harness", "cursor"); r.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if yaml := readFile(t, filepath.Join(ws, ".podium", "sync.yaml")); !strings.Contains(yaml, "harness: cursor") {
		t.Errorf("sync.yaml missing harness: cursor:\n%s", yaml)
	}
	mustExist(t, filepath.Join(ws, ".cursor", "rules", "naming.mdc"))
}

// T-D-configure-harness-34 — cursor MCP startup.
func TestConfigureHarness_MCPCursor(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_HARNESS=cursor"))
}

// ---- OpenCode ---------------------------------------------------------------

// T-D-configure-harness-35 — opencode rule writes .opencode/rules/<name>.md.
func TestConfigureHarness_OpenCodeRule(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"style/naming/ARTIFACT.md": chRule("explicit", "")})
	target := t.TempDir()
	chSync(t, reg, target, "opencode")
	got := readFile(t, filepath.Join(target, ".opencode", "rules", "naming.md"))
	if !strings.Contains(got, "Rule body") {
		t.Errorf("rule file missing content:\n%s", got)
	}
}

// T-D-configure-harness-36 — opencode non-rule lands under .opencode/packages/<id>/.
func TestConfigureHarness_OpenCodeNonRuleDefaultCase(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/greet/ARTIFACT.md": greetSkillArtifact,
		"tools/greet/SKILL.md":    skillBody("greet"),
	})
	target := t.TempDir()
	chSync(t, reg, target, "opencode")
	mustExist(t, filepath.Join(target, ".opencode", "packages", "tools", "greet", "ARTIFACT.md"))
}

// T-D-configure-harness-37 — opencode rule_mode auto lint error.
func TestConfigureHarness_OpenCodeAutoLintError(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-6.7.1: the ingest-time capability-matrix lint is absent, so opencode/auto materializes without an error")
}

// T-D-configure-harness-38 — opencode auto passes lint when target_harnesses excludes opencode.
func TestConfigureHarness_OpenCodeAutoTargetHarnessesExclude(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-6.7.2: target_harnesses is parsed but never honored, so it cannot suppress a cross-harness error")
}

// T-D-configure-harness-39 — opencode init+sync two-step.
func TestConfigureHarness_OpenCodeInitSync(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"style/naming/ARTIFACT.md": chRule("always", "")})
	ws := t.TempDir()
	if r := runPodium(t, ws, nil, "init", "--registry", reg, "--harness", "opencode"); r.Exit != 0 {
		t.Fatalf("init exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if r := runPodium(t, ws, nil, "sync", "--harness", "opencode", "--registry", reg); r.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if yaml := readFile(t, filepath.Join(ws, ".podium", "sync.yaml")); !strings.Contains(yaml, "harness: opencode") {
		t.Errorf("sync.yaml missing harness: opencode:\n%s", yaml)
	}
	mustExist(t, filepath.Join(ws, ".opencode"))
}

// T-D-configure-harness-40 — opencode MCP startup.
func TestConfigureHarness_MCPOpenCode(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_HARNESS=opencode"))
}

// ---- Codex ------------------------------------------------------------------

// T-D-configure-harness-41 — codex rule writes .codex/rules/<name>.md plus the
// canonical .codex/packages/<id>/ copy.
func TestConfigureHarness_CodexRule(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"style/naming/ARTIFACT.md": chRule("always", "")})
	target := t.TempDir()
	chSync(t, reg, target, "codex")
	mustExist(t, filepath.Join(target, ".codex", "rules", "naming.md"))
	mustExist(t, filepath.Join(target, ".codex", "packages", "style", "naming", "ARTIFACT.md"))
}

// T-D-configure-harness-42 — codex rule_mode auto lint error.
func TestConfigureHarness_CodexAutoLintError(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-6.7.1: the ingest-time capability-matrix lint is absent, so codex/auto materializes without an error")
}

// T-D-configure-harness-43 — codex hook-type lint error.
func TestConfigureHarness_CodexHookLintError(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-6.7.1: the ingest-time capability-matrix lint is absent, so a codex hook artifact is not flagged")
}

// T-D-configure-harness-44 — codex init+sync two-step.
func TestConfigureHarness_CodexInitSync(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"glossary/ARTIFACT.md": contextArtifact("glossary")})
	ws := t.TempDir()
	if r := runPodium(t, ws, nil, "init", "--registry", reg, "--harness", "codex"); r.Exit != 0 {
		t.Fatalf("init exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if r := runPodium(t, ws, nil, "sync", "--harness", "codex", "--registry", reg); r.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if yaml := readFile(t, filepath.Join(ws, ".podium", "sync.yaml")); !strings.Contains(yaml, "harness: codex") {
		t.Errorf("sync.yaml missing harness: codex:\n%s", yaml)
	}
	mustExist(t, filepath.Join(ws, ".codex", "packages", "glossary", "ARTIFACT.md"))
}

// ---- Gemini -----------------------------------------------------------------

// T-D-configure-harness-45 — gemini writes under .gemini/extensions/<id>/.
func TestConfigureHarness_GeminiExtensionLayout(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"glossary/ARTIFACT.md": contextArtifact("glossary")})
	target := t.TempDir()
	chSync(t, reg, target, "gemini")
	mustExist(t, filepath.Join(target, ".gemini", "extensions", "glossary", "ARTIFACT.md"))
}

// T-D-configure-harness-46 — gemini rule_mode always fallback warning.
func TestConfigureHarness_GeminiAlwaysLintWarning(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-6.7.1: the ingest-time capability-matrix lint is absent, so gemini/always emits no fallback warning")
}

// T-D-configure-harness-47 — gemini init+sync two-step.
func TestConfigureHarness_GeminiInitSync(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"glossary/ARTIFACT.md": contextArtifact("glossary")})
	ws := t.TempDir()
	if r := runPodium(t, ws, nil, "init", "--registry", reg, "--harness", "gemini"); r.Exit != 0 {
		t.Fatalf("init exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if r := runPodium(t, ws, nil, "sync", "--harness", "gemini", "--registry", reg); r.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if yaml := readFile(t, filepath.Join(ws, ".podium", "sync.yaml")); !strings.Contains(yaml, "harness: gemini") {
		t.Errorf("sync.yaml missing harness: gemini:\n%s", yaml)
	}
}

// ---- Pi ---------------------------------------------------------------------

// T-D-configure-harness-48 — pi rule writes .pi/rules/<name>.md.
func TestConfigureHarness_PiRule(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"style/naming/ARTIFACT.md": chRule("explicit", "")})
	target := t.TempDir()
	chSync(t, reg, target, "pi")
	mustExist(t, filepath.Join(target, ".pi", "rules", "naming.md"))
}

// T-D-configure-harness-49 — pi non-rule lands under .pi/packages/<id>/.
func TestConfigureHarness_PiNonRuleDefaultCase(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/greet/ARTIFACT.md": greetSkillArtifact,
		"tools/greet/SKILL.md":    skillBody("greet"),
	})
	target := t.TempDir()
	chSync(t, reg, target, "pi")
	mustExist(t, filepath.Join(target, ".pi", "packages", "tools", "greet", "ARTIFACT.md"))
}

// T-D-configure-harness-50 — pi rule_mode auto lint failure.
func TestConfigureHarness_PiAutoLintError(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-6.7.1: the ingest-time capability-matrix lint is absent, so pi/auto materializes without an error")
}

// T-D-configure-harness-51 — pi init+sync two-step.
func TestConfigureHarness_PiInitSync(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"style/naming/ARTIFACT.md": chRule("explicit", "")})
	ws := t.TempDir()
	if r := runPodium(t, ws, nil, "init", "--registry", reg, "--harness", "pi"); r.Exit != 0 {
		t.Fatalf("init exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if r := runPodium(t, ws, nil, "sync", "--harness", "pi", "--registry", reg); r.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if yaml := readFile(t, filepath.Join(ws, ".podium", "sync.yaml")); !strings.Contains(yaml, "harness: pi") {
		t.Errorf("sync.yaml missing harness: pi:\n%s", yaml)
	}
	mustExist(t, filepath.Join(ws, ".pi"))
}

// T-D-configure-harness-52 — pi MCP startup.
func TestConfigureHarness_MCPPi(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_HARNESS=pi"))
}

// ---- Hermes -----------------------------------------------------------------

// T-D-configure-harness-53 — hermes rule writes .claude/rules/<name>.md.
func TestConfigureHarness_HermesRule(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"style/naming/ARTIFACT.md": chRule("always", "")})
	target := t.TempDir()
	chSync(t, reg, target, "hermes")
	got := readFile(t, filepath.Join(target, ".claude", "rules", "naming.md"))
	if !strings.Contains(got, "Rule body") {
		t.Errorf("rule file missing content:\n%s", got)
	}
}

// T-D-configure-harness-54 — hermes non-rule lands under .hermes/packages/<id>/.
func TestConfigureHarness_HermesNonRuleDefaultCase(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/greet/ARTIFACT.md": greetSkillArtifact,
		"tools/greet/SKILL.md":    skillBody("greet"),
	})
	target := t.TempDir()
	chSync(t, reg, target, "hermes")
	mustExist(t, filepath.Join(target, ".hermes", "packages", "tools", "greet", "ARTIFACT.md"))
}

// T-D-configure-harness-55 — hermes: all four rule_mode values materialize.
func TestConfigureHarness_HermesAllRuleModes(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"r/always/ARTIFACT.md":   chRule("always", ""),
		"r/glob/ARTIFACT.md":     chRule("glob", `rule_globs: "src/**/*.ts"`),
		"r/auto/ARTIFACT.md":     chRule("auto", "rule_description: Use for TS"),
		"r/explicit/ARTIFACT.md": chRule("explicit", ""),
	})
	target := t.TempDir()
	chSync(t, reg, target, "hermes")
	for _, n := range []string{"always", "glob", "auto", "explicit"} {
		mustExist(t, filepath.Join(target, ".claude", "rules", n+".md"))
	}
}

// T-D-configure-harness-56 — hermes init+sync two-step.
func TestConfigureHarness_HermesInitSync(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"style/naming/ARTIFACT.md": chRule("always", "")})
	ws := t.TempDir()
	if r := runPodium(t, ws, nil, "init", "--registry", reg, "--harness", "hermes"); r.Exit != 0 {
		t.Fatalf("init exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if r := runPodium(t, ws, nil, "sync", "--harness", "hermes", "--registry", reg); r.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if yaml := readFile(t, filepath.Join(ws, ".podium", "sync.yaml")); !strings.Contains(yaml, "harness: hermes") {
		t.Errorf("sync.yaml missing harness: hermes:\n%s", yaml)
	}
	mustExist(t, filepath.Join(ws, ".claude", "rules", "naming.md"))
}

// T-D-configure-harness-57 — hermes MCP startup.
func TestConfigureHarness_MCPHermes(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_HARNESS=hermes"))
}

// ---- Generic / none ---------------------------------------------------------

// T-D-configure-harness-58 — none writes the canonical <id>/ layout.
func TestConfigureHarness_NoneCanonicalLayout(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"glossary/ARTIFACT.md": contextArtifact("glossary"),
		"greet/ARTIFACT.md":    greetSkillArtifact,
		"greet/SKILL.md":       skillBody("greet"),
	})
	target := t.TempDir()
	chSync(t, reg, target, "none")
	mustExist(t, filepath.Join(target, "glossary", "ARTIFACT.md"))
	mustExist(t, filepath.Join(target, "greet", "ARTIFACT.md"))
	mustExist(t, filepath.Join(target, "greet", "SKILL.md"))
	for _, dir := range []string{".claude", ".cursor", ".opencode", ".codex", ".gemini", ".pi", ".hermes"} {
		if _, err := os.Stat(filepath.Join(target, dir)); err == nil {
			t.Errorf("none adapter created a harness-specific dir: %s", dir)
		}
	}
}

// T-D-configure-harness-59 — none MCP startup.
func TestConfigureHarness_MCPNone(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_HARNESS=none"))
}

// T-D-configure-harness-60 — none init+sync (default adapter when omitted).
func TestConfigureHarness_NoneInitSync(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"greet/ARTIFACT.md": greetSkillArtifact,
		"greet/SKILL.md":    skillBody("greet"),
	})
	ws := t.TempDir()
	if r := runPodium(t, ws, nil, "init", "--registry", reg, "--harness", "none"); r.Exit != 0 {
		t.Fatalf("init exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if r := runPodium(t, ws, nil, "sync", "--registry", reg); r.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	mustExist(t, filepath.Join(ws, "greet", "ARTIFACT.md"))
}

// T-D-configure-harness-61 — unknown harness rejected with config.unknown_harness.
func TestConfigureHarness_UnknownHarness(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", t.TempDir(), "--harness", "totally-unknown-harness")
	if res.Exit == 0 {
		t.Errorf("expected non-zero exit for unknown harness")
	}
	if !strings.Contains(res.Stderr, "config.unknown_harness") {
		t.Errorf("stderr missing config.unknown_harness:\n%s", res.Stderr)
	}
}

// ---- Standalone -------------------------------------------------------------

// T-D-configure-harness-62 — init --standalone writes the localhost registry.
func TestConfigureHarness_InitStandalone(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	res := runPodium(t, ws, nil, "init", "--standalone")
	if res.Exit != 0 {
		t.Fatalf("init --standalone exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if yaml := readFile(t, filepath.Join(ws, ".podium", "sync.yaml")); !strings.Contains(yaml, "registry: http://127.0.0.1:8080") {
		t.Errorf("sync.yaml missing standalone registry:\n%s", yaml)
	}
}

// T-D-configure-harness-63 — standalone sync against a server URL from sync.yaml.
func TestConfigureHarness_StandaloneSyncFromServer(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-14.11.1: podium sync has no server/URL registry source — sync.Run only opens a filesystem registry")
}

// T-D-configure-harness-64 — standalone MCP does not resolve the registry from
// sync.yaml (known gap): it requires PODIUM_REGISTRY.
func TestConfigureHarness_StandaloneMCPNoSyncYAMLFallback(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	chWriteSyncYAML(t, home, "defaults:\n  registry: http://127.0.0.1:8080\n")
	// rename to ~/.podium/sync.yaml location already satisfied (home/.podium/sync.yaml).
	res := mcpExec(t, []string{"PODIUM_REGISTRY=", "HOME=" + home},
		rpcReq{ID: 1, Method: "initialize", Params: chInitParams})
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "PODIUM_REGISTRY is required") {
		t.Errorf("stderr missing 'PODIUM_REGISTRY is required':\n%s", res.Stderr)
	}
}

// T-D-configure-harness-65 — init --standalone conflicts with --registry.
func TestConfigureHarness_StandaloneRegistryConflict(t *testing.T) {
	t.Parallel()
	res := runPodium(t, t.TempDir(), nil, "init", "--standalone", "--registry", "https://other.example.com")
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--standalone conflicts with --registry") {
		t.Errorf("stderr missing conflict message:\n%s", res.Stderr)
	}
}

// T-D-configure-harness-66 — init --global and --local are mutually exclusive.
func TestConfigureHarness_GlobalLocalExclusive(t *testing.T) {
	t.Parallel()
	res := runPodium(t, t.TempDir(), nil, "init", "--global", "--local", "--registry", "http://127.0.0.1:8080")
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--global and --local are mutually exclusive") {
		t.Errorf("stderr missing exclusivity message:\n%s", res.Stderr)
	}
}

// T-D-configure-harness-67 — init refuses to overwrite sync.yaml without --force.
func TestConfigureHarness_InitOverwriteGuard(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	if r := runPodium(t, ws, nil, "init", "--registry", "https://first.example.com"); r.Exit != 0 {
		t.Fatalf("first init exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	first := readFile(t, filepath.Join(ws, ".podium", "sync.yaml"))
	r2 := runPodium(t, ws, nil, "init", "--registry", "https://new.example.com")
	if r2.Exit != 2 || !strings.Contains(r2.Stderr, "already exists") {
		t.Errorf("second init exit=%d stderr=%s, want exit 2 with 'already exists'", r2.Exit, r2.Stderr)
	}
	if readFile(t, filepath.Join(ws, ".podium", "sync.yaml")) != first {
		t.Errorf("sync.yaml changed despite refused overwrite")
	}
	if r3 := runPodium(t, ws, nil, "init", "--registry", "https://new.example.com", "--force"); r3.Exit != 0 {
		t.Fatalf("forced init exit=%d stderr=%s", r3.Exit, r3.Stderr)
	}
	if yaml := readFile(t, filepath.Join(ws, ".podium", "sync.yaml")); !strings.Contains(yaml, "https://new.example.com") {
		t.Errorf("sync.yaml not overwritten with --force:\n%s", yaml)
	}
}

// T-D-configure-harness-68 — workspace init adds .gitignore entries.
func TestConfigureHarness_InitGitignore(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	if r := runPodium(t, ws, nil, "init", "--registry", "https://example.com"); r.Exit != 0 {
		t.Fatalf("init exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	gi := readFile(t, filepath.Join(ws, ".gitignore"))
	for _, want := range []string{".podium/sync.local.yaml", ".podium/overlay/"} {
		if !strings.Contains(gi, want) {
			t.Errorf(".gitignore missing %q:\n%s", want, gi)
		}
	}
}

// T-D-configure-harness-69 — sync --dry-run reports without writing.
func TestConfigureHarness_SyncDryRun(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"glossary/ARTIFACT.md": contextArtifact("glossary")})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none", "--dry-run")
	if res.Exit != 0 {
		t.Fatalf("dry-run exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "dry-run") {
		t.Errorf("stdout missing dry-run marker:\n%s", res.Stdout)
	}
	if _, err := os.Stat(filepath.Join(target, "glossary", "ARTIFACT.md")); err == nil {
		t.Errorf("dry-run wrote a file")
	}
}

// T-D-configure-harness-70 — sync --json emits a structured envelope.
func TestConfigureHarness_SyncJSON(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"glossary/ARTIFACT.md": contextArtifact("glossary")})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none", "--json")
	if res.Exit != 0 {
		t.Fatalf("json sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, res.Stdout)
	}
	for _, k := range []string{"adapter", "target", "artifacts"} {
		if _, ok := env[k]; !ok {
			t.Errorf("json envelope missing %q: %v", k, env)
		}
	}
}

// T-D-configure-harness-71 — PODIUM_IDENTITY_PROVIDER=injected-session-token accepted.
func TestConfigureHarness_MCPInjectedSessionToken(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	// A syntactically valid (unsigned) JWT shape; the bridge accepts the var at startup.
	tok := "eyJhbGciOiJub25lIn0.eyJzdWIiOiJhbGljZSJ9."
	chAssertInitOK(t, chMCPEnv(t, reg,
		"PODIUM_HARNESS=claude-code",
		"PODIUM_IDENTITY_PROVIDER=injected-session-token",
		"PODIUM_SESSION_TOKEN="+tok))
}

// T-D-configure-harness-72 — oauth-device-code is the default (absent var) and the binary still starts.
func TestConfigureHarness_MCPDefaultIdentityProvider(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_IDENTITY_PROVIDER="))
}

// T-D-configure-harness-73 — every documented harness adapter id is registered.
func TestConfigureHarness_AllHarnessIDsRegistered(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	ids := []string{"none", "claude-code", "claude-desktop", "claude-cowork", "cursor", "codex", "gemini", "opencode", "pi", "hermes"}
	for _, id := range ids {
		id := id
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			res := mcpExec(t, chMCPEnv(t, reg, "PODIUM_HARNESS="+id), rpcReq{ID: 1, Method: "initialize", Params: chInitParams})
			if strings.Contains(res.Stdout, "config.unknown_harness") || strings.Contains(res.Stderr, "config.unknown_harness") {
				t.Errorf("harness %q reported config.unknown_harness", id)
			}
			result := rpcResult(t, res.Stdout, 1)
			if result["protocolVersion"] != "2024-11-05" {
				t.Errorf("harness %q: protocolVersion=%v", id, result["protocolVersion"])
			}
		})
	}
}

// T-D-configure-harness-74 — sync is idempotent across two runs.
func TestConfigureHarness_SyncIdempotent(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"greet/ARTIFACT.md":      greetSkillArtifact,
		"greet/SKILL.md":         skillBody("greet"),
		"rules/code/ARTIFACT.md": chRule("always", ""),
	})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	first := readTreeFiltered(t, target)
	chSync(t, reg, target, "claude-code")
	second := readTreeFiltered(t, target)
	if len(first) != len(second) {
		t.Fatalf("file count changed: %d -> %d", len(first), len(second))
	}
	for path, content := range first {
		if second[path] != content {
			t.Errorf("content for %s changed between runs", path)
		}
	}
}

// T-D-configure-harness-75 — PODIUM_OVERLAY_PATH overrides the registry for load_artifact.
func TestConfigureHarness_MCPOverlayOverridesRegistry(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"my-rule/ARTIFACT.md": "---\ntype: rule\nversion: 1.0.0\nrule_mode: always\n---\n\nREGISTRY V1 body.\n",
	})
	overlay := writeRegistry(t, map[string]string{
		"my-rule/ARTIFACT.md": "---\ntype: rule\nversion: 2.0.0\nrule_mode: always\n---\n\nOVERLAY V2 body.\n",
	})
	mat := t.TempDir()
	res := mcpExec(t, chMCPEnv(t, reg, "PODIUM_HARNESS=none", "PODIUM_OVERLAY_PATH="+overlay, "PODIUM_MATERIALIZE_ROOT="+mat),
		toolCall(1, "load_artifact", map[string]any{"id": "my-rule"}))
	result := rpcResult(t, res.Stdout, 1)
	body, _ := result["manifest_body"].(string)
	fm, _ := result["frontmatter"].(string)
	if !strings.Contains(body+fm, "OVERLAY V2") && !strings.Contains(fm, "2.0.0") {
		t.Errorf("overlay did not take precedence; body=%q frontmatter=%q", body, fm)
	}
}
