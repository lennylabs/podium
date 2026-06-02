package e2e

// End-to-end tests for docs/consuming/configure-your-harness.md
// (D-configure-harness). The page documents per-harness setup via
// `podium sync` (filesystem materialization) and the `podium-mcp` server
// (runtime discovery). Tests drive the real CLI and the podium-mcp stdio
// bridge against filesystem-source registries.
//
// The §6.7.1 capability-matrix lint and the §4.3.5 target_harnesses opt-out are
// implemented (pkg/lint/harness_capability.go), so the lint-warning and
// lint-error cases run here rather than being skipped. The exact per-harness
// on-disk layout these tests check is pinned byte-for-byte by the golden suite
// in test/materialization, which materializes a canonical artifact set through
// every adapter; this file asserts the documented behaviors over the real CLI
// and MCP bridge.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// ---- fixtures + helpers -----------------------------------------------------

func chRule(mode, extra string) string {
	s := "---\ntype: rule\nversion: 1.0.0\nrule_mode: " + mode + "\n"
	if extra != "" {
		s += extra + "\n"
	}
	return s + "---\n\nRule body for " + mode + ".\n"
}

// chMCPEnv starts a standalone server over the filesystem registry and returns
// the env a podium-mcp subprocess needs to talk to it. The MCP server speaks
// HTTP and rejects a filesystem-source PODIUM_REGISTRY (config.filesystem_registry_unsupported,
// §6.1 / §7.5.2), so the bridge is pointed at the server's URL. `extra` carries
// bridge-side settings (PODIUM_HARNESS, PODIUM_OVERLAY_PATH, PODIUM_MATERIALIZE_ROOT, …).
func chMCPEnv(t *testing.T, reg string, extra ...string) []string {
	srv := startServer(t, reg)
	return append(mcpServerEnv(t, srv.BaseURL), extra...)
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
func TestHarness_InitWritesSyncYAML(t *testing.T) {
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
func TestHarness_SyncReadsRegistryFromConfig(t *testing.T) {
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

// T-D-configure-harness-3 — podium sync honors the sync.yaml harness field
// (F-7.5.13). With defaults.harness: claude-code and no --harness flag, sync
// materializes the Claude Code layout.
func TestHarness_SyncHonorsConfigHarness(t *testing.T) {
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
	// spec §7.5.2: the configured harness wins, so the Claude Code layout
	// appears and the none-layout ARTIFACT.md does not.
	mustExist(t, filepath.Join(ws, ".claude", "skills", "greet", "SKILL.md"))
	if _, err := os.Stat(filepath.Join(ws, "greet", "ARTIFACT.md")); err == nil {
		t.Errorf("F-7.5.13: sync.yaml harness ignored; none-layout greet/ARTIFACT.md appeared")
	}
}

// T-D-configure-harness-4 — podium sync honors PODIUM_HARNESS (F-7.5.13).
func TestHarness_SyncHonorsEnvHarness(t *testing.T) {
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
	mustExist(t, filepath.Join(target, ".claude", "skills", "greet", "SKILL.md"))
	if _, err := os.Stat(filepath.Join(target, "greet", "ARTIFACT.md")); err == nil {
		t.Errorf("F-7.5.13: PODIUM_HARNESS ignored; none-layout greet/ARTIFACT.md appeared")
	}
}

// T-D-configure-harness-5 — sync with no registry and no sync.yaml exits 2.
func TestHarness_SyncNoRegistryFails(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	res := runPodium(t, ws, []string{"PODIUM_REGISTRY="}, "sync")
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	// spec: §6.10 — an unconfigured registry surfaces the config.no_registry
	// error code and points the user at `podium init` / --registry.
	if !strings.Contains(res.Stderr, "config.no_registry") {
		t.Errorf("stderr missing 'config.no_registry':\n%s", res.Stderr)
	}
}

// T-D-configure-harness-6 — sync one-shot completes without --watch.
func TestHarness_SyncOneShot(t *testing.T) {
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
func TestHarness_WatchSIGINT(t *testing.T) {
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
func TestHarness_WatchPicksUpNewArtifact(t *testing.T) {
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
func TestHarness_MCPRequiresRegistry(t *testing.T) {
	t.Parallel()
	res := mcpExec(t, []string{"PODIUM_REGISTRY="}, rpcReq{ID: 1, Method: "initialize", Params: chInitParams})
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	// spec: §6.10 / §7.5.2 — unset across all scopes surfaces config.no_registry
	// and points the user at `podium init` (F-6.11.1).
	if !strings.Contains(res.Stderr, "config.no_registry") {
		t.Errorf("stderr missing 'config.no_registry':\n%s", res.Stderr)
	}
}

// T-D-configure-harness-10 — podium-mcp accepts PODIUM_HARNESS=claude-code and
// responds to initialize.
func TestHarness_MCPClaudeCodeInitialize(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_HARNESS=claude-code"))
}

// T-D-configure-harness-11 — podium-mcp accepts PODIUM_OVERLAY_PATH.
func TestHarness_MCPOverlayPathAccepted(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	overlay := t.TempDir()
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_HARNESS=claude-code", "PODIUM_OVERLAY_PATH="+overlay))
}

// T-D-configure-harness-12 — podium-mcp exposes the four meta-tools.
func TestHarness_MCPFourTools(t *testing.T) {
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
func TestHarness_ClaudeCodeSkill(t *testing.T) {
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
func TestHarness_ClaudeCodeAgent(t *testing.T) {
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
func TestHarness_ClaudeCodeRule(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"rules/code-style/ARTIFACT.md": chRule("always", "")})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	got := readFile(t, filepath.Join(target, ".claude", "rules", "code-style.md"))
	if !strings.Contains(got, "Rule body") {
		t.Errorf("rule file missing content:\n%s", got)
	}
}

// T-D-configure-harness-16 — claude-code context lands in the harness-neutral
// .podium/context/<id>/ bucket (§6.7).
func TestHarness_ClaudeCodeContextDefaultCase(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"glossary/company/ARTIFACT.md": contextArtifact("company glossary")})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	mustExist(t, filepath.Join(target, ".podium", "context", "glossary", "company", "ARTIFACT.md"))
}

// T-D-configure-harness-17 — claude-code command lands at .claude/commands/<name>.md.
func TestHarness_ClaudeCodeCommandDefaultCase(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/deploy/ARTIFACT.md": "---\ntype: command\nversion: 1.0.0\ndescription: Deploy.\n---\n\n$ARGUMENTS\n",
	})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	mustExist(t, filepath.Join(target, ".claude", "commands", "deploy.md"))
}

// T-D-configure-harness-18 — claude-code hook merges into .claude/settings.json.
func TestHarness_ClaudeCodeHookDefaultCase(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/pre-commit/ARTIFACT.md": "---\ntype: hook\nversion: 1.0.0\ndescription: A hook.\nhook_event: stop\nhook_action: echo done\n---\n\nbody\n",
	})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	got := readFile(t, filepath.Join(target, ".claude", "settings.json"))
	if !strings.Contains(got, "Stop") || !strings.Contains(got, "echo done") {
		t.Errorf("settings.json missing the merged hook (Stop / echo done):\n%s", got)
	}
}

// T-D-configure-harness-19 — claude-code skill bundled resources co-locate under
// .claude/skills/<name>/.
func TestHarness_ClaudeCodeSkillResources(t *testing.T) {
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

// T-D-configure-harness-20 — claude-code command is a native file; its non-skill
// bundled resources land in the .podium/resources/<id>/ bucket.
func TestHarness_ClaudeCodeNonSkillResources(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/deploy/ARTIFACT.md":       "---\ntype: command\nversion: 1.0.0\ndescription: Deploy.\n---\n\n$ARGUMENTS\n",
		"tools/deploy/scripts/deploy.sh": "#!/bin/sh\necho deploy\n",
	})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	mustExist(t, filepath.Join(target, ".claude", "commands", "deploy.md"))
	mustExist(t, filepath.Join(target, ".podium", "resources", "tools", "deploy", "scripts", "deploy.sh"))
}

