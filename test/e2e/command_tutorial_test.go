package e2e

// End-to-end tests for docs/authoring/your-first-command.md (D-first-command).
// The tutorial authors a /standup user-invoked skill and materializes it. A
// user-invoked skill is delivered through harness-native materialization
// (§5.2, §6.7) rather than an MCP prompt projection.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const standupID = "personal/dev-loop/standup"

// standupFile is the registry path of the tutorial's SKILL.md.
const standupFile = standupID + "/SKILL.md"

// standupSkill is the tutorial's SKILL.md: a user-invoked skill whose
// metadata.invocation is "user" and whose body carries $ARGUMENTS (§4.3.4,
// §5.2).
const standupSkill = "---\n" +
	"name: standup\n" +
	"description: Format a daily standup update from a free-text summary of yesterday's work.\n" +
	"metadata:\n" +
	"  version: \"1.0.0\"\n" +
	"  invocation: \"user\"\n" +
	"  tags: \"dev-loop, standup, daily\"\n" +
	"  sensitivity: \"low\"\n" +
	"---\n\n" +
	"# Daily standup\n\n## User input\n\n$ARGUMENTS\n\n## Instructions\n\n" +
	"Reformat the user's free-text input into the team's standup format.\n"

// mcpServerEnv returns the env a podium-mcp subprocess needs to talk to a
// standalone server without touching the developer's real cache dir. The
// registry signs at ingest by default (§13.10) and a consumer verifies any
// signature it is served under every policy above never (§4.7.9), so the
// bridge these suites spawn verifies nothing; a suite whose subject is signing
// states its own policy and key.
func mcpServerEnv(t *testing.T, baseURL string) []string {
	return []string{"PODIUM_REGISTRY=" + baseURL, "PODIUM_CACHE_DIR=" + t.TempDir(), "PODIUM_VERIFY_SIGNATURES=never"}
}

// the skill directory holds a SKILL.md at the documented path with
// metadata.invocation "user", a $ARGUMENTS body, and no ARTIFACT.md.
func TestCommandTutorial_ArtifactLayout(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupFile: standupSkill})
	skill := readFile(t, filepath.Join(reg, standupFile))
	if !strings.HasPrefix(skill, "---") {
		t.Errorf("SKILL.md does not start with frontmatter delimiter")
	}
	if !strings.Contains(skill, "invocation: \"user\"") || !strings.Contains(skill, "$ARGUMENTS") {
		t.Errorf("SKILL.md missing metadata.invocation or $ARGUMENTS:\n%s", skill)
	}
	if _, err := os.Stat(filepath.Join(reg, standupID, "ARTIFACT.md")); err == nil {
		t.Errorf("a plain user-invoked skill needs no ARTIFACT.md")
	}
}

// Spec: §4.3.4, §7.3.1 — a skill scaffolded with --invocation user carries
// metadata.invocation "user" and lints clean.
func TestCommandTutorial_ScaffoldLintsClean(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	out := filepath.Join(root, standupID)
	sc := runPodium(t, "", nil, "artifact", "scaffold",
		"--type", "skill",
		"--description", "Format a daily standup update from a free-text summary of yesterday's work.",
		"--tags", "dev-loop,standup,daily",
		"--sensitivity", "low",
		"--invocation", "user",
		"--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	skill := readFile(t, filepath.Join(out, "SKILL.md"))
	for _, want := range []string{"metadata:\n", "sensitivity: \"low\"", "invocation: \"user\""} {
		if !strings.Contains(skill, want) {
			t.Errorf("scaffolded SKILL.md missing %q:\n%s", want, skill)
		}
	}
	lint := runPodium(t, "", nil, "lint", "--registry", root)
	if lint.Exit != 0 || !strings.Contains(lint.Stdout, "lint: no issues.") {
		t.Errorf("lint exit=%d stdout=%q", lint.Exit, lint.Stdout)
	}
}

// lint passes on the tutorial standup manifest.
func TestCommandTutorial_LintClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupFile: standupSkill})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q stderr=%q", res.Exit, res.Stdout, res.Stderr)
	}
}

// a positional `podium lint <path>` is rejected;
// --registry is required, exit 2. The doc now uses `podium lint --registry`.
func TestCommandTutorial_LintPositionalRejected(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupFile: standupSkill})
	res := runPodium(t, "", nil, "lint", filepath.Join(reg, standupID))
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--registry is required") {
		t.Errorf("stderr missing '--registry is required': %q", res.Stderr)
	}
}

