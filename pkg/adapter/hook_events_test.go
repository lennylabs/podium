package adapter

import (
	"context"
	"strings"
	"testing"
)

// canonicalHookEvents lists the §4.3.5 hook events the spec table
// defines. Each pairs with the claude-code adapter (the ✓-row in the
// §6.7.1 hook_event row).
var canonicalHookEvents = []string{
	"session_start", "session_end",
	"user_prompt_submit",
	"pre_tool_use", "post_tool_use", "post_tool_use_failure",
	"pre_shell_execution", "post_shell_execution",
	"pre_mcp_execution", "post_mcp_execution",
	"pre_read_file", "post_file_edit",
	"permission_request", "permission_denied",
	"subagent_start", "subagent_stop",
	"stop", "pre_compact", "post_compact",
	"notification",
}

// runHookEventCell tests one (event, claude-code) cell of the §4.3.5
// canonical-hook-events matrix: a hook artifact with the named event
// produces output through the claude-code adapter and the event name
// is preserved through to the materialized output.
func runHookEventCell(t *testing.T, event string) {
	t.Helper()
	r := DefaultRegistry()
	a, err := r.Get("claude-code")
	if err != nil {
		t.Fatalf("Get(claude-code): %v", err)
	}
	src := Source{
		ArtifactID: "hooks/" + event,
		ArtifactBytes: []byte("---\ntype: hook\nversion: 1.0.0\n" +
			"description: a hook\n" +
			"hook_event: " + event + "\n" +
			"hook_action: |\n  echo " + event + "\n" +
			"---\n\n"),
	}
	out, err := a.Adapt(context.Background(), src)
	if err != nil {
		t.Fatalf("Adapt(%s): %v", event, err)
	}
	if len(out) == 0 {
		t.Errorf("hook %s: produced no output", event)
		return
	}
	found := false
	for _, f := range out {
		if strings.Contains(string(f.Content), event) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("hook %s: event name not preserved in output", event)
	}
}

// Spec: §4.3.5 / §6.7.1 — claude-code adapter materializes every
// canonical hook event from the §4.3.5 table.
// Matrix: §4.3.5 (session_start)
// Matrix: §4.3.5 (session_end)
// Matrix: §4.3.5 (user_prompt_submit)
// Matrix: §4.3.5 (pre_tool_use)
// Matrix: §4.3.5 (post_tool_use)
// Matrix: §4.3.5 (post_tool_use_failure)
// Matrix: §4.3.5 (pre_shell_execution)
// Matrix: §4.3.5 (post_shell_execution)
// Matrix: §4.3.5 (pre_mcp_execution)
// Matrix: §4.3.5 (post_mcp_execution)
// Matrix: §4.3.5 (pre_read_file)
// Matrix: §4.3.5 (post_file_edit)
// Matrix: §4.3.5 (permission_request)
// Matrix: §4.3.5 (permission_denied)
// Matrix: §4.3.5 (subagent_start)
// Matrix: §4.3.5 (subagent_stop)
// Matrix: §4.3.5 (stop)
// Matrix: §4.3.5 (pre_compact)
// Matrix: §4.3.5 (post_compact)
// Matrix: §4.3.5 (notification)
func TestHookEvents_AllCanonicalEventsClaudeCode(t *testing.T) {
	t.Parallel()
	for _, event := range canonicalHookEvents {
		runHookEventCell(t, event)
	}
}

// Spec: §6.7 / §6.7.1 — the cursor adapter config-merges a hook whose canonical
// event has a Cursor-native subtype into .cursor/hooks.json under that native
// event key. pre_shell_execution maps to beforeShellExecution.
func TestHookEvents_CursorSubtypeConfigMerge(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID: "hooks/audit",
		ArtifactBytes: []byte("---\ntype: hook\nversion: 1.0.0\ndescription: a hook\n" +
			"hook_event: pre_shell_execution\nhook_action: |\n  echo audit\n---\n\n"),
	}
	out, err := Cursor{}.Adapt(context.Background(), src)
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	if len(out) != 1 || out[0].Path != ".cursor/hooks.json" || out[0].Op != OpMergeJSON {
		t.Fatalf("got %+v, want a single .cursor/hooks.json OpMergeJSON", out)
	}
	body := string(out[0].Content)
	if !strings.Contains(body, "beforeShellExecution") || !strings.Contains(body, "echo audit") {
		t.Errorf("fragment missing the native event / command:\n%s", body)
	}
	// Cursor's hooks.json schema requires a top-level version field.
	if !strings.Contains(body, `"version":1`) {
		t.Errorf("fragment missing the required top-level version:\n%s", body)
	}
}

