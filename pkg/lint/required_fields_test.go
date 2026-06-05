package lint

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// hasErrorMessage reports whether some error-severity diagnostic with code
// contains substr in its message.
func hasErrorMessage(diags []Diagnostic, code, substr string) bool {
	for _, d := range diags {
		if d.Code == code && d.Severity == SeverityError && strings.Contains(d.Message, substr) {
			return true
		}
	}
	return false
}

func hasWarnMessage(diags []Diagnostic, code, substr string) bool {
	for _, d := range diags {
		if d.Code == code && d.Severity == SeverityWarning && strings.Contains(d.Message, substr) {
			return true
		}
	}
	return false
}

// spec: §4.3 rule_mode table — rule_globs is required when rule_mode: glob.
func TestLint_RuleGlobMissingGlobsErrors(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, testharness.WriteTreeOption{
		Path:    "style/react/ARTIFACT.md",
		Content: "---\ntype: rule\nversion: 1.0.0\nrule_mode: glob\n---\n\nrule body\n",
	})
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if !hasErrorMessage(diags, "lint.required_field_missing", "rule_globs") {
		t.Errorf("expected a required_field_missing error for rule_globs, got: %v", diags)
	}
}

// spec: §4.3 rule_mode table — a glob rule that supplies rule_globs is clean.
func TestLint_RuleGlobWithGlobsClean(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, testharness.WriteTreeOption{
		Path:    "style/react/ARTIFACT.md",
		Content: "---\ntype: rule\nversion: 1.0.0\nrule_mode: glob\nrule_globs: \"src/**/*.tsx\"\n---\n\nrule body\n",
	})
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if hasErrorMessage(diags, "lint.required_field_missing", "rule_globs") {
		t.Errorf("glob rule with rule_globs must not error: %v", diags)
	}
}

// spec: §4.3 rule_mode table — rule_description is required when
// rule_mode: auto.
func TestLint_RuleAutoMissingDescriptionErrors(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, testharness.WriteTreeOption{
		Path:    "rules/db/ARTIFACT.md",
		Content: "---\ntype: rule\nversion: 1.0.0\nrule_mode: auto\n---\n\nrule body\n",
	})
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if !hasErrorMessage(diags, "lint.required_field_missing", "rule_description") {
		t.Errorf("expected a required_field_missing error for rule_description, got: %v", diags)
	}
}

// spec: §4.3 hook schema — hook_event and hook_action are the defining fields
// of a type: hook artifact; missing either is an ingest error.
func TestLint_HookMissingEventAndActionErrors(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, testharness.WriteTreeOption{
		Path:    "hooks/bare/ARTIFACT.md",
		Content: "---\ntype: hook\nversion: 1.0.0\ndescription: a hook\n---\n\nbody\n",
	})
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if !hasErrorMessage(diags, "lint.required_field_missing", "hook_event") {
		t.Errorf("expected required_field_missing for hook_event, got: %v", diags)
	}
	if !hasErrorMessage(diags, "lint.required_field_missing", "hook_action") {
		t.Errorf("expected required_field_missing for hook_action, got: %v", diags)
	}
}

// spec: §4.3 hook schema — a complete hook lints clean.
func TestLint_HookCompleteClean(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, testharness.WriteTreeOption{
		Path:    "hooks/stop/ARTIFACT.md",
		Content: "---\ntype: hook\nversion: 1.0.0\ndescription: a hook\nhook_event: stop\nhook_action: |\n  echo hi\n---\n\nbody\n",
	})
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	for _, d := range diags {
		if d.Severity == SeverityError {
			t.Errorf("complete hook must lint clean, got error: %v", d)
		}
	}
}

// spec: §4.3 / docs rule-modes "Lint behavior" — rule_mode: glob with a
// rule_description set warns that rule_description is ignored.
func TestLint_RuleGlobWithDescriptionWarns(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, testharness.WriteTreeOption{
		Path: "style/react/ARTIFACT.md",
		Content: "---\ntype: rule\nversion: 1.0.0\nrule_mode: glob\nrule_globs: \"src/**/*.tsx\"\n" +
			"rule_description: ignored here\n---\n\nrule body\n",
	})
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if !hasWarnMessage(diags, "lint.ignored_companion_field", "rule-description is ignored") {
		t.Errorf("expected an ignored_companion_field warning, got: %v", diags)
	}
}

// spec: §4.3 / docs rule-modes "Lint behavior" — rule_mode: auto with
// rule_globs set warns that rule_globs is ignored.
func TestLint_RuleAutoWithGlobsWarns(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, testharness.WriteTreeOption{
		Path: "rules/db/ARTIFACT.md",
		Content: "---\ntype: rule\nversion: 1.0.0\nrule_mode: auto\nrule_description: when migrating\n" +
			"rule_globs: \"src/**\"\n---\n\nrule body\n",
	})
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if !hasWarnMessage(diags, "lint.ignored_companion_field", "rule-globs is ignored") {
		t.Errorf("expected an ignored_companion_field warning, got: %v", diags)
	}
}

