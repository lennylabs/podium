// Package lint runs ingest-time validation across artifact and domain
// manifests. Lint rules implement spec §4.3 (universal field constraints),
// §4.3.4 (agentskills.io compliance for skills), §4.5.1 / §4.5.2 (DOMAIN.md
// rules), and §4.4 (bundled-resource size budgets).
//
// Linter.Lint walks an open filesystem registry and returns a diagnostic
// slice; severity drives whether a host treats a result as an error
// (rejects the artifact) or a warning (proceeds with notice).
package lint

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/typeprovider"
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
}

// String returns a one-line representation suitable for CLI output.
func (d Diagnostic) String() string {
	if d.ArtifactID != "" {
		return fmt.Sprintf("[%s] %s: %s (%s)", d.Severity, d.ArtifactID, d.Message, d.Code)
	}
	return fmt.Sprintf("[%s] %s (%s)", d.Severity, d.Message, d.Code)
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
	// Check evaluates the rule and returns any diagnostics it produces.
	Check(reg *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic
}

// AllRules returns the set of lint rules registered for the active
// build.
func AllRules() []Rule {
	return []Rule{
		ruleRequiredFields{},
		ruleTypeProviderValidate{},
		ruleSkillCompliance{},
		ruleNameSyntax{},
		ruleVersionSemver{},
		ruleHookEventCanonical{},
		ruleHookConsistency{},
		ruleEffortHintAppliesToType{},
		ruleBundledResourceSize{},
		ruleManifestSize{},
		ruleArtifactBodyForSkill{},
		ruleProseReferenceResolution{},
		ruleDomainImportsResolve{},
		ruleDomainImportCycle{},
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

// ruleRequiredFields enforces the §4.3 universal-field requirements
// (type and version) and the §4.1 type-registration check. providers is
// the TypeProvider registry consulted for the type check; a nil registry
// defaults to typeprovider.Default so the shipped binary recognizes
// first-class and built-in extension types.
type ruleRequiredFields struct {
	providers *typeprovider.Registry
}

func (ruleRequiredFields) Code() string { return "lint.required_field_missing" }

func (r ruleRequiredFields) Check(_ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	providers := resolveProviders(r.providers)
	var out []Diagnostic
	for _, rec := range records {
		a := rec.Artifact
		if a.Type == "" {
			out = append(out, errMsg(rec.ID, r, "type is required"))
		} else if err := providers.Require(a.Type); errors.Is(err, manifest.ErrUnknownType) {
			// §4.1: a type registered with any TypeProvider (first-class,
			// built-in extension, or deployment-registered extension) is
			// accepted. Only an unregistered type warns, because the
			// deployment may register a provider for it.
			out = append(out, warn(rec.ID, "lint.unknown_type",
				fmt.Sprintf("type %q is not registered with any TypeProvider; register an extension TypeProvider for it", a.Type)))
		}
		if a.Version == "" {
			out = append(out, errMsg(rec.ID, r, "version is required"))
		}
	}
	return out
}

// ruleTypeProviderValidate dispatches each artifact to the TypeProvider
// registered for its type and surfaces the provider's diagnostics
// (§4.1 type-system extensibility, §9 TypeProvider SPI). The built-in
// providers are no-ops; deployment-registered extension types contribute
// their type-specific lint rules here. providers defaults to
// typeprovider.Default when nil.
type ruleTypeProviderValidate struct {
	providers *typeprovider.Registry
}

func (ruleTypeProviderValidate) Code() string { return "lint.type_provider" }

func (r ruleTypeProviderValidate) Check(_ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	providers := resolveProviders(r.providers)
	var out []Diagnostic
	for _, rec := range records {
		if rec.Artifact == nil {
			continue
		}
		for _, d := range providers.Validate(rec.Artifact) {
			msg := d.Message
			if d.Path != "" {
				msg = fmt.Sprintf("%s (%s)", msg, d.Path)
			}
			code := d.Code
			if code == "" {
				code = r.Code()
			}
			out = append(out, Diagnostic{
				ArtifactID: rec.ID,
				Code:       code,
				Severity:   severityFromProvider(d.Severity),
				Message:    msg,
			})
		}
	}
	return out
}

type ruleSkillCompliance struct{}

func (ruleSkillCompliance) Code() string { return "lint.skill_md_compliance" }

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

func (ruleNameSyntax) Code() string { return "lint.invalid_name" }

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

func (ruleVersionSemver) Code() string { return "lint.invalid_version" }

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

// ruleHookEventCanonical enforces §4.3.5: a type: hook artifact's
// hook_event is "constrained to a canonical event name from the table".
// An unknown or misspelled event (for example on_stop) is an ingest
// error, since the adapter has no canonical-to-native mapping for it. An
// empty hook_event is left to ruleRequiredFields-style per-type checks;
// this rule only rejects a non-empty value outside the canonical set.
type ruleHookEventCanonical struct{}

func (ruleHookEventCanonical) Code() string { return "lint.unknown_hook_event" }

func (r ruleHookEventCanonical) Check(_ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		if rec.Artifact == nil || rec.Artifact.Type != manifest.TypeHook {
			continue
		}
		event := rec.Artifact.HookEvent
		if event == "" || manifest.IsCanonicalHookEvent(event) {
			continue
		}
		out = append(out, errMsg(rec.ID, r,
			fmt.Sprintf("hook_event %q is not a canonical §4.3.5 event; valid events: %s",
				event, strings.Join(manifest.CanonicalHookEvents(), ", "))))
	}
	return out
}

type ruleHookConsistency struct{}

func (ruleHookConsistency) Code() string { return "lint.hook_generic_and_subtype" }

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
		// Lint inspects a single hook_event field and emits info-
		// level guidance when an authored generic event would
		// shadow a more specific subtype.
		if subs, ok := genericToSubtypes[event]; ok {
			out = append(out, Diagnostic{
				ArtifactID: rec.ID,
				Code:       r.Code(),
				Severity:   SeverityInfo,
				Message: fmt.Sprintf("hook_event %q matches every subtype (%s); pick a subtype if you only need one",
					event, strings.Join(subs, ", ")),
			})
		}
	}
	return out
}

