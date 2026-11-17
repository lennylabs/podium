package lint

import (
	"context"
	"slices"
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
// version, the `metadata` key of the same name for a field that **Podium
// fields in `metadata`** lists, and the `ARTIFACT.md` overlay for every other
// field."
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
			sawTags = strings.Contains(d.Message, "metadata.tags")
		}
	}
	if !sawVersion {
		t.Errorf("expected the version diagnostic to recommend metadata.version: %v", diags)
	}
	if !sawTags {
		t.Errorf("expected the tags diagnostic to recommend metadata.tags: %v", diags)
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

// messagesFor returns the messages of the diagnostics carrying code at sev.
func messagesFor(diags []Diagnostic, code string, sev Severity) []string {
	var out []string
	for _, d := range diags {
		if d.Code == code && d.Severity == sev {
			out = append(out, d.Message)
		}
	}
	return out
}

// Spec: §4.3.4 — "The diagnostic recommends ... the `metadata` key of the
// same name for a field that **Podium fields in `metadata`** lists, and the
// `ARTIFACT.md` overlay for every other field."
func TestLint_SkillPodiumOnlyFieldNamesMetadataKey(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, skillTree(
		"type: skill\nversion: 1.0.0\n",
		"name: hello\ndescription: Greet the user.\ntags: demo\nsandbox_profile: read-only-fs\n",
	)...)
	msgs := messagesFor((&Linter{}).Lint(context.Background(), reg, records), "lint.skill_podium_only_field", SeverityError)
	want := []string{
		`SKILL.md contains Podium-only field "tags" at the top level; set metadata.tags in SKILL.md or move it to ARTIFACT.md`,
		`SKILL.md contains Podium-only field "sandbox_profile"; move it to ARTIFACT.md`,
	}
	for _, msg := range want {
		if !slices.Contains(msgs, msg) {
			t.Errorf("want message %q, got %q", msg, msgs)
		}
	}
	if len(msgs) != len(want) {
		t.Errorf("want %d diagnostics, got %d: %q", len(want), len(msgs), msgs)
	}
}

// Spec: §4.3.4 — "A `SKILL.md` `metadata` value for a key that **Podium
// fields in `metadata`** lists and that does not follow its encoding is
// ignored (warning that names the key, the value, and the encoding)." The
// warning carries no suppression, so a lint_suppress entry does not silence
// it.
func TestLint_SkillMetadataFieldIgnoredWarns(t *testing.T) {
	t.Parallel()
	trees := map[string][]testharness.WriteTreeOption{
		"plain": plainSkillTree(
			"name: hello\ndescription: Greet the user.\nmetadata:\n  version: \"1.0.0\"\n  sensitivity: secret\n",
		),
		"suppressed": skillTree(
			"type: skill\nversion: 1.0.0\nlint_suppress:\n  - lint.skill_metadata_field_ignored\n",
			"name: hello\ndescription: Greet the user.\nmetadata:\n  sensitivity: secret\n",
		),
	}
	for name, tree := range trees {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			reg, records := openFixture(t, tree...)
			diags := (&Linter{}).Lint(context.Background(), reg, records)
			got := messagesFor(diags, "lint.skill_metadata_field_ignored", SeverityWarning)
			want := `metadata.sensitivity "secret" is ignored: the value must be low, medium, or high`
			if len(got) != 1 || got[0] != want {
				t.Errorf("want one warning %q, got %q", want, got)
			}
			if hasAnySeverity(diags, SeverityError) {
				t.Errorf("an ignored metadata value must not error: %v", diags)
			}
			if records[0].Artifact.Sensitivity != "" {
				t.Errorf("ignored value reached the artifact: sensitivity %q", records[0].Artifact.Sensitivity)
			}
		})
	}
}

