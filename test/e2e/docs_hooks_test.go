package e2e

// End-to-end tests for docs/authoring/hooks.md (D-hooks). A hook is a
// type: hook artifact carrying the type-specific hook_event and hook_action
// fields; it has no SKILL.md. The tests drive the podium CLI (scaffold, lint,
// sync), the standalone HTTP server, and the podium-mcp bridge.
//
// Two surfacing facts shape the assertions:
//   - The MCP load_artifact result has no `frontmatter` field (it carries
//     id, type, version, content_hash, manifest_body, materialized_at). To
//     observe hook_event/hook_action after an MCP call, the test materializes
//     to disk (PODIUM_HARNESS + PODIUM_MATERIALIZE_ROOT) and reads the
//     materialized ARTIFACT.md.
//   - The HTTP /v1/load_artifact response does include `frontmatter` (the raw
//     child YAML), which carries hook_event/hook_action.
//
// Behaviors blocked by a known BUILD-GAPS finding are recorded as skips. Doc
// claims the implementation does not honor (with no finding filed) are
// asserted against actual behavior with a note so a future change is detected.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hkCanonicalEvents lists the canonical hook_event names from the doc's
// "Canonical events" tables.
var hkCanonicalEvents = []string{
	"session_start", "session_end", "user_prompt_submit",
	"pre_tool_use", "post_tool_use", "post_tool_use_failure",
	"pre_shell_execution", "post_shell_execution",
	"pre_mcp_execution", "post_mcp_execution",
	"pre_read_file", "post_file_edit",
	"permission_request", "permission_denied",
	"subagent_start", "subagent_stop",
	"stop", "pre_compact", "post_compact", "notification",
}

// hkLogScript is the bundled-script body from the doc's bundled-script example.
const hkLogScript = `#!/usr/bin/env bash
set -euo pipefail

INPUT=$(cat)
LOG_FILE="${HOME}/.podium/session-audit.log"

CONV_ID=$(echo "$INPUT" | jq -r '.session_id // .conversation_id // "unknown"')
TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)

echo "[${TIMESTAMP}] session end: ${CONV_ID}" >> "${LOG_FILE}"
`

// hkBundledArtifact is the full bundled-script hook ARTIFACT.md from the doc
// (type, name, version, description, tags, sensitivity, hook_event,
// hook_action -> scripts/log.sh, runtime_requirements system_packages [jq]).
const hkBundledArtifact = "---\n" +
	"type: hook\n" +
	"name: log-session-end\n" +
	"version: 1.0.0\n" +
	"description: Log session-end events to a local audit file.\n" +
	"tags: [hook, audit]\n" +
	"sensitivity: low\n" +
	"hook_event: stop\n" +
	"hook_action: |\n" +
	"  scripts/log.sh\n" +
	"runtime_requirements:\n" +
	"  system_packages: [jq]\n" +
	"---\n\n" +
	"Hook body.\n"

// hkArtifact builds a minimal hook ARTIFACT.md with the given event and a
// simple echo action plus any extra frontmatter lines.
func hkArtifact(name, event string, extra ...string) string {
	b := "---\ntype: hook\nname: " + name + "\nversion: 1.0.0\ndescription: A hook.\n"
	for _, e := range extra {
		b += e + "\n"
	}
	b += "hook_event: " + event + "\nhook_action: |\n  echo hi\n---\n\nbody\n"
	return b
}

// hkNoError reports whether the lint output is free of error-severity lines.
func hkNoError(out string) bool { return !strings.Contains(out, "[error]") }

// ---- Scaffold ---------------------------------------------------------------

// T-D-hooks-1 — scaffold a hook with a canonical event writes type: hook,
// hook_event, a pipe-literal hook_action, a semver version, the description,
// and no SKILL.md.
func TestHooks_ScaffoldStopFields(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "audit/log-session-end")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "hook",
		"--description", "Log session-end events to a local audit file.", "--hook-event", "stop", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	if _, err := os.Stat(filepath.Join(out, "SKILL.md")); err == nil {
		t.Errorf("hook must not have a SKILL.md")
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	for _, want := range []string{
		"type: hook",
		"hook_event: stop",
		"hook_action: |",
		"version: 0.1.0",
		"description: Log session-end events to a local audit file.",
	} {
		if !strings.Contains(art, want) {
			t.Errorf("ARTIFACT.md missing %q:\n%s", want, art)
		}
	}
}

// T-D-hooks-2 — scaffold hook without --hook-event under --yes fails exit 2.
func TestHooks_ScaffoldMissingEventFails(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "my-hook")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "hook", "--description", "x", "--yes", dir)
	if sc.Exit != 2 {
		t.Fatalf("scaffold exit=%d, want 2\nstderr=%s", sc.Exit, sc.Stderr)
	}
	if !strings.Contains(sc.Stderr, "hook-event required") {
		t.Errorf("stderr missing 'hook-event required':\n%s", sc.Stderr)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Errorf("no directory should be created at %s", dir)
	}
}

