package manifest

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// skillMDAllowedFields is the agentskills.io subset that may appear as
// top-level SKILL.md frontmatter, per the §4.3.4 field-allocation table.
// Every other manifest field is Podium-only and belongs in ARTIFACT.md.
var skillMDAllowedFields = map[string]bool{
	"name":          true,
	"description":   true,
	"license":       true,
	"compatibility": true,
	"metadata":      true,
	"allowed-tools": true,
}

// podiumOnlySkillFields lists the ARTIFACT.md-only manifest fields that must
// not appear in a skill's SKILL.md (spec §4.3.4: "SKILL.md does not contain
// Podium-only fields (`type`, `version`, `when_to_use`, `tags`, etc.); if
// present, error"). It is every Podium manifest field outside the
// agentskills.io subset in skillMDAllowedFields, including the type-specific
// fields, so a stray hook_event or rule_mode in SKILL.md is flagged too.
var podiumOnlySkillFields = []string{
	"type",
	"version",
	"when_to_use",
	"tags",
	"sensitivity",
	"search_visibility",
	"deprecated",
	"replaced_by",
	"release_notes",
	"audit_redact",
	"mcpServers",
	"requiresApproval",
	"runtime_requirements",
	"sandbox_profile",
	"effort_hint",
	"model_class_hint",
	"sbom",
	"input",
	"output",
	"delegates_to",
	"rule_mode",
	"rule_globs",
	"rule_description",
	"hook_event",
	"hook_action",
	"server_identifier",
	"extends",
	"target_harnesses",
	"external_resources",
	"lint_suppress",
}

// podiumOnlySkillFieldSet is the membership index for podiumOnlySkillFields.
var podiumOnlySkillFieldSet = func() map[string]bool {
	m := make(map[string]bool, len(podiumOnlySkillFields))
	for _, f := range podiumOnlySkillFields {
		m[f] = true
	}
	return m
}()

// PodiumOnlySkillFields returns the manifest field names that are reserved to
// ARTIFACT.md and must not appear in a skill's SKILL.md (spec §4.3.4). The
// returned slice is a copy the caller may modify.
func PodiumOnlySkillFields() []string {
	return append([]string(nil), podiumOnlySkillFields...)
}

// IsPodiumOnlySkillField reports whether key is a Podium-only manifest field
// that belongs in ARTIFACT.md rather than SKILL.md (spec §4.3.4).
func IsPodiumOnlySkillField(key string) bool {
	return podiumOnlySkillFieldSet[key]
}

// IsAgentSkillsField reports whether key is part of the agentskills.io subset
// permitted at the top level of a SKILL.md (spec §4.3.4 field allocation).
func IsAgentSkillsField(key string) bool {
	return skillMDAllowedFields[key]
}

// Suppresses reports whether the artifact's lint_suppress list names code,
// the per-artifact lint suppression flag described in spec §4.3.4. A nil
// artifact suppresses nothing.
func (a *Artifact) Suppresses(code string) bool {
	if a == nil {
		return false
	}
	for _, c := range a.LintSuppress {
		if c == code {
			return true
		}
	}
	return false
}

// TopLevelFrontmatterKeys returns the top-level mapping keys of a manifest's
// YAML frontmatter in source order. It is used by lint rules that must
// inspect which fields are present in a manifest without the typed decoders
// silently dropping unknown keys (ParseSkill / ParseArtifact use
// yaml.Unmarshal without KnownFields, so unrecognized fields are otherwise
// invisible). Returns ErrNoFrontmatter when src has no frontmatter block and
// ErrInvalidYAML when the frontmatter does not decode.
func TopLevelFrontmatterKeys(src []byte) ([]string, error) {
	fm, _, err := SplitFrontmatter(src)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(fm, &doc); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidYAML, err)
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil
	}
	keys := make([]string, 0, len(root.Content)/2)
	for i := 0; i+1 < len(root.Content); i += 2 {
		keys = append(keys, root.Content[i].Value)
	}
	return keys, nil
}