// T-D-configure-harness-21 — claude-code/glob is native: the rule file carries
// Claude Code's `paths:` list, which scopes the rule per file, so targeting
// claude-code for a glob rule lints clean. §6.7.1: claude-code/glob = ✓.
func TestHarness_ClaudeCodeGlobNativeClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/ts/ARTIFACT.md": rmRuleArtifact("ts", "glob",
			[]string{`rule_globs: "src/**/*.ts"`, "target_harnesses: [claude-code]"}, "TS rules.\n"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "lint.harness_capability") {
		t.Errorf("claude-code/glob is native; expected no capability diagnostic:\n%s", res.Stdout)
	}
}

// T-D-configure-harness-22 — claude-code has no description-attach for rules, so
// an auto-mode rule falls back to a load-always .claude/rules/ file: the prose
// body is preserved with no scoping frontmatter (no undocumented description:
// rules key). §6.7.1: claude-code/auto = ⚠.
func TestHarness_ClaudeCodeAutoFallback(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/py-style/ARTIFACT.md": chRule("auto", `rule_description: "Use when working with Python files"`),
	})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	got := readFile(t, filepath.Join(target, ".claude", "rules", "py-style.md"))
	if !strings.Contains(got, "Rule body for auto.") {
		t.Errorf("rule file missing the prose body:\n%s", got)
	}
	if strings.Contains(got, "rule_mode") || strings.Contains(got, "rule_description") || strings.Contains(got, "description:") {
		t.Errorf("auto rule leaked frontmatter into the Claude rule file:\n%s", got)
	}
}

