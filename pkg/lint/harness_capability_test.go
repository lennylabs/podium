package lint

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

const capCode = "lint.harness_capability"

// capDiags runs only the §6.7.1 capability rule over a single artifact so
// the assertions are not perturbed by the other rules.
func capDiags(t *testing.T, content string) []Diagnostic {
	t.Helper()
	reg, records := openFixture(t, testharness.WriteTreeOption{
		Path:    "team/art/ARTIFACT.md",
		Content: content,
	})
	return (&Linter{Rules: []Rule{ruleHarnessCapability{}}}).Lint(context.Background(), reg, records)
}

// Spec: §6.7.1 / §4.3.5 — an artifact without target_harnesses declares no
// harness set and draws no capability diagnostic, even when it uses fields
// that are ✗ on some harness. The doc-derived suite lints such artifacts
// clean (T-D-artifact-types-37/38, T-D-first-agent-2).
func TestHarnessCapability_NoTargetHarnessesSilent(t *testing.T) {
	t.Parallel()
	cases := []string{
		"---\ntype: rule\nversion: 1.0.0\nname: r\ndescription: d.\nrule_mode: auto\nrule_description: when migrating\n---\n\nbody\n",
		"---\ntype: agent\nversion: 1.0.0\nname: a\ndescription: d.\nsandbox_profile: read-only-fs\n---\n\nbody\n",
		"---\ntype: agent\nversion: 1.0.0\nname: a\ndescription: d.\ndelegates_to:\n  - x/y\n---\n\nbody\n",
		"---\ntype: hook\nversion: 1.0.0\nname: h\ndescription: d.\nhook_event: stop\nhook_action: |\n  echo hi\n---\n\nbody\n",
	}
	for _, c := range cases {
		if diags := capDiags(t, c); hasCode(diags, capCode) {
			t.Errorf("expected no capability diagnostic without target_harnesses, got: %v\nartifact:\n%s", diags, c)
		}
	}
}

// Spec: §6.7.1 / §4.3.5 — declaring target_harnesses that names a harness
// whose cell is ✗ for a used field is an ingest error.
func TestHarnessCapability_TargetUnsupportedErrors(t *testing.T) {
	t.Parallel()
	// codex is ✗ for sandbox_profile (the TOML agent translation drops it).
	diags := capDiags(t, "---\ntype: agent\nversion: 1.0.0\nname: a\ndescription: d.\nsandbox_profile: read-only-fs\ntarget_harnesses: [codex]\n---\n\nbody\n")
	if !hasErrorMessage(diags, capCode, "cannot translate") {
		t.Errorf("expected a capability error for codex + sandbox_profile, got: %v", diags)
	}
	if !hasErrorMessage(diags, capCode, "codex") {
		t.Errorf("error should name the harness codex, got: %v", diags)
	}
}

// Spec: §6.7.1 — a glob rule that targets claude-desktop (✗) errors.
func TestHarnessCapability_GlobTargetingClaudeDesktopErrors(t *testing.T) {
	t.Parallel()
	diags := capDiags(t, "---\ntype: rule\nversion: 1.0.0\nname: r\ndescription: d.\nrule_mode: glob\nrule_globs: \"src/**\"\ntarget_harnesses: [claude-desktop]\n---\n\nbody\n")
	if !hasErrorMessage(diags, capCode, "rule_mode: glob") {
		t.Errorf("expected a capability error naming rule_mode: glob, got: %v", diags)
	}
}