type ruleEffortHintAppliesToType struct{}

func (ruleEffortHintAppliesToType) Code() string { return "lint.hint_on_unsupported_type" }

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
			out = append(out, warn(rec.ID, r.Code(),
				fmt.Sprintf("effort_hint set on type %q; hints apply only to agent / skill / command", ty)))
		}
		if rec.Artifact.ModelClassHint != "" {
			out = append(out, warn(rec.ID, r.Code(),
				fmt.Sprintf("model_class_hint set on type %q; hints apply only to agent / skill / command", ty)))
		}
	}
	return out
}

// ----- helpers --------------------------------------------------------------

// resolveProviders returns p, or the process-global typeprovider.Default
// when p is nil, so AllRules() works without explicit wiring while tests
// can inject a registry.
func resolveProviders(p *typeprovider.Registry) *typeprovider.Registry {
	if p != nil {
		return p
	}
	return typeprovider.Default
}

// severityFromProvider maps a typeprovider.Diagnostic severity string to a
// lint Severity. typeprovider uses "warn"; lint uses "warning". Unknown
// values default to warning so a misconfigured provider does not silently
// drop a finding.
func severityFromProvider(s string) Severity {
	switch s {
	case "error":
		return SeverityError
	case "info":
		return SeverityInfo
	default:
		return SeverityWarning
	}
}

func errMsg(id string, r Rule, msg string) Diagnostic {
	return Diagnostic{
		ArtifactID: id,
		Code:       r.Code(),
		Severity:   SeverityError,
		Message:    msg,
	}
}

func warn(id, code, msg string) Diagnostic {
	return Diagnostic{
		ArtifactID: id,
		Code:       code,
		Severity:   SeverityWarning,
		Message:    msg,
	}
}

func lastPathSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