// T-D-hooks-3 — scaffold with --hook-action produces a lint-clean artifact.
func TestHooks_ScaffoldLintClean(t *testing.T) {
	t.Parallel()
	reg := filepath.Join(t.TempDir(), "reg")
	out := filepath.Join(reg, "audit/log-session-end")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "hook",
		"--description", "Log session-end events to a local audit file.", "--hook-event", "stop",
		"--hook-action", "INPUT=$(cat); echo \"$INPUT\" >> ~/.podium/sessions.log", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if !hkNoError(res.Stdout + res.Stderr) {
		t.Errorf("lint emitted [error] lines:\nstdout=%s\nstderr=%s", res.Stdout, res.Stderr)
	}
}

// T-D-hooks-4 — every canonical event name round-trips through scaffold and
// the resulting registry lints without error.
func TestHooks_ScaffoldAllCanonicalEvents(t *testing.T) {
	t.Parallel()
	reg := filepath.Join(t.TempDir(), "reg")
	for _, ev := range hkCanonicalEvents {
		dir := filepath.Join(reg, strings.ReplaceAll(ev, "_", "-"))
		sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "hook",
			"--description", "Test hook.", "--hook-event", ev, "--yes", dir)
		if sc.Exit != 0 {
			t.Fatalf("scaffold %s exit=%d stderr=%s", ev, sc.Exit, sc.Stderr)
		}
		if art := readFile(t, filepath.Join(dir, "ARTIFACT.md")); !strings.Contains(art, "hook_event: "+ev) {
			t.Errorf("event %s missing from ARTIFACT.md:\n%s", ev, art)
		}
	}
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !hkNoError(res.Stdout+res.Stderr) {
		t.Errorf("lint over all events exit=%d, want clean\nstdout=%s", res.Exit, res.Stdout)
	}
}

// ---- Sync (filesystem) ------------------------------------------------------

// T-D-hooks-5 — a hook materializes under .claude/podium/<id>/ for claude-code,
// carrying hook_event, and is not routed to .claude/rules or .claude/agents.
func TestHooks_SyncClaudeCodePodiumLayout(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"audit/log-session-end/ARTIFACT.md": hkArtifact("log-session-end", "stop"),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, ".claude/podium/audit/log-session-end/ARTIFACT.md"))
	if !strings.Contains(got, "hook_event: stop") {
		t.Errorf("materialized ARTIFACT.md missing hook_event: stop:\n%s", got)
	}
	for _, bad := range []string{".claude/rules", ".claude/agents"} {
		if _, err := os.Stat(filepath.Join(tgt, bad)); err == nil {
			t.Errorf("hook must not produce a %s subtree", bad)
		}
	}
}

// T-D-hooks-6 — a hook materializes at the canonical <id>/ARTIFACT.md path for
// the none harness, carrying hook_event.
func TestHooks_SyncNoneCanonicalLayout(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"audit/log-session-end/ARTIFACT.md": hkArtifact("log-session-end", "stop"),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, "audit/log-session-end/ARTIFACT.md"))
	if !strings.Contains(got, "hook_event: stop") {
		t.Errorf("materialized ARTIFACT.md missing hook_event: stop:\n%s", got)
	}
}

// T-D-hooks-7 — a bundled-script hook materializes ARTIFACT.md and
// scripts/log.sh alongside it for the none harness, with the script content
// preserved.
func TestHooks_SyncNoneBundledScript(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/audit/log-session-end/ARTIFACT.md":    hkBundledArtifact,
		"finance/audit/log-session-end/scripts/log.sh": hkLogScript,
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, "finance/audit/log-session-end/ARTIFACT.md"))
	got := readFile(t, filepath.Join(tgt, "finance/audit/log-session-end/scripts/log.sh"))
	if got != hkLogScript {
		t.Errorf("materialized script content differs from source:\n%s", got)
	}
}

// T-D-hooks-8 — a bundled-script hook materializes ARTIFACT.md and scripts/
// nested under .claude/podium/<id>/ for claude-code.
func TestHooks_SyncClaudeCodeBundledScript(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/audit/log-session-end/ARTIFACT.md":    hkBundledArtifact,
		"finance/audit/log-session-end/scripts/log.sh": hkLogScript,
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, ".claude/podium/finance/audit/log-session-end/ARTIFACT.md"))
	got := readFile(t, filepath.Join(tgt, ".claude/podium/finance/audit/log-session-end/scripts/log.sh"))
	if got != hkLogScript {
		t.Errorf("materialized script content differs from source:\n%s", got)
	}
}

// ---- Lint -------------------------------------------------------------------

