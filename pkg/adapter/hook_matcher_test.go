package adapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// hookSrc builds a hook artifact carrying one canonical event.
func hookSrc(event string) Source {
	return Source{
		ArtifactID: "hooks/" + event,
		ArtifactBytes: []byte("---\ntype: hook\nversion: 1.0.0\ndescription: a hook\n" +
			"hook_event: " + event + "\nhook_action: |\n  echo " + event + "\n---\n\n"),
	}
}

// adaptOne runs the adapter and returns the single config-merge fragment.
func adaptOne(t *testing.T, a HarnessAdapter, event string) string {
	t.Helper()
	out, err := a.Adapt(context.Background(), hookSrc(event))
	if err != nil {
		t.Fatalf("Adapt(%s): %v", event, err)
	}
	if len(out) == 0 {
		t.Fatalf("hook %s: produced no output", event)
	}
	return string(out[0].Content)
}

// matcherOf reads the matcher of the single entry under the single native
// event key of a {"hooks": {"<event>": [entry]}} fragment. It returns the
// matcher and whether the key was present at all.
func matcherOf(t *testing.T, fragment string) (string, bool) {
	t.Helper()
	var doc struct {
		Hooks map[string][]struct {
			Matcher *string `json:"matcher"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(fragment), &doc); err != nil {
		t.Fatalf("fragment is not the expected JSON: %v\n%s", err, fragment)
	}
	if len(doc.Hooks) != 1 {
		t.Fatalf("want exactly one native event key, got %d:\n%s", len(doc.Hooks), fragment)
	}
	for _, entries := range doc.Hooks {
		if len(entries) != 1 {
			t.Fatalf("want exactly one entry, got %d:\n%s", len(entries), fragment)
		}
		if entries[0].Matcher == nil {
			return "", false
		}
		return *entries[0].Matcher, true
	}
	return "", false
}

// Spec: §4.3.5 — when the harness emits only the generic tool event natively,
// the adapter installs the subtype hook with a tool-name matcher so only
// matching tool calls fire it. Without the matcher a hook declared for one tool
// category runs on every tool call at that phase, which is a silent
// over-trigger: the action still succeeds, so nothing surfaces the defect.
//
// The matcher literals are the harness's own tool names. Claude Code names the
// shell tool Bash, the read tool Read, and the edit tools Edit, Write, and
// NotebookEdit, and prefixes MCP tools with mcp__. Codex fires tool hooks for
// Bash and carries mcp__ matcher aliases for MCP calls. Gemini CLI names the
// shell tool run_shell_command and prefixes MCP tools with mcp_.
func TestHookMatcher_SubtypeRestrictsToItsToolCategory(t *testing.T) {
	t.Parallel()
	cases := []struct {
		adapter HarnessAdapter
		name    string
		event   string
		want    string
	}{
		{ClaudeCode{}, "claude-code", "pre_shell_execution", "^Bash$"},
		{ClaudeCode{}, "claude-code", "post_shell_execution", "^Bash$"},
		{ClaudeCode{}, "claude-code", "pre_mcp_execution", "^mcp__"},
		{ClaudeCode{}, "claude-code", "post_mcp_execution", "^mcp__"},
		{ClaudeCode{}, "claude-code", "pre_read_file", "^Read$"},
		{ClaudeCode{}, "claude-code", "post_file_edit", "^(Edit|Write|NotebookEdit)$"},
		{ClaudeCode{}, "claude-code", "subagent_start", "^Task$"},
		{Gemini{}, "gemini", "pre_shell_execution", "^run_shell_command$"},
		{Gemini{}, "gemini", "post_shell_execution", "^run_shell_command$"},
		{Gemini{}, "gemini", "pre_mcp_execution", "^mcp_"},
		{Gemini{}, "gemini", "post_mcp_execution", "^mcp_"},
	}
	for _, tc := range cases {
		got, ok := matcherOf(t, adaptOne(t, tc.adapter, tc.event))
		if !ok {
			t.Errorf("%s %s: no matcher, so the hook fires on every tool call at that phase; want %q",
				tc.name, tc.event, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("%s %s: matcher = %q, want %q", tc.name, tc.event, got, tc.want)
		}
	}
}

// Spec: §4.3.5 — a generic event is the whole tool phase, so it carries no
// matcher. Emitting one would narrow a hook the author declared broadly.
func TestHookMatcher_GenericEventCarriesNoMatcher(t *testing.T) {
	t.Parallel()
	for _, event := range []string{"pre_tool_use", "post_tool_use"} {
		if _, ok := matcherOf(t, adaptOne(t, ClaudeCode{}, event)); ok {
			t.Errorf("claude-code %s: generic event must carry no matcher", event)
		}
	}
}

// Spec: §4.3.5 — an event that is not a tool category carries no tool-name
// matcher. permission_request and permission_denied are permission-category
// events and post_tool_use_failure is an outcome, so none of them narrows to a
// tool name even though Claude Code receives them on a generic tool event.
func TestHookMatcher_NonToolCategoryEventsCarryNoMatcher(t *testing.T) {
	t.Parallel()
	for _, event := range []string{"permission_request", "permission_denied", "post_tool_use_failure"} {
		if _, ok := matcherOf(t, adaptOne(t, ClaudeCode{}, event)); ok {
			t.Errorf("claude-code %s: not a tool category, so it must carry no tool-name matcher", event)
		}
	}
}

// Spec: §4.3.5 — a harness that emits the subtype natively wires it directly,
// so no matcher is involved. Cursor has beforeShellExecution, and narrowing it
// by tool name would be redundant at best.
func TestHookMatcher_NativeSubtypeCarriesNoMatcher(t *testing.T) {
	t.Parallel()
	body := adaptOne(t, Cursor{}, "pre_shell_execution")
	if strings.Contains(body, "matcher") {
		t.Errorf("cursor pre_shell_execution is native, so the fragment carries no matcher:\n%s", body)
	}
}

// Spec: §4.3.5 — Codex reaches its tool hooks through config.toml rather than
// JSON, and the matcher is a key on the event's array-of-tables entry.
func TestHookMatcher_CodexTOMLCarriesTheMatcher(t *testing.T) {
	t.Parallel()
	out, err := Codex{}.Adapt(context.Background(), hookSrc("pre_shell_execution"))
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("codex pre_shell_execution: produced no output")
	}
	body := string(out[0].Content)
	if !strings.Contains(body, `matcher = "^Bash$"`) {
		t.Errorf("codex pre_shell_execution: fragment carries no matcher:\n%s", body)
	}
	// The matcher keys the event table, so it precedes the nested hooks table.
	if i, j := strings.Index(body, "matcher ="), strings.Index(body, ".hooks]]"); i > j {
		t.Errorf("matcher must key the event table rather than the handler:\n%s", body)
	}
}

// Spec: §4.3.5 — a generic Codex tool event carries no matcher, so it keeps
// firing for every tool the harness reports.
func TestHookMatcher_CodexGenericEventCarriesNoMatcher(t *testing.T) {
	t.Parallel()
	out, err := Codex{}.Adapt(context.Background(), hookSrc("pre_tool_use"))
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("codex pre_tool_use: produced no output")
	}
	if body := string(out[0].Content); strings.Contains(body, "matcher") {
		t.Errorf("codex pre_tool_use: generic event must carry no matcher:\n%s", body)
	}
}
