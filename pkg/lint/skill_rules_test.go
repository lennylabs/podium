package lint

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// skillTree builds a (ARTIFACT.md, SKILL.md) fixture pair for a skill named
// hello under greetings/hello, with caller-supplied frontmatter for each file.
func skillTree(artifactFM, skillFM string) []testharness.WriteTreeOption {
	return []testharness.WriteTreeOption{
		{Path: "greetings/hello/ARTIFACT.md", Content: "---\n" + artifactFM + "---\n\n"},
		{Path: "greetings/hello/SKILL.md", Content: "---\n" + skillFM + "---\n\nbody\n"},
	}
}

// spec: §4.3.4 — "SKILL.md does not contain Podium-only fields (`type`,
// `version`, `when_to_use`, `tags`, etc.); if present, error." ParseSkill
// drops the unknown keys, so without the rule the violation is invisible.
func TestLint_SkillPodiumOnlyFieldInSkillMD(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, skillTree(
		"type: skill\nversion: 1.0.0\n",
		"name: hello\ndescription: Greet the user.\ntype: skill\nwhen_to_use:\n  - greeting\n",
	)...)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if !hasSeverity(diags, "lint.skill_podium_only_field", SeverityError) {
		t.Fatalf("expected lint.skill_podium_only_field error, got: %v", diags)
	}
	var sawType, sawWhenToUse bool
	for _, d := range diags {
		if d.Code != "lint.skill_podium_only_field" {
			continue
		}
		if strings.Contains(d.Message, `"type"`) {
			sawType = true
		}
		if strings.Contains(d.Message, "when_to_use") {
			sawWhenToUse = true
		}
	}
	if !sawType || !sawWhenToUse {
		t.Errorf("expected diagnostics naming both type and when_to_use, got: %v", diags)
	}
}

// spec: §4.3.4 — a SKILL.md confined to the agentskills.io subset draws no
// Podium-only-field diagnostic.
func TestLint_SkillCleanSkillMDNoPodiumField(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, skillTree(
		"type: skill\nversion: 1.0.0\n",
		"name: hello\ndescription: Greet the user.\nlicense: MIT\ncompatibility: Any harness.\nallowed-tools:\n  - Read\n",
	)...)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if hasCode(diags, "lint.skill_podium_only_field") {
		t.Errorf("clean SKILL.md must not flag Podium-only fields: %v", diags)
	}
}

// spec: §4.3.4 — "ARTIFACT.md does not contain `name`, `description`, or
// `license` fields (warning)"; a matching value is still redundant and warns
// but does not error.
func TestLint_SkillArtifactRedundantFieldWarns(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, skillTree(
		"type: skill\nversion: 1.0.0\nname: hello\n",
		"name: hello\ndescription: Greet the user.\n",
	)...)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if !hasSeverity(diags, "lint.skill_artifact_field", SeverityWarning) {
		t.Errorf("expected lint.skill_artifact_field warning, got: %v", diags)
	}
	if hasCode(diags, "lint.skill_artifact_field_mismatch") {
		t.Errorf("matching value must not raise a mismatch error: %v", diags)
	}
}

// spec: §4.3.4 — "if present, the values must match `SKILL.md` exactly (error
// on mismatch)."
func TestLint_SkillArtifactFieldMismatchErrors(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, skillTree(
		"type: skill\nversion: 1.0.0\ndescription: A different description.\n",
		"name: hello\ndescription: Greet the user.\n",
	)...)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if !hasSeverity(diags, "lint.skill_artifact_field_mismatch", SeverityError) {
		t.Fatalf("expected lint.skill_artifact_field_mismatch error, got: %v", diags)
	}
	// Presence still warns alongside the mismatch error.
	if !hasSeverity(diags, "lint.skill_artifact_field", SeverityWarning) {
		t.Errorf("expected the presence warning too, got: %v", diags)
	}
}

// spec: §4.3.4 — the omitted-from-ARTIFACT.md happy path draws neither the
// presence warning nor the mismatch error.
func TestLint_SkillArtifactFieldsAbsentClean(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, skillTree(
		"type: skill\nversion: 1.0.0\n",
		"name: hello\ndescription: Greet the user.\nlicense: MIT\n",
	)...)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if hasCode(diags, "lint.skill_artifact_field") || hasCode(diags, "lint.skill_artifact_field_mismatch") {
		t.Errorf("clean skill must not flag ARTIFACT.md name/description/license: %v", diags)
	}
}

// spec: §4.3.4 — the skills-ref reference check warns when the SKILL.md
// description exceeds the agentskills.io 1024-char cap.
func TestLint_SkillRefValidateDescriptionTooLong(t *testing.T) {
	t.Parallel()
	longDesc := strings.Repeat("a", SkillDescriptionMaxChars+1)
	reg, records := openFixture(t, skillTree(
		"type: skill\nversion: 1.0.0\n",
		"name: hello\ndescription: "+longDesc+"\n",
	)...)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if !hasSeverity(diags, "lint.skill_ref_validate", SeverityWarning) {
		t.Errorf("expected lint.skill_ref_validate warning for an over-long description, got: %v", diags)
	}
}

// spec: §4.3.4 — compatibility is capped at 500 chars; an over-long value
// warns via the skills-ref check.
func TestLint_SkillRefValidateCompatibilityTooLong(t *testing.T) {
	t.Parallel()
	longCompat := strings.Repeat("b", SkillCompatibilityMaxChars+1)
	reg, records := openFixture(t, skillTree(
		"type: skill\nversion: 1.0.0\n",
		"name: hello\ndescription: Greet.\ncompatibility: "+longCompat+"\n",
	)...)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	var found bool
	for _, d := range diags {
		if d.Code == "lint.skill_ref_validate" && strings.Contains(d.Message, "compatibility") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a skill_ref_validate warning naming compatibility, got: %v", diags)
	}
}

// spec: §4.3.4 — "lint suppression flag available for cases where the
// standard's validator is overly strict." lint_suppress on ARTIFACT.md
// silences the skills-ref warning.
func TestLint_SkillRefValidateSuppressed(t *testing.T) {
	t.Parallel()
	longDesc := strings.Repeat("a", SkillDescriptionMaxChars+1)
	reg, records := openFixture(t, skillTree(
		"type: skill\nversion: 1.0.0\nlint_suppress:\n  - lint.skill_ref_validate\n",
		"name: hello\ndescription: "+longDesc+"\n",
	)...)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if hasCode(diags, "lint.skill_ref_validate") {
		t.Errorf("lint_suppress must silence skill_ref_validate, got: %v", diags)
	}
}

// spec: §4.3.4 — a compliant skill within the agentskills.io limits passes
// the skills-ref check cleanly.
func TestLint_SkillRefValidateWithinLimitsClean(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, skillTree(
		"type: skill\nversion: 1.0.0\n",
		"name: hello\ndescription: Greet the user.\ncompatibility: Runs on any harness.\n",
	)...)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if hasCode(diags, "lint.skill_ref_validate") {
		t.Errorf("a within-limits skill must not warn skill_ref_validate: %v", diags)
	}
}
