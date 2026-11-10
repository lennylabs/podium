package lint

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// agentskills.io SKILL.md field constraints (spec §4.3.4 field-allocation
// table). These back the skills-ref reference check below.
const (
	// SkillDescriptionMaxChars caps the SKILL.md description (spec §4.3.4:
	// "≤ 1024 chars").
	SkillDescriptionMaxChars = 1024
	// SkillCompatibilityMaxChars caps the SKILL.md compatibility string
	// (spec §4.3.4: "≤ 500 chars; human-readable string").
	SkillCompatibilityMaxChars = 500
)

// ruleSkillPodiumOnlyFields enforces spec §4.3.4: "SKILL.md does not contain
// Podium-only fields (`type`, `version`, `when_to_use`, `tags`, etc.); if
// present, error. The diagnostic recommends `metadata.version` for a version
// and the `ARTIFACT.md` overlay for every other field." SKILL.md stays within
// the agentskills.io subset so the standard's skills-ref validator passes.
// ParseSkill drops unknown keys silently, so the rule scans the raw SKILL.md
// frontmatter keys rather than the typed Skill value.
//
// Spec: §4.3.4
type ruleSkillPodiumOnlyFields struct{}

func (ruleSkillPodiumOnlyFields) Code() string { return "lint.skill_podium_only_field" }

func (r ruleSkillPodiumOnlyFields) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		if rec.Artifact == nil || rec.Artifact.Type != manifest.TypeSkill || len(rec.SkillBytes) == 0 {
			continue
		}
		keys, err := manifest.TopLevelFrontmatterKeys(rec.SkillBytes)
		if err != nil {
			// A malformed SKILL.md already fails Walk/ParseSkill upstream.
			continue
		}
		for _, k := range keys {
			if manifest.IsPodiumOnlySkillField(k) {
				out = append(out, errMsg(rec.ID, r, podiumOnlyFieldMessage(k)))
			}
		}
	}
	return out
}

// podiumOnlyFieldMessage names the home of a Podium-only field found in
// SKILL.md. A version stays in SKILL.md as metadata.version, which keeps the
// file within the Agent Skills standard; every other field moves to the
// ARTIFACT.md overlay.
//
// Spec: §4.3.4
func podiumOnlyFieldMessage(key string) string {
	if key == "version" {
		return fmt.Sprintf("SKILL.md contains Podium-only field %q; set metadata.version in SKILL.md instead", key)
	}
	return fmt.Sprintf("SKILL.md contains Podium-only field %q; move it to ARTIFACT.md", key)
}

// ruleSkillArtifactFields enforces spec §4.3.4: "When `ARTIFACT.md` is
// present, it does not contain `name`, `description`, or `license` fields
// (warning); if present, the values must match `SKILL.md` exactly (error on
// mismatch). A `version` in `ARTIFACT.md` equals `SKILL.md` `metadata.version`
// when both are set and `metadata.version` is a `major.minor.patch` value
// (error on mismatch)." Presence is read from the raw ARTIFACT.md frontmatter
// keys so an explicit empty value still warns; the value comparison uses the
// parsed Artifact and Skill. A skill with no ARTIFACT.md has nil
// ArtifactBytes, which yields no keys, or the synthesized document, which
// carries only type and version.
//
// Spec: §4.3.4
type ruleSkillArtifactFields struct{}

func (ruleSkillArtifactFields) Code() string { return "lint.skill_artifact_field" }

func (r ruleSkillArtifactFields) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		if rec.Artifact == nil || rec.Artifact.Type != manifest.TypeSkill {
			continue
		}
		keys, err := manifest.TopLevelFrontmatterKeys(rec.ArtifactBytes)
		if err != nil {
			continue
		}
		present := make(map[string]bool, len(keys))
		for _, k := range keys {
			present[k] = true
		}
		var sName, sDesc, sLicense string
		if rec.Skill != nil {
			sName, sDesc, sLicense = rec.Skill.Name, rec.Skill.Description, rec.Skill.License
		}
		for _, f := range []struct{ field, artVal, skillVal string }{
			{"name", rec.Artifact.Name, sName},
			{"description", rec.Artifact.Description, sDesc},
			{"license", rec.Artifact.License, sLicense},
		} {
			if !present[f.field] {
				continue
			}
			out = append(out, warn(rec.ID, r.Code(),
				fmt.Sprintf("ARTIFACT.md sets %q for a skill; omit it so Podium reads the value from SKILL.md", f.field)))
			if rec.Skill != nil && f.artVal != f.skillVal {
				out = append(out, Diagnostic{
					ArtifactID: rec.ID,
					Code:       "lint.skill_artifact_field_mismatch",
					Severity:   SeverityError,
					Message: fmt.Sprintf("ARTIFACT.md %s %q does not match SKILL.md %q (the values must match exactly)",
						f.field, f.artVal, f.skillVal),
				})
			}
		}
		if d, ok := skillVersionMismatch(rec); ok {
			out = append(out, d)
		}
	}
	return out
}