// missing version fails lint.
func TestCommandTutorial_MissingVersion(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"standup/ARTIFACT.md": "---\ntype: agent\ndescription: A standup command.\n---\n\n$ARGUMENTS\n"})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.required_field_missing") || !strings.Contains(res.Stdout, "version") {
		t.Errorf("missing version diagnostic:\n%s", res.Stdout)
	}
}

// missing type fails lint.
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

// a non-semver version fails lint.
func TestCommandTutorial_NonSemver(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"standup/ARTIFACT.md": "---\ntype: agent\nversion: v1\ndescription: x\n---\n\n$ARGUMENTS\n"})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.invalid_version") {
		t.Errorf("missing lint.invalid_version:\n%s", res.Stdout)
	}
}

// lint with no --registry exits 2.
func TestCommandTutorial_LintNoRegistry(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "lint")
	if res.Exit != 2 || !strings.Contains(res.Stderr, "error: --registry is required") {
		t.Errorf("exit=%d stderr=%q, want 2 + '--registry is required'", res.Exit, res.Stderr)
	}
}

// lint on a non-existent registry exits 1.
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

// the none harness materializes the canonical layout.
func TestCommandTutorial_NoneCanonical(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupFile: standupSkill})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, standupFile))
	if !strings.Contains(got, "invocation: \"user\"") {
		t.Errorf("materialized SKILL.md missing metadata.invocation:\n%s", got)
	}
	if !strings.Contains(res.Stdout, standupID) {
		t.Errorf("stdout missing artifact id:\n%s", res.Stdout)
	}
}

// claude-code places a user-invoked skill at .claude/skills/<n>/SKILL.md
// (§6.7 type-target table), and writes neither .claude/commands/ nor the
// .claude/podium/ extension bucket.
func TestCommandTutorial_ClaudeCodePodiumNotCommands(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupFile: standupSkill})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, ".claude/skills/standup/SKILL.md"))
	mustNotExist(t, filepath.Join(tgt, ".claude/commands"))
	if _, err := os.Stat(filepath.Join(tgt, ".claude/podium", standupID, "ARTIFACT.md")); err == nil {
		t.Errorf("a user-invoked skill must not land under the .claude/podium/ extension bucket")
	}
}

// sync reads the registry from .podium/sync.yaml.
func TestCommandTutorial_SyncReadsConfig(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	reg := writeRegistry(t, map[string]string{standupFile: standupSkill})
	runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "init", "--registry", reg, "--harness", "none")
	tgt := t.TempDir()
	res := runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "sync", "--target", tgt)
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, standupFile))
}

// sync with no registry and no sync.yaml exits 2.
func TestCommandTutorial_SyncNoRegistry(t *testing.T) {
	t.Parallel()
	res := runPodium(t, t.TempDir(), []string{"HOME=" + t.TempDir(), "PODIUM_REGISTRY="}, "sync", "--target", t.TempDir())
	if res.Exit != 2 || !strings.Contains(res.Stderr, "config.no_registry") {
		t.Errorf("exit=%d stderr=%q, want 2 + 'registry is required'", res.Exit, res.Stderr)
	}
}

// sync against a missing registry directory exits 1.
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

// sync with an unknown harness fails and writes nothing.
func TestCommandTutorial_UnknownHarness(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupFile: standupSkill})
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

// sync --dry-run writes nothing.
func TestCommandTutorial_DryRun(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupFile: standupSkill})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none", "--dry-run")
	if res.Exit != 0 || !strings.Contains(res.Stdout, "(dry-run; nothing written)") {
		t.Fatalf("dry-run exit=%d stdout=%q", res.Exit, res.Stdout)
	}
	if files := readTreeFiltered(t, tgt); len(files) != 0 {
		t.Errorf("dry-run wrote %d files", len(files))
	}
}

// sync --json emits valid JSON with the user-invoked skill.
func TestCommandTutorial_SyncJSON(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupFile: standupSkill})
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

// human sync output lists adapter, target, and the
// artifact id.
func TestCommandTutorial_SyncHumanOutput(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupFile: standupSkill})
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

// $ARGUMENTS is preserved verbatim through sync.
func TestCommandTutorial_ArgumentsPreserved(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupFile: standupSkill})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	got := readFile(t, filepath.Join(tgt, standupFile))
	if !strings.Contains(got, "$ARGUMENTS") {
		t.Errorf("materialized skill lost $ARGUMENTS:\n%s", got)
	}
}

