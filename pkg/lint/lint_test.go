package lint

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/typeprovider"
)

func openFixture(t *testing.T, opts ...testharness.WriteTreeOption) (*filesystem.Registry, []filesystem.ArtifactRecord) {
	t.Helper()
	root := t.TempDir()
	testharness.WriteTree(t, root, opts...)
	reg, err := filesystem.Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	records, err := reg.Walk(filesystem.WalkOptions{
		CollisionPolicy: filesystem.CollisionPolicyHighestWins,
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	return reg, records
}

// Spec: §4.3 universal fields — type and version are required; missing
// fields produce a lint.required_field_missing error.
func TestLint_RequiredFieldsMissing(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t,
		testharness.WriteTreeOption{
			Path: "x/ARTIFACT.md",
			Content: `---
description: missing type and version
---

body
`,
		},
	)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	wantCodes := []string{
		"lint.required_field_missing",
		"lint.required_field_missing",
	}
	gotCodes := codesOf(diags)
	if len(gotCodes) < 2 {
		t.Fatalf("want at least 2 diagnostics, got %d: %v", len(gotCodes), diags)
	}
	for _, want := range wantCodes {
		if !contains(gotCodes, want) {
			t.Errorf("missing diagnostic code %q in %v", want, gotCodes)
		}
	}
}

// Spec: §4.3.4 SKILL.md compliance — type: skill artifact missing SKILL.md
// fails Walk before lint runs (Phase 0); a skill with SKILL.md whose name
// does not match the parent directory triggers lint.skill_md_compliance.
func TestLint_SkillNameMustMatchDirectory(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t,
		testharness.WriteTreeOption{
			Path: "greetings/hello/ARTIFACT.md",
			Content: `---
type: skill
version: 1.0.0
---

`,
		},
		testharness.WriteTreeOption{
			Path: "greetings/hello/SKILL.md",
			Content: `---
name: not-hello
description: Mismatched name.
---

body
`,
		},
	)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if !hasCode(diags, "lint.skill_md_compliance") {
		t.Errorf("expected lint.skill_md_compliance, got: %v", diags)
	}
}

// Spec: §4.3.4 — invalid SKILL.md name (uppercase / underscore) triggers
// lint.invalid_name.
func TestLint_InvalidNameSyntax(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t,
		testharness.WriteTreeOption{
			Path: "greetings/Bad_Name/ARTIFACT.md",
			Content: `---
type: skill
version: 1.0.0
---

`,
		},
		testharness.WriteTreeOption{
			Path: "greetings/Bad_Name/SKILL.md",
			Content: `---
name: Bad_Name
description: nope
---

body
`,
		},
	)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if !hasCode(diags, "lint.invalid_name") {
		t.Errorf("expected lint.invalid_name, got: %v", diags)
	}
}

// Spec: §4.7.6 — version must be valid semver.
func TestLint_InvalidSemverVersion(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t,
		testharness.WriteTreeOption{
			Path: "x/ARTIFACT.md",
			Content: `---
type: context
version: v1
description: invalid version
---

body
`,
		},
	)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if !hasCode(diags, "lint.invalid_version") {
		t.Errorf("expected lint.invalid_version, got: %v", diags)
	}
}

// Spec: §4.3 — effort_hint and model_class_hint apply only to types
// agent, skill, and command. Setting them on context, rule, hook, or
// mcp-server emits a warning.
func TestLint_EffortHintWarnsOnUnsupportedType(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t,
		testharness.WriteTreeOption{
			Path: "x/ARTIFACT.md",
			Content: `---
type: context
version: 1.0.0
description: context with hints
effort_hint: high
---

body
`,
		},
	)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if !hasCode(diags, "lint.hint_on_unsupported_type") {
		t.Errorf("expected lint.hint_on_unsupported_type, got: %v", diags)
	}
	if !hasSeverity(diags, "lint.hint_on_unsupported_type", SeverityWarning) {
		t.Errorf("expected severity warning")
	}
}