// T-D-configure-harness-23 — claude-code always and explicit are fully supported.
func TestHarness_ClaudeCodeAlwaysExplicit(t *testing.T) {
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

// T-D-configure-harness-24 — claude-code init+sync two-step. init writes
// harness: claude-code into sync.yaml; the subsequent sync honors it without
// an explicit --harness flag (F-7.5.13).
func TestHarness_ClaudeCodeInitSync(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"greetings/hello/ARTIFACT.md": greetSkillArtifact,
		"greetings/hello/SKILL.md":    "---\nname: hello\ndescription: Say hi.\n---\n\nBody.\n",
	})
	ws := t.TempDir()
	if r := runPodium(t, ws, nil, "init", "--registry", reg, "--harness", "claude-code"); r.Exit != 0 {
		t.Fatalf("init exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if r := runPodium(t, ws, []string{"PODIUM_HARNESS="}, "sync"); r.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	yaml := readFile(t, filepath.Join(ws, ".podium", "sync.yaml"))
	if !strings.Contains(yaml, "harness: claude-code") || !strings.Contains(yaml, "registry: "+reg) {
		t.Errorf("sync.yaml missing registry/harness:\n%s", yaml)
	}
	mustExist(t, filepath.Join(ws, ".claude", "skills", "hello", "SKILL.md"))
}

// ---- Claude Desktop ---------------------------------------------------------

// T-D-configure-harness-25 — claude-desktop has no project-level surface, so
// sync materializes nothing for it (§6.7).
func TestHarness_ClaudeDesktopExtensionLayout(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"greet/ARTIFACT.md": greetSkillArtifact,
		"greet/SKILL.md":    skillBody("greet"),
	})
	target := t.TempDir()
	chSync(t, reg, target, "claude-desktop")
	mustNotExist(t, filepath.Join(target, ".claude-desktop"))
	mustNotExist(t, filepath.Join(target, ".claude", "skills", "greet", "SKILL.md"))
}

// T-D-configure-harness-26 — claude-desktop MCP startup.
func TestHarness_MCPClaudeDesktop(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_HARNESS=claude-desktop"))
}

// ---- Claude Cowork ----------------------------------------------------------

// T-D-configure-harness-27 — claude-cowork writes .claude-cowork/plugins/<id>/.
func TestHarness_ClaudeCoworkPluginLayout(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"greet/ARTIFACT.md": greetSkillArtifact,
		"greet/SKILL.md":    skillBody("greet"),
	})
	cowork := t.TempDir()
	chSync(t, reg, cowork, "claude-cowork")
	mustExist(t, filepath.Join(cowork, "plugins", "greet", "skills", "greet", "SKILL.md"))
	mustExist(t, filepath.Join(cowork, "plugins", "greet", ".claude-plugin", "plugin.json"))
	// The repository-root marketplace.json lists the plugin so a Cowork admin
	// can import the synced repo as a private marketplace.
	market := filepath.Join(cowork, ".claude-plugin", "marketplace.json")
	mustExist(t, market)
	if b, err := os.ReadFile(market); err != nil {
		t.Fatalf("read marketplace.json: %v", err)
	} else if !strings.Contains(string(b), `"./plugins/greet"`) {
		t.Errorf("marketplace.json does not list the greet plugin source:\n%s", b)
	}
}

// T-D-configure-harness-28 — claude-cowork sync, git add, git commit sequence.
func TestHarness_ClaudeCoworkGitCommit(t *testing.T) {
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
	if r, _ := runExternal(t, cowork, 30*time.Second, "git", "show", "--stat", "HEAD"); !strings.Contains(r.Stdout, "plugins/greet") {
		t.Errorf("commit does not include the plugin layout:\n%s", r.Stdout)
	}
}

// T-D-configure-harness-29 — claude-cowork MCP startup is not refused.
func TestHarness_MCPClaudeCowork(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_HARNESS=claude-cowork"))
}

// ---- Cursor -----------------------------------------------------------------

// T-D-configure-harness-30 — cursor rule writes .cursor/rules/<name>.mdc.
func TestHarness_CursorRule(t *testing.T) {
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

// T-D-configure-harness-31 — cursor skill lands at .cursor/skills/<name>/SKILL.md.
func TestHarness_CursorNonRuleDefaultCase(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/greet/ARTIFACT.md": greetSkillArtifact,
		"tools/greet/SKILL.md":    skillBody("greet"),
	})
	target := t.TempDir()
	chSync(t, reg, target, "cursor")
	mustExist(t, filepath.Join(target, ".cursor", "skills", "greet", "SKILL.md"))
}

