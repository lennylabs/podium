package e2e

// End-to-end tests for docs/authoring/your-first-command.md (D-first-command).
// The tutorial authors a /standup slash command, materializes it, and
// exposes it through MCP's prompts/list + prompts/get. Tests drive the
// podium CLI, and the podium-mcp bridge against a standalone server for
// the prompt-projection surface. The doc claims commands land in
// .claude/commands/<name>.md; the claude-code adapter actually places
// type: command under .claude/podium/<id>/, asserted here as a
// doc-accuracy gap.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const standupID = "personal/dev-loop/standup"

// standupArtifact is the tutorial's ARTIFACT.md (type: command,
// expose_as_mcp_prompt: true, $ARGUMENTS body).
const standupArtifact = "---\n" +
	"type: command\n" +
	"name: standup\n" +
	"version: 1.0.0\n" +
	"description: Format a daily standup update from a free-text summary of yesterday's work.\n" +
	"when_to_use:\n" +
	"  - \"At standup time, when summarizing yesterday's work into the team's standard format.\"\n" +
	"tags: [dev-loop, standup, daily]\n" +
	"sensitivity: low\n" +
	"expose_as_mcp_prompt: true\n" +
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
func TestFirstCommand_ArtifactLayout(t *testing.T) {
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

// T-D-first-command-2 — scaffold produces a lint-clean command (no SKILL.md,
// expose_as_mcp_prompt: true).
func TestFirstCommand_ScaffoldLintsClean(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	out := filepath.Join(root, standupID)
	sc := runPodium(t, "", nil, "artifact", "scaffold",
		"--type", "command",
		"--description", "Format a daily standup update from a free-text summary of yesterday's work.",
		"--tags", "dev-loop,standup,daily",
		"--sensitivity", "low",
		"--expose-as-mcp-prompt",
		"--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	for _, want := range []string{"type: command", "expose_as_mcp_prompt: true", "sensitivity: low"} {
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
func TestFirstCommand_LintClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q stderr=%q", res.Exit, res.Stdout, res.Stderr)
	}
}

// T-D-first-command-4 — the doc's positional `podium lint <path>` form is
// not runnable; --registry is required, exit 2. (doc-accuracy)
func TestFirstCommand_LintPositionalRejected(t *testing.T) {
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
func TestFirstCommand_MissingVersion(t *testing.T) {
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
func TestFirstCommand_MissingType(t *testing.T) {
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
func TestFirstCommand_NonSemver(t *testing.T) {
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
func TestFirstCommand_LintNoRegistry(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "lint")
	if res.Exit != 2 || !strings.Contains(res.Stderr, "error: --registry is required") {
		t.Errorf("exit=%d stderr=%q, want 2 + '--registry is required'", res.Exit, res.Stderr)
	}
}

// T-D-first-command-9 — lint on a non-existent registry exits 1.
func TestFirstCommand_LintBadPath(t *testing.T) {
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
func TestFirstCommand_NoneCanonical(t *testing.T) {
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

// T-D-first-command-11 — claude-code places a command under .claude/podium/,
// not .claude/commands/ as the doc claims. (doc-accuracy)
func TestFirstCommand_ClaudeCodePodiumNotCommands(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if _, err := os.Stat(filepath.Join(tgt, ".claude/commands/standup.md")); err == nil {
		t.Errorf(".claude/commands/standup.md exists; the doc claim is inaccurate per the current adapter")
	}
	mustExist(t, filepath.Join(tgt, ".claude/podium", standupID, "ARTIFACT.md"))
}

// T-D-first-command-12 — sync reads the registry from .podium/sync.yaml.
func TestFirstCommand_SyncReadsConfig(t *testing.T) {
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
func TestFirstCommand_SyncNoRegistry(t *testing.T) {
	t.Parallel()
	res := runPodium(t, t.TempDir(), []string{"HOME=" + t.TempDir(), "PODIUM_REGISTRY="}, "sync", "--target", t.TempDir())
	if res.Exit != 2 || !strings.Contains(res.Stderr, "registry is required") {
		t.Errorf("exit=%d stderr=%q, want 2 + 'registry is required'", res.Exit, res.Stderr)
	}
}

// T-D-first-command-14 — sync against a missing registry directory exits 1.
func TestFirstCommand_SyncMissingRegistry(t *testing.T) {
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
func TestFirstCommand_UnknownHarness(t *testing.T) {
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
func TestFirstCommand_DryRun(t *testing.T) {
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
func TestFirstCommand_SyncJSON(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none", "--json")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	var env struct {
		Adapter   string `json:"adapter"`
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
	if env.Adapter == "" || !found {
		t.Errorf("envelope missing adapter or %s: %+v", standupID, env)
	}
}

// T-D-first-command-18 — human sync output lists adapter, target, and the
// artifact id.
func TestFirstCommand_SyncHumanOutput(t *testing.T) {
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
func TestFirstCommand_ArgumentsPreserved(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	got := readFile(t, filepath.Join(tgt, standupID, "ARTIFACT.md"))
	if !strings.Contains(got, "$ARGUMENTS") {
		t.Errorf("materialized command lost $ARGUMENTS:\n%s", got)
	}
}

// T-D-first-command-20 — expose_as_mcp_prompt survives materialization.
func TestFirstCommand_ExposeFieldPreserved(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	got := readFile(t, filepath.Join(tgt, standupID, "ARTIFACT.md"))
	if !strings.Contains(got, "expose_as_mcp_prompt: true") {
		t.Errorf("materialized command lost expose_as_mcp_prompt:\n%s", got)
	}
}

// T-D-first-command-21 — expose_as_mcp_prompt: true makes the command appear
// in MCP prompts/list, keyed by its canonical ID. spec: §5.2.
func TestFirstCommand_PromptsListIncludes(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact}))
	res := mcpExec(t, mcpServerEnv(t, srv.BaseURL), rpcReq{ID: 1, Method: "prompts/list", Params: map[string]any{}})
	result := rpcResult(t, res.Stdout, 1)
	prompts, _ := result["prompts"].([]any)
	found := false
	for _, p := range prompts {
		m, _ := p.(map[string]any)
		if m["name"] == standupID {
			found = true
			if desc, _ := m["description"].(string); !strings.Contains(desc, "standup update") {
				t.Errorf("prompt description does not match artifact: %q", desc)
			}
		}
	}
	if !found {
		t.Errorf("prompts/list missing %s: %v", standupID, prompts)
	}
}

// T-D-first-command-22 — a command without expose_as_mcp_prompt is excluded
// from prompts/list but remains discoverable via search_artifacts.
func TestFirstCommand_PromptsListExcludesUnexposed(t *testing.T) {
	t.Parallel()
	notExposed := "---\ntype: command\nname: notes\nversion: 1.0.0\ndescription: A private notes command not exposed to the slash menu.\n---\n\n$ARGUMENTS\n"
	srv := startServer(t, writeRegistry(t, map[string]string{
		standupID + "/ARTIFACT.md":            standupArtifact,
		"personal/dev-loop/notes/ARTIFACT.md": notExposed,
	}))
	env := mcpServerEnv(t, srv.BaseURL)
	res := mcpExec(t, env, rpcReq{ID: 1, Method: "prompts/list", Params: map[string]any{}})
	body := mustJSON(rpcResult(t, res.Stdout, 1))
	if !strings.Contains(body, standupID) {
		t.Errorf("prompts/list missing the exposed command:\n%s", body)
	}
	if strings.Contains(body, "personal/dev-loop/notes") {
		t.Errorf("prompts/list leaked the non-exposed command:\n%s", body)
	}
	// Still discoverable via search_artifacts.
	sres := mcpExec(t, env, toolCall(2, "search_artifacts", map[string]any{"type": "command"}))
	if sbody := mustJSON(rpcResult(t, sres.Stdout, 2)); !strings.Contains(sbody, "personal/dev-loop/notes") {
		t.Errorf("non-exposed command not discoverable via search_artifacts:\n%s", sbody)
	}
}

// T-D-first-command-23 — prompts/get returns the command body (including
// $ARGUMENTS) as a user-message content block. spec: §5.2.
func TestFirstCommand_PromptsGetBody(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact}))
	res := mcpExec(t, mcpServerEnv(t, srv.BaseURL), rpcReq{ID: 1, Method: "prompts/get", Params: map[string]any{"name": standupID}})
	result := rpcResult(t, res.Stdout, 1)
	if desc, _ := result["description"].(string); !strings.Contains(desc, "standup update") {
		t.Errorf("description does not match artifact: %q", desc)
	}
	msgs, _ := result["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatalf("messages empty: %v", result)
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "user" {
		t.Errorf("first message role=%v, want user", first["role"])
	}
	content, _ := first["content"].(map[string]any)
	if content["type"] != "text" {
		t.Errorf("content type=%v, want text", content["type"])
	}
	if text, _ := content["text"].(string); !strings.Contains(text, "$ARGUMENTS") {
		t.Errorf("content text missing $ARGUMENTS: %q", text)
	}
}

// T-D-first-command-24 — prompts/get on a command with expose absent returns
// prompts.not_exposed.
func TestFirstCommand_PromptsGetNotExposed(t *testing.T) {
	t.Parallel()
	notExposed := "---\ntype: command\nname: notes\nversion: 1.0.0\ndescription: A private notes command.\n---\n\n$ARGUMENTS\n"
	srv := startServer(t, writeRegistry(t, map[string]string{"personal/dev-loop/notes/ARTIFACT.md": notExposed}))
	res := mcpExec(t, mcpServerEnv(t, srv.BaseURL), rpcReq{ID: 1, Method: "prompts/get", Params: map[string]any{"name": "personal/dev-loop/notes"}})
	result := rpcResult(t, res.Stdout, 1)
	if e, _ := result["error"].(string); !strings.Contains(e, "prompts.not_exposed") {
		t.Errorf("error=%q, want prompts.not_exposed", e)
	}
}

// T-D-first-command-25 — prompts/get on a non-command artifact returns
// prompts.not_a_command.
func TestFirstCommand_PromptsGetNotACommand(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"reference/glossary/ARTIFACT.md": contextArtifact("glossary")}))
	res := mcpExec(t, mcpServerEnv(t, srv.BaseURL), rpcReq{ID: 1, Method: "prompts/get", Params: map[string]any{"name": "reference/glossary"}})
	result := rpcResult(t, res.Stdout, 1)
	if e, _ := result["error"].(string); !strings.Contains(e, "prompts.not_a_command") {
		t.Errorf("error=%q, want prompts.not_a_command", e)
	}
}

// T-D-first-command-26 — prompts/get with no name returns prompts.invalid_argument.
func TestFirstCommand_PromptsGetInvalidArgument(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact}))
	res := mcpExec(t, mcpServerEnv(t, srv.BaseURL), rpcReq{ID: 1, Method: "prompts/get", Params: map[string]any{}})
	result := rpcResult(t, res.Stdout, 1)
	if e, _ := result["error"].(string); !strings.Contains(e, "prompts.invalid_argument") {
		t.Errorf("error=%q, want prompts.invalid_argument", e)
	}
}

