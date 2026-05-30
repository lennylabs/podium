package manifest

import (
	"errors"
	"testing"
)

// Spec: §4.3 Artifact Manifest Schema — the canonical universal fields
// (type, name, version, description, when_to_use, tags, sensitivity,
// license) round-trip through ParseArtifact.
func TestParseArtifact_UniversalFields(t *testing.T) {
	t.Parallel()
	src := []byte(`---
type: agent
name: pay-invoice
version: 1.2.0
description: Pay an approved vendor invoice.
when_to_use:
  - "When AP has approved an invoice for payment."
tags: [finance, ap]
sensitivity: medium
license: MIT
---

This is the agent body.
`)
	got, err := ParseArtifact(src)
	if err != nil {
		t.Fatalf("ParseArtifact: %v", err)
	}
	if got.Type != TypeAgent {
		t.Errorf("Type = %q, want %q", got.Type, TypeAgent)
	}
	if got.Name != "pay-invoice" {
		t.Errorf("Name = %q, want pay-invoice", got.Name)
	}
	if got.Version != "1.2.0" {
		t.Errorf("Version = %q, want 1.2.0", got.Version)
	}
	if got.Sensitivity != SensitivityMedium {
		t.Errorf("Sensitivity = %q, want medium", got.Sensitivity)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "finance" || got.Tags[1] != "ap" {
		t.Errorf("Tags = %v, want [finance ap]", got.Tags)
	}
	if got.Body == "" {
		t.Errorf("Body should be the prose body, got empty")
	}
}

// Spec: §4.3 Artifact Manifest Schema — missing frontmatter is rejected
// with ErrNoFrontmatter so callers can present a clear lint error.
func TestParseArtifact_NoFrontmatter(t *testing.T) {
	t.Parallel()
	src := []byte("just prose body, no frontmatter\n")
	_, err := ParseArtifact(src)
	if !errors.Is(err, ErrNoFrontmatter) {
		t.Fatalf("expected ErrNoFrontmatter, got %v", err)
	}
}

// Spec: §4.3 Artifact Manifest Schema — malformed YAML frontmatter is
// rejected with ErrInvalidYAML.
func TestParseArtifact_InvalidYAML(t *testing.T) {
	t.Parallel()
	src := []byte(`---
type: agent
name: bad
version: 1.0.0
tags: [unclosed list
---

body
`)
	_, err := ParseArtifact(src)
	if !errors.Is(err, ErrInvalidYAML) {
		t.Fatalf("expected ErrInvalidYAML, got %v", err)
	}
}

// Spec: §4.3 caller-interpreted fields — mcpServers list is preserved
// verbatim and reachable from the parsed Artifact.
func TestParseArtifact_MCPServersPreserved(t *testing.T) {
	t.Parallel()
	src := []byte(`---
type: agent
name: with-mcp
version: 1.0.0
mcpServers:
  - name: finance-warehouse
    transport: stdio
    command: npx
    args: ["-y", "@company/finance-warehouse-mcp"]
---

body
`)
	got, err := ParseArtifact(src)
	if err != nil {
		t.Fatalf("ParseArtifact: %v", err)
	}
	if len(got.MCPServers) != 1 {
		t.Fatalf("MCPServers len = %d, want 1", len(got.MCPServers))
	}
	if got.MCPServers[0].Name != "finance-warehouse" {
		t.Errorf("Name = %q, want finance-warehouse", got.MCPServers[0].Name)
	}
}

// Spec: §4.3 type-specific fields — for type: rule, rule_mode, rule_globs,
// and rule_description appear on the parsed Artifact.
func TestParseArtifact_RuleFields(t *testing.T) {
	t.Parallel()
	src := []byte(`---
type: rule
name: ts-rules
version: 1.0.0
rule_mode: glob
rule_globs: "src/**/*.ts,src/**/*.tsx"
---

Apply when working with TypeScript files.
`)
	got, err := ParseArtifact(src)
	if err != nil {
		t.Fatalf("ParseArtifact: %v", err)
	}
	if got.RuleMode != RuleModeGlob {
		t.Errorf("RuleMode = %q, want glob", got.RuleMode)
	}
	if got.RuleGlobs != "src/**/*.ts,src/**/*.tsx" {
		t.Errorf("RuleGlobs = %q, want match", got.RuleGlobs)
	}
}

// Spec: §4.3 type-specific fields (F-4.3.1) — for type: agent the
// documented input/output mapping form ({ $ref: ./schemas/input.json })
// decodes into SchemaRef.Ref. Before Input/Output were typed as
// *SchemaRef this manifest failed to parse with ErrInvalidYAML ("cannot
// unmarshal !!map into string").
func TestParseArtifact_InputOutputRefObjectForm(t *testing.T) {
	t.Parallel()
	src := []byte(`---
type: agent
name: run-variance-analysis
version: 1.0.0
input: { $ref: ./schemas/input.json }
output: { $ref: ./schemas/output.json }
---

agent body
`)
	got, err := ParseArtifact(src)
	if err != nil {
		t.Fatalf("ParseArtifact rejected the documented $ref form: %v", err)
	}
	if got.Input == nil || got.Input.Ref != "./schemas/input.json" {
		t.Errorf("Input = %+v, want Ref=./schemas/input.json", got.Input)
	}
	if got.Output == nil || got.Output.Ref != "./schemas/output.json" {
		t.Errorf("Output = %+v, want Ref=./schemas/output.json", got.Output)
	}
}

// Spec: §4.3 type-specific fields (F-4.3.1) — a bare scalar path is also
// accepted for input/output and populates Ref, so authors who omit the
// $ref wrapper are not rejected at parse time.
func TestParseArtifact_InputOutputRefScalarForm(t *testing.T) {
	t.Parallel()
	src := []byte(`---
type: agent
name: scalar-ref
version: 1.0.0
input: ./schemas/in.json
---

body
`)
	got, err := ParseArtifact(src)
	if err != nil {
		t.Fatalf("ParseArtifact: %v", err)
	}
	if got.Input == nil || got.Input.Ref != "./schemas/in.json" {
		t.Errorf("Input = %+v, want Ref=./schemas/in.json", got.Input)
	}
	if got.Output != nil {
		t.Errorf("Output = %+v, want nil when absent", got.Output)
	}
}

// Spec: §4.3 type-specific fields (F-4.3.1) — a malformed schema ref (a
// sequence rather than a scalar or { $ref: ... } mapping) is rejected with
// ErrInvalidYAML rather than silently accepted.
func TestParseArtifact_InputRefInvalidKind(t *testing.T) {
	t.Parallel()
	src := []byte(`---
type: agent
name: bad-ref
version: 1.0.0
input: [a, b]
---

body
`)
	_, err := ParseArtifact(src)
	if !errors.Is(err, ErrInvalidYAML) {
		t.Fatalf("expected ErrInvalidYAML for a sequence schema ref, got %v", err)
	}
}

// Spec: §4.3 inheritance — extends: <id>@<semver> is preserved verbatim.
func TestParseArtifact_Extends(t *testing.T) {
	t.Parallel()
	src := []byte(`---
type: skill
name: child
version: 2.0.0
extends: finance/ap/pay-invoice@1.2.0
---

extends parent.
`)
	got, err := ParseArtifact(src)
	if err != nil {
		t.Fatalf("ParseArtifact: %v", err)
	}
	if got.Extends != "finance/ap/pay-invoice@1.2.0" {
		t.Errorf("Extends = %q, want finance/ap/pay-invoice@1.2.0", got.Extends)
	}
}

// Spec: §4.3 target_harnesses (F-4.3.4) — an empty or absent list targets
// every harness; a non-empty list restricts materialization to the named
// harnesses (exact adapter-ID match).
func TestTargetsHarness(t *testing.T) {
	t.Parallel()
	if !TargetsHarness(nil, "claude-code") {
		t.Errorf("nil target_harnesses must target every harness")
	}
	if !TargetsHarness([]string{}, "cursor") {
		t.Errorf("empty target_harnesses must target every harness")
	}
	if !TargetsHarness([]string{"claude-code", "opencode"}, "opencode") {
		t.Errorf("a listed harness must be targeted")
	}
	if TargetsHarness([]string{"claude-code", "opencode"}, "cursor") {
		t.Errorf("an unlisted harness must not be targeted")
	}
	if TargetsHarness([]string{"claude-code"}, "") {
		t.Errorf("an empty harness id must not match a non-empty list")
	}
}

// Spec: §4.1 first-class types — IsFirstClassType reports true for the
// first-class types (skill, agent, context, command, rule, hook) and
// false for the built-in extension type mcp-server, which §4.1 lists
// separately under "Built-in extension types".
func TestIsFirstClassType(t *testing.T) {
	t.Parallel()
	for _, ty := range []ArtifactType{
		TypeSkill, TypeAgent, TypeContext, TypeCommand, TypeRule, TypeHook,
	} {
		if !IsFirstClassType(ty) {
			t.Errorf("%q should be first-class", ty)
		}
	}
	if IsFirstClassType(TypeMCPServer) {
		t.Errorf("mcp-server is a built-in extension type, not first-class")
	}
	if IsFirstClassType("workflow") {
		t.Errorf("workflow is reserved but not first-class")
	}
	if IsFirstClassType("dataset") {
		t.Errorf("dataset is an extension type, not first-class")
	}
}

// Spec: §4.1 built-in extension types — IsBuiltinExtensionType reports
// true only for mcp-server, and false for first-class and unregistered
// types.
func TestIsBuiltinExtensionType(t *testing.T) {
	t.Parallel()
	if !IsBuiltinExtensionType(TypeMCPServer) {
		t.Errorf("mcp-server should be a built-in extension type")
	}
	for _, ty := range []ArtifactType{
		TypeSkill, TypeAgent, TypeContext, TypeCommand, TypeRule, TypeHook, "dataset", "",
	} {
		if IsBuiltinExtensionType(ty) {
			t.Errorf("%q should not be a built-in extension type", ty)
		}
	}
}

// Spec: §4.1 type taxonomy — FirstClassTypes and BuiltinExtensionTypes
// are disjoint, each agrees with its predicate, and the accessors return
// copies the caller cannot use to mutate package state.
func TestTypeTaxonomyLists(t *testing.T) {
	t.Parallel()
	fc := FirstClassTypes()
	for _, ty := range fc {
		if !IsFirstClassType(ty) {
			t.Errorf("FirstClassTypes lists %q but IsFirstClassType is false", ty)
		}
		if IsBuiltinExtensionType(ty) {
			t.Errorf("%q appears in both the first-class and built-in extension sets", ty)
		}
	}
	be := BuiltinExtensionTypes()
	if len(be) != 1 || be[0] != TypeMCPServer {
		t.Fatalf("BuiltinExtensionTypes = %v, want [mcp-server]", be)
	}
	// Mutating the returned slice must not affect a subsequent call.
	fc[0] = "mutated"
	if FirstClassTypes()[0] == "mutated" {
		t.Errorf("FirstClassTypes returned a shared backing array")
	}
}