// T-D-configure-harness-32 — cursor: all four rule_mode values produce .mdc with no lint errors.
func TestHarness_CursorAllRuleModes(t *testing.T) {
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
func TestHarness_CursorInitSync(t *testing.T) {
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
func TestHarness_MCPCursor(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_HARNESS=cursor"))
}

// ---- OpenCode ---------------------------------------------------------------

// T-D-configure-harness-35 — opencode rule injects into AGENTS.md.
func TestHarness_OpenCodeRule(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"style/naming/ARTIFACT.md": chRule("explicit", "")})
	target := t.TempDir()
	chSync(t, reg, target, "opencode")
	got := readFile(t, filepath.Join(target, "AGENTS.md"))
	if !strings.Contains(got, "Rule body") || !strings.Contains(got, "podium:begin:style/naming") {
		t.Errorf("AGENTS.md missing the injected rule:\n%s", got)
	}
}

// T-D-configure-harness-36 — opencode skill lands at .opencode/skills/<name>/SKILL.md.
func TestHarness_OpenCodeNonRuleDefaultCase(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/greet/ARTIFACT.md": greetSkillArtifact,
		"tools/greet/SKILL.md":    skillBody("greet"),
	})
	target := t.TempDir()
	chSync(t, reg, target, "opencode")
	mustExist(t, filepath.Join(target, ".opencode", "skills", "greet", "SKILL.md"))
}

// T-D-configure-harness-37 — opencode/auto falls back (the AGENTS.md inject
// loses the auto-attach trigger), so targeting opencode warns. §6.7.1: opencode/auto = ⚠.
func TestHarness_OpenCodeAutoFallbackWarning(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/db/ARTIFACT.md": rmRuleArtifact("db", "auto",
			[]string{`rule_description: "When migrating"`, "target_harnesses: [opencode]"}, "DB checks.\n"),
	})
	rmExpectWarn(t, reg, "opencode")
}

// T-D-configure-harness-38 — an auto rule targeting only cursor (native for
// auto) lints clean: opencode is not in target_harnesses, so its ⚠ cell is not
// checked. spec: §4.3.5 target_harnesses scopes the capability lint.
func TestHarness_OpenCodeAutoTargetHarnessesExclude(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/db/ARTIFACT.md": rmRuleArtifact("db", "auto",
			[]string{`rule_description: "When migrating"`, "target_harnesses: [cursor]"}, "DB checks.\n"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "lint.harness_capability") {
		t.Errorf("opencode excluded from target_harnesses; expected no capability diagnostic:\n%s", res.Stdout)
	}
}

// T-D-configure-harness-39 — opencode init+sync two-step.
func TestHarness_OpenCodeInitSync(t *testing.T) {
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
	mustExist(t, filepath.Join(ws, "AGENTS.md"))
}

// T-D-configure-harness-40 — opencode MCP startup.
func TestHarness_MCPOpenCode(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_HARNESS=opencode"))
}

// ---- Codex ------------------------------------------------------------------

// T-D-configure-harness-41 — codex rule injects into AGENTS.md.
func TestHarness_CodexRule(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"style/naming/ARTIFACT.md": chRule("always", "")})
	target := t.TempDir()
	chSync(t, reg, target, "codex")
	got := readFile(t, filepath.Join(target, "AGENTS.md"))
	if !strings.Contains(got, "Rule body") || !strings.Contains(got, "podium:begin:style/naming") {
		t.Errorf("AGENTS.md missing the injected rule:\n%s", got)
	}
}

// T-D-configure-harness-42 — codex/auto falls back (the AGENTS.md inject loses
// the auto-attach trigger), so targeting codex warns. §6.7.1: codex/auto = ⚠.
func TestHarness_CodexAutoFallbackWarning(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/db/ARTIFACT.md": rmRuleArtifact("db", "auto",
			[]string{`rule_description: "When migrating"`, "target_harnesses: [codex]"}, "DB checks.\n"),
	})
	rmExpectWarn(t, reg, "codex")
}

// T-D-configure-harness-43 — Codex has a native hook surface (the config.toml
// `hooks` table), so a hook targeting codex lints clean rather than failing
// ingest. §6.7.1: codex hook_event = ✓.
func TestHarness_CodexHookNativeClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"audit/log/ARTIFACT.md": "---\ntype: hook\nname: log\nversion: 1.0.0\ndescription: Log stop.\nhook_event: stop\nhook_action: |\n  echo hi\ntarget_harnesses: [codex]\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (codex supports hooks natively)\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "lint.harness_capability") {
		t.Errorf("codex natively supports hooks; expected no capability diagnostic:\n%s", res.Stdout)
	}
}