// Spec: §4.3.4 — "When `ARTIFACT.md` states a key that **Podium fields in
// `metadata`** lists and `SKILL.md` `metadata` carries a value for that key
// that follows its encoding, the two values are equal (error on mismatch).
// `tags` and `when_to_use` compare as ordered lists." Before linting, each
// case overwrites the record's artifact with the values resolved as if
// ARTIFACT.md stated nothing, so a check that compared metadata with
// rec.Artifact instead of the authored ARTIFACT.md would report no mismatch.
func TestLint_SkillMetadataMismatch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		artifactFM string
		metadata   string
		want       string
	}{
		{
			name:       "explicit deprecated false",
			artifactFM: "deprecated: false\n",
			metadata:   "  deprecated: \"true\"\n",
			want:       "ARTIFACT.md deprecated false does not match SKILL.md metadata.deprecated true",
		},
		{
			name:       "explicit empty sensitivity",
			artifactFM: "sensitivity: \"\"\n",
			metadata:   "  sensitivity: medium\n",
			want:       "ARTIFACT.md sensitivity  does not match SKILL.md metadata.sensitivity medium",
		},
		{
			name:       "explicit empty tags",
			artifactFM: "tags: []\n",
			metadata:   "  tags: \"a\"\n",
			want:       "ARTIFACT.md tags [] does not match SKILL.md metadata.tags [a]",
		},
		{
			name:       "different sensitivity",
			artifactFM: "sensitivity: high\n",
			metadata:   "  sensitivity: low\n",
			want:       "ARTIFACT.md sensitivity high does not match SKILL.md metadata.sensitivity low",
		},
		{
			name:       "tags in another order",
			artifactFM: "tags: [b, a]\n",
			metadata:   "  tags: \"a, b\"\n",
			want:       "ARTIFACT.md tags [b a] does not match SKILL.md metadata.tags [a b]",
		},
		{
			name:       "different when_to_use",
			artifactFM: "when_to_use:\n  - After close\n",
			metadata:   "  when_to_use: |\n    After close\n    On variance\n",
			want:       "ARTIFACT.md when_to_use [After close] does not match SKILL.md metadata.when_to_use [After close On variance]",
		},
		{
			name:       "different search_visibility",
			artifactFM: "search_visibility: indexed\n",
			metadata:   "  search_visibility: direct-only\n",
			want:       "ARTIFACT.md search_visibility indexed does not match SKILL.md metadata.search_visibility direct-only",
		},
		{
			name:       "different replaced_by",
			artifactFM: "replaced_by: greetings/hi\n",
			metadata:   "  replaced_by: greetings/hey\n",
			want:       "ARTIFACT.md replaced_by greetings/hi does not match SKILL.md metadata.replaced_by greetings/hey",
		},
		{
			name:       "different release_notes",
			artifactFM: "release_notes: First.\n",
			metadata:   "  release_notes: Second.\n",
			want:       "ARTIFACT.md release_notes First. does not match SKILL.md metadata.release_notes Second.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg, records := openFixture(t, skillTree(
				"type: skill\nversion: 1.0.0\n"+tc.artifactFM,
				"name: hello\ndescription: Greet the user.\nmetadata:\n"+tc.metadata,
			)...)
			manifest.ResolveSkillFields(records[0].Artifact, nil, records[0].Skill)
			diags := (&Linter{}).Lint(context.Background(), reg, records)
			got := messagesFor(diags, "lint.skill_artifact_field_mismatch", SeverityError)
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("want one mismatch %q, got %q", tc.want, got)
			}
		})
	}
}

// Spec: §4.3.4 — the metadata mismatch check errors only on a stated
// ARTIFACT.md key beside a metadata value that decodes to a different value.
// Equal values, a metadata value that does not follow its encoding, a key
// ARTIFACT.md does not state, and a plain skill draw no mismatch.
func TestLint_SkillMetadataMismatchClean(t *testing.T) {
	t.Parallel()
	trees := map[string][]testharness.WriteTreeOption{
		"equal values": skillTree(
			"type: skill\nversion: 1.0.0\ntags: [a, b]\ndeprecated: true\nsensitivity: low\nwhen_to_use:\n  - After close\n",
			"name: hello\ndescription: Greet the user.\nmetadata:\n  tags: \"a, b\"\n  deprecated: \"true\"\n  sensitivity: low\n  when_to_use: After close\n",
		),
		"invalid metadata value": skillTree(
			"type: skill\nversion: 1.0.0\nsensitivity: high\n",
			"name: hello\ndescription: Greet the user.\nmetadata:\n  sensitivity: secret\n",
		),
		"key not stated": skillTree(
			"type: skill\nversion: 1.0.0\n",
			"name: hello\ndescription: Greet the user.\nmetadata:\n  sensitivity: high\n",
		),
		"plain skill": plainSkillTree(
			"name: hello\ndescription: Greet the user.\nmetadata:\n  version: \"1.0.0\"\n  deprecated: \"true\"\n  tags: \"a\"\n",
		),
	}
	for name, tree := range trees {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			reg, records := openFixture(t, tree...)
			diags := (&Linter{}).Lint(context.Background(), reg, records)
			if hasCode(diags, "lint.skill_artifact_field_mismatch") {
				t.Errorf("unexpected mismatch: %v", diags)
			}
		})
	}
}