// Spec: §4.1 — extension types (e.g., dataset, model, eval) require a
// TypeProvider; in Phase 1 they trigger a lint.unknown_type warning.
func TestLint_UnknownTypeWarning(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t,
		testharness.WriteTreeOption{
			Path: "x/ARTIFACT.md",
			Content: `---
type: dataset
version: 1.0.0
description: extension type
---

body
`,
		},
	)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if !hasCode(diags, "lint.unknown_type") {
		t.Errorf("expected lint.unknown_type, got: %v", diags)
	}
}

// Spec: §4.1 — mcp-server is a built-in extension type
// registered in the default TypeProvider registry, so it lints without a
// lint.unknown_type warning even though IsFirstClassType is false for it.
func TestLint_MCPServerDoesNotWarnUnknownType(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t,
		testharness.WriteTreeOption{
			Path: "m/ARTIFACT.md",
			Content: `---
type: mcp-server
version: 1.0.0
description: server registration
---

body
`,
		},
	)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	for _, d := range diags {
		if d.Code == "lint.unknown_type" {
			t.Errorf("mcp-server must not warn unknown_type: %v", d)
		}
	}
}

// datasetProvider is a stand-in for a deployment-registered extension
// TypeProvider whose Validate contributes a type-specific lint rule.
type datasetProvider struct{}

func (datasetProvider) ID() string                  { return "dataset" }
func (datasetProvider) Type() manifest.ArtifactType { return "dataset" }
func (datasetProvider) Validate(context.Context, *manifest.Artifact) []typeprovider.Diagnostic {
	return []typeprovider.Diagnostic{{
		Severity: "warn",
		Code:     "dataset.needs-rows",
		Message:  "dataset requires a row count",
	}}
}

// Spec: §4.1 / §9 — when a deployment registers a TypeProvider
// for an extension type, lint stops warning lint.unknown_type for it and
// dispatches to the provider's Validate so the extension's lint rules run
// at ingest.
func TestLint_RegisteredExtensionTypeSuppressesUnknownAndValidates(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t,
		testharness.WriteTreeOption{
			Path: "d/ARTIFACT.md",
			Content: `---
type: dataset
version: 1.0.0
description: extension type
---

body
`,
		},
	)
	providers := typeprovider.NewRegistry()
	if err := providers.Register(datasetProvider{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	l := &Linter{Rules: []Rule{
		ruleRequiredFields{providers: providers},
		ruleTypeProviderValidate{providers: providers},
	}}
	diags := l.Lint(context.Background(), reg, records)
	for _, d := range diags {
		if d.Code == "lint.unknown_type" {
			t.Errorf("a registered extension type must not warn unknown_type: %v", d)
		}
	}
	if !hasCode(diags, "dataset.needs-rows") {
		t.Errorf("expected the provider's dataset.needs-rows diagnostic, got: %v", diags)
	}
}

// Spec: §4.1 / §9 — an unregistered extension type still warns
// lint.unknown_type, because the deployment may register a provider for
// it; ingest is not rejected.
func TestLint_UnregisteredExtensionTypeStillWarns(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t,
		testharness.WriteTreeOption{
			Path: "d/ARTIFACT.md",
			Content: `---
type: dataset
version: 1.0.0
description: extension type
---

body
`,
		},
	)
	// Empty registry: no provider for dataset, no built-in types either.
	providers := typeprovider.NewRegistry()
	l := &Linter{Rules: []Rule{
		ruleRequiredFields{providers: providers},
		ruleTypeProviderValidate{providers: providers},
	}}
	diags := l.Lint(context.Background(), reg, records)
	if !hasCode(diags, "lint.unknown_type") {
		t.Errorf("expected lint.unknown_type for an unregistered type, got: %v", diags)
	}
	for _, d := range diags {
		if d.Severity == SeverityError {
			t.Errorf("unknown type must warn, not error: %v", d)
		}
	}
}

