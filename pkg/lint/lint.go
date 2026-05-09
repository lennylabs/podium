// Package lint runs ingest-time validation across artifact and domain
// manifests. Lint rules implement spec §4.3 (universal field constraints),
// §4.3.4 (agentskills.io compliance for skills), §4.5.1 / §4.5.2 (DOMAIN.md
// rules), §4.7.7 (SBOM enforcement for sensitivity ≥ medium), and
// §4.4 (bundled-resource size budgets).
//
// Linter.Lint walks an open filesystem registry and returns a diagnostic
// slice; severity drives whether a host treats a result as an error
// (rejects the artifact) or a warning (proceeds with notice).
package lint

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// Severity is one of error, warning, info.
type Severity string

// Severity values.
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Diagnostic is one lint finding.
type Diagnostic struct {
	// ArtifactID is the canonical ID of the artifact the diagnostic
	// applies to (empty for whole-registry diagnostics).
	ArtifactID string
	// Code is the namespaced lint rule identifier (e.g.,
	// "lint.skill_missing_skill_md", "lint.invalid_name").
	Code string
	// Severity is error / warning / info.
	Severity Severity
	// Message is a human-readable description.
	Message string
	// Rule is the spec section the rule derives from (e.g., "§4.3.4").
	Rule string
}

// String returns a one-line representation suitable for CLI output.
func (d Diagnostic) String() string {
	if d.ArtifactID != "" {
		return fmt.Sprintf("[%s] %s: %s (%s, %s)", d.Severity, d.ArtifactID, d.Message, d.Code, d.Rule)
	}
	return fmt.Sprintf("[%s] %s (%s, %s)", d.Severity, d.Message, d.Code, d.Rule)
}

// Linter applies the configured rules to a registry.
type Linter struct {
	// Rules is the ordered set of rules to apply. Defaults to
	// AllRules() when empty.
	Rules []Rule
}

// Rule is one lint check. Receiving the registry plus parsed records
// (rather than a single artifact) enables cross-artifact rules
// (DOMAIN.md import resolution, name-collision checks).
type Rule interface {
	// Code returns the namespaced rule identifier.
	Code() string
	// SpecSection returns the spec section the rule derives from.
	SpecSection() string
	// Check evaluates the rule and returns any diagnostics it produces.
	Check(reg *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic
}

// AllRules returns the set of lint rules registered for the active
// build. Phase 1 ships the rules below; later phases add rules that
// require infrastructure built in those phases (e.g., DOMAIN.md
// composition rules in Phase 8, dependency-graph rules in Phase 15).
func AllRules() []Rule {
	return []Rule{
		ruleRequiredFields{},
		ruleSkillCompliance{},
		ruleNameSyntax{},
		ruleVersionSemver{},
		ruleSensitivitySBOM{},
		ruleHookConsistency{},
		ruleEffortHintAppliesToType{},
	}
}

// Lint runs every configured rule against the registry and returns the
// concatenated diagnostics, sorted by ArtifactID then Code so output is
// deterministic.
func (l *Linter) Lint(reg *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	rules := l.Rules
	if len(rules) == 0 {
		rules = AllRules()
	}
	var out []Diagnostic
	for _, r := range rules {
		out = append(out, r.Check(reg, records)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ArtifactID != out[j].ArtifactID {
			return out[i].ArtifactID < out[j].ArtifactID
		}
		return out[i].Code < out[j].Code
	})
	return out
}

// ----- Rules -----------------------------------------------------------------

type ruleRequiredFields struct{}

func (ruleRequiredFields) Code() string        { return "lint.required_field_missing" }
func (ruleRequiredFields) SpecSection() string { return "§4.3" }

func (r ruleRequiredFields) Check(_ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		a := rec.Artifact
		if a.Type == "" {
			out = append(out, errMsg(rec.ID, r, "type is required"))
		} else if !manifest.IsFirstClassType(a.Type) {
			// Extension types are handled in Phase 13 via TypeProvider.
			out = append(out, warn(rec.ID, "lint.unknown_type", "§4.1",
				fmt.Sprintf("type %q is not first-class; extension TypeProvider required", a.Type)))
		}
		if a.Version == "" {
			out = append(out, errMsg(rec.ID, r, "version is required"))
		}
	}
	return out
}

type ruleSkillCompliance struct{}

func (ruleSkillCompliance) Code() string        { return "lint.skill_md_compliance" }
func (ruleSkillCompliance) SpecSection() string { return "§4.3.4" }

func (r ruleSkillCompliance) Check(_ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		if rec.Artifact.Type != manifest.TypeSkill {
			continue
		}
		if rec.Skill == nil {
			out = append(out, errMsg(rec.ID, r, "type: skill requires SKILL.md alongside ARTIFACT.md"))
			continue
		}
		if rec.Skill.Name == "" {
			out = append(out, errMsg(rec.ID, r, "SKILL.md must declare top-level name"))
		}
		if rec.Skill.Description == "" {
			out = append(out, errMsg(rec.ID, r, "SKILL.md must declare top-level description"))
		}
		// SKILL.md name must match the parent directory name.
		dir := lastPathSegment(rec.ID)
		if rec.Skill.Name != "" && rec.Skill.Name != dir {
			out = append(out, errMsg(rec.ID, r,
				fmt.Sprintf("SKILL.md name %q does not match parent directory %q", rec.Skill.Name, dir)))
		}
	}
	return out
}