// Spec: §6.7 — a config-merge hook's bundled script materializes to the
// harness-neutral .podium/resources/<id>/ bucket, and the merged command is
// rewritten from the registry-relative path to that materialized path so it
// resolves from the project root.
func TestHookEvents_BundledScriptPathRewrite(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID: "audit/log-stop",
		ArtifactBytes: []byte("---\ntype: hook\nversion: 1.0.0\ndescription: a hook\n" +
			"hook_event: stop\nhook_action: |\n  scripts/log.sh\n---\n\n"),
		Resources: map[string][]byte{"scripts/log.sh": []byte("#!/bin/sh\necho hi\n")},
	}
	out, err := ClaudeCode{}.Adapt(context.Background(), src)
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	var settings, script string
	var haveScript bool
	for _, f := range out {
		switch f.Path {
		case ".claude/settings.json":
			settings = string(f.Content)
		case ".podium/resources/audit/log-stop/scripts/log.sh":
			script = string(f.Content)
			haveScript = true
		}
	}
	if !haveScript || script == "" {
		t.Fatalf("bundled script not materialized to the resource bucket: %+v", out)
	}
	if !strings.Contains(settings, ".podium/resources/audit/log-stop/scripts/log.sh") {
		t.Errorf("merged command not rewritten to the materialized path:\n%s", settings)
	}
}

// codexCommonHookEvents are the §6.7.1 "common events" the Codex hook_event ✓
// grade requires the adapter to config-merge: session_start, session_end,
// pre_tool_use, post_tool_use, stop, and pre_compact.
var codexCommonHookEvents = []string{
	"session_start", "session_end",
	"pre_tool_use", "post_tool_use",
	"stop", "pre_compact",
}

// Spec: §6.7.1 — the Codex hook_event cell is graded ✓, which the spec defines
// as config-merging all the common events. session_end was previously unmapped
// in codexHookEvents, so a session_end hook materialized nothing while the
// matrix still advertised ✓ (the ⚠ subset condition). Every common event must
// now config-merge a [[hooks.<Event>]] table into .codex/config.toml.
func TestHookEvents_CodexCoversCommonEvents(t *testing.T) {
	t.Parallel()
	a, err := DefaultRegistry().Get("codex")
	if err != nil {
		t.Fatalf("Get(codex): %v", err)
	}
	for _, event := range codexCommonHookEvents {
		src := Source{
			ArtifactID: "hooks/" + event,
			ArtifactBytes: []byte("---\ntype: hook\nversion: 1.0.0\ndescription: a hook\n" +
				"hook_event: " + event + "\nhook_action: |\n  echo " + event + "\n---\n\n"),
		}
		out, err := a.Adapt(context.Background(), src)
		if err != nil {
			t.Fatalf("Adapt(%s): %v", event, err)
		}
		var merged string
		for _, f := range out {
			if f.Path == ".codex/config.toml" && f.Op == OpInject {
				merged = string(f.Content)
			}
		}
		if merged == "" {
			t.Errorf("codex hook %s: no .codex/config.toml inject produced; the ✓ grade requires every common event to config-merge", event)
			continue
		}
		if !strings.Contains(merged, "[[hooks.") || !strings.Contains(merged, "echo "+event) {
			t.Errorf("codex hook %s: fragment missing the hooks table or command:\n%s", event, merged)
		}
	}
}

// Spec: §6.7.1 — session_end specifically maps to the Codex-native SessionEnd
// event, the regression this finding fixed.
func TestHookEvents_CodexSessionEnd(t *testing.T) {
	t.Parallel()
	a, err := DefaultRegistry().Get("codex")
	if err != nil {
		t.Fatalf("Get(codex): %v", err)
	}
	src := Source{
		ArtifactID: "hooks/teardown",
		ArtifactBytes: []byte("---\ntype: hook\nversion: 1.0.0\ndescription: a hook\n" +
			"hook_event: session_end\nhook_action: |\n  echo bye\n---\n\n"),
	}
	out, err := a.Adapt(context.Background(), src)
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	var merged string
	for _, f := range out {
		if f.Path == ".codex/config.toml" {
			merged = string(f.Content)
		}
	}
	if !strings.Contains(merged, "[[hooks.SessionEnd]]") {
		t.Errorf("session_end did not config-merge a SessionEnd table:\n%s", merged)
	}
}

// Spec: §6.7.1 — a canonical event Cursor has no native subtype for (the
// generic pre_tool_use) produces no Cursor hook output, reflecting the ⚠
// partial-coverage cell.
func TestHookEvents_CursorGenericEventNoOutput(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID: "hooks/generic",
		ArtifactBytes: []byte("---\ntype: hook\nversion: 1.0.0\ndescription: a hook\n" +
			"hook_event: pre_tool_use\nhook_action: |\n  echo generic\n---\n\n"),
	}
	out, err := Cursor{}.Adapt(context.Background(), src)
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("generic pre_tool_use should produce no Cursor hook output, got %+v", out)
	}
}