// T-D-hooks-9 — runtime_requirements system_packages [jq] is accepted by lint;
// the field is not flagged as unknown on a hook.
func TestHooks_LintRuntimeRequirementsAccepted(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"audit/log-session-end/ARTIFACT.md": hkArtifact("log-session-end", "stop", "runtime_requirements:\n  system_packages: [jq]"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if !hkNoError(res.Stdout+res.Stderr) || strings.Contains(res.Stdout, "unknown field") {
		t.Errorf("runtime_requirements wrongly flagged:\n%s", res.Stdout)
	}
}

// T-D-hooks-10 — a hook whose runtime_requirements names a missing system
// package should fail materialization with runtime_unavailable. The check is
// implemented but never invoked, so the guarantee cannot be exercised.
func TestHooks_RuntimeUnavailableRefusesMaterialize(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.4.1: the materialize.runtime_unavailable check is implemented but never invoked, so a missing system package does not block materialization")
}

// T-D-hooks-11 — a generic pre_tool_use hook emits an info diagnostic naming
// its subtypes.
func TestHooks_LintGenericPreToolUse(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/x/ARTIFACT.md": hkArtifact("x", "pre_tool_use"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.hook_generic_and_subtype") || !strings.Contains(res.Stdout, "pre_shell_execution") {
		t.Errorf("missing generic-hook info naming pre_shell_execution:\n%s", res.Stdout)
	}
}

// T-D-hooks-12 — a generic post_tool_use hook emits an info diagnostic naming
// its post-subtypes.
func TestHooks_LintGenericPostToolUse(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/y/ARTIFACT.md": hkArtifact("y", "post_tool_use"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.hook_generic_and_subtype") || !strings.Contains(res.Stdout, "post_file_edit") {
		t.Errorf("missing generic post-hook info naming post_file_edit:\n%s", res.Stdout)
	}
}

// T-D-hooks-13 — a specific subtype event does not trigger the generic info
// diagnostic.
func TestHooks_LintSubtypeNoGenericInfo(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/z/ARTIFACT.md": hkArtifact("z", "pre_shell_execution"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "lint.hook_generic_and_subtype") {
		t.Errorf("subtype event wrongly flagged:\n%s", res.Stdout)
	}
}

// T-D-hooks-14 — a stop hook is lint-clean with no generic/subtype diagnostic.
func TestHooks_LintStopNoGenericInfo(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/audit-stop/ARTIFACT.md": hkArtifact("audit-stop", "stop"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "lint.hook_generic_and_subtype") || !hkNoError(res.Stdout+res.Stderr) {
		t.Errorf("stop hook should be clean with no generic/subtype info:\n%s", res.Stdout)
	}
}

// ---- MCP bridge -------------------------------------------------------------

// T-D-hooks-15 — full ingest round-trip: a hook served by the standalone
// server loads via the MCP bridge (none harness) and materializes to disk; the
// materialized ARTIFACT.md carries hook_event: stop.
func TestHooks_MCPLoadArtifactRoundTrip(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"audit/log-session-end/ARTIFACT.md": hkArtifact("log-session-end", "stop"),
	}))
	mat := t.TempDir()
	env := append(mcpServerEnv(t, srv.BaseURL), "PODIUM_HARNESS=none", "PODIUM_MATERIALIZE_ROOT="+mat)
	res := mcpExec(t, env, toolCall(1, "load_artifact", map[string]any{"id": "audit/log-session-end"}))
	r := rpcResult(t, res.Stdout, 1)
	if r["id"] != "audit/log-session-end" {
		t.Errorf("result id=%v, want audit/log-session-end", r["id"])
	}
	if r["type"] != "hook" {
		t.Errorf("result type=%v, want hook", r["type"])
	}
	if m, ok := r["materialized_at"].([]any); !ok || len(m) == 0 {
		t.Errorf("materialized_at empty: %v", r["materialized_at"])
	}
	got := readFile(t, filepath.Join(mat, "audit/log-session-end/ARTIFACT.md"))
	if !strings.Contains(got, "hook_event: stop") {
		t.Errorf("materialized ARTIFACT.md missing hook_event: stop:\n%s", got)
	}
}

// T-D-hooks-16 — a bundled-script hook is discoverable via the MCP
// search_artifacts tool by its description keywords.
func TestHooks_MCPSearchArtifacts(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/audit/log-session-end/ARTIFACT.md":    hkBundledArtifact,
		"finance/audit/log-session-end/scripts/log.sh": hkLogScript,
	}))
	res := mcpExec(t, mcpServerEnv(t, srv.BaseURL), toolCall(1, "search_artifacts", map[string]any{"query": "audit session log"}))
	body := mustJSON(rpcResult(t, res.Stdout, 1))
	if !strings.Contains(body, "finance/audit/log-session-end") {
		t.Errorf("search results missing the hook id:\n%s", body)
	}
}

// ---- Payload handling round-trips ------------------------------------------

// T-D-hooks-17 — the simple cat-based hook_action round-trips through sync; the
// materialized ARTIFACT.md preserves INPUT=$(cat).
func TestHooks_SyncSimpleCatAction(t *testing.T) {
	t.Parallel()
	art := "---\ntype: hook\nname: sessions\nversion: 1.0.0\ndescription: Log sessions.\nhook_event: session_end\n" +
		"hook_action: |\n  INPUT=$(cat)\n  echo \"$INPUT\" >> ~/.podium/sessions.log\n---\n\nbody\n"
	reg := writeRegistry(t, map[string]string{"audit/sessions/ARTIFACT.md": art})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, "audit/sessions/ARTIFACT.md"))
	if !strings.Contains(got, "INPUT=$(cat)") {
		t.Errorf("materialized hook_action lost INPUT=$(cat):\n%s", got)
	}
}

