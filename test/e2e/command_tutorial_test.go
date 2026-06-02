package e2e

// End-to-end tests for docs/authoring/your-first-command.md (D-first-command).
// The tutorial authors a /standup slash command and materializes it. Commands
// are delivered through harness-native materialization (§6.7), not an MCP
// prompt projection.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const standupID = "personal/dev-loop/standup"

// standupArtifact is the tutorial's ARTIFACT.md (type: command, $ARGUMENTS body).
const standupArtifact = "---\n" +
	"type: command\n" +
	"name: standup\n" +
	"version: 1.0.0\n" +
	"description: Format a daily standup update from a free-text summary of yesterday's work.\n" +
	"when_to_use:\n" +
	"  - \"At standup time, when summarizing yesterday's work into the team's standard format.\"\n" +
	"tags: [dev-loop, standup, daily]\n" +
	"sensitivity: low\n" +
	"---\n\n" +
	"# Daily standup\n\n## User input\n\n$ARGUMENTS\n\n## Instructions\n\n" +
	"Reformat the user's free-text input into the team's standup format.\n"

// mcpServerEnv returns the env a podium-mcp subprocess needs to talk to a
// standalone server without touching the developer's real cache dir.
func mcpServerEnv(t *testing.T, baseURL string) []string {
	return []string{"PODIUM_REGISTRY=" + baseURL, "PODIUM_CACHE_DIR=" + t.TempDir()}
}

// T-D-first-command-1 — the command directory and ARTIFACT.md exist at the
// documented path with a $ARGUMENTS body and no SKILL.md.
func TestCommandTutorial_ArtifactLayout(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	art := readFile(t, filepath.Join(reg, standupID, "ARTIFACT.md"))
	if !strings.HasPrefix(art, "---") {
		t.Errorf("ARTIFACT.md does not start with frontmatter delimiter")
	}
	if !strings.Contains(art, "type: command") || !strings.Contains(art, "$ARGUMENTS") {
		t.Errorf("ARTIFACT.md missing type: command or $ARGUMENTS:\n%s", art)
	}
	if _, err := os.Stat(filepath.Join(reg, standupID, "SKILL.md")); err == nil {
		t.Errorf("a command must not have a SKILL.md")
	}
}

// T-D-first-command-2 — scaffold produces a lint-clean command (no SKILL.md).
func TestCommandTutorial_ScaffoldLintsClean(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	out := filepath.Join(root, standupID)
	sc := runPodium(t, "", nil, "artifact", "scaffold",
		"--type", "command",
		"--description", "Format a daily standup update from a free-text summary of yesterday's work.",
		"--tags", "dev-loop,standup,daily",
		"--sensitivity", "low",
		"--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	for _, want := range []string{"type: command", "sensitivity: low"} {
		if !strings.Contains(art, want) {
			t.Errorf("scaffolded ARTIFACT.md missing %q:\n%s", want, art)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "SKILL.md")); err == nil {
		t.Errorf("scaffold produced a SKILL.md for a command")
	}
	lint := runPodium(t, "", nil, "lint", "--registry", root)
	if lint.Exit != 0 || !strings.Contains(lint.Stdout, "lint: no issues.") {
		t.Errorf("lint exit=%d stdout=%q", lint.Exit, lint.Stdout)
	}
}

// T-D-first-command-3 — lint passes on the tutorial standup manifest.
func TestCommandTutorial_LintClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q stderr=%q", res.Exit, res.Stdout, res.Stderr)
	}
}

// T-D-first-command-4 — the doc's positional `podium lint <path>` form is
// not runnable; --registry is required, exit 2. (doc-accuracy)
func TestCommandTutorial_LintPositionalRejected(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	res := runPodium(t, "", nil, "lint", filepath.Join(reg, standupID))
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--registry is required") {
		t.Errorf("stderr missing '--registry is required': %q", res.Stderr)
	}
}

// T-D-first-command-5 — missing version fails lint.
func TestCommandTutorial_MissingVersion(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"standup/ARTIFACT.md": "---\ntype: command\ndescription: A standup command.\n---\n\n$ARGUMENTS\n"})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.required_field_missing") || !strings.Contains(res.Stdout, "version") {
		t.Errorf("missing version diagnostic:\n%s", res.Stdout)
	}
}

// T-D-first-command-6 — missing type fails lint.
func TestCommandTutorial_MissingType(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"standup/ARTIFACT.md": "---\nname: standup\nversion: 1.0.0\n---\n\n$ARGUMENTS\n"})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.required_field_missing") || !strings.Contains(res.Stdout, "type is required") {
		t.Errorf("missing 'type is required' diagnostic:\n%s", res.Stdout)
	}
}

