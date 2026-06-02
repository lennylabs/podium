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
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

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
	// Rules is the ordered set of rules to apply. Defaults to the
	// rule set from AllRulesWithClient(HTTPClient) when empty.
	Rules []Rule
	// HTTPClient, when non-nil, enables the §4.4 URL HEAD check in the
	// default rule set: prose references to URLs are validated by an
	// HTTP HEAD that must return 200/3xx. When nil the URL check is
	// skipped (offline ingest); the bundled-file existence check still
	// runs. Ignored when Rules is set explicitly.
	HTTPClient *http.Client
	// AllowPerDomainOverrides reflects the §13.12 tenant
	// discovery.allow_per_domain_overrides setting. When non-nil and
	// false, the default rule set adds a warning on any DOMAIN.md that
	// carries a `discovery:` block, since per-domain overrides are
	// disabled registry-wide and the block has no effect (§4.5.5). Nil
	// (the default) leaves overrides allowed and skips the check. Ignored
	// when Rules is set explicitly.
	AllowPerDomainOverrides *bool
}

// Rule is one lint check. Receiving the registry plus parsed records
// (rather than a single artifact) enables cross-artifact rules
// (DOMAIN.md import resolution, name-collision checks).
type Rule interface {
	// Code returns the namespaced rule identifier.
	Code() string
	// Check evaluates the rule and returns any diagnostics it produces.
	Check(ctx context.Context, reg *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic
}

// AllRules returns the set of lint rules registered for the active
// build, with the §4.4 URL HEAD check disabled (offline). Equivalent to
// AllRulesWithClient(nil).
func AllRules() []Rule {
	return AllRulesWithClient(nil)
}

// AllRulesWithClient returns the registered rule set with the §4.4
// prose-reference rule driven by client: a non-nil client enables URL
// HEAD validation, a nil client skips it. Every other rule is
// client-independent.
func AllRulesWithClient(client *http.Client) []Rule {
	return []Rule{
		ruleRequiredFields{},
		ruleTypeRequiredFields{},
		ruleRuleModeHygiene{},
		ruleRuleModeCanonical{},
		ruleTypeProviderValidate{},
		ruleSkillCompliance{},
		ruleSkillPodiumOnlyFields{},
		ruleSkillArtifactFields{},
		ruleSkillRefValidate{},
		ruleNameSyntax{},
		ruleVersionSemver{},
		ruleHookEventCanonical{},
		ruleHookConsistency{},
		ruleHarnessCapability{},
		ruleEffortHintAppliesToType{},
		ruleBundledResourceSize{},
		ruleManifestSize{},
		ruleArtifactBodyForSkill{},
		ruleProseReferenceResolution{HTTPClient: client},
		ruleDomainImportsResolve{},
		ruleDomainImportCycle{},
		ruleDomainImportBroadGlob{},
	}
}

// DefaultHTTPClient is the client NewIngestLinter uses for §4.4 URL HEAD
// checks. The 10s overall timeout bounds a slow or unreachable host; the
// per-request context in the prose rule adds a 5s ceiling per probe.
func DefaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}

// NewIngestLinter returns a Linter for an ingest pass. When offline is
// false it enables §4.4 URL HEAD validation with DefaultHTTPClient();
// when true it skips the network probe (the bundled-file existence check
// still runs). Callers supply offline from their own deployment config
// (for example PODIUM_INGEST_OFFLINE or a --offline flag).
func NewIngestLinter(offline bool) *Linter {
	if offline {
		return &Linter{}
	}
	return &Linter{HTTPClient: DefaultHTTPClient()}
}

