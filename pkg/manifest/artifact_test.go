package manifest

import (
	"errors"
	"testing"
)

// Spec: §4.3 Artifact Manifest Schema — the canonical universal fields
// (type, name, version, description, when_to_use, tags, sensitivity,
// license) round-trip through ParseArtifact.
// Phase: 0
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
// Phase: 0
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
// Phase: 0
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
// Phase: 0
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
// Phase: 0
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

// Spec: §4.3 inheritance — extends: <id>@<semver> is preserved verbatim.
// Phase: 0
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

// Spec: §4.1 first-class types — IsFirstClassType reports true for each
// of the seven canonical types.
// Phase: 0
func TestIsFirstClassType_AllSevenAreRecognized(t *testing.T) {
	t.Parallel()
	for _, ty := range []ArtifactType{
		TypeSkill, TypeAgent, TypeContext, TypeCommand,
		TypeRule, TypeHook, TypeMCPServer,
	} {
		if !IsFirstClassType(ty) {
			t.Errorf("%q should be first-class", ty)
		}
	}
	if IsFirstClassType("workflow") {
		t.Errorf("workflow is reserved but not first-class")
	}
	if IsFirstClassType("dataset") {
		t.Errorf("dataset is an extension type, not first-class")
	}
}