// T-D-first-command-7 — a non-semver version fails lint.
func TestCommandTutorial_NonSemver(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"standup/ARTIFACT.md": "---\ntype: command\nversion: v1\ndescription: x\n---\n\n$ARGUMENTS\n"})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.invalid_version") {
		t.Errorf("missing lint.invalid_version:\n%s", res.Stdout)
	}
}

// T-D-first-command-8 — lint with no --registry exits 2.
func TestCommandTutorial_LintNoRegistry(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "lint")
	if res.Exit != 2 || !strings.Contains(res.Stderr, "error: --registry is required") {
		t.Errorf("exit=%d stderr=%q, want 2 + '--registry is required'", res.Exit, res.Stderr)
	}
}

// T-D-first-command-9 — lint on a non-existent registry exits 1.
func TestCommandTutorial_LintBadPath(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "lint", "--registry", filepath.Join(t.TempDir(), "nope"))
	if res.Exit != 1 {
		t.Fatalf("exit=%d, want 1\nstderr=%s", res.Exit, res.Stderr)
	}
	if res.Stderr == "" {
		t.Errorf("expected an error message on stderr")
	}
}

// T-D-first-command-10 — the none harness materializes the canonical layout.
func TestCommandTutorial_NoneCanonical(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, standupID, "ARTIFACT.md"))
	if !strings.Contains(got, "type: command") {
		t.Errorf("materialized ARTIFACT.md missing type: command:\n%s", got)
	}
	if !strings.Contains(res.Stdout, standupID) {
		t.Errorf("stdout missing artifact id:\n%s", res.Stdout)
	}
}

// T-D-first-command-11 — claude-code places a command at .claude/commands/<n>.md
// (§6.7 type-target table), not under the .claude/podium/ extension bucket.
func TestCommandTutorial_ClaudeCodePodiumNotCommands(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, ".claude/commands/standup.md"))
	if _, err := os.Stat(filepath.Join(tgt, ".claude/podium", standupID, "ARTIFACT.md")); err == nil {
		t.Errorf("a command must not land under the .claude/podium/ extension bucket")
	}
}

// T-D-first-command-12 — sync reads the registry from .podium/sync.yaml.
func TestCommandTutorial_SyncReadsConfig(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "init", "--registry", reg, "--harness", "none")
	tgt := t.TempDir()
	res := runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "sync", "--target", tgt)
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, standupID, "ARTIFACT.md"))
}

// T-D-first-command-13 — sync with no registry and no sync.yaml exits 2.
func TestCommandTutorial_SyncNoRegistry(t *testing.T) {
	t.Parallel()
	res := runPodium(t, t.TempDir(), []string{"HOME=" + t.TempDir(), "PODIUM_REGISTRY="}, "sync", "--target", t.TempDir())
	if res.Exit != 2 || !strings.Contains(res.Stderr, "config.no_registry") {
		t.Errorf("exit=%d stderr=%q, want 2 + 'registry is required'", res.Exit, res.Stderr)
	}
}

// T-D-first-command-14 — sync against a missing registry directory exits 1.
func TestCommandTutorial_SyncMissingRegistry(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "sync", "--registry", filepath.Join(t.TempDir(), "nope"), "--target", t.TempDir(), "--harness", "none")
	if res.Exit != 1 {
		t.Fatalf("exit=%d, want 1\nstderr=%s", res.Exit, res.Stderr)
	}
	if res.Stderr == "" {
		t.Errorf("expected an error message on stderr")
	}
}

// T-D-first-command-15 — sync with an unknown harness fails and writes nothing.
func TestCommandTutorial_UnknownHarness(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "not-a-real-harness")
	if res.Exit == 0 {
		t.Fatalf("expected non-zero exit\nstdout=%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "config.unknown_harness") {
		t.Errorf("stderr missing config.unknown_harness: %q", res.Stderr)
	}
	if files := readTreeFiltered(t, tgt); len(files) != 0 {
		t.Errorf("unknown harness wrote %d files", len(files))
	}
}

// T-D-first-command-16 — sync --dry-run writes nothing.
func TestCommandTutorial_DryRun(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none", "--dry-run")
	if res.Exit != 0 || !strings.Contains(res.Stdout, "(dry-run; nothing written)") {
		t.Fatalf("dry-run exit=%d stdout=%q", res.Exit, res.Stdout)
	}
	if files := readTreeFiltered(t, tgt); len(files) != 0 {
		t.Errorf("dry-run wrote %d files", len(files))
	}
}

