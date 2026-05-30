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
	// cursor is ✗ for sandbox_profile.
	diags := capDiags(t, "---\ntype: agent\nversion: 1.0.0\nname: a\ndescription: d.\nsandbox_profile: read-only-fs\ntarget_harnesses: [cursor]\n---\n\nbody\n")
	if !hasErrorMessage(diags, capCode, "cannot translate") {
		t.Errorf("expected a capability error for cursor + sandbox_profile, got: %v", diags)
	}
	if !hasErrorMessage(diags, capCode, "cursor") {
		t.Errorf("error should name the harness cursor, got: %v", diags)
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
	// claude-code is ⚠ for rule_mode: glob.
	diags := capDiags(t, "---\ntype: rule\nversion: 1.0.0\nname: r\ndescription: d.\nrule_mode: glob\nrule_globs: \"src/**\"\ntarget_harnesses: [claude-code]\n---\n\nbody\n")
	if hasErrorMessage(diags, capCode, "") {
		t.Errorf("a ⚠ cell must not error, got: %v", diags)
	}
	if !hasWarnMessage(diags, capCode, "falls back") {
		t.Errorf("expected a fallback warning for claude-code + glob, got: %v", diags)
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

// Spec: §6.7.1 — gemini is ⚠ for rule_mode: always; targeting it warns.
func TestHarnessCapability_AlwaysGeminiWarns(t *testing.T) {
	t.Parallel()
	diags := capDiags(t, "---\ntype: rule\nversion: 1.0.0\nname: r\ndescription: d.\nrule_mode: always\ntarget_harnesses: [gemini]\n---\n\nbody\n")
	if hasErrorMessage(diags, capCode, "") {
		t.Errorf("always on gemini is ⚠, must not error: %v", diags)
	}
	if !hasWarnMessage(diags, capCode, "falls back") {
		t.Errorf("expected a fallback warning for gemini + always, got: %v", diags)
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