// T-D-hooks-18 — the jq-based hook_action round-trips through sync; the
// materialized ARTIFACT.md preserves the jq extraction snippet verbatim.
func TestHooks_SyncJqAction(t *testing.T) {
	t.Parallel()
	art := "---\ntype: hook\nname: stats\nversion: 1.0.0\ndescription: Log stats.\nhook_event: session_end\n" +
		"hook_action: |\n  INPUT=$(cat)\n  CONV_ID=$(echo \"$INPUT\" | jq -r '.session_id // .conversation_id // \"unknown\"')\n" +
		"  echo \"$CONV_ID,$(date -u +%Y-%m-%dT%H:%M:%SZ)\" >> ~/.podium/session-stats.csv\n---\n\nbody\n"
	reg := writeRegistry(t, map[string]string{"audit/stats/ARTIFACT.md": art})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, "audit/stats/ARTIFACT.md"))
	if !strings.Contains(got, `jq -r '.session_id // .conversation_id // "unknown"'`) {
		t.Errorf("materialized hook_action lost the jq snippet:\n%s", got)
	}
}

// T-D-hooks-19 — scaffold with no --hook-action emits a default pipe-literal
// action with an echo stub, and the artifact passes lint.
func TestHooks_ScaffoldDefaultAction(t *testing.T) {
	t.Parallel()
	reg := filepath.Join(t.TempDir(), "reg")
	out := filepath.Join(reg, "hooks/my-hook")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "hook",
		"--description", "x", "--hook-event", "session_start", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "hook_action: |") || !strings.Contains(art, `echo "hook fired"`) {
		t.Errorf("ARTIFACT.md missing default action stub:\n%s", art)
	}
	if res := runPodium(t, "", nil, "lint", "--registry", reg); res.Exit != 0 || !hkNoError(res.Stdout+res.Stderr) {
		t.Errorf("default-action hook should lint clean: exit=%d stdout=%s", res.Exit, res.Stdout)
	}
}

// T-D-hooks-20 — a session_end hook materializes under claude-code with the
// canonical event name unchanged.
func TestHooks_SyncClaudeCodeSessionEnd(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"audit/session-logger/ARTIFACT.md": hkArtifact("session-logger", "session_end"),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, ".claude/podium/audit/session-logger/ARTIFACT.md"))
	if !strings.Contains(got, "hook_event: session_end") {
		t.Errorf("materialized ARTIFACT.md missing hook_event: session_end:\n%s", got)
	}
}

// ---- Required-field lint ----------------------------------------------------

// T-D-hooks-21 — a hook missing type fails lint.required_field_missing.
func TestHooks_LintMissingType(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"bad-hook/ARTIFACT.md": "---\nversion: 1.0.0\nhook_event: stop\nhook_action: |\n  echo hi\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.required_field_missing") {
		t.Errorf("missing lint.required_field_missing:\n%s", res.Stdout)
	}
}

// T-D-hooks-22 — a hook missing version fails lint.required_field_missing and
// the diagnostic names version.
func TestHooks_LintMissingVersion(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"bad-hook/ARTIFACT.md": "---\ntype: hook\nhook_event: stop\nhook_action: |\n  echo hi\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.required_field_missing") || !strings.Contains(res.Stdout, "version") {
		t.Errorf("missing 'version' required-field diagnostic:\n%s", res.Stdout)
	}
}

// T-D-hooks-23 — a non-semver hook version fails lint.invalid_version.
func TestHooks_LintInvalidVersion(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"bad-hook/ARTIFACT.md": "---\ntype: hook\nname: bad-hook\nversion: not-a-version\ndescription: x\nhook_event: stop\nhook_action: |\n  echo hi\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.invalid_version") {
		t.Errorf("missing lint.invalid_version:\n%s", res.Stdout)
	}
}

// T-D-hooks-24 — scaffold rejects an underscore in the artifact name (exit 2).
func TestHooks_ScaffoldRejectsUnderscore(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "my_hook")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "hook",
		"--description", "x", "--hook-event", "stop", "--yes", dir)
	if sc.Exit != 2 {
		t.Fatalf("scaffold exit=%d, want 2\nstderr=%s", sc.Exit, sc.Stderr)
	}
	if !strings.Contains(sc.Stderr, "kebab-case") {
		t.Errorf("stderr missing kebab-case reference:\n%s", sc.Stderr)
	}
}

// T-D-hooks-25 — a hook with target_harnesses [claude-code] and event
// pre_compact lints without error.
func TestHooks_LintTargetHarnessesScoped(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/scoped-hook/ARTIFACT.md": hkArtifact("scoped-hook", "pre_compact", "target_harnesses:\n  - claude-code"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !hkNoError(res.Stdout+res.Stderr) {
		t.Errorf("scoped hook should lint clean: exit=%d stdout=%s", res.Exit, res.Stdout)
	}
}

