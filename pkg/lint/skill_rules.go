package lint

import (
	"context"
	"fmt"
	"slices"
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
// Podium-only fields (`type`, `tags`, etc.) at the top level; if present,
// error. A top-level `version` is a warning rather than an error, because many
// published skills carry one: Podium ignores it and takes the version from
// `metadata.version` or `ARTIFACT.md`. A top-level `when_to_use` is a warning
// rather than an error, because Claude Code defines it." The diagnostic recommends
// `metadata.version` for a version, the `metadata` key of the same name for a
// field that **Podium fields in `metadata`** lists, and the `ARTIFACT.md`
// overlay for every other field." SKILL.md stays within the agentskills.io
// subset so the standard's skills-ref validator passes. ParseSkill drops
// unknown keys silently, so the rule scans the raw SKILL.md frontmatter keys
// rather than the typed Skill value.
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
			// An unparseable SKILL.md is refused before lint (§4.1), so it never reaches this rule.
			continue
		}
		for _, k := range keys {
			if !manifest.IsPodiumOnlySkillField(k) {
				continue
			}
			if k == "version" || k == "when_to_use" {
				out = append(out, warn(rec.ID, r.Code(), podiumOnlyFieldMessage(k)))
				continue
			}
			out = append(out, errMsg(rec.ID, r, podiumOnlyFieldMessage(k)))
		}
	}
	return out
}

// podiumOnlyFieldMessage names the home of a Podium-only field found at the
// top level of SKILL.md. A version and a metadata-eligible field stay in
// SKILL.md under metadata, which keeps the file within the Agent Skills
// standard; every other field moves to the ARTIFACT.md overlay.
//
// Spec: §4.3.4
func podiumOnlyFieldMessage(key string) string {
	if key == "version" {
		return fmt.Sprintf("SKILL.md contains Podium-only field %q, which Podium ignores; set metadata.version in SKILL.md instead", key)
	}
	if key == "when_to_use" {
		return "SKILL.md sets when_to_use at the top level, as Claude Code reads it, and Podium reads the value; " +
			"the Agent Skills standard does not define the key, so a skill upload outside Claude Code refuses it"
	}
	if manifest.IsSkillMetadataField(key) {
		return fmt.Sprintf("SKILL.md contains Podium-only field %q at the top level; set metadata.%s in SKILL.md or move it to ARTIFACT.md", key, key)
	}
	return fmt.Sprintf("SKILL.md contains Podium-only field %q; move it to ARTIFACT.md", key)
}

// ruleSkillArtifactFields enforces spec §4.3.4: "When `ARTIFACT.md` is
// present, it does not contain `name`, `description`, or `license` fields
// (warning); if present, the values must match `SKILL.md` exactly (error on
// mismatch). A `version` in `ARTIFACT.md` equals `SKILL.md` `metadata.version`
// when both are set and `metadata.version` is a `major.minor.patch` value
// (error on mismatch)." It also enforces "When `ARTIFACT.md` states a key that
// **Podium fields in `metadata`** lists and `SKILL.md` `metadata` carries a
// value for that key that follows its encoding, the two values are equal
// (error on mismatch)." Presence is read from the raw ARTIFACT.md frontmatter
// keys so an explicit empty value still warns or compares; the value
// comparison uses the parsed Artifact and Skill. A skill with no ARTIFACT.md
// has nil ArtifactBytes, which yields no keys, or the synthesized document,
// which carries only type and version.
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
		out = append(out, skillMetadataMismatches(rec, keys)...)
	}
	return out
}

// skillVersionMismatch reports an ARTIFACT.md version that differs from a
// major.minor.patch SKILL.md metadata.version. The walk's
// ArtifactRecord.ResolveSkillFields copies metadata.version into the record
// when ARTIFACT.md declares no version, so a difference arises only from an
// authored mismatch. A metadata.version in any other form is ignored by §4.3.4
// and never mismatches.
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

