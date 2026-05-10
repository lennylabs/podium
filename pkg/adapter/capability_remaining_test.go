package adapter

import (
	"strings"
	"testing"
)

// runFieldCell tests one (field, adapter) cell of the §6.7.1
// capability matrix by building an artifact that sets the field and
// asserting the adapter does not reject it. The marker stored in the
// frontmatter must be reachable through the adapter's output bytes.
func runFieldCell(t *testing.T, adapterID, frontmatter, marker string) {
	t.Helper()
	r := DefaultRegistry()
	a, err := r.Get(adapterID)
	if err != nil {
		t.Fatalf("Get(%q): %v", adapterID, err)
	}
	src := Source{
		ArtifactID:    "test/cell",
		ArtifactBytes: []byte(frontmatter),
	}
	out, err := a.Adapt(src)
	if err != nil {
		t.Fatalf("%s.Adapt: %v", adapterID, err)
	}
	if len(out) == 0 {
		t.Errorf("%s: no output for cell", adapterID)
		return
	}
	if marker != "" {
		found := false
		for _, f := range out {
			if strings.Contains(string(f.Content), marker) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: marker %q not preserved", adapterID, marker)
		}
	}
}

// Spec: §6.7.1 — delegates_to per first-class adapter.
// Matrix: §6.7.1 (claude-code, delegates_to)
// Matrix: §6.7.1 (claude-desktop, delegates_to)
// Matrix: §6.7.1 (claude-cowork, delegates_to)
// Matrix: §6.7.1 (cursor, delegates_to)
// Matrix: §6.7.1 (codex, delegates_to)
// Matrix: §6.7.1 (opencode, delegates_to)
// Matrix: §6.7.1 (gemini, delegates_to)
// Matrix: §6.7.1 (pi, delegates_to)
// Matrix: §6.7.1 (hermes, delegates_to)
func TestCapabilityMatrix_DelegatesTo(t *testing.T) {
	t.Parallel()
	const fm = "---\ntype: agent\nversion: 1.0.0\n" +
		"delegates_to:\n  - finance/sub-agent@1.x\n---\n\nbody\n"
	for _, id := range firstClassAdapters {
		runFieldCell(t, id, fm, "finance/sub-agent")
	}
}

// Spec: §6.7.1 — requiresApproval per adapter.
// Matrix: §6.7.1 (claude-code, requiresApproval)
// Matrix: §6.7.1 (claude-desktop, requiresApproval)
// Matrix: §6.7.1 (claude-cowork, requiresApproval)
// Matrix: §6.7.1 (cursor, requiresApproval)
// Matrix: §6.7.1 (codex, requiresApproval)
// Matrix: §6.7.1 (opencode, requiresApproval)
// Matrix: §6.7.1 (gemini, requiresApproval)
// Matrix: §6.7.1 (pi, requiresApproval)
// Matrix: §6.7.1 (hermes, requiresApproval)
func TestCapabilityMatrix_RequiresApproval(t *testing.T) {
	t.Parallel()
	const fm = "---\ntype: agent\nversion: 1.0.0\n" +
		"requiresApproval:\n  - tool: payment-submit\n    reason: irreversible\n---\n\nbody\n"
	for _, id := range firstClassAdapters {
		runFieldCell(t, id, fm, "payment-submit")
	}
}

// Spec: §6.7.1 — sandbox_profile per adapter.
// Matrix: §6.7.1 (claude-code, sandbox_profile)
// Matrix: §6.7.1 (claude-desktop, sandbox_profile)
// Matrix: §6.7.1 (claude-cowork, sandbox_profile)
// Matrix: §6.7.1 (cursor, sandbox_profile)
// Matrix: §6.7.1 (codex, sandbox_profile)
// Matrix: §6.7.1 (opencode, sandbox_profile)
// Matrix: §6.7.1 (gemini, sandbox_profile)
// Matrix: §6.7.1 (pi, sandbox_profile)
// Matrix: §6.7.1 (hermes, sandbox_profile)
func TestCapabilityMatrix_SandboxProfile(t *testing.T) {
	t.Parallel()
	const fm = "---\ntype: agent\nversion: 1.0.0\n" +
		"sandbox_profile: read-only-fs\n---\n\nbody\n"
	for _, id := range firstClassAdapters {
		runFieldCell(t, id, fm, "read-only-fs")
	}
}

// Spec: §6.7.1 — rule_mode: glob per adapter.
// Matrix: §6.7.1 (claude-code, rule_mode_glob)
// Matrix: §6.7.1 (claude-desktop, rule_mode_glob)
// Matrix: §6.7.1 (claude-cowork, rule_mode_glob)
// Matrix: §6.7.1 (cursor, rule_mode_glob)
// Matrix: §6.7.1 (codex, rule_mode_glob)
// Matrix: §6.7.1 (opencode, rule_mode_glob)
// Matrix: §6.7.1 (gemini, rule_mode_glob)
// Matrix: §6.7.1 (pi, rule_mode_glob)
// Matrix: §6.7.1 (hermes, rule_mode_glob)
func TestCapabilityMatrix_RuleModeGlob(t *testing.T) {
	t.Parallel()
	const fm = "---\ntype: rule\nversion: 1.0.0\nrule_mode: glob\n" +
		"rule_globs: src/**/*.ts\n---\n\nrules\n"
	for _, id := range firstClassAdapters {
		runFieldCell(t, id, fm, "")
	}
}

// Spec: §6.7.1 — rule_mode: auto per adapter.
// Matrix: §6.7.1 (claude-code, rule_mode_auto)
// Matrix: §6.7.1 (claude-desktop, rule_mode_auto)
// Matrix: §6.7.1 (claude-cowork, rule_mode_auto)
// Matrix: §6.7.1 (cursor, rule_mode_auto)
// Matrix: §6.7.1 (codex, rule_mode_auto)
// Matrix: §6.7.1 (opencode, rule_mode_auto)
// Matrix: §6.7.1 (gemini, rule_mode_auto)
// Matrix: §6.7.1 (pi, rule_mode_auto)
// Matrix: §6.7.1 (hermes, rule_mode_auto)
func TestCapabilityMatrix_RuleModeAuto(t *testing.T) {
	t.Parallel()
	const fm = "---\ntype: rule\nversion: 1.0.0\nrule_mode: auto\n" +
		"rule_description: Apply when migrating databases.\n---\n\nrules\n"
	for _, id := range firstClassAdapters {
		runFieldCell(t, id, fm, "")
	}
}

// Spec: §6.7.1 — hook_event per adapter (the field-level row in the
// matrix; per-event coverage for claude-code is in
// hook_events_test.go).
// Matrix: §6.7.1 (claude-code, hook_event)
// Matrix: §6.7.1 (claude-desktop, hook_event)
// Matrix: §6.7.1 (claude-cowork, hook_event)
// Matrix: §6.7.1 (cursor, hook_event)
// Matrix: §6.7.1 (codex, hook_event)
// Matrix: §6.7.1 (opencode, hook_event)
// Matrix: §6.7.1 (gemini, hook_event)
// Matrix: §6.7.1 (pi, hook_event)
// Matrix: §6.7.1 (hermes, hook_event)
func TestCapabilityMatrix_HookEvent(t *testing.T) {
	t.Parallel()
	const fm = "---\ntype: hook\nversion: 1.0.0\n" +
		"hook_event: stop\nhook_action: |\n  echo done\n---\n\n"
	for _, id := range firstClassAdapters {
		runFieldCell(t, id, fm, "stop")
	}
}
