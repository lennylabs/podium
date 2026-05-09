package lint

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
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
// Phase: 1
func TestLint_RequiredFieldsMissing(t *testing.T) {
	testharness.RequirePhase(t, 1)
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
	diags := (&Linter{}).Lint(reg, records)
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
// Phase: 1
func TestLint_SkillNameMustMatchDirectory(t *testing.T) {
	testharness.RequirePhase(t, 1)
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
	diags := (&Linter{}).Lint(reg, records)
	if !hasCode(diags, "lint.skill_md_compliance") {
		t.Errorf("expected lint.skill_md_compliance, got: %v", diags)
	}
}

// Spec: §4.3.4 — invalid SKILL.md name (uppercase / underscore) triggers
// lint.invalid_name.
// Phase: 1
func TestLint_InvalidNameSyntax(t *testing.T) {
	testharness.RequirePhase(t, 1)
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
	diags := (&Linter{}).Lint(reg, records)
	if !hasCode(diags, "lint.invalid_name") {
		t.Errorf("expected lint.invalid_name, got: %v", diags)
	}
}

// Spec: §4.7.6 — version must be valid semver.
// Phase: 1
func TestLint_InvalidSemverVersion(t *testing.T) {
	testharness.RequirePhase(t, 1)
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
	diags := (&Linter{}).Lint(reg, records)
	if !hasCode(diags, "lint.invalid_version") {
		t.Errorf("expected lint.invalid_version, got: %v", diags)
	}
}

// Spec: §4.3 — effort_hint and model_class_hint apply only to types
// agent, skill, and command. Setting them on context, rule, hook, or
// mcp-server emits a warning.
// Phase: 1
func TestLint_EffortHintWarnsOnUnsupportedType(t *testing.T) {
	testharness.RequirePhase(t, 1)
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
	diags := (&Linter{}).Lint(reg, records)
	if !hasCode(diags, "lint.hint_on_unsupported_type") {
		t.Errorf("expected lint.hint_on_unsupported_type, got: %v", diags)
	}
	if !hasSeverity(diags, "lint.hint_on_unsupported_type", SeverityWarning) {
		t.Errorf("expected severity warning")
	}
}

// Spec: §4.1 — extension types (e.g., dataset, model, eval) require a
// TypeProvider; in Phase 1 they trigger a lint.unknown_type warning.
// Phase: 1
func TestLint_UnknownTypeWarning(t *testing.T) {
	testharness.RequirePhase(t, 1)
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
	diags := (&Linter{}).Lint(reg, records)
	if !hasCode(diags, "lint.unknown_type") {
		t.Errorf("expected lint.unknown_type, got: %v", diags)
	}
}

// Spec: §4.3.5 — hook_event using a generic event (pre_tool_use) is
// allowed but lint emits an info-level note recommending the more
// specific subtype where possible.
// Phase: 1
func TestLint_HookGenericEventInfo(t *testing.T) {
	testharness.RequirePhase(t, 1)
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
	diags := (&Linter{}).Lint(reg, records)
	if !hasCode(diags, "lint.hook_generic_and_subtype") {
		t.Errorf("expected lint.hook_generic_and_subtype, got: %v", diags)
	}
}

// Spec: §4.3 — a clean fixture with all required fields produces no
// diagnostics.
// Phase: 1
func TestLint_CleanArtifactProducesNoDiagnostics(t *testing.T) {
	testharness.RequirePhase(t, 1)
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
	diags := (&Linter{}).Lint(reg, records)
	for _, d := range diags {
		if d.Severity == SeverityError {
			t.Errorf("unexpected error diagnostic: %s", d)
		}
	}
}

// Spec: §4.3 — lint output is sorted deterministically by artifact ID
// then code so CLI golden output and SIEM-side diffing remain stable.
// Phase: 1
func TestLint_OutputIsDeterministic(t *testing.T) {
	testharness.RequirePhase(t, 1)
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
	diags := (&Linter{}).Lint(reg, records)
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

// Spec: §4.3 — Diagnostic.String renders severity, ID, message, code, and
// rule so CLI output is grep-able.
// Phase: 1
func TestDiagnostic_StringIncludesAllFields(t *testing.T) {
	testharness.RequirePhase(t, 1)
	t.Parallel()
	d := Diagnostic{
		ArtifactID: "x/y",
		Code:       "lint.example",
		Severity:   SeverityError,
		Message:    "msg",
		Rule:       "§4.3",
	}
	s := d.String()
	for _, want := range []string{"x/y", "lint.example", "§4.3", "msg", "error"} {
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
