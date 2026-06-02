package lint

import (
	"context"
	"fmt"
	"strings"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// ruleTypeRequiredFields enforces the per-type required fields that §4.3
// documents but ruleRequiredFields (universal type/version) does not cover:
//
//   - type: rule, rule_mode: glob requires rule_globs (§4.3 rule_mode table).
//   - type: rule, rule_mode: auto requires rule_description (§4.3 rule_mode
//     table).
//   - type: hook requires hook_event and hook_action (§4.3 hook schema).
//
// Each missing field is an ingest error, since the harness adapter cannot
// materialize the artifact without it.
type ruleTypeRequiredFields struct{}

func (ruleTypeRequiredFields) Code() string { return "lint.required_field_missing" }

func (r ruleTypeRequiredFields) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		a := rec.Artifact
		if a == nil {
			continue
		}
		switch a.Type {
		case manifest.TypeRule:
			switch a.RuleMode {
			case manifest.RuleModeGlob:
				if a.RuleGlobs == "" {
					out = append(out, errMsg(rec.ID, r, "rule_mode: glob requires rule_globs"))
				}
			case manifest.RuleModeAuto:
				if a.RuleDescription == "" {
					out = append(out, errMsg(rec.ID, r, "rule_mode: auto requires rule_description"))
				}
			}
		case manifest.TypeHook:
			if a.HookEvent == "" {
				out = append(out, errMsg(rec.ID, r, "type: hook requires hook_event"))
			}
			if a.HookAction == "" {
				out = append(out, errMsg(rec.ID, r, "type: hook requires hook_action"))
			}
		}
	}
	return out
}

// ruleRuleModeHygiene emits the advisory warnings for rule_mode misuse that
// §4.3 implies and docs/authoring/rule-modes.md § "Lint behavior" states:
//
//   - rule_mode: glob with rule_description set (rule_description is ignored).
//   - rule_mode: auto with rule_globs set (rule_globs is ignored).
//   - rule_mode set on a type other than rule (the field is type: rule only).
//
// These are warnings: the artifact still materializes, but a field it carries
// has no effect.
type ruleRuleModeHygiene struct{}

func (ruleRuleModeHygiene) Code() string { return "lint.rule_mode" }

func (r ruleRuleModeHygiene) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		a := rec.Artifact
		if a == nil || a.RuleMode == "" {
			continue
		}
		if a.Type != manifest.TypeRule {
			out = append(out, warn(rec.ID, "lint.rule_mode_on_non_rule",
				"rule-mode is only applicable to type: rule"))
			continue
		}
		switch a.RuleMode {
		case manifest.RuleModeGlob:
			if a.RuleDescription != "" {
				out = append(out, warn(rec.ID, "lint.ignored_companion_field",
					"rule-mode 'glob' uses globs only; rule-description is ignored"))
			}
		case manifest.RuleModeAuto:
			if a.RuleGlobs != "" {
				out = append(out, warn(rec.ID, "lint.ignored_companion_field",
					"rule-mode 'auto' uses description only; rule-globs is ignored"))
			}
		}
	}
	return out
}

// ruleRuleModeCanonical enforces spec 04-artifact-model.md §4.3: a
// type: rule artifact's rule_mode is constrained to the closed
// enumeration "always | glob | auto | explicit". An unrecognized value
// (for example rule_mode: sometimes) is an ingest error, since no harness
// adapter has a native mapping for it and the Cursor adapter would emit a
// rule with no auto-apply key. An empty rule_mode is left to the §4.3
// default (always); this rule only rejects a non-empty value outside the
// canonical set, mirroring ruleHookEventCanonical for hook_event.
type ruleRuleModeCanonical struct{}

func (ruleRuleModeCanonical) Code() string { return "lint.unknown_rule_mode" }

func (r ruleRuleModeCanonical) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		a := rec.Artifact
		if a == nil || a.Type != manifest.TypeRule {
			continue
		}
		if a.RuleMode == "" || manifest.IsCanonicalRuleMode(a.RuleMode) {
			continue
		}
		out = append(out, errMsg(rec.ID, r,
			fmt.Sprintf("rule_mode %q is not a canonical §4.3 value; valid modes: %s",
				a.RuleMode, strings.Join(manifest.CanonicalRuleModes(), ", "))))
	}
	return out
}