// skillMetadataMismatches reports each metadata-eligible key that ARTIFACT.md
// states, in the order of keys, whose authored value differs from the decoded
// SKILL.md metadata value. tags and when_to_use compare as ordered lists,
// deprecated as a bool, and the rest as strings. A metadata value that does
// not decode never mismatches; ruleSkillMetadataFields warns on it instead.
//
// The check re-parses rec.AuthoredBytes rather than reading rec.Artifact,
// because the walk's ArtifactRecord.ResolveSkillFields has already written the
// resolved values into rec.Artifact. A comparison with rec.Artifact would miss
// an explicit deprecated: false, sensitivity: "", or tags: [] in ARTIFACT.md,
// which the resolver keeps and which lint must still compare. A plain skill
// has empty AuthoredBytes and its synthesized document states only type and
// version, so it is skipped.
//
// Spec: §4.3.4
func skillMetadataMismatches(rec filesystem.ArtifactRecord, keys []string) []Diagnostic {
	if rec.Skill == nil || len(rec.AuthoredBytes) == 0 {
		return nil
	}
	authored, err := manifest.ParseArtifact(rec.AuthoredBytes)
	if err != nil {
		// The walk parsed the same bytes before lint runs.
		return nil
	}
	dec := manifest.DecodeSkillMetadata(rec.Skill)
	var out []Diagnostic
	if top, _ := manifest.DecodeSkillTopLevelWhenToUse(rec.Skill); top != nil && slices.Contains(keys, "when_to_use") &&
		!skillMetadataValuesEqual(authored.WhenToUse, top) {
		out = append(out, Diagnostic{
			ArtifactID: rec.ID,
			Code:       "lint.skill_artifact_field_mismatch",
			Severity:   SeverityError,
			Message:    fmt.Sprintf("ARTIFACT.md when_to_use %v does not match SKILL.md top-level when_to_use %v", authored.WhenToUse, top),
		})
	}
	for _, k := range keys {
		if !dec.Decoded[k] {
			continue
		}
		av, mv := skillMetadataFieldValue(authored, k), skillMetadataFieldValue(&dec.Values, k)
		if skillMetadataValuesEqual(av, mv) {
			continue
		}
		out = append(out, Diagnostic{
			ArtifactID: rec.ID,
			Code:       "lint.skill_artifact_field_mismatch",
			Severity:   SeverityError,
			Message:    fmt.Sprintf("ARTIFACT.md %s %v does not match SKILL.md metadata.%s %v", k, av, k, mv),
		})
	}
	return out
}

// skillMetadataFieldValue returns the field of a that the metadata-eligible
// key names, in the form the §4.3.4 equality check compares: a []string for
// tags and when_to_use, a bool for deprecated, and a string for the rest. Any
// other key returns nil.
//
// Spec: §4.3.4
func skillMetadataFieldValue(a *manifest.Artifact, key string) any {
	switch key {
	case "when_to_use":
		return a.WhenToUse
	case "tags":
		return a.Tags
	case "sensitivity":
		return string(a.Sensitivity)
	case "search_visibility":
		return string(a.SearchVisibility)
	case "deprecated":
		return a.Deprecated
	case "replaced_by":
		return a.ReplacedBy
	case "release_notes":
		return a.ReleaseNotes
	case "invocation":
		return string(a.Invocation)
	}
	return nil
}

// skillMetadataValuesEqual compares two skillMetadataFieldValue results. Lists
// compare element by element in order, so a nil list equals an empty one;
// every other value compares with ==, which a []string would panic on.
func skillMetadataValuesEqual(x, y any) bool {
	xs, xList := x.([]string)
	ys, yList := y.([]string)
	if xList || yList {
		return xList && yList && slices.Equal(xs, ys)
	}
	return x == y
}

// ruleSkillInvocation enforces spec §4.3 and §4.3.4 for the invocation value
// that ARTIFACT.md authors. On a skill, a non-empty value other than any or
// user warns and the skill resolves to any. On every other type, a set value
// warns, because invocation applies only to skills.
//
// The rule reads the authored bytes rather than rec.Artifact.Invocation,
// because the resolved value can hold the SKILL.md metadata.invocation value,
// which ruleSkillMetadataFields already reports. Each invalid value is
// therefore reported once, in the file that holds it. A plain skill has no
// ARTIFACT.md and states no value. The warning honors no lint_suppress entry.
//
// Spec: §4.3, §4.3.4
type ruleSkillInvocation struct{}

func (ruleSkillInvocation) Code() string { return "lint.skill_invocation" }

func (r ruleSkillInvocation) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		if rec.Artifact == nil || len(rec.AuthoredBytes) == 0 {
			continue
		}
		authored, err := manifest.ParseArtifact(rec.AuthoredBytes)
		if err != nil || authored.Invocation == "" {
			// The walk parsed the same bytes before lint runs.
			continue
		}
		if msg := invocationProblem(rec.Artifact.Type, authored.Invocation); msg != "" {
			out = append(out, warn(rec.ID, r.Code(), msg))
		}
	}
	return out
}

// invocationProblem returns the warning text for a non-empty authored
// invocation value on an artifact of type ty, or "" when the value is valid.
//
// Spec: §4.3, §4.3.4
func invocationProblem(ty manifest.ArtifactType, inv manifest.Invocation) string {
	if ty != manifest.TypeSkill {
		return fmt.Sprintf("invocation set on type %q; invocation applies only to skill", ty)
	}
	if inv == manifest.InvocationAny || inv == manifest.InvocationUser {
		return ""
	}
	return fmt.Sprintf("invocation %q is not a valid value; valid values: %s, %s. The skill resolves to %s",
		inv, manifest.InvocationAny, manifest.InvocationUser, manifest.InvocationAny)
}

// ruleSkillMetadataFields enforces spec §4.3.4: "A `SKILL.md` `metadata` value
// for a key that **Podium fields in `metadata`** lists and that does not
// follow its encoding, including a sequence or a mapping, is ignored (warning
// that names the key, the value, and the encoding). A top-level `SKILL.md`
// `when_to_use` that does not follow its encoding is ignored the same way." It
// also enforces "A top-level `SKILL.md` `when_to_use` that follows its encoding
// ... equals a `metadata.when_to_use` that follows its encoding (error on each
// mismatch)", because both values come from SKILL.md alone. The warning honors no
// lint_suppress entry, because §4.3.4 grants the list only to the skills-ref
// check and the catalog advisories.
//
// Spec: §4.3.4
type ruleSkillMetadataFields struct{}

