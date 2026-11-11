package lint

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/manifest"
)

// skillTree builds a (ARTIFACT.md, SKILL.md) fixture pair for a skill named
// hello under greetings/hello, with caller-supplied frontmatter for each file.
func skillTree(artifactFM, skillFM string) []testharness.WriteTreeOption {
	return []testharness.WriteTreeOption{
		{Path: "greetings/hello/ARTIFACT.md", Content: "---\n" + artifactFM + "---\n\n"},
		{Path: "greetings/hello/SKILL.md", Content: "---\n" + skillFM + "---\n\nbody\n"},
	}
}

// plainSkillTree builds a SKILL.md-only fixture for a skill named hello under
// greetings/hello, with caller-supplied SKILL.md frontmatter.
func plainSkillTree(skillFM string) []testharness.WriteTreeOption {
	return []testharness.WriteTreeOption{
		{Path: "greetings/hello/SKILL.md", Content: "---\n" + skillFM + "---\n\nbody\n"},
	}
}

// countCode returns the number of diagnostics carrying code.
func countCode(diags []Diagnostic, code string) int {
	n := 0
	for _, d := range diags {
		if d.Code == code {
			n++
		}
	}
	return n
}

// hasAnySeverity reports whether any diagnostic carries severity sev.
func hasAnySeverity(diags []Diagnostic, sev Severity) bool {
	for _, d := range diags {
		if d.Severity == sev {
			return true
		}
	}
	return false
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

// Spec: §4.3.4 — "A `version` in `ARTIFACT.md` equals `SKILL.md`
// `metadata.version` when both are set and `metadata.version` is a
// `major.minor.patch` value (error on mismatch)."
func TestLint_SkillVersionMismatchErrors(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, skillTree(
		"type: skill\nversion: 1.0.0\n",
		"name: hello\ndescription: Greet the user.\nmetadata:\n  version: \"2.0.0\"\n",
	)...)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	var found bool
	for _, d := range diags {
		if d.Code == "lint.skill_artifact_field_mismatch" && d.Severity == SeverityError &&
			strings.Contains(d.Message, `"1.0.0"`) && strings.Contains(d.Message, `"2.0.0"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a version mismatch error naming both values, got: %v", diags)
	}
}

// Spec: §4.3.4 — a metadata.version outside major.minor.patch is ignored, so
// it neither mismatches the ARTIFACT.md version nor leaves the version
// assigned.
func TestLint_SkillVersionIgnoredMetadataBesideArtifactVersion(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, skillTree(
		"type: skill\nversion: 1.0.0\n",
		"name: hello\ndescription: Greet the user.\nmetadata:\n  version: \"1.0\"\n",
	)...)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if hasCode(diags, "lint.skill_artifact_field_mismatch") || hasCode(diags, "lint.skill_version_assigned") {
		t.Errorf("an ignored metadata.version must draw no mismatch or assigned-version diagnostic: %v", diags)
	}
}

// Spec: §4.3.4 — "A skill that declares no usable version has its version
// assigned (warning that names any ignored `metadata.version` value and the
// fix)."
func TestLint_SkillVersionAssignedNamesIgnoredValue(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, plainSkillTree(
		"name: hello\ndescription: Greet the user.\nmetadata:\n  version: \"1.0\"\n",
	)...)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if n := countCode(diags, "lint.skill_version_assigned"); n != 1 {
		t.Fatalf("want exactly one lint.skill_version_assigned, got %d: %v", n, diags)
	}
	for _, d := range diags {
		if d.Code != "lint.skill_version_assigned" {
			continue
		}
		if d.Severity != SeverityWarning {
			t.Errorf("severity = %s, want warning", d.Severity)
		}
		for _, want := range []string{`metadata.version "1.0" is not major.minor.patch and is ignored`, "0.0.0-auto.<N>", "Set metadata.version in SKILL.md"} {
			if !strings.Contains(d.Message, want) {
				t.Errorf("message %q missing %q", d.Message, want)
			}
		}
	}
	if hasAnySeverity(diags, SeverityError) {
		t.Errorf("a plain skill with an ignored metadata.version must lint without error: %v", diags)
	}
}

// Spec: §4.3.4 — a plain skill with no metadata.version warns without the
// ignored-value prefix.
func TestLint_SkillVersionAssignedWithoutMetadata(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, plainSkillTree("name: hello\ndescription: Greet the user.\n")...)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if n := countCode(diags, "lint.skill_version_assigned"); n != 1 {
		t.Fatalf("want exactly one lint.skill_version_assigned, got %d: %v", n, diags)
	}
	for _, d := range diags {
		if d.Code == "lint.skill_version_assigned" && strings.Contains(d.Message, "is ignored") {
			t.Errorf("no metadata.version was set, so the message must not name an ignored value: %q", d.Message)
		}
	}
}

// Spec: §4.3.4 — a plain skill with a major.minor.patch metadata.version
// declares its version and draws no assigned-version warning.
func TestLint_SkillMetadataVersionDeclaresVersion(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, plainSkillTree(
		"name: hello\ndescription: Greet the user.\nmetadata:\n  version: \"1.2.3\"\n",
	)...)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if hasCode(diags, "lint.skill_version_assigned") || hasAnySeverity(diags, SeverityError) {
		t.Errorf("a declared metadata.version must lint clean of the warning and errors: %v", diags)
	}
}

// Spec: §4.3.4 — "An authored version in the reserved form of §4.7.6, in
// either file, is an error (`lint.invalid_version`)." This row covers
// SKILL.md metadata.version.
func TestLint_SkillReservedMetadataVersionErrors(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, plainSkillTree(
		"name: hello\ndescription: Greet the user.\nmetadata:\n  version: \"0.0.0-auto.1\"\n",
	)...)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	var found bool
	for _, d := range diags {
		if d.Code == "lint.invalid_version" && d.Severity == SeverityError &&
			strings.Contains(d.Message, `"0.0.0-auto.1"`) && strings.Contains(d.Message, "registry-reserved") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a reserved-form lint.invalid_version error, got: %v", diags)
	}
}

// Spec: §4.3.4 — the reserved form in the ARTIFACT.md version is an error.
func TestLint_SkillReservedArtifactVersionErrors(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, skillTree(
		"type: skill\nversion: 0.0.0-auto.1\n",
		"name: hello\ndescription: Greet the user.\n",
	)...)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if !hasSeverity(diags, "lint.invalid_version", SeverityError) {
		t.Fatalf("expected lint.invalid_version for a reserved ARTIFACT.md version, got: %v", diags)
	}
	if hasCode(diags, "lint.skill_version_assigned") {
		t.Errorf("an authored ARTIFACT.md version is not assigned: %v", diags)
	}
}

// Spec: §4.3.4 — a plain skill served with the synthesized ARTIFACT.md draws
// none of the ARTIFACT.md overlay diagnostics, because the document carries
// only type and version and a single HTML comment body.
func TestLint_SkillSynthesizedDocumentLintsClean(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, plainSkillTree(
		"name: hello\ndescription: Greet the user.\nmetadata:\n  version: \"1.0.0\"\n",
	)...)
	doc, err := manifest.SynthesizedSkillDocument("1.0.0")
	if err != nil {
		t.Fatalf("SynthesizedSkillDocument: %v", err)
	}
	records[0].ArtifactBytes = doc
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	for _, code := range []string{"lint.skill_artifact_field", "lint.skill_artifact_field_mismatch", "lint.skill_artifact_body"} {
		if hasCode(diags, code) {
			t.Errorf("synthesized document must not draw %s: %v", code, diags)
		}
	}
}

// Spec: §4.3.4 — "The diagnostic recommends `metadata.version` for a
// version and the `ARTIFACT.md` overlay for every other field."
func TestLint_SkillPodiumOnlyVersionRecommendsMetadataVersion(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, skillTree(
		"type: skill\nversion: 1.0.0\n",
		"name: hello\ndescription: Greet the user.\nversion: 1.0.0\ntags: [demo]\n",
	)...)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	var sawVersion, sawTags bool
	for _, d := range diags {
		if d.Code != "lint.skill_podium_only_field" || d.Severity != SeverityError {
			continue
		}
		switch {
		case strings.Contains(d.Message, `"version"`):
			sawVersion = strings.Contains(d.Message, "metadata.version")
		case strings.Contains(d.Message, `"tags"`):
			sawTags = strings.Contains(d.Message, "ARTIFACT.md")
		}
	}
	if !sawVersion {
		t.Errorf("expected the version diagnostic to recommend metadata.version: %v", diags)
	}
	if !sawTags {
		t.Errorf("expected the tags diagnostic to recommend ARTIFACT.md: %v", diags)
	}
}

// Spec: §4.3.4, §4.6 — extends is no Podium manifest field, so a top-level
// extends key in SKILL.md is an unknown agentskills.io key rather than a
// Podium-only field, with or without an ARTIFACT.md overlay.
func TestLint_SkillMDExtendsKeyIsNotPodiumOnly(t *testing.T) {
	t.Parallel()
	const skillFM = "name: hello\ndescription: Greet the user.\nextends: other/skill\n"
	trees := map[string][]testharness.WriteTreeOption{
		"with ARTIFACT.md": skillTree("type: skill\nversion: 1.0.0\n", skillFM),
		"plain":            plainSkillTree(skillFM),
	}
	for name, tree := range trees {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			reg, records := openFixture(t, tree...)
			diags := (&Linter{}).Lint(context.Background(), reg, records)
			if n := countCode(diags, "lint.skill_podium_only_field"); n != 0 {
				t.Errorf("SKILL.md extends drew %d lint.skill_podium_only_field diagnostics: %v", n, diags)
			}
		})
	}
}