// spec: §4.3 — rule_mode is a type: rule field; setting it on another type
// warns that the field has no effect.
func TestLint_RuleModeOnNonRuleWarns(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, testharness.WriteTreeOption{
		Path:    "ctx/note/ARTIFACT.md",
		Content: "---\ntype: context\nversion: 1.0.0\nrule_mode: glob\n---\n\nbody\n",
	})
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if !hasWarnMessage(diags, "lint.rule_mode_on_non_rule", "only applicable to type: rule") {
		t.Errorf("expected a rule_mode_on_non_rule warning, got: %v", diags)
	}
	// rule_globs is not required when the rule_mode is ignored anyway.
	if hasErrorMessage(diags, "lint.required_field_missing", "rule_globs") {
		t.Errorf("a non-rule must not be required to carry rule_globs: %v", diags)
	}
}

// spec: §4.3 — an always-mode rule with no companion fields lints clean
// (no required-field error, no hygiene warning).
func TestLint_RuleAlwaysClean(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, testharness.WriteTreeOption{
		Path:    "style/house/ARTIFACT.md",
		Content: "---\ntype: rule\nversion: 1.0.0\nrule_mode: always\n---\n\nrule body\n",
	})
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	for _, d := range diags {
		switch d.Code {
		case "lint.required_field_missing", "lint.ignored_companion_field", "lint.rule_mode_on_non_rule":
			t.Errorf("always-mode rule must lint clean, got: %v", d)
		}
	}
}

// rmCanonicalDiags runs only the §4.3 rule_mode enum rule over a single
// artifact so the assertions are not perturbed by the other rules.
func rmCanonicalDiags(t *testing.T, content string) []Diagnostic {
	t.Helper()
	reg, records := openFixture(t, testharness.WriteTreeOption{
		Path:    "rules/x/ARTIFACT.md",
		Content: content,
	})
	return (&Linter{Rules: []Rule{ruleRuleModeCanonical{}}}).Lint(context.Background(), reg, records)
}

// spec: 04-artifact-model.md §4.3 — rule_mode is constrained to the closed
// enumeration always | glob | auto | explicit. An out-of-enum value on a
// type: rule artifact is an ingest error.
func TestLint_RuleModeOutOfEnumErrors(t *testing.T) {
	t.Parallel()
	diags := rmCanonicalDiags(t, "---\ntype: rule\nversion: 1.0.0\nrule_mode: sometimes\n---\n\nbody\n")
	if !hasErrorMessage(diags, "lint.unknown_rule_mode", "sometimes") {
		t.Errorf("expected an unknown_rule_mode error naming the bad value, got: %v", diags)
	}
	if !hasErrorMessage(diags, "lint.unknown_rule_mode", "always, glob, auto, explicit") {
		t.Errorf("error should list the canonical modes, got: %v", diags)
	}
}

// spec: §4.3 — each canonical rule_mode value passes the enum rule.
func TestLint_RuleModeCanonicalClean(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"always", "glob", "auto", "explicit"} {
		diags := rmCanonicalDiags(t, "---\ntype: rule\nversion: 1.0.0\nrule_mode: "+mode+"\n---\n\nbody\n")
		if hasCode(diags, "lint.unknown_rule_mode") {
			t.Errorf("canonical rule_mode %q must not error: %v", mode, diags)
		}
	}
}

// spec: §4.3 — an unset rule_mode defaults to always at materialization, so
// the enum rule leaves it clean rather than treating "" as out of enum.
func TestLint_RuleModeUnsetClean(t *testing.T) {
	t.Parallel()
	diags := rmCanonicalDiags(t, "---\ntype: rule\nversion: 1.0.0\n---\n\nbody\n")
	if hasCode(diags, "lint.unknown_rule_mode") {
		t.Errorf("an unset rule_mode must not error (defaults to always): %v", diags)
	}
}

// spec: §4.3 — the enum rule scopes to type: rule (mirroring the hook_event
// canonical rule). A non-rule artifact's stray rule_mode is covered by the
// rule_mode_on_non_rule hygiene warning, not by this enum error.
func TestLint_RuleModeEnumScopedToRule(t *testing.T) {
	t.Parallel()
	diags := rmCanonicalDiags(t, "---\ntype: context\nversion: 1.0.0\nrule_mode: sometimes\n---\n\nbody\n")
	if hasCode(diags, "lint.unknown_rule_mode") {
		t.Errorf("the enum rule must only apply to type: rule, got: %v", diags)
	}
}

// The enum rule ships in the default rule set so both `podium lint` and
// ingest enforce it.
func TestLint_RuleModeCanonicalRegistered(t *testing.T) {
	t.Parallel()
	for _, r := range AllRules() {
		if r.Code() == "lint.unknown_rule_mode" {
			return
		}
	}
	t.Errorf("ruleRuleModeCanonical is not registered in AllRules()")
}