// T-D-hooks-26 — an MCP load_artifact under claude-code materializes the hook
// to .claude/podium/<id>/ and not to skills/rules/agents subtrees.
func TestHooks_MCPClaudeCodeLayout(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"audit/log-session-end/ARTIFACT.md": hkArtifact("log-session-end", "stop"),
	}))
	mat := t.TempDir()
	env := append(mcpServerEnv(t, srv.BaseURL), "PODIUM_HARNESS=claude-code", "PODIUM_MATERIALIZE_ROOT="+mat)
	res := mcpExec(t, env, toolCall(1, "load_artifact", map[string]any{"id": "audit/log-session-end"}))
	rpcResult(t, res.Stdout, 1)
	mustExist(t, filepath.Join(mat, ".claude/podium/audit/log-session-end/ARTIFACT.md"))
	for _, bad := range []string{".claude/skills", ".claude/rules", ".claude/agents"} {
		if _, err := os.Stat(filepath.Join(mat, bad)); err == nil {
			t.Errorf("hook must not produce a %s subtree", bad)
		}
	}
}

// T-D-hooks-27 — the full bundled-script hook ARTIFACT.md (with all doc fields)
// plus scripts/log.sh lints without error.
func TestHooks_LintBundledScriptClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/audit/log-session-end/ARTIFACT.md":    hkBundledArtifact,
		"finance/audit/log-session-end/scripts/log.sh": hkLogScript,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if !hkNoError(res.Stdout + res.Stderr) {
		t.Errorf("bundled-script hook emitted [error] lines:\n%s", res.Stdout)
	}
}

// T-D-hooks-28 — the bundled-script hook materializes ARTIFACT.md (with the
// pipe-literal action referencing scripts/log.sh) and the script under
// .claude/podium/<id>/ for claude-code.
func TestHooks_SyncClaudeCodeBundledFull(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/audit/log-session-end/ARTIFACT.md":    hkBundledArtifact,
		"finance/audit/log-session-end/scripts/log.sh": hkLogScript,
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	art := readFile(t, filepath.Join(tgt, ".claude/podium/finance/audit/log-session-end/ARTIFACT.md"))
	if !strings.Contains(art, "hook_action: |") || !strings.Contains(art, "scripts/log.sh") {
		t.Errorf("materialized ARTIFACT.md missing pipe-literal action / scripts/log.sh:\n%s", art)
	}
	got := readFile(t, filepath.Join(tgt, ".claude/podium/finance/audit/log-session-end/scripts/log.sh"))
	if got != hkLogScript {
		t.Errorf("materialized script content differs from source:\n%s", got)
	}
}

// T-D-hooks-29 — the doc says the bundled script is "testable in isolation",
// implying an executable bit. The materialize path writes via writeAtomic,
// which defaults to mode 0o644, so the materialized script is not executable.
// No BUILD-GAPS finding is filed for this; the test asserts the actual
// (non-executable) mode as a change-detector.
func TestHooks_BundledScriptNotExecutable(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/audit/log-session-end/ARTIFACT.md":    hkBundledArtifact,
		"finance/audit/log-session-end/scripts/log.sh": hkLogScript,
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	fi, err := os.Stat(filepath.Join(tgt, ".claude/podium/finance/audit/log-session-end/scripts/log.sh"))
	if err != nil {
		t.Fatalf("stat materialized script: %v", err)
	}
	if fi.Mode().Perm()&0o111 != 0 {
		t.Errorf("materialized script is executable (mode=%o); the executable bit is not expected to be preserved", fi.Mode().Perm())
	}
}

// T-D-hooks-30 — one hook per canonical event materializes under claude-code,
// each carrying its hook_event.
func TestHooks_SyncClaudeCodeAllEvents(t *testing.T) {
	t.Parallel()
	entries := map[string]string{}
	for _, ev := range hkCanonicalEvents {
		id := "hooks/" + strings.ReplaceAll(ev, "_", "-")
		entries[id+"/ARTIFACT.md"] = hkArtifact(strings.ReplaceAll(ev, "_", "-"), ev)
	}
	reg := writeRegistry(t, entries)
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	for _, ev := range hkCanonicalEvents {
		id := "hooks/" + strings.ReplaceAll(ev, "_", "-")
		got := readFile(t, filepath.Join(tgt, ".claude/podium", id, "ARTIFACT.md"))
		if !strings.Contains(got, "hook_event: "+ev) {
			t.Errorf("materialized %s missing hook_event: %s:\n%s", id, ev, got)
		}
	}
}

// T-D-hooks-31 — a hook lands under .claude/podium/<id>/ARTIFACT.md (containing
// type: hook) and not under .claude/rules or .claude/agents for claude-code.
func TestHooks_SyncClaudeCodeNotRulesOrAgents(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/stop-hook/ARTIFACT.md": hkArtifact("stop-hook", "stop"),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, ".claude/podium/hooks/stop-hook/ARTIFACT.md"))
	if !strings.Contains(got, "type: hook") {
		t.Errorf("materialized ARTIFACT.md missing type: hook:\n%s", got)
	}
	for _, bad := range []string{".claude/rules", ".claude/agents"} {
		if _, err := os.Stat(filepath.Join(tgt, bad)); err == nil {
			t.Errorf("hook must not produce a %s subtree", bad)
		}
	}
}