// init writes sync.yaml with registry + harness so a
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

// init refuses to overwrite an existing sync.yaml.
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

// the full tutorial sequence on the none harness:
// write, lint, sync, verify the file with the $ARGUMENTS body preserved.
func TestCommandTutorial_FullSequenceNone(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupFile: standupSkill})
	if l := runPodium(t, "", nil, "lint", "--registry", reg); l.Exit != 0 || !strings.Contains(l.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q", l.Exit, l.Stdout)
	}
	tgt := t.TempDir()
	if s := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); s.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", s.Exit, s.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, standupFile))
	if !strings.Contains(got, "$ARGUMENTS") {
		t.Errorf("materialized skill missing $ARGUMENTS body:\n%s", got)
	}
}

// the full sequence on the claude-code harness: lint is
// clean and the user-invoked skill lands at .claude/skills/standup/SKILL.md,
// matching the tutorial's verify step (§6.7 type-target table).
func TestCommandTutorial_FullSequenceClaudeCode(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupFile: standupSkill})
	if l := runPodium(t, "", nil, "lint", "--registry", reg); l.Exit != 0 {
		t.Fatalf("lint exit=%d stdout=%q", l.Exit, l.Stdout)
	}
	tgt := t.TempDir()
	if s := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); s.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", s.Exit, s.Stderr)
	}
	skill := readFile(t, filepath.Join(tgt, ".claude/skills/standup/SKILL.md"))
	if !strings.Contains(skill, "$ARGUMENTS") {
		t.Errorf("materialized skill missing the $ARGUMENTS body:\n%s", skill)
	}
	if _, err := os.Stat(filepath.Join(tgt, ".claude/podium", standupID, "ARTIFACT.md")); err == nil {
		t.Errorf("a user-invoked skill must not also land under the .claude/podium/ extension bucket")
	}
}

// The MCP server projects no prompts: a user-invoked skill reaches a harness
// through materialization (§5.2), and the MCP server advertises no `prompts`
// capability (cmd/podium-mcp/main.go initialize). The tests below assert that
// behavior.

// TestCommandTutorial_MaterializedBodyVerbatim asserts that the none adapter
// writes the skill's SKILL.md to disk byte-for-byte, so every frontmatter
// field and the prose body survive intact for a harness that reads the file
// directly.
// spec: §5.2, §6.7 (none adapter writes the canonical layout); doc "Materialize".
func TestCommandTutorial_MaterializedBodyVerbatim(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupFile: standupSkill})
	tgt := t.TempDir()
	if s := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); s.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", s.Exit, s.Stderr)
	}
	if got := readFile(t, filepath.Join(tgt, standupFile)); got != standupSkill {
		t.Errorf("none adapter did not write SKILL.md verbatim:\n--- got ---\n%s\n--- want ---\n%s", got, standupSkill)
	}
}

// TestCommandTutorial_NotProjectedThroughMCP drives podium-mcp against a
// standalone server holding the standup user-invoked skill and asserts the
// doc's claim that user-invoked skills are not projected through MCP. The prompt-projection methods
// the removed feature defined (`prompts/list`, `prompts/get`) are absent, so
// every former argument and error branch collapses to a single JSON-RPC
// "method not found" (-32601): the prompts/list entry, the false/absent exclusion, the
// user-message body, not_exposed, not_a_command, invalid_argument, and
// not_found. The
// companion TestMCPInitialize_AdvertisesNoPromptsCapability asserts the
// matching absence of a `prompts` capability on initialize.
// spec: §5.2 (no MCP projection); cmd/podium-mcp/main.go default case.
func TestCommandTutorial_NotProjectedThroughMCP(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{standupFile: standupSkill}))
	env := mcpServerEnv(t, srv.BaseURL)
	res := mcpExec(t, env,
		rpcReq{ID: 1, Method: "prompts/list", Params: map[string]any{}},
		rpcReq{ID: 2, Method: "prompts/get", Params: map[string]any{"name": standupID}},
		rpcReq{ID: 3, Method: "prompts/get", Params: map[string]any{}},
		rpcReq{ID: 4, Method: "prompts/get", Params: map[string]any{"name": "does-not-exist"}},
	)
	assertMethodNotFound(t, res.Stdout, 1, "prompts/list")
	assertMethodNotFound(t, res.Stdout, 2, "prompts/get(name=skill-id)")
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
