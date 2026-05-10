package adapter

import (
	"testing"
)

// runRuleModeCell tests one (mode, adapter) cell of the §4.3 rule_mode
// matrix: a rule artifact with the named mode produces output through
// the adapter.
//
// For ✗ cells the spec mandates ingest rejection unless target_harnesses
// excludes the harness; that's a lint-side check and lives in pkg/lint.
// At the adapter level we only verify that producing output is feasible
// when ingest does not reject — i.e., the adapter does not panic and
// produces a non-empty file set for ✓ and ⚠ cells.
func runRuleModeCell(t *testing.T, adapterID, mode string) {
	t.Helper()
	r := DefaultRegistry()
	a, err := r.Get(adapterID)
	if err != nil {
		t.Fatalf("Get(%q): %v", adapterID, err)
	}
	src := Source{
		ArtifactID: "rules/" + mode,
		ArtifactBytes: []byte("---\ntype: rule\nversion: 1.0.0\n" +
			"description: a rule\n" +
			"rule_mode: " + mode + "\n" +
			"---\n\nrule body\n"),
	}
	out, err := a.Adapt(src)
	if err != nil {
		t.Fatalf("%s rule_mode=%s: %v", adapterID, mode, err)
	}
	if len(out) == 0 {
		t.Errorf("%s rule_mode=%s: produced no output", adapterID, mode)
	}
}

// Spec: §4.3 rule_mode × harness — every (mode, adapter) cell must
// either produce output (✓ / ⚠) or be reject-eligible (✗) at lint. The
// adapter-level test verifies the production half: even ⚠ and (when
// reachable) ✗ cells produce a fallback rather than panicking.
// Matrix: §4.3 (always, claude-desktop)
// Matrix: §4.3 (always, claude-cowork)
// Matrix: §4.3 (always, cursor)
// Matrix: §4.3 (always, opencode)
// Matrix: §4.3 (always, gemini)
// Matrix: §4.3 (always, pi)
// Matrix: §4.3 (always, hermes)
func TestRuleMode_Always(t *testing.T) {
	t.Parallel()
	for _, id := range []string{
		"claude-desktop", "claude-cowork", "cursor", "opencode",
		"gemini", "pi", "hermes",
	} {
		runRuleModeCell(t, id, "always")
	}
}

// Spec: §4.3 rule_mode: glob — ⚠ on claude-code / claude-cowork /
// codex / opencode / pi, ✓ on cursor / hermes, ✗ on claude-desktop /
// gemini per §6.7.1.
// Matrix: §4.3 (glob, claude-code)
// Matrix: §4.3 (glob, claude-desktop)
// Matrix: §4.3 (glob, claude-cowork)
// Matrix: §4.3 (glob, cursor)
// Matrix: §4.3 (glob, codex)
// Matrix: §4.3 (glob, opencode)
// Matrix: §4.3 (glob, gemini)
// Matrix: §4.3 (glob, pi)
// Matrix: §4.3 (glob, hermes)
func TestRuleMode_Glob(t *testing.T) {
	t.Parallel()
	for _, id := range firstClassAdapters {
		runRuleModeCell(t, id, "glob")
	}
}

// Spec: §4.3 rule_mode: auto — ⚠ on most, ✓ on cursor, ✗ on
// codex / opencode / gemini / pi.
// Matrix: §4.3 (auto, claude-code)
// Matrix: §4.3 (auto, claude-desktop)
// Matrix: §4.3 (auto, claude-cowork)
// Matrix: §4.3 (auto, cursor)
// Matrix: §4.3 (auto, codex)
// Matrix: §4.3 (auto, opencode)
// Matrix: §4.3 (auto, gemini)
// Matrix: §4.3 (auto, pi)
// Matrix: §4.3 (auto, hermes)
func TestRuleMode_Auto(t *testing.T) {
	t.Parallel()
	for _, id := range firstClassAdapters {
		runRuleModeCell(t, id, "auto")
	}
}

// Spec: §4.3 rule_mode: explicit — ✓ on claude-desktop, claude-cowork,
// gemini (⚠), claude-code, codex, and the rule-aware adapters covered
// elsewhere by TestRuleAdapters_PlaceUnderNativeRulesDir.
// Matrix: §4.3 (explicit, claude-code)
// Matrix: §4.3 (explicit, claude-desktop)
// Matrix: §4.3 (explicit, claude-cowork)
// Matrix: §4.3 (explicit, codex)
// Matrix: §4.3 (explicit, gemini)
func TestRuleMode_Explicit(t *testing.T) {
	t.Parallel()
	for _, id := range []string{
		"claude-code", "claude-desktop", "claude-cowork",
		"codex", "gemini",
	} {
		runRuleModeCell(t, id, "explicit")
	}
}