// T-D-hooks-32 — a hook with a jq action and runtime_requirements
// system_packages [jq] materializes under claude-code preserving all of it.
func TestHooks_SyncClaudeCodeRuntimeAndAction(t *testing.T) {
	t.Parallel()
	art := "---\ntype: hook\nname: stats\nversion: 1.0.0\ndescription: Log stats.\nhook_event: session_end\n" +
		"hook_action: |\n  INPUT=$(cat)\n  CONV_ID=$(echo \"$INPUT\" | jq -r '.session_id // .conversation_id // \"unknown\"')\n" +
		"runtime_requirements:\n  system_packages: [jq]\n---\n\nbody\n"
	reg := writeRegistry(t, map[string]string{"audit/stats/ARTIFACT.md": art})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, ".claude/podium/audit/stats/ARTIFACT.md"))
	for _, want := range []string{"system_packages", "jq", "hook_action: |", `jq -r '.session_id // .conversation_id // "unknown"'`} {
		if !strings.Contains(got, want) {
			t.Errorf("materialized ARTIFACT.md missing %q:\n%s", want, got)
		}
	}
}

// T-D-hooks-33 — interactive scaffold (no --yes) prompts for hook_event and the
// supplied value lands in ARTIFACT.md.
func TestHooks_ScaffoldInteractiveEvent(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "interactive-hook")
	sc := runPodiumStdin(t, "", nil, "pre_tool_use\n",
		"artifact", "scaffold", "--type", "hook", "--description", "x", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	if art := readFile(t, filepath.Join(out, "ARTIFACT.md")); !strings.Contains(art, "hook_event: pre_tool_use") {
		t.Errorf("ARTIFACT.md missing hook_event: pre_tool_use:\n%s", art)
	}
}

// T-D-hooks-34 — an MCP load_artifact under claude-code for a hook materializes
// the ARTIFACT.md carrying hook_event and hook_action; the result has no error.
func TestHooks_MCPLoadArtifactFields(t *testing.T) {
	t.Parallel()
	art := "---\ntype: hook\nname: log-stop\nversion: 1.0.0\ndescription: Log stop.\nhook_event: stop\nhook_action: |\n  echo done\n---\n\nbody\n"
	srv := startServer(t, writeRegistry(t, map[string]string{"hooks/log-stop/ARTIFACT.md": art}))
	mat := t.TempDir()
	env := append(mcpServerEnv(t, srv.BaseURL), "PODIUM_HARNESS=claude-code", "PODIUM_MATERIALIZE_ROOT="+mat)
	res := mcpExec(t, env, toolCall(1, "load_artifact", map[string]any{"id": "hooks/log-stop"}))
	rpcResult(t, res.Stdout, 1)
	got := readFile(t, filepath.Join(mat, ".claude/podium/hooks/log-stop/ARTIFACT.md"))
	if !strings.Contains(got, "hook_event: stop") || !strings.Contains(got, "hook_action") {
		t.Errorf("materialized ARTIFACT.md missing hook_event/hook_action:\n%s", got)
	}
}

// T-D-hooks-35 — an MCP load_artifact should materialize a hook's bundled
// script under PODIUM_MATERIALIZE_ROOT. The server ingest path discards bundled
// resource bytes, so the script never transits the bridge.
func TestHooks_MCPBundledScriptMaterialize(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-7.2.2: the server ingest path discards bundled resource bytes, so load_artifact materializes only ARTIFACT.md and never the bundled scripts/log.sh")
}

// T-D-hooks-36 — when the configured harness does not support the event, lint
// should reject ingest unless target_harnesses excludes it. No ingest-time
// capability-matrix lint exists.
func TestHooks_LintUnsupportedEventRejected(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-6.7.1: there is no ingest-time capability-matrix lint, so an event unsupported by the configured harness is not rejected")
}

// T-D-hooks-37 — target_harnesses should filter materialization to the listed
// harnesses. The field is parsed but never honored at sync time.
func TestHooks_TargetHarnessesFiltersMaterialize(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-6.7.2: target_harnesses is parsed but never honored, so a hook materializes for every harness regardless of the list")
}

// T-D-hooks-38 — effort_hint on a hook produces a hint_on_unsupported_type
// warning.
func TestHooks_LintEffortHintWarns(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/bad-hint/ARTIFACT.md": hkArtifact("bad-hint", "stop", "effort_hint: high"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.hint_on_unsupported_type") || !strings.Contains(res.Stdout, "[warning]") {
		t.Errorf("missing hint_on_unsupported_type warning:\n%s", res.Stdout)
	}
}

// T-D-hooks-39 — model_class_hint on a hook produces a hint_on_unsupported_type
// warning.
func TestHooks_LintModelClassHintWarns(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/bad-hint/ARTIFACT.md": hkArtifact("bad-hint", "stop", "model_class_hint: frontier"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.hint_on_unsupported_type") || !strings.Contains(res.Stdout, "[warning]") {
		t.Errorf("missing hint_on_unsupported_type warning:\n%s", res.Stdout)
	}
}