// Spec: §6.7.1 — a ⚠ cell on a targeted harness is a warning, not an error.
func TestHarnessCapability_FallbackWarns(t *testing.T) {
	t.Parallel()
	// codex is ⚠ for rule_mode: glob (the AGENTS.md inject loses per-file
	// scoping). claude-code is ✓ for glob via the native `paths:` list.
	diags := capDiags(t, "---\ntype: rule\nversion: 1.0.0\nname: r\ndescription: d.\nrule_mode: glob\nrule_globs: \"src/**\"\ntarget_harnesses: [codex]\n---\n\nbody\n")
	if hasErrorMessage(diags, capCode, "") {
		t.Errorf("a ⚠ cell must not error, got: %v", diags)
	}
	if !hasWarnMessage(diags, capCode, "falls back") {
		t.Errorf("expected a fallback warning for codex + glob, got: %v", diags)
	}
}

// Spec: §6.7.1 — a ✓ cell on the targeted harness is clean. cursor is the
// rule-native harness (✓ for glob).
func TestHarnessCapability_NativeClean(t *testing.T) {
	t.Parallel()
	diags := capDiags(t, "---\ntype: rule\nversion: 1.0.0\nname: r\ndescription: d.\nrule_mode: glob\nrule_globs: \"src/**\"\ntarget_harnesses: [cursor]\n---\n\nbody\n")
	if hasCode(diags, capCode) {
		t.Errorf("cursor ✓ for glob must be clean, got: %v", diags)
	}
}

// Spec: §6.7.1 — gemini is ⚠ for rule_mode: glob (the GEMINI.md inject loses
// glob scoping); targeting it warns.
func TestHarnessCapability_GlobGeminiWarns(t *testing.T) {
	t.Parallel()
	diags := capDiags(t, "---\ntype: rule\nversion: 1.0.0\nname: r\ndescription: d.\nrule_mode: glob\nrule_globs: \"src/**\"\ntarget_harnesses: [gemini]\n---\n\nbody\n")
	if hasErrorMessage(diags, capCode, "") {
		t.Errorf("glob on gemini is ⚠, must not error: %v", diags)
	}
	if !hasWarnMessage(diags, capCode, "falls back") {
		t.Errorf("expected a fallback warning for gemini + glob, got: %v", diags)
	}
}

// Spec: §6.7.1 — the mcpServers field is graded ✗ for codex (the TOML agent
// translation drops it). An agent that declares mcpServers and targets codex
// is an ingest error. Before the fix the lint never evaluated the mcpServers
// row, so this combination passed silently and the field was dropped.
func TestHarnessCapability_MCPServersTargetingCodexErrors(t *testing.T) {
	t.Parallel()
	diags := capDiags(t, "---\ntype: agent\nversion: 1.0.0\nname: a\ndescription: d.\nmcpServers:\n  - name: finance-warehouse\n    command: npx\ntarget_harnesses: [codex]\n---\n\nbody\n")
	if !hasErrorMessage(diags, capCode, "mcpServers") {
		t.Errorf("expected a capability error naming mcpServers for codex, got: %v", diags)
	}
	if !hasErrorMessage(diags, capCode, "codex") {
		t.Errorf("error should name the harness codex, got: %v", diags)
	}
}

// Spec: §6.7.1 — mcpServers is ✓ for claude-code (the pass-through .md agent
// preserves it), so targeting claude-code is clean.
func TestHarnessCapability_MCPServersTargetingClaudeCodeClean(t *testing.T) {
	t.Parallel()
	diags := capDiags(t, "---\ntype: agent\nversion: 1.0.0\nname: a\ndescription: d.\nmcpServers:\n  - name: finance-warehouse\n    command: npx\ntarget_harnesses: [claude-code]\n---\n\nbody\n")
	if hasCode(diags, capCode) {
		t.Errorf("claude-code ✓ for mcpServers must be clean, got: %v", diags)
	}
}

// The capability rule ships in the default rule set so both `podium lint`
// and ingest enforce it (§6.7.1 "ingest-time lint").
func TestHarnessCapability_RegisteredInAllRules(t *testing.T) {
	t.Parallel()
	for _, r := range AllRules() {
		if r.Code() == capCode {
			return
		}
	}
	t.Errorf("ruleHarnessCapability is not registered in AllRules()")
}