// Spec: §4.3.4 — the equality check compares lists element by element, so a
// list never equals a scalar and a nil list equals an empty one, and a key
// outside the metadata-eligible set has no value.
func TestSkillMetadataValuesEqual(t *testing.T) {
	t.Parallel()
	if !skillMetadataValuesEqual([]string(nil), []string{}) {
		t.Error("a nil list must equal an empty list")
	}
	if skillMetadataValuesEqual([]string{"a"}, "a") {
		t.Error("a list must not equal a string")
	}
	if got := skillMetadataFieldValue(&manifest.Artifact{}, "sandbox_profile"); got != nil {
		t.Errorf("sandbox_profile value = %v, want nil", got)
	}
}

// Spec: §4.3, §4.3.4 — an invalid invocation value resolves to any and draws
// one warning, in the file that holds it: lint.skill_invocation for an
// ARTIFACT.md value and lint.skill_metadata_field_ignored for a metadata
// value. Neither warning honors lint_suppress, and a valid ARTIFACT.md value
// draws no diagnostic. The test fails against a rule that reports the
// resolved value, one that errors, and one that warns on every set value.
func TestLint_SkillInvocationInvalid(t *testing.T) {
	t.Parallel()
	const (
		skillMD     = "name: hello\ndescription: Greet the user.\n"
		invokeMsg   = `invocation "yes" is not a valid value; valid values: any, user. The skill resolves to any`
		metadataMsg = `metadata.invocation "User" is ignored: the value must be any or user`
	)
	cases := []struct {
		name         string
		tree         []testharness.WriteTreeOption
		wantInvoke   []string
		wantMetadata []string
		wantUser     bool
	}{
		{
			name:       "artifact value",
			tree:       skillTree("type: skill\nversion: 1.0.0\ninvocation: yes\n", skillMD),
			wantInvoke: []string{invokeMsg},
		},
		{
			name:         "metadata value",
			tree:         plainSkillTree(skillMD + "metadata:\n  version: \"1.0.0\"\n  invocation: User\n"),
			wantMetadata: []string{metadataMsg},
		},
		{
			name:         "both values",
			tree:         skillTree("type: skill\nversion: 1.0.0\ninvocation: yes\n", skillMD+"metadata:\n  invocation: User\n"),
			wantInvoke:   []string{invokeMsg},
			wantMetadata: []string{metadataMsg},
		},
		{
			name: "suppressed",
			tree: skillTree(
				"type: skill\nversion: 1.0.0\ninvocation: yes\nlint_suppress:\n  - lint.skill_invocation\n", skillMD),
			wantInvoke: []string{invokeMsg},
		},
		{name: "valid user", tree: skillTree("type: skill\nversion: 1.0.0\ninvocation: user\n", skillMD), wantUser: true},
		{name: "valid any", tree: skillTree("type: skill\nversion: 1.0.0\ninvocation: any\n", skillMD)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg, records := openFixture(t, tc.tree...)
			diags := (&Linter{}).Lint(context.Background(), reg, records)
			if got := messagesFor(diags, "lint.skill_invocation", SeverityWarning); !slices.Equal(got, tc.wantInvoke) {
				t.Errorf("lint.skill_invocation = %q, want %q", got, tc.wantInvoke)
			}
			if got := messagesFor(diags, "lint.skill_metadata_field_ignored", SeverityWarning); !slices.Equal(got, tc.wantMetadata) {
				t.Errorf("lint.skill_metadata_field_ignored = %q, want %q", got, tc.wantMetadata)
			}
			if want := len(tc.wantInvoke) + len(tc.wantMetadata); len(diags) != want {
				t.Errorf("want %d diagnostics, got %d: %v", want, len(diags), diags)
			}
			if got := records[0].Artifact.UserInvoked(); got != tc.wantUser {
				t.Errorf("UserInvoked = %v, want %v (invocation %q)", got, tc.wantUser, records[0].Artifact.Invocation)
			}
		})
	}
}