// Spec: §4.3.5 — a lone generic hook is valid ("Authors choose the
// level of specificity that matches the action's intent") and draws no
// generic/subtype diagnostic. The pre-fix rule wrongly flagged every generic
// hook at info severity.
func TestLint_HookLoneGenericClean(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t,
		testharness.WriteTreeOption{
			Path: "x/ARTIFACT.md",
			Content: `---
type: hook
version: 1.0.0
description: a hook
hook_event: pre_tool_use
hook_action: |
  echo hook
---

body
`,
		},
	)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if hasCode(diags, "lint.hook_generic_and_subtype") {
		t.Errorf("a lone generic hook must not draw the generic/subtype diagnostic: %v", diags)
	}
}

// Spec: §4.3.5 — "Authors should not declare both a generic hook and
// the corresponding subtype hook ...; lint warns when this happens." Because
// hook_event is a single scalar, the reachable form is a generic hook and a
// corresponding subtype hook present together; the rule warns (not info) and
// names the overlapping subtype.
func TestLint_HookGenericAndSubtypeWarns(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t,
		testharness.WriteTreeOption{
			Path: "hooks/broad/ARTIFACT.md",
			Content: `---
type: hook
version: 1.0.0
description: broad
hook_event: pre_tool_use
hook_action: |
  echo broad
---

body
`,
		},
		testharness.WriteTreeOption{
			Path: "hooks/narrow/ARTIFACT.md",
			Content: `---
type: hook
version: 1.0.0
description: narrow
hook_event: pre_shell_execution
hook_action: |
  echo narrow
---

body
`,
		},
	)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if !hasSeverity(diags, "lint.hook_generic_and_subtype", SeverityWarning) {
		t.Fatalf("expected a hook_generic_and_subtype warning, got: %v", diags)
	}
	var named bool
	for _, d := range diags {
		if d.Code == "lint.hook_generic_and_subtype" && strings.Contains(d.Message, "pre_shell_execution") {
			named = true
		}
		if d.Code == "lint.hook_generic_and_subtype" && d.Severity == SeverityInfo {
			t.Errorf("severity must be warning, not info: %v", d)
		}
	}
	if !named {
		t.Errorf("warning should name the overlapping subtype pre_shell_execution: %v", diags)
	}
}

// Spec: §4.3.5 — a type: hook artifact whose hook_event is not
// in the canonical taxonomy (here the misspelling on_stop) is rejected
// with a lint.unknown_hook_event error. Without the rule the artifact
// passes ingest.
func TestLint_HookEventUnknownErrors(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t,
		testharness.WriteTreeOption{
			Path: "x/bad-hook/ARTIFACT.md",
			Content: `---
type: hook
version: 1.0.0
description: a hook
hook_event: on_stop
hook_action: |
  echo hook
---

body
`,
		},
	)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	var gotErr bool
	for _, d := range diags {
		if d.Code == "lint.unknown_hook_event" {
			if d.Severity != SeverityError {
				t.Errorf("unknown hook_event must error, got severity %q", d.Severity)
			}
			if !strings.Contains(d.Message, "on_stop") {
				t.Errorf("message should name the offending event: %s", d.Message)
			}
			gotErr = true
		}
	}
	if !gotErr {
		t.Errorf("expected lint.unknown_hook_event for hook_event: on_stop, got: %v", diags)
	}
}

// Spec: §4.3.5 — every canonical event name passes the
// hook_event check. Each event is its own single-artifact fixture, so no
// generic/subtype overlap arises and no unknown-event error is raised.
func TestLint_HookEventCanonicalAccepted(t *testing.T) {
	t.Parallel()
	for _, event := range manifest.CanonicalHookEvents() {
		event := event
		t.Run(event, func(t *testing.T) {
			t.Parallel()
			reg, records := openFixture(t,
				testharness.WriteTreeOption{
					Path: "hooks/" + event + "/ARTIFACT.md",
					Content: `---
type: hook
version: 1.0.0
description: a hook
hook_event: ` + event + `
hook_action: |
  echo hook
---

body
`,
				},
			)
			diags := (&Linter{}).Lint(context.Background(), reg, records)
			if hasCode(diags, "lint.unknown_hook_event") {
				t.Errorf("canonical event %q must not trigger lint.unknown_hook_event: %v", event, diags)
			}
		})
	}
}