func (ruleSkillMetadataFields) Code() string { return "lint.skill_metadata_field_ignored" }

func (r ruleSkillMetadataFields) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		if rec.Artifact == nil || rec.Artifact.Type != manifest.TypeSkill || rec.Skill == nil {
			continue
		}
		dec := manifest.DecodeSkillMetadata(rec.Skill)
		for _, is := range dec.Issues {
			out = append(out, warn(rec.ID, r.Code(), metadataIssueMessage(is)))
		}
		top, issue := manifest.DecodeSkillTopLevelWhenToUse(rec.Skill)
		if issue != nil {
			out = append(out, warn(rec.ID, r.Code(), metadataIssueMessage(*issue)))
		}
		if top != nil && dec.Decoded["when_to_use"] && !skillMetadataValuesEqual(top, dec.Values.WhenToUse) {
			out = append(out, Diagnostic{
				ArtifactID: rec.ID,
				Code:       "lint.skill_artifact_field_mismatch",
				Severity:   SeverityError,
				Message: fmt.Sprintf("SKILL.md top-level when_to_use %v does not match SKILL.md metadata.when_to_use %v",
					top, dec.Values.WhenToUse),
			})
		}
	}
	return out
}

// metadataIssueMessage names the key, the value, and the encoding of an
// ignored metadata value. A string value is quoted, and a sequence or a
// mapping is named by its kind and its single-line YAML flow encoding, so the
// two never read alike.
//
// Spec: §4.3.4
func metadataIssueMessage(is manifest.SkillMetadataIssue) string {
	if is.TopLevel {
		if is.Kind != "" {
			return fmt.Sprintf("SKILL.md top-level %s is a %s (%s) and is ignored: the value must be %s", is.Key, is.Kind, is.Value, is.Encoding)
		}
		return fmt.Sprintf("SKILL.md top-level %s %q is ignored: the value must be %s", is.Key, is.Value, is.Encoding)
	}
	if is.Kind != "" {
		return fmt.Sprintf("metadata.%s is a %s (%s) and is ignored: the value must be %s", is.Key, is.Kind, is.Value, is.Encoding)
	}
	return fmt.Sprintf("metadata.%s %q is ignored: the value must be %s", is.Key, is.Value, is.Encoding)
}

// ruleSkillMetadataNonString enforces spec §4.3.4: "A `SKILL.md` `metadata`
// value that is a sequence or a mapping under a key that is neither `version`
// nor a key **Podium fields in `metadata`** lists is kept and not read
// (warning `lint.skill_metadata_nonstring` that names the key and says the
// Agent Skills standard defines `metadata` values as strings)." Published
// skills nest values under keys of their own, such as requires and openclaw,
// so the SKILL.md parses and its bytes reach every materialized copy. A
// sequence or a mapping under a key Podium reads is reported by
// ruleSkillMetadataFields, and one under version by ruleSkillVersionAssigned.
// The warning honors no lint_suppress entry (§4.3.4).
//
// Spec: §4.3.4
type ruleSkillMetadataNonString struct{}

func (ruleSkillMetadataNonString) Code() string { return "lint.skill_metadata_nonstring" }

func (r ruleSkillMetadataNonString) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		if rec.Artifact == nil || rec.Artifact.Type != manifest.TypeSkill {
			continue
		}
		for _, nv := range rec.Skill.UnreadNonStringMetadata() {
			out = append(out, warn(rec.ID, r.Code(), fmt.Sprintf(
				"metadata.%s is a %s; Podium keeps it unchanged and does not read it, but the Agent Skills standard defines metadata values as strings",
				nv.Key, nv.Kind)))
		}
	}
	return out
}

// ruleSkillVersionAssigned enforces spec §4.3.4: "A skill that declares no
// usable version has its version assigned (info that names any ignored
// `metadata.version` value and the fix)." A sequence or a mapping under
// version is such an ignored value. The diagnostic is informational
// because a plain Agent Skill commonly declares no version, and an info
// diagnostic never fails `podium lint` under either --fail-on value (§7.3.1)
// and never blocks ingest. The record's VersionAssigned flag is set by the
// walker, so the rule fires the same way before ingest assigns the version
// and under `podium lint`, where the filesystem source reports the reserved
// form.
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
			} else if nv, ok := rec.Skill.NonStringMetadataValue("version"); ok {
				msg = fmt.Sprintf("metadata.version is a %s (%s), not a string, and is ignored; ", nv.Kind, nv.Value) + msg
			}
		}
		out = append(out, Diagnostic{ArtifactID: rec.ID, Code: r.Code(), Severity: SeverityInfo, Message: msg})
	}
	return out
}

// ruleSkillReservedVersion enforces spec §4.3.4: "An authored version in the
// reserved form of §4.7.6, in either file, is an error." It covers SKILL.md
// metadata.version, which manifest.ResolveSkillFields ignores because the
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
