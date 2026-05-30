package manifest

import "testing"

// Spec: §4.3.5 (F-4.3.2) — the canonical hook event taxonomy has the 20
// names the spec table defines, every one is recognized, and the list is
// returned as a defensive copy.
func TestCanonicalHookEvents(t *testing.T) {
	t.Parallel()
	want := []string{
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
	got := CanonicalHookEvents()
	if len(got) != len(want) {
		t.Fatalf("CanonicalHookEvents len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("CanonicalHookEvents[%d] = %q, want %q", i, got[i], want[i])
		}
		if !IsCanonicalHookEvent(want[i]) {
			t.Errorf("IsCanonicalHookEvent(%q) = false, want true", want[i])
		}
	}
	// The accessor returns a copy; mutating it must not affect the package.
	got[0] = "mutated"
	if CanonicalHookEvents()[0] == "mutated" {
		t.Errorf("CanonicalHookEvents returned a shared backing array")
	}
}

// Spec: §4.3.5 (F-4.3.2) — names outside the taxonomy (misspellings,
// empty, native harness names) are not canonical.
func TestIsCanonicalHookEvent_Rejects(t *testing.T) {
	t.Parallel()
	for _, e := range []string{"", "on_stop", "Stop", "beforeShellExecution", "pre_tool", "post_tool_use_failed"} {
		if IsCanonicalHookEvent(e) {
			t.Errorf("IsCanonicalHookEvent(%q) = true, want false", e)
		}
	}
}