// Spec: §4.3.5 — the canonical-event rule applies only to
// type: hook. A non-hook artifact that happens to carry a hook_event
// value is not the rule's concern (the field is type-specific to hooks),
// so no unknown-event error is raised.
func TestLint_HookEventIgnoredForNonHook(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t,
		testharness.WriteTreeOption{
			Path: "ctx/note/ARTIFACT.md",
			Content: `---
type: context
version: 1.0.0
hook_event: totally-made-up
---

body
`,
		},
	)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	if hasCode(diags, "lint.unknown_hook_event") {
		t.Errorf("non-hook artifact must not trigger lint.unknown_hook_event: %v", diags)
	}
}

// Spec: §4.3 — a clean fixture with all required fields produces no
// diagnostics.
func TestLint_CleanArtifactProducesNoDiagnostics(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t,
		testharness.WriteTreeOption{
			Path: "greetings/hello/ARTIFACT.md",
			Content: `---
type: skill
version: 1.0.0
---

`,
		},
		testharness.WriteTreeOption{
			Path: "greetings/hello/SKILL.md",
			Content: `---
name: hello
description: Greet the user.
license: MIT
---

Body.
`,
		},
	)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	for _, d := range diags {
		if d.Severity == SeverityError {
			t.Errorf("unexpected error diagnostic: %s", d)
		}
	}
}

// Spec: §4.3 — lint output is sorted deterministically by artifact ID
// then code so CLI golden output and SIEM-side diffing remain stable.
func TestLint_OutputIsDeterministic(t *testing.T) {
	t.Parallel()
	reg, records := openFixture(t,
		testharness.WriteTreeOption{
			Path: "z/ARTIFACT.md",
			Content: `---
type: agent
version: notsemver
---

body
`,
		},
		testharness.WriteTreeOption{
			Path: "a/ARTIFACT.md",
			Content: `---
type: agent
version: 1.0.0
sensitivity: medium
description: needs sbom
---

body
`,
		},
	)
	diags := (&Linter{}).Lint(context.Background(), reg, records)
	for i := 1; i < len(diags); i++ {
		prev, cur := diags[i-1], diags[i]
		if prev.ArtifactID > cur.ArtifactID {
			t.Errorf("diagnostics not sorted by artifact ID: %s before %s",
				prev.ArtifactID, cur.ArtifactID)
		}
		if prev.ArtifactID == cur.ArtifactID && prev.Code > cur.Code {
			t.Errorf("diagnostics not sorted by code within artifact %s",
				prev.ArtifactID)
		}
	}
}

// Spec: §4.3 — Diagnostic.String renders severity, ID, message, and code so
// CLI output is grep-able.
func TestDiagnostic_StringIncludesAllFields(t *testing.T) {
	t.Parallel()
	d := Diagnostic{
		ArtifactID: "x/y",
		Code:       "lint.example",
		Severity:   SeverityError,
		Message:    "msg",
	}
	s := d.String()
	for _, want := range []string{"x/y", "lint.example", "msg", "error"} {
		if !strings.Contains(s, want) {
			t.Errorf("String() missing %q: %s", want, s)
		}
	}
}

func codesOf(diags []Diagnostic) []string {
	out := make([]string, len(diags))
	for i, d := range diags {
		out[i] = d.Code
	}
	return out
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func hasCode(diags []Diagnostic, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

func hasSeverity(diags []Diagnostic, code string, sev Severity) bool {
	for _, d := range diags {
		if d.Code == code && d.Severity == sev {
			return true
		}
	}
	return false
}