// Spec: §4.3 — invocation applies to type: skill only, so a value set on any
// other type draws a lint.skill_invocation warning, and an unset value draws
// none.
func TestLint_InvocationOnAgentWarns(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, frontmatter string
		want              []string
	}{
		{"agent set", "type: agent\nversion: 1.0.0\ndescription: Close the books.\ninvocation: user\n",
			[]string{`invocation set on type "agent"; invocation applies only to skill`}},
		{"rule set", "type: rule\nversion: 1.0.0\ndescription: Style.\nrule_mode: always\ninvocation: any\n",
			[]string{`invocation set on type "rule"; invocation applies only to skill`}},
		{"agent unset", "type: agent\nversion: 1.0.0\ndescription: Close the books.\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg, records := openFixture(t, testharness.WriteTreeOption{
				Path: "x/ARTIFACT.md", Content: "---\n" + tc.frontmatter + "---\n\nbody\n",
			})
			diags := (&Linter{}).Lint(context.Background(), reg, records)
			if got := messagesFor(diags, "lint.skill_invocation", SeverityWarning); !slices.Equal(got, tc.want) {
				t.Errorf("lint.skill_invocation = %q, want %q", got, tc.want)
			}
			if countCode(diags, "lint.skill_invocation") != len(tc.want) {
				t.Errorf("lint.skill_invocation must be a warning: %v", diags)
			}
		})
	}
}

// Spec: §4.3.4 — invocation joins the ARTIFACT.md-versus-metadata equality
// check. A stated ARTIFACT.md value of any, or a stated null, beside
// metadata.invocation: user is a lint.skill_artifact_field_mismatch error,
// and the stated value decides the resolved field. Without the invocation arm
// of skillMetadataFieldValue the check compares nil with nil and reports
// nothing.
func TestLint_InvocationMismatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, artifactFM, want string }{
		{"stated any", "invocation: any\n", "ARTIFACT.md invocation any does not match SKILL.md metadata.invocation user"},
		{"stated null", "invocation:\n", "ARTIFACT.md invocation  does not match SKILL.md metadata.invocation user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg, records := openFixture(t, skillTree(
				"type: skill\nversion: 1.0.0\n"+tc.artifactFM,
				"name: hello\ndescription: Greet the user.\nmetadata:\n  invocation: user\n",
			)...)
			diags := (&Linter{}).Lint(context.Background(), reg, records)
			got := messagesFor(diags, "lint.skill_artifact_field_mismatch", SeverityError)
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("want one mismatch %q, got %q", tc.want, got)
			}
			if records[0].Artifact.UserInvoked() {
				t.Errorf("the stated ARTIFACT.md value must decide: resolved %q", records[0].Artifact.Invocation)
			}
		})
	}
}

// Spec: §4.3.4 — a top-level invocation in SKILL.md is a Podium-only field,
// and the error recommends metadata.invocation.
func TestLint_TopLevelInvocationNamesMetadataKey(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t, skillTree(
		"type: skill\nversion: 1.0.0\n",
		"name: hello\ndescription: Greet the user.\ninvocation: user\n",
	)...)
	got := messagesFor((&Linter{}).Lint(context.Background(), reg, records), "lint.skill_podium_only_field", SeverityError)
	want := `SKILL.md contains Podium-only field "invocation" at the top level; set metadata.invocation in SKILL.md or move it to ARTIFACT.md`
	if len(got) != 1 || got[0] != want {
		t.Errorf("want one error %q, got %q", want, got)
	}
}