// T-D-first-command-27 — prompts/get for an unknown id surfaces a not_found
// error. Against the standalone server the unknown id 404s at the registry,
// so the bridge returns the registry's "registry.not_found" rather than
// synthesizing "prompts.not_found" (which only fires when load_artifact
// returns 200 with an empty id).
func TestFirstCommand_PromptsGetNotFound(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact}))
	res := mcpExec(t, mcpServerEnv(t, srv.BaseURL), rpcReq{ID: 1, Method: "prompts/get", Params: map[string]any{"name": "does-not-exist"}})
	result := rpcResult(t, res.Stdout, 1)
	if e, _ := result["error"].(string); !strings.Contains(e, "not_found") {
		t.Errorf("error=%q, want a not_found error", e)
	}
}

// T-D-first-command-28 — init writes sync.yaml with registry + harness so a
// later `podium sync` needs no flags.
func TestFirstCommand_InitWritesSyncYAML(t *testing.T) {
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
func TestFirstCommand_InitRefusesOverwrite(t *testing.T) {
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
func TestFirstCommand_VariablesAccepted(t *testing.T) {
	t.Parallel()
	art := "---\n" +
		"type: command\n" +
		"name: refactor\n" +
		"version: 1.0.0\n" +
		"description: Guided module refactoring with configurable focus areas.\n" +
		"sensitivity: low\n" +
		"expose_as_mcp_prompt: true\n" +
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
// write, lint, sync, verify the file with $ARGUMENTS and the expose field.
func TestFirstCommand_FullSequenceNone(t *testing.T) {
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
	if !strings.Contains(got, "$ARGUMENTS") || !strings.Contains(got, "expose_as_mcp_prompt: true") {
		t.Errorf("materialized command missing $ARGUMENTS / expose field:\n%s", got)
	}
}

// T-D-first-command-32 — the full sequence on the claude-code harness: the
// command lands under .claude/podium/<id>/, and the doc's
// .claude/commands/standup.md does not exist. (doc-accuracy)
func TestFirstCommand_FullSequenceClaudeCode(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact})
	if l := runPodium(t, "", nil, "lint", "--registry", reg); l.Exit != 0 {
		t.Fatalf("lint exit=%d stdout=%q", l.Exit, l.Stdout)
	}
	tgt := t.TempDir()
	if s := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); s.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", s.Exit, s.Stderr)
	}
	mustExist(t, filepath.Join(tgt, ".claude/podium", standupID, "ARTIFACT.md"))
	if _, err := os.Stat(filepath.Join(tgt, ".claude/commands/standup.md")); err == nil {
		t.Errorf(".claude/commands/standup.md exists; the tutorial's verify step is inaccurate")
	}
	// The .claude/commands directory the tutorial's `ls` targets is not
	// created at all.
	if entries, err := os.ReadDir(filepath.Join(tgt, ".claude/commands")); err == nil && len(entries) > 0 {
		t.Errorf(".claude/commands is non-empty: %v", entries)
	}
}
