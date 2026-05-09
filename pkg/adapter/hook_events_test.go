package adapter

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
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
	out, err := a.Adapt(src)
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
// Phase: 13
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
	testharness.RequirePhase(t, 13)
	t.Parallel()
	for _, event := range canonicalHookEvents {
		runHookEventCell(t, event)
	}
}