// T-D-configure-harness-43a — a codex hook materializes into the config.toml
// `hooks` table (the `[[hooks.<Event>]]` array-of-tables), not a standalone
// .codex/hooks.json (which codex never reads). The block-scalar action's newline
// is escaped so the config.toml stays valid TOML. §6.7: codex hook = config.toml
// (cfg).
func TestHarness_CodexHookMaterializesToConfigToml(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"audit/log/ARTIFACT.md": "---\ntype: hook\nname: log\nversion: 1.0.0\ndescription: Log stop.\nhook_event: stop\nhook_action: |\n  echo hi\n  echo bye\ntarget_harnesses: [codex]\n---\n\nbody\n",
	})
	target := t.TempDir()
	chSync(t, reg, target, "codex")

	// The hook merges into config.toml under [[hooks.Stop]], inside a marker block.
	cfg := readFile(t, filepath.Join(target, ".codex", "config.toml"))
	for _, want := range []string{"podium:begin:audit/log", "[[hooks.Stop]]", "[[hooks.Stop.hooks]]", `type = "command"`, `command = "echo hi\necho bye`} {
		if !strings.Contains(cfg, want) {
			t.Errorf(".codex/config.toml missing %q:\n%s", want, cfg)
		}
	}
	// The embedded newline must be escaped, never a literal newline inside the
	// basic string (that would be invalid TOML).
	if strings.Contains(cfg, "command = \"echo hi\n") {
		t.Errorf(".codex/config.toml has an unescaped newline in the hook command (invalid TOML):\n%s", cfg)
	}
	// The dead standalone hooks.json must not be written.
	mustNotExist(t, filepath.Join(target, ".codex", "hooks.json"))
}

// T-D-configure-harness-44 — codex init+sync two-step.
func TestHarness_CodexInitSync(t *testing.T) {
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
	mustExist(t, filepath.Join(ws, ".podium", "context", "glossary", "ARTIFACT.md"))
}

// ---- Gemini -----------------------------------------------------------------

// T-D-configure-harness-45 — gemini context lands in .podium/context/<id>/.
func TestHarness_GeminiExtensionLayout(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"glossary/ARTIFACT.md": contextArtifact("glossary")})
	target := t.TempDir()
	chSync(t, reg, target, "gemini")
	mustExist(t, filepath.Join(target, ".podium", "context", "glossary", "ARTIFACT.md"))
}

// T-D-configure-harness-46 — gemini/always maps natively to GEMINI.md, so an
// always-mode rule targeting gemini lints clean. §6.7.1: gemini/always = ✓.
func TestHarness_GeminiAlwaysNativeClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/house/ARTIFACT.md": rmRuleArtifact("house", "always",
			[]string{"target_harnesses: [gemini]"}, "House style.\n"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "lint.harness_capability") {
		t.Errorf("gemini/always is native; expected no capability diagnostic:\n%s", res.Stdout)
	}
}

// T-D-configure-harness-47 — gemini init+sync two-step.
func TestHarness_GeminiInitSync(t *testing.T) {
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

// T-D-configure-harness-48 — pi rule injects into AGENTS.md.
func TestHarness_PiRule(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"style/naming/ARTIFACT.md": chRule("explicit", "")})
	target := t.TempDir()
	chSync(t, reg, target, "pi")
	got := readFile(t, filepath.Join(target, "AGENTS.md"))
	if !strings.Contains(got, "Rule body") {
		t.Errorf("AGENTS.md missing the injected rule:\n%s", got)
	}
}

// T-D-configure-harness-49 — pi skill lands at .pi/skills/<name>/SKILL.md.
func TestHarness_PiNonRuleDefaultCase(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/greet/ARTIFACT.md": greetSkillArtifact,
		"tools/greet/SKILL.md":    skillBody("greet"),
	})
	target := t.TempDir()
	chSync(t, reg, target, "pi")
	mustExist(t, filepath.Join(target, ".pi", "skills", "greet", "SKILL.md"))
}

// T-D-configure-harness-50 — pi/auto falls back (the AGENTS.md inject loses the
// auto-attach trigger), so targeting pi warns. §6.7.1: pi/auto = ⚠.
func TestHarness_PiAutoFallbackWarning(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/db/ARTIFACT.md": rmRuleArtifact("db", "auto",
			[]string{`rule_description: "When migrating"`, "target_harnesses: [pi]"}, "DB checks.\n"),
	})
	rmExpectWarn(t, reg, "pi")
}

// T-D-configure-harness-51 — pi init+sync two-step.
func TestHarness_PiInitSync(t *testing.T) {
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
	mustExist(t, filepath.Join(ws, "AGENTS.md"))
}

// T-D-configure-harness-52 — pi MCP startup.
func TestHarness_MCPPi(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_HARNESS=pi"))
}

// ---- Hermes -----------------------------------------------------------------

// T-D-configure-harness-53 — hermes rule writes .cursor/rules/<name>.mdc.
func TestHarness_HermesRule(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"style/naming/ARTIFACT.md": chRule("always", "")})
	target := t.TempDir()
	chSync(t, reg, target, "hermes")
	got := readFile(t, filepath.Join(target, ".cursor", "rules", "naming.mdc"))
	if !strings.Contains(got, "Rule body") {
		t.Errorf("rule file missing content:\n%s", got)
	}
}