// Lint runs every configured rule against the registry and returns the
// concatenated diagnostics, sorted by ArtifactID then Code so output is
// deterministic.
func (l *Linter) Lint(ctx context.Context, reg *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	rules := l.Rules
	if len(rules) == 0 {
		rules = AllRulesWithClient(l.HTTPClient)
		// §4.5.5: when the tenant disables per-domain discovery
		// overrides, warn on any DOMAIN.md that still carries a
		// `discovery:` block (ingest succeeds; the block is ignored).
		if l.AllowPerDomainOverrides != nil && !*l.AllowPerDomainOverrides {
			rules = append(rules, ruleDomainDiscoveryOverrideDisallowed{})
		}
	}
	var out []Diagnostic
	for _, r := range rules {
		out = append(out, r.Check(ctx, reg, records)...)
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

func (r ruleRequiredFields) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
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

func (r ruleTypeProviderValidate) Check(ctx context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	providers := resolveProviders(r.providers)
	var out []Diagnostic
	for _, rec := range records {
		if rec.Artifact == nil {
			continue
		}
		for _, d := range providers.Validate(ctx, rec.Artifact) {
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

func (r ruleSkillCompliance) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
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

func (r ruleNameSyntax) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
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

func (r ruleVersionSemver) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
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

func (r ruleHookEventCanonical) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
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

// genericToSubtypes maps each generic tool event to its subtype family
// (§4.3.5). Used to detect when a generic hook and one of its corresponding
// subtype hooks are both declared.
var genericToSubtypes = map[string][]string{
	"pre_tool_use":  {"pre_shell_execution", "pre_mcp_execution", "pre_read_file"},
	"post_tool_use": {"post_shell_execution", "post_mcp_execution", "post_file_edit"},
}

// subtypeToGeneric inverts genericToSubtypes for subtype lookup.
var subtypeToGeneric = func() map[string]string {
	m := map[string]string{}
	for generic, subs := range genericToSubtypes {
		for _, s := range subs {
			m[s] = generic
		}
	}
	return m
}()

// Check implements spec §4.3.5: "Authors should not declare both a generic
// hook and the corresponding subtype hook for the same artifact; lint warns
// when this happens." A hook_event is a single scalar (§4.3.5 shows
// `hook_event: stop`), so one artifact cannot hold both a generic and a
// subtype; the reachable form of "declaring both" is a generic hook and a
// corresponding subtype hook present together in the linted set. The rule
// warns on the generic hook and names the overlapping subtype hook. A lone
// generic hook is valid (§4.3.5: "Authors choose the level of specificity")
// and draws no diagnostic.
func (r ruleHookConsistency) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	// Collect the subtype hooks present, grouped by their parent generic.
	subtypesByGeneric := map[string][]filesystem.ArtifactRecord{}
	for _, rec := range records {
		if rec.Artifact == nil || rec.Artifact.Type != manifest.TypeHook {
			continue
		}
		if generic, ok := subtypeToGeneric[rec.Artifact.HookEvent]; ok {
			subtypesByGeneric[generic] = append(subtypesByGeneric[generic], rec)
		}
	}
	var out []Diagnostic
	for _, rec := range records {
		if rec.Artifact == nil || rec.Artifact.Type != manifest.TypeHook {
			continue
		}
		overlaps := subtypesByGeneric[rec.Artifact.HookEvent]
		if len(overlaps) == 0 {
			continue
		}
		labels := make([]string, 0, len(overlaps))
		for _, s := range overlaps {
			labels = append(labels, fmt.Sprintf("%q (%s)", s.Artifact.HookEvent, s.ID))
		}
		sort.Strings(labels)
		out = append(out, Diagnostic{
			ArtifactID: rec.ID,
			Code:       r.Code(),
			Severity:   SeverityWarning,
			Message: fmt.Sprintf("generic hook_event %q overlaps the subtype hook(s) %s; a generic hook already covers the subtype, so pick one level of specificity",
				rec.Artifact.HookEvent, strings.Join(labels, ", ")),
		})
	}
	return out
}

type ruleEffortHintAppliesToType struct{}

func (ruleEffortHintAppliesToType) Code() string { return "lint.hint_on_unsupported_type" }

func (r ruleEffortHintAppliesToType) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
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
