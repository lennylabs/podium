package manifest

// Canonical hook event names per §4.3.5. A type: hook artifact's
// hook_event field is constrained to one of these names; the harness
// adapter (§6.7) translates the canonical name to the harness's native
// event at materialization time. The grouping comments mirror the spec
// table (session, prompt, tool generic, tool subtype, permission,
// subagent, turn, compaction, notification).
const (
	// session group.
	HookEventSessionStart = "session_start"
	HookEventSessionEnd   = "session_end"
	// prompt group.
	HookEventUserPromptSubmit = "user_prompt_submit"
	// tool group (generic).
	HookEventPreToolUse         = "pre_tool_use"
	HookEventPostToolUse        = "post_tool_use"
	HookEventPostToolUseFailure = "post_tool_use_failure"
	// tool group (subtype).
	HookEventPreShellExecution  = "pre_shell_execution"
	HookEventPostShellExecution = "post_shell_execution"
	HookEventPreMCPExecution    = "pre_mcp_execution"
	HookEventPostMCPExecution   = "post_mcp_execution"
	HookEventPreReadFile        = "pre_read_file"
	HookEventPostFileEdit       = "post_file_edit"
	// permission group.
	HookEventPermissionRequest = "permission_request"
	HookEventPermissionDenied  = "permission_denied"
	// subagent group.
	HookEventSubagentStart = "subagent_start"
	HookEventSubagentStop  = "subagent_stop"
	// turn group.
	HookEventStop = "stop"
	// compaction group.
	HookEventPreCompact  = "pre_compact"
	HookEventPostCompact = "post_compact"
	// notification group.
	HookEventNotification = "notification"
)

// canonicalHookEvents is the ordered §4.3.5 taxonomy. The order matches
// the spec table so CanonicalHookEvents() and lint messages list events
// in a stable, document-aligned sequence.
var canonicalHookEvents = []string{
	HookEventSessionStart, HookEventSessionEnd,
	HookEventUserPromptSubmit,
	HookEventPreToolUse, HookEventPostToolUse, HookEventPostToolUseFailure,
	HookEventPreShellExecution, HookEventPostShellExecution,
	HookEventPreMCPExecution, HookEventPostMCPExecution,
	HookEventPreReadFile, HookEventPostFileEdit,
	HookEventPermissionRequest, HookEventPermissionDenied,
	HookEventSubagentStart, HookEventSubagentStop,
	HookEventStop, HookEventPreCompact, HookEventPostCompact,
	HookEventNotification,
}

// canonicalHookEventSet indexes canonicalHookEvents for O(1) membership.
var canonicalHookEventSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(canonicalHookEvents))
	for _, e := range canonicalHookEvents {
		m[e] = struct{}{}
	}
	return m
}()

// CanonicalHookEvents returns the §4.3.5 canonical hook event names in
// spec-table order. The returned slice is a copy the caller may modify.
func CanonicalHookEvents() []string {
	return append([]string(nil), canonicalHookEvents...)
}

// IsCanonicalHookEvent reports whether event is one of the §4.3.5
// canonical hook event names.
func IsCanonicalHookEvent(event string) bool {
	_, ok := canonicalHookEventSet[event]
	return ok
}