// T-D-hooks-40 — the doc says lint requires sandbox_profile for hooks at
// sensitivity ≥ medium. No such rule exists, so a medium-sensitivity hook with
// no sandbox_profile lints clean and emits no sandbox diagnostic. No
// BUILD-GAPS finding is filed; this asserts the actual behavior as a
// change-detector.
func TestHooks_LintMediumNoSandboxClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/medium-no-sandbox/ARTIFACT.md": hkArtifact("medium-no-sandbox", "stop", "sensitivity: medium"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "sandbox") {
		t.Errorf("unexpected sandbox diagnostic (no such rule exists):\n%s", res.Stdout)
	}
}

// T-D-hooks-41 — the same gap at sensitivity high: no sandbox_profile rule
// fires. Asserted against actual behavior (no finding filed).
func TestHooks_LintHighNoSandboxClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/high-no-sandbox/ARTIFACT.md": hkArtifact("high-no-sandbox", "stop", "sensitivity: high"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "sandbox") {
		t.Errorf("unexpected sandbox diagnostic (no such rule exists):\n%s", res.Stdout)
	}
}

// T-D-hooks-42 — a medium-sensitivity hook that sets sandbox_profile lints
// without error.
func TestHooks_LintMediumWithSandboxClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/medium-sandbox/ARTIFACT.md": hkArtifact("medium-sandbox", "stop", "sensitivity: medium", "sandbox_profile: read-only-fs"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !hkNoError(res.Stdout+res.Stderr) {
		t.Errorf("medium+sandbox hook should lint clean: exit=%d stdout=%s", res.Exit, res.Stdout)
	}
}

// T-D-hooks-43 — a low-sensitivity hook with no sandbox_profile lints without
// error.
func TestHooks_LintLowNoSandboxClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/low-no-sandbox/ARTIFACT.md": hkArtifact("low-no-sandbox", "stop", "sensitivity: low"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !hkNoError(res.Stdout+res.Stderr) {
		t.Errorf("low-sensitivity hook should lint clean: exit=%d stdout=%s", res.Exit, res.Stdout)
	}
}

// T-D-hooks-44 — the doc presents hook_event as required, but ruleRequiredFields
// does not check it. A hook missing hook_event is not flagged.
func TestHooks_LintMissingEventNotRequired(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.3.7: type-specific required fields are not validated, so a hook missing hook_event is not flagged by lint")
}

// T-D-hooks-45 — scaffold produces a placeholder body after the closing
// frontmatter delimiter.
func TestHooks_ScaffoldBodyPlaceholder(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "body-hook")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "hook",
		"--description", "x", "--hook-event", "stop", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	idx := strings.LastIndex(art, "---")
	if idx < 0 {
		t.Fatalf("ARTIFACT.md has no frontmatter delimiter:\n%s", art)
	}
	body := art[idx+3:]
	if !strings.Contains(body, "Hook body. Document the side effect") {
		t.Errorf("body after frontmatter missing placeholder text:\n%s", body)
	}
}

// T-D-hooks-46 — a hook materializes at the canonical <id>/ARTIFACT.md path for
// the none harness, carrying type: hook and hook_event: stop.
func TestHooks_SyncNoneStopLogger(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/stop-logger/ARTIFACT.md": hkArtifact("stop-logger", "stop"),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, "hooks/stop-logger/ARTIFACT.md"))
	if !strings.Contains(got, "type: hook") || !strings.Contains(got, "hook_event: stop") {
		t.Errorf("materialized ARTIFACT.md missing type/hook_event:\n%s", got)
	}
}

// T-D-hooks-47 — tags and sensitivity survive a claude-code sync round-trip.
func TestHooks_SyncClaudeCodeTagsSensitivity(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/tagged/ARTIFACT.md": hkArtifact("tagged", "stop", "tags: [hook, audit]", "sensitivity: low"),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, ".claude/podium/hooks/tagged/ARTIFACT.md"))
	for _, want := range []string{"hook", "audit", "sensitivity: low"} {
		if !strings.Contains(got, want) {
			t.Errorf("materialized ARTIFACT.md missing %q:\n%s", want, got)
		}
	}
}

// T-D-hooks-48 — scaffold rejects an uppercase artifact name (exit 2).
func TestHooks_ScaffoldRejectsUppercase(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "My-Hook")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "hook",
		"--description", "x", "--hook-event", "stop", "--yes", dir)
	if sc.Exit != 2 {
		t.Fatalf("scaffold exit=%d, want 2\nstderr=%s", sc.Exit, sc.Stderr)
	}
	if !strings.Contains(sc.Stderr, "kebab-case") {
		t.Errorf("stderr missing kebab-case reference:\n%s", sc.Stderr)
	}
}

// T-D-hooks-49 — a hook with a specific description is searchable over HTTP by
// its description keywords, and the result names type hook.
func TestHooks_HTTPSearchByDescription(t *testing.T) {
	t.Parallel()
	art := "---\ntype: hook\nname: log-session-end\nversion: 1.0.0\ndescription: Log session-end events to a local audit file.\nhook_event: stop\nhook_action: |\n  echo hi\n---\n\nbody\n"
	srv := startServer(t, writeRegistry(t, map[string]string{"finance/audit/log-session-end/ARTIFACT.md": art}))
	_, body := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query=session-end+events+audit")
	s := string(body)
	if !strings.Contains(s, "finance/audit/log-session-end") || !strings.Contains(s, "hook") {
		t.Errorf("search results missing hook id / type:\n%s", s)
	}
}