// T-D-configure-harness-54 — hermes does not materialize skills at project
// scope (its skill surface is user-scope ~/.hermes/), so sync writes nothing.
func TestHarness_HermesNonRuleDefaultCase(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/greet/ARTIFACT.md": greetSkillArtifact,
		"tools/greet/SKILL.md":    skillBody("greet"),
	})
	target := t.TempDir()
	chSync(t, reg, target, "hermes")
	mustNotExist(t, filepath.Join(target, ".hermes"))
	mustNotExist(t, filepath.Join(target, ".cursor", "skills", "greet", "SKILL.md"))
}

// T-D-configure-harness-55 — hermes: all four rule_mode values materialize.
func TestHarness_HermesAllRuleModes(t *testing.T) {
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
		mustExist(t, filepath.Join(target, ".cursor", "rules", n+".mdc"))
	}
}

// T-D-configure-harness-56 — hermes init+sync two-step.
func TestHarness_HermesInitSync(t *testing.T) {
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
	mustExist(t, filepath.Join(ws, ".cursor", "rules", "naming.mdc"))
}

// T-D-configure-harness-57 — hermes MCP startup.
func TestHarness_MCPHermes(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_HARNESS=hermes"))
}

// ---- Generic / none ---------------------------------------------------------

// T-D-configure-harness-58 — none writes the canonical <id>/ layout.
func TestHarness_NoneCanonicalLayout(t *testing.T) {
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
func TestHarness_MCPNone(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_HARNESS=none"))
}

// T-D-configure-harness-60 — none init+sync (default adapter when omitted).
func TestHarness_NoneInitSync(t *testing.T) {
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
func TestHarness_UnknownHarness(t *testing.T) {
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

// T-D-configure-harness-63b — sync auto-resolves <CWD>/.podium/overlay/ with no
// --overlay flag and no env var (§6.4 rule 3 / F-14.6.2). The overlay artifact
// sits at the highest precedence and overrides the registry at the same ID.
func TestHarness_SyncAutoResolvesWorkspaceOverlay(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/intro/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: from registry\nsensitivity: low\n---\n\nfrom registry\n",
	})
	ws := t.TempDir()
	overlayArt := "---\ntype: context\nversion: 1.0.0\ndescription: from overlay\nsensitivity: low\n---\n\nfrom overlay\n"
	if err := os.MkdirAll(filepath.Join(ws, ".podium", "overlay", "finance", "intro"), 0o755); err != nil {
		t.Fatalf("mkdir overlay: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".podium", "overlay", "finance", "intro", "ARTIFACT.md"), []byte(overlayArt), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	target := t.TempDir()
	// No --overlay flag and no PODIUM_OVERLAY_PATH: the CWD fallback applies.
	res := runPodium(t, ws, nil, "sync", "--registry", reg, "--target", target)
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(target, "finance", "intro", "ARTIFACT.md"))
	if !strings.Contains(got, "from overlay") {
		t.Errorf("overlay did not auto-resolve; materialized:\n%s", got)
	}
}

// T-D-configure-harness-63c — sync --dry-run resolves a server source from
// PODIUM_REGISTRY alone and writes nothing (§7.5.2 / §14.15.3, F-14.15.3).
func TestHarness_SyncDryRunServerFromEnv(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/sync/manifest":
			_, _ = w.Write([]byte(`{"artifacts":[{"id":"greet","type":"context","version":"1.2.0","layer":"org"}]}`))
		case "/v1/load_artifact":
			_, _ = w.Write([]byte(`{"id":"greet","type":"context","layer":"org","frontmatter":"---\ntype: context\nversion: 1.2.0\ndescription: served\nsensitivity: low\n---\n\nbody\n"}`))
		default:
			http.Error(w, `{"code":"not_found","message":"x"}`, 404)
		}
	}))
	defer srv.Close()

	ws := t.TempDir()
	target := t.TempDir()
	env := []string{"PODIUM_REGISTRY=" + srv.URL}
	res := runPodium(t, ws, env, "sync", "--harness", "claude-code", "--target", target, "--dry-run")
	if res.Exit != 0 {
		t.Fatalf("dry-run server sync from env exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	entries, _ := os.ReadDir(target)
	if len(entries) != 0 {
		t.Errorf("--dry-run wrote %d entries to target, want 0", len(entries))
	}
}

// ---- Standalone -------------------------------------------------------------

// T-D-configure-harness-62 — init --standalone writes the localhost registry.
func TestHarness_InitStandalone(t *testing.T) {
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
// spec: §7.5.2, §14.11 — a URL routes podium sync to the Podium server over
// HTTP. The test stands up a stub registry serving the §7.5 server-source
// endpoints, points sync.yaml at its URL, and asserts the CLI materializes the
// served artifact and forwards the §6.3.2 injected session token as a bearer
// credential (F-14.6.1 / F-14.11.1 / F-14.11.5).
func TestHarness_StandaloneSyncFromServer(t *testing.T) {
	t.Parallel()
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a := r.Header.Get("Authorization"); a != "" {
			gotAuth = a
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/sync/manifest":
			_, _ = w.Write([]byte(`{"artifacts":[{"id":"greet","type":"context","version":"1.0.0","layer":"org"}]}`))
		case "/v1/load_artifact":
			_, _ = w.Write([]byte(`{"id":"greet","type":"context","layer":"org","frontmatter":"---\ntype: context\nversion: 1.0.0\ndescription: served\nsensitivity: low\n---\n\nhello from server\n"}`))
		default:
			http.Error(w, `{"code":"not_found","message":"x"}`, 404)
		}
	}))
	defer srv.Close()

	ws := t.TempDir()
	chWriteSyncYAML(t, ws, "defaults:\n  registry: "+srv.URL+"\n")
	target := t.TempDir()
	env := []string{"PODIUM_SESSION_TOKEN=tok-abc123"}
	res := runPodium(t, ws, env, "sync", "--harness", "claude-code", "--target", target)
	if res.Exit != 0 {
		t.Fatalf("server-source sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if gotAuth != "Bearer tok-abc123" {
		t.Errorf("registry did not receive forwarded session token; Authorization=%q", gotAuth)
	}
	// claude-code materializes a context artifact under .claude/.
	found := false
	_ = filepath.Walk(target, func(p string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() && strings.Contains(readFile(t, p), "hello from server") {
			found = true
		}
		return nil
	})
	if !found {
		t.Errorf("server-source artifact body not materialized under %s", target)
	}
}