// T-D-first-command-17 — sync --json emits valid JSON with the command.
func TestCommandTutorial_SyncJSON(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none", "--json")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	var env struct {
		Harness   string `json:"harness"`
		Artifacts []struct {
			ID string `json:"id"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", err, res.Stdout)
	}
	found := false
	for _, a := range env.Artifacts {
		if a.ID == standupID {
			found = true
		}
	}
	if env.Harness == "" || !found {
		t.Errorf("envelope missing adapter or %s: %+v", standupID, env)
	}
}

// T-D-first-command-18 — human sync output lists adapter, target, and the
// artifact id.
func TestCommandTutorial_SyncHumanOutput(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if !strings.Contains(res.Stdout, "adapter: none") {
		t.Errorf("stdout missing 'adapter: none':\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "target:") || !strings.Contains(res.Stdout, tgt) {
		t.Errorf("stdout missing resolved target %q:\n%s", tgt, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "artifacts:") || !strings.Contains(res.Stdout, standupID) {
		t.Errorf("stdout missing artifacts listing:\n%s", res.Stdout)
	}
}

// T-D-first-command-19 — $ARGUMENTS is preserved verbatim through sync.
func TestCommandTutorial_ArgumentsPreserved(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	got := readFile(t, filepath.Join(tgt, standupID, "ARTIFACT.md"))
	if !strings.Contains(got, "$ARGUMENTS") {
		t.Errorf("materialized command lost $ARGUMENTS:\n%s", got)
	}
}

// T-D-first-command-28 — init writes sync.yaml with registry + harness so a
// later `podium sync` needs no flags.
func TestCommandTutorial_InitWritesSyncYAML(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	reg := t.TempDir()
	res := runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "init", "--registry", reg, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("init exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(ws, ".podium", "sync.yaml"))
	if !strings.Contains(got, "registry: "+reg) || !strings.Contains(got, "harness: claude-code") {
		t.Errorf("sync.yaml missing registry/harness under defaults:\n%s", got)
	}
}

// T-D-first-command-29 — init refuses to overwrite an existing sync.yaml.
func TestCommandTutorial_InitRefusesOverwrite(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	home := t.TempDir()
	runPodium(t, ws, []string{"HOME=" + home}, "init", "--registry", "/first")
	res := runPodium(t, ws, []string{"HOME=" + home}, "init", "--registry", "/second")
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "already exists") || !strings.Contains(res.Stderr, "--force") {
		t.Errorf("stderr missing 'already exists'/'--force': %q", res.Stderr)
	}
}

// T-D-first-command-30 — a command declaring variables: with defaults lints
// clean. spec: doc "What's next".
func TestCommandTutorial_VariablesAccepted(t *testing.T) {
	t.Parallel()
	art := "---\n" +
		"type: command\n" +
		"name: refactor\n" +
		"version: 1.0.0\n" +
		"description: Guided module refactoring with configurable focus areas.\n" +
		"sensitivity: low\n" +
		"variables:\n" +
		"  FOCUS: all\n" +
		"  PRESERVE_API: \"true\"\n" +
		"---\n\n# Refactor\n\n$ARGUMENTS\n\nFocus on {{FOCUS}}.\n"
	reg := writeRegistry(t, map[string]string{"commands/refactor/ARTIFACT.md": art})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// T-D-first-command-31 — the full tutorial sequence on the none harness:
// write, lint, sync, verify the file with the $ARGUMENTS body preserved.
func TestCommandTutorial_FullSequenceNone(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	if l := runPodium(t, "", nil, "lint", "--registry", reg); l.Exit != 0 || !strings.Contains(l.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q", l.Exit, l.Stdout)
	}
	tgt := t.TempDir()
	if s := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); s.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", s.Exit, s.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, standupID, "ARTIFACT.md"))
	if !strings.Contains(got, "$ARGUMENTS") {
		t.Errorf("materialized command missing $ARGUMENTS body:\n%s", got)
	}
}

// T-D-first-command-32 — the full sequence on the claude-code harness: lint is
// clean and the command lands at .claude/commands/standup.md, matching the
// tutorial's verify step (§6.7 type-target table).
func TestCommandTutorial_FullSequenceClaudeCode(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	if l := runPodium(t, "", nil, "lint", "--registry", reg); l.Exit != 0 {
		t.Fatalf("lint exit=%d stdout=%q", l.Exit, l.Stdout)
	}
	tgt := t.TempDir()
	if s := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); s.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", s.Exit, s.Stderr)
	}
	cmd := readFile(t, filepath.Join(tgt, ".claude/commands/standup.md"))
	if !strings.Contains(cmd, "$ARGUMENTS") {
		t.Errorf("materialized command missing the $ARGUMENTS body:\n%s", cmd)
	}
	if _, err := os.Stat(filepath.Join(tgt, ".claude/podium", standupID, "ARTIFACT.md")); err == nil {
		t.Errorf("a command must not also land under the .claude/podium/ extension bucket")
	}
}

// The following tests cover T-D-first-command-20 through T-D-first-command-27.
// Those gap entries were written against an earlier draft that projected
// commands through MCP via an `expose_as_mcp_prompt` field and `prompts/list`
// / `prompts/get` endpoints. That feature was removed. The spec now states the
// canonical behavior directly: "A `type: command` artifact ... Podium does not
// project commands through MCP. Both `podium sync` and the MCP server
// materialize a command into the target harness's native command location and
// format" (spec/05-meta-tools.md §command). The doc repeats it in the "Create
// the artifact" field notes ("Podium does not project commands through MCP").
// The schema carries no `expose_as_mcp_prompt` field, and the MCP server
// advertises no `prompts` capability (cmd/podium-mcp/main.go initialize). The
// tests below assert the current behavior the spec and doc claim.

// TestCommandTutorial_MaterializedBodyVerbatim asserts that the none adapter
// writes the command manifest to disk byte-for-byte, so every frontmatter
// field and the prose body survive intact for a harness that reads the file
// directly. This is the surviving intent of T-D-first-command-20 after the
// MCP-projection field was removed: delivery is the canonical layout on disk.
// spec: §6.7 (none adapter writes ArtifactBytes verbatim); doc "Materialize".
func TestCommandTutorial_MaterializedBodyVerbatim(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	tgt := t.TempDir()
	if s := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); s.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", s.Exit, s.Stderr)
	}
	if got := readFile(t, filepath.Join(tgt, standupID, "ARTIFACT.md")); got != standupArtifact {
		t.Errorf("none adapter did not write the manifest verbatim:\n--- got ---\n%s\n--- want ---\n%s", got, standupArtifact)
	}
}

// TestCommandTutorial_NotProjectedThroughMCP drives podium-mcp against a
// standalone server holding the standup command and asserts the doc's claim
// that commands are not projected through MCP. The prompt-projection methods
// the removed feature defined (`prompts/list`, `prompts/get`) are absent, so
// every former argument and error branch collapses to a single JSON-RPC
// "method not found" (-32601): T-D-first-command-21 (the prompts/list entry),
// T-D-first-command-22 (the false/absent exclusion), T-D-first-command-23 (the
// user-message body), T-D-first-command-24 (not_exposed), T-D-first-command-25
// (not_a_command), T-D-first-command-26 (invalid_argument), and
// T-D-first-command-27 (not_found). The
// companion TestMCPInitialize_AdvertisesNoPromptsCapability asserts the
// matching absence of a `prompts` capability on initialize.
// spec: §command (no MCP projection); cmd/podium-mcp/main.go default case.
func TestCommandTutorial_NotProjectedThroughMCP(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact}))
	env := mcpServerEnv(t, srv.BaseURL)
	res := mcpExec(t, env,
		rpcReq{ID: 1, Method: "prompts/list", Params: map[string]any{}},
		rpcReq{ID: 2, Method: "prompts/get", Params: map[string]any{"name": standupID}},
		rpcReq{ID: 3, Method: "prompts/get", Params: map[string]any{}},
		rpcReq{ID: 4, Method: "prompts/get", Params: map[string]any{"name": "does-not-exist"}},
	)
	assertMethodNotFound(t, res.Stdout, 1, "prompts/list")
	assertMethodNotFound(t, res.Stdout, 2, "prompts/get(name=command-id)")
	assertMethodNotFound(t, res.Stdout, 3, "prompts/get(no name)")
	assertMethodNotFound(t, res.Stdout, 4, "prompts/get(unknown id)")
}

// assertMethodNotFound asserts the JSON-RPC response with the given id is a
// -32601 "method not found" error. A removed MCP method returns this for every
// call regardless of params, which is why the former prompt-projection error
// envelopes no longer apply.
func assertMethodNotFound(t testing.TB, stdout string, id int, label string) {
	t.Helper()
	env := rpcEnvelope(t, stdout, id)
	e, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("%s (id=%d): expected a JSON-RPC error, got envelope %v", label, id, env)
	}
	if code, _ := e["code"].(float64); int(code) != -32601 {
		t.Errorf("%s (id=%d): error code = %v, want -32601", label, id, e["code"])
	}
	if msg, _ := e["message"].(string); !strings.Contains(msg, "method not found") {
		t.Errorf("%s (id=%d): error message = %q, want it to contain \"method not found\"", label, id, msg)
	}
}