// T-D-hooks-50 — the HTTP load_artifact endpoint returns a frontmatter string
// carrying hook_event and hook_action.
func TestHooks_HTTPLoadArtifactFrontmatter(t *testing.T) {
	t.Parallel()
	art := "---\ntype: hook\nname: log-stop\nversion: 1.0.0\ndescription: Log stop.\nhook_event: stop\nhook_action: |\n  echo done\n---\n\nbody\n"
	srv := startServer(t, writeRegistry(t, map[string]string{"hooks/log-stop/ARTIFACT.md": art}))
	st, body := getRaw(t, srv.BaseURL+"/v1/load_artifact?id=hooks/log-stop")
	if st != 200 {
		t.Fatalf("load = HTTP %d, want 200\n%s", st, body)
	}
	var load struct {
		Frontmatter string `json:"frontmatter"`
	}
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id=hooks/log-stop", &load)
	if !strings.Contains(load.Frontmatter, "hook_event") || !strings.Contains(load.Frontmatter, "hook_action") {
		t.Errorf("frontmatter missing hook_event/hook_action:\n%s", load.Frontmatter)
	}
}

// T-D-hooks-51 — a hook with a non-canonical event value does not panic during
// sync; the event passes through and the file materializes.
func TestHooks_SyncUnknownEventNoPanic(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/vendor/ARTIFACT.md": hkArtifact("vendor", "custom_vendor_event"),
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d, want 0\nstderr=%s", res.Exit, res.Stderr)
	}
	if strings.Contains(res.Stderr, "panic") {
		t.Errorf("sync stderr contains panic:\n%s", res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, ".claude/podium/hooks/vendor/ARTIFACT.md"))
}

// T-D-hooks-52 — a hook in a domain hierarchy is discoverable via domain show;
// the listing names the artifact and type hook.
func TestHooks_DomainShowListsHook(t *testing.T) {
	t.Parallel()
	art := "---\ntype: hook\nname: log-session-end\nversion: 1.0.0\ndescription: Log session-end events to a local audit file.\nhook_event: stop\nhook_action: |\n  echo hi\n---\n\nbody\n"
	srv := startServer(t, writeRegistry(t, map[string]string{"finance/audit/log-session-end/ARTIFACT.md": art}))
	res := runPodium(t, "", nil, "domain", "show", "--registry", srv.BaseURL, "finance/audit")
	if res.Exit != 0 {
		t.Fatalf("domain show exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "log-session-end") || !strings.Contains(res.Stdout, "hook") {
		t.Errorf("domain show missing the hook / type:\n%s", res.Stdout)
	}
}

// T-D-hooks-53 — a direct-only hook is hidden from default search while an
// indexed hook still appears.
// spec: §4.3 universal fields (search_visibility), §4.5.3 (F-4.3.3).
func TestHooks_DirectOnlyHiddenFromSearch(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"hooks/audit-logger/ARTIFACT.md":  hkArtifact("audit-logger", "stop"),
		"hooks/secret-logger/ARTIFACT.md": hkArtifact("secret-logger", "stop", "search_visibility: direct-only"),
	}))
	_, sbody := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query=hook")
	if strings.Contains(string(sbody), "hooks/secret-logger") {
		t.Errorf("direct-only hook appeared in search results:\n%s", sbody)
	}
	if !strings.Contains(string(sbody), "hooks/audit-logger") {
		t.Errorf("indexed hook missing from search:\n%s", sbody)
	}
}

// T-D-hooks-54 — a second sync over the same state is idempotent: both runs
// exit 0, the materialized ARTIFACT.md content is identical, and no .tmp files
// remain in the target.
func TestHooks_SyncIdempotent(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/stop-logger/ARTIFACT.md": hkArtifact("stop-logger", "stop"),
	})
	tgt := t.TempDir()
	dst := filepath.Join(tgt, "hooks/stop-logger/ARTIFACT.md")
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("first sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	first := readFile(t, dst)
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("second sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if second := readFile(t, dst); second != first {
		t.Errorf("materialized content changed across syncs:\nfirst=%s\nsecond=%s", first, second)
	}
	for path := range readTreeAll(t, tgt) {
		if strings.HasSuffix(path, ".tmp") {
			t.Errorf("found leftover temp file: %s", path)
		}
	}
}

// T-D-hooks-55 — scaffold with --tags, --sensitivity, and --hook-event writes
// the tags list, sensitivity, and hook_event into ARTIFACT.md.
func TestHooks_ScaffoldTagsSensitivityEvent(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "full-hook")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "hook",
		"--description", "Log session-end events to a local audit file.",
		"--hook-event", "stop", "--tags", "hook,audit", "--sensitivity", "low", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	for _, want := range []string{"tags: [hook, audit]", "sensitivity: low", "hook_event: stop"} {
		if !strings.Contains(art, want) {
			t.Errorf("ARTIFACT.md missing %q:\n%s", want, art)
		}
	}
}