// T-D-configure-harness-64 — standalone recipe (§6.11): the MCP server resolves
// the registry from the bootstrapped ~/.podium/sync.yaml and PODIUM_REGISTRY can
// be omitted (F-6.11.1). With defaults.registry set there, initialize succeeds
// rather than failing with config.no_registry.
func TestHarness_StandaloneMCPResolvesRegistryFromSyncYAML(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	// The §6.11 standalone bootstrap writes the loopback server URL; initialize
	// is a local handshake and does not contact the registry, so no live server
	// is required to exercise the resolution.
	chWriteSyncYAML(t, home, "defaults:\n  registry: http://127.0.0.1:8080\n")
	chAssertInitOK(t, []string{"PODIUM_REGISTRY=", "PODIUM_CACHE_DIR=" + t.TempDir(), "HOME=" + home})
}

// T-D-configure-harness-66 — standalone recipe negative: with no PODIUM_REGISTRY
// and no sync.yaml in any scope, the MCP server exits 2 with config.no_registry
// (§6.10) rather than a bare "required" message (F-6.11.1).
func TestHarness_StandaloneMCPNoRegistryAnywhere(t *testing.T) {
	t.Parallel()
	home := t.TempDir() // empty: no ~/.podium/sync.yaml
	res := mcpExec(t, []string{"PODIUM_REGISTRY=", "PODIUM_CACHE_DIR=" + t.TempDir(), "HOME=" + home},
		rpcReq{ID: 1, Method: "initialize", Params: chInitParams})
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "config.no_registry") {
		t.Errorf("stderr missing 'config.no_registry':\n%s", res.Stderr)
	}
}

