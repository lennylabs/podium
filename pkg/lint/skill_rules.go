package lint

import (
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
// present, error and recommend moving the field to ARTIFACT.md." SKILL.md
// stays within the agentskills.io subset so the standard's skills-ref
// validator passes. ParseSkill drops unknown keys silently, so the rule scans
// the raw SKILL.md frontmatter keys rather than the typed Skill value.
type ruleSkillPodiumOnlyFields struct{}

func (ruleSkillPodiumOnlyFields) Code() string { return "lint.skill_podium_only_field" }

func (r ruleSkillPodiumOnlyFields) Check(_ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
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
				out = append(out, errMsg(rec.ID, r,
					fmt.Sprintf("SKILL.md contains Podium-only field %q; move it to ARTIFACT.md", k)))
			}
		}
	}
	return out
}

// ruleSkillArtifactFields enforces spec §4.3.4: "ARTIFACT.md does not contain
// `name`, `description`, or `license` fields (warning); if present, the values
// must match `SKILL.md` exactly (error on mismatch)." Presence is read from
// the raw ARTIFACT.md frontmatter keys so an explicit empty value still warns;
// the value comparison uses the parsed Artifact and Skill.
type ruleSkillArtifactFields struct{}

func (ruleSkillArtifactFields) Code() string { return "lint.skill_artifact_field" }

func (r ruleSkillArtifactFields) Check(_ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
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

func (r ruleSkillRefValidate) Check(_ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
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