// skillVersionMismatch reports an ARTIFACT.md version that differs from a
// major.minor.patch SKILL.md metadata.version. manifest.ResolveSkillVersion
// copies metadata.version into the record when ARTIFACT.md declares no
// version, so a difference arises only from an authored mismatch. A
// metadata.version in any other form is ignored by §4.3.4 and never
// mismatches.
//
// Spec: §4.3.4
func skillVersionMismatch(rec filesystem.ArtifactRecord) (Diagnostic, bool) {
	if rec.Skill == nil {
		return Diagnostic{}, false
	}
	mv := rec.Skill.Metadata["version"]
	if manifest.ValidateVersion(mv) != nil || rec.Artifact.Version == mv {
		return Diagnostic{}, false
	}
	return Diagnostic{
		ArtifactID: rec.ID,
		Code:       "lint.skill_artifact_field_mismatch",
		Severity:   SeverityError,
		Message: fmt.Sprintf("ARTIFACT.md version %q does not match SKILL.md metadata.version %q",
			rec.Artifact.Version, mv),
	}, true
}

// ruleSkillVersionAssigned enforces spec §4.3.4: "A skill that declares no
// usable version has its version assigned (warning that names any ignored
// `metadata.version` value and the fix)." The record's VersionAssigned flag
// is set by the walker, so the rule fires the same way before ingest assigns
// the version and under `podium lint`, where the filesystem source reports
// the reserved form.
//
// Spec: §4.3.4
type ruleSkillVersionAssigned struct{}

func (ruleSkillVersionAssigned) Code() string { return "lint.skill_version_assigned" }

func (r ruleSkillVersionAssigned) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		if !rec.VersionAssigned {
			continue
		}
		msg := "no usable version declared; the registry assigns 0.0.0-auto.<N>. Set metadata.version in SKILL.md to choose one"
		if rec.Skill != nil {
			if mv := rec.Skill.Metadata["version"]; mv != "" {
				msg = fmt.Sprintf("metadata.version %q is not major.minor.patch and is ignored; ", mv) + msg
			}
		}
		out = append(out, warn(rec.ID, r.Code(), msg))
	}
	return out
}

// ruleSkillReservedVersion enforces spec §4.3.4: "An authored version in the
// reserved form of §4.7.6, in either file, is an error." It covers SKILL.md
// metadata.version, which manifest.ResolveSkillVersion ignores because the
// value is not major.minor.patch, so no other rule sees it. An ARTIFACT.md
// version in the reserved form fails ruleVersionSemver.
//
// Spec: §4.3.4, §4.7.6
type ruleSkillReservedVersion struct{}

func (ruleSkillReservedVersion) Code() string { return "lint.invalid_version" }

func (r ruleSkillReservedVersion) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		if rec.Artifact == nil || rec.Artifact.Type != manifest.TypeSkill || rec.Skill == nil {
			continue
		}
		mv := rec.Skill.Metadata["version"]
		if _, ok := manifest.IsAutoVersion(mv); ok {
			out = append(out, errMsg(rec.ID, r,
				fmt.Sprintf("metadata.version %q uses the registry-reserved form", mv)))
		}
	}
	return out
}

// ruleSkillRefValidate stands in for spec §4.3.4's "`skills-ref validate`
// reference check from the agentskills.io project ... (warning on failure;
// lint suppression flag available)." Shelling out to the external skills-ref
// binary is not possible at ingest (no network, the tool may be absent), so
// this rule applies the agentskills.io SKILL.md constraints that the spec
// documents and that the hard-error skill rules do not already cover: the
// description length cap and the compatibility length cap. Failures warn and
// honor the per-artifact lint_suppress flag (spec §4.3.4).
type ruleSkillRefValidate struct{}

func (ruleSkillRefValidate) Code() string { return "lint.skill_ref_validate" }

func (r ruleSkillRefValidate) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		if rec.Artifact == nil || rec.Artifact.Type != manifest.TypeSkill || rec.Skill == nil {
			continue
		}
		if rec.Artifact.Suppresses(r.Code()) {
			continue
		}
		if n := utf8.RuneCountInString(rec.Skill.Description); n > SkillDescriptionMaxChars {
			out = append(out, warn(rec.ID, r.Code(),
				fmt.Sprintf("SKILL.md description is %d chars; the agentskills.io skills-ref validator caps it at %d", n, SkillDescriptionMaxChars)))
		}
		if n := utf8.RuneCountInString(rec.Skill.Compatibility); n > SkillCompatibilityMaxChars {
			out = append(out, warn(rec.ID, r.Code(),
				fmt.Sprintf("SKILL.md compatibility is %d chars; the agentskills.io skills-ref validator caps it at %d", n, SkillCompatibilityMaxChars)))
		}
	}
	return out
}