// T-D-configure-harness-65 — init --standalone conflicts with --registry.
func TestHarness_StandaloneRegistryConflict(t *testing.T) {
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
func TestHarness_GlobalLocalExclusive(t *testing.T) {
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
func TestHarness_InitOverwriteGuard(t *testing.T) {
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
func TestHarness_InitGitignore(t *testing.T) {
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
func TestHarness_SyncDryRun(t *testing.T) {
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
func TestHarness_SyncJSON(t *testing.T) {
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
	// spec §7.5: {profile, target, harness, scope, artifacts} (F-7.5.9).
	for _, k := range []string{"profile", "target", "harness", "scope", "artifacts"} {
		if _, ok := env[k]; !ok {
			t.Errorf("json envelope missing %q: %v", k, env)
		}
	}
	if env["harness"] != "none" {
		t.Errorf("harness = %v, want none", env["harness"])
	}
}

// T-D-configure-harness-71 — PODIUM_IDENTITY_PROVIDER=injected-session-token accepted.
func TestHarness_MCPInjectedSessionToken(t *testing.T) {
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
func TestHarness_MCPDefaultIdentityProvider(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	chAssertInitOK(t, chMCPEnv(t, reg, "PODIUM_IDENTITY_PROVIDER="))
}

// T-D-configure-harness-73 — every documented harness adapter id is registered.
func TestHarness_AllHarnessIDsRegistered(t *testing.T) {
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
func TestHarness_SyncIdempotent(t *testing.T) {
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
func TestHarness_MCPOverlayOverridesRegistry(t *testing.T) {
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

// ---- §6.7 harness-adapter findings (F-6.7.1, F-6.7.2, F-6.7.3) --------------

// F-6.7.1 — a session_end hook materializes for codex. The §6.7.1 matrix grades
// codex hook_event ✓, which requires the adapter to config-merge every common
// event; session_end was previously unmapped and produced no output. spec: §6.7.1.
func TestHarness_CodexSessionEndHookMaterializes(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"audit/teardown/ARTIFACT.md": "---\ntype: hook\nversion: 1.0.0\ndescription: teardown.\n" +
			"hook_event: session_end\nhook_action: |\n  echo bye\n---\n\nbody\n",
	})
	target := t.TempDir()
	chSync(t, reg, target, "codex")
	cfg := readFile(t, filepath.Join(target, ".codex", "config.toml"))
	if !strings.Contains(cfg, "[[hooks.SessionEnd]]") {
		t.Errorf("session_end hook did not config-merge a SessionEnd table:\n%s", cfg)
	}
}

// F-6.7.2 — an agent that declares mcpServers and targets codex (✗ for the
// mcpServers field) is an ingest lint error. Before the fix the lint never
// evaluated the mcpServers row, so the field was dropped silently. spec: §6.7.1.
func TestHarness_MCPServersTargetingCodexLintErrors(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"agents/warehouse/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: d.\n" +
			"mcpServers:\n  - name: finance-warehouse\n    command: npx\n" +
			"target_harnesses: [codex]\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "mcpServers") || !strings.Contains(res.Stdout, "codex") {
		t.Errorf("expected a capability error naming mcpServers and codex:\n%s", res.Stdout)
	}
}

// F-6.7.2 — the same agent targeting claude-code (✓ for mcpServers) lints clean.
func TestHarness_MCPServersTargetingClaudeCodeLintClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"agents/warehouse/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: d.\n" +
			"mcpServers:\n  - name: finance-warehouse\n    command: npx\n" +
			"target_harnesses: [claude-code]\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "[error]") {
		t.Errorf("claude-code ✓ for mcpServers must lint clean:\n%s", res.Stdout)
	}
}

// F-6.7.3 — podium sync refuses when sync.yaml pins a min_server_version above
// this binary (§6.7 "Versioning": older binaries refuse to start).
func TestHarness_SyncRefusesBelowMinServerVersion(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"glossary/ARTIFACT.md": contextArtifact("glossary")})
	ws := t.TempDir()
	chWriteSyncYAML(t, ws, "defaults:\n  registry: "+reg+"\n  min_server_version: \"99.0.0\"\n")
	res := runPodium(t, ws, []string{"PODIUM_REGISTRY="}, "sync")
	if res.Exit != 2 {
		t.Fatalf("sync exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "config.server_version_too_old") {
		t.Errorf("stderr missing config.server_version_too_old:\n%s", res.Stderr)
	}
}

// F-6.7.3 — podium sync proceeds when the pinned min_server_version is at or
// below this binary's version.
func TestHarness_SyncAllowsAtOrAboveMinServerVersion(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"glossary/ARTIFACT.md": contextArtifact("glossary")})
	ws := t.TempDir()
	chWriteSyncYAML(t, ws, "defaults:\n  registry: "+reg+"\n  min_server_version: \"0.0.1\"\n")
	res := runPodium(t, ws, []string{"PODIUM_REGISTRY="}, "sync")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d, want 0\nstderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(ws, "glossary", "ARTIFACT.md"))
}

// F-6.7.3 — the podium-mcp bridge refuses to start when sync.yaml pins a
// min_server_version above this binary. The bridge reads the pin from the
// workspace .podium/sync.yaml in its working directory. spec: §6.7 "Versioning".
func TestHarness_MCPRefusesBelowMinServerVersion(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	chWriteSyncYAML(t, ws, "defaults:\n  registry: https://registry.example\n  min_server_version: \"99.0.0\"\n")
	env := []string{
		"PODIUM_REGISTRY=https://registry.example",
		"PODIUM_CACHE_DIR=" + t.TempDir(),
	}
	// loadConfig refuses before the serve loop, so the bridge exits without
	// reading stdin; a bounded deadline guards against any hang.
	res := runBin(t, cmdharness.Bin(t, "podium-mcp"), ws, env, nil, 30*time.Second)
	if res.Exit != 2 {
		t.Fatalf("podium-mcp exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "config.server_version_too_old") {
		t.Errorf("stderr missing config.server_version_too_old:\n%s", res.Stderr)
	}
}