type ruleNameSyntax struct{}

func (ruleNameSyntax) Code() string        { return "lint.invalid_name" }
func (ruleNameSyntax) SpecSection() string { return "§4.3.4" }

func (r ruleNameSyntax) Check(_ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		if rec.Skill != nil && rec.Skill.Name != "" {
			if err := manifest.ValidateName(rec.Skill.Name); err != nil {
				out = append(out, errMsg(rec.ID, r, err.Error()))
			}
		}
		if rec.Artifact.Name != "" {
			if err := manifest.ValidateName(rec.Artifact.Name); err != nil {
				out = append(out, errMsg(rec.ID, r, err.Error()))
			}
		}
	}
	return out
}

type ruleVersionSemver struct{}

func (ruleVersionSemver) Code() string        { return "lint.invalid_version" }
func (ruleVersionSemver) SpecSection() string { return "§4.7.6" }

func (r ruleVersionSemver) Check(_ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		if rec.Artifact.Version == "" {
			continue
		}
		if err := manifest.ValidateVersion(rec.Artifact.Version); err != nil {
			out = append(out, errMsg(rec.ID, r, err.Error()))
		}
	}
	return out
}

type ruleSensitivitySBOM struct{}

func (ruleSensitivitySBOM) Code() string        { return "lint.sbom_required_for_sensitive" }
func (ruleSensitivitySBOM) SpecSection() string { return "§4.7.7" }

func (r ruleSensitivitySBOM) Check(_ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		s := rec.Artifact.Sensitivity
		if s != manifest.SensitivityMedium && s != manifest.SensitivityHigh {
			continue
		}
		if rec.Artifact.SBOM == nil {
			out = append(out, errMsg(rec.ID, r,
				fmt.Sprintf("sensitivity: %s requires sbom: declaration", s)))
		}
	}
	return out
}

type ruleHookConsistency struct{}

func (ruleHookConsistency) Code() string        { return "lint.hook_generic_and_subtype" }
func (ruleHookConsistency) SpecSection() string { return "§4.3.5" }

// genericToSubtypes maps each generic event to its subtype family. Used to
// flag when both the generic and a subtype are declared on the same
// artifact.
var genericToSubtypes = map[string][]string{
	"pre_tool_use":  {"pre_shell_execution", "pre_mcp_execution", "pre_read_file"},
	"post_tool_use": {"post_shell_execution", "post_mcp_execution", "post_file_edit"},
}

func (r ruleHookConsistency) Check(_ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		if rec.Artifact.Type != manifest.TypeHook {
			continue
		}
		event := rec.Artifact.HookEvent
		// Generic + subtype combos only matter if multiple hooks were
		// declared on the same artifact. Phase 1's lint inspects a
		// single hook_event field; the generic-vs-subtype pairing
		// becomes meaningful once a hook artifact ships with both
		// declarations (Phase 13 multi-event support). Phase 1 emits
		// info-level guidance when an authored generic event would
		// shadow a more specific subtype.
		if subs, ok := genericToSubtypes[event]; ok {
			out = append(out, Diagnostic{
				ArtifactID: rec.ID,
				Code:       r.Code(),
				Severity:   SeverityInfo,
				Message: fmt.Sprintf("hook_event %q matches every subtype (%s); pick a subtype if you only need one",
					event, strings.Join(subs, ", ")),
				Rule: r.SpecSection(),
			})
		}
	}
	return out
}

type ruleEffortHintAppliesToType struct{}

func (ruleEffortHintAppliesToType) Code() string        { return "lint.hint_on_unsupported_type" }
func (ruleEffortHintAppliesToType) SpecSection() string { return "§4.3" }

func (r ruleEffortHintAppliesToType) Check(_ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		ty := rec.Artifact.Type
		isHintTarget := ty == manifest.TypeAgent ||
			ty == manifest.TypeSkill ||
			ty == manifest.TypeCommand
		if isHintTarget {
			continue
		}
		if rec.Artifact.EffortHint != "" {
			out = append(out, warn(rec.ID, r.Code(), r.SpecSection(),
				fmt.Sprintf("effort_hint set on type %q; hints apply only to agent / skill / command", ty)))
		}
		if rec.Artifact.ModelClassHint != "" {
			out = append(out, warn(rec.ID, r.Code(), r.SpecSection(),
				fmt.Sprintf("model_class_hint set on type %q; hints apply only to agent / skill / command", ty)))
		}
	}
	return out
}

// ----- helpers --------------------------------------------------------------

func errMsg(id string, r Rule, msg string) Diagnostic {
	return Diagnostic{
		ArtifactID: id,
		Code:       r.Code(),
		Severity:   SeverityError,
		Message:    msg,
		Rule:       r.SpecSection(),
	}
}

func warn(id, code, section, msg string) Diagnostic {
	return Diagnostic{
		ArtifactID: id,
		Code:       code,
		Severity:   SeverityWarning,
		Message:    msg,
		Rule:       section,
	}
}

func lastPathSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
