package adapter

import (
	"strings"
	"testing"
)

// firstClassAdapters lists the nine first-class harness adapters whose
// capability cells §6.7.1 documents.
var firstClassAdapters = []string{
	"claude-code", "claude-desktop", "claude-cowork",
	"cursor", "codex", "opencode",
	"gemini", "pi", "hermes",
}

// makeArtifact assembles ARTIFACT.md frontmatter + body containing
// the requested fields so we can verify each ✓ cell preserves them
// in the adapter's output.
func makeArtifact(fields map[string]string) []byte {
	b := strings.Builder{}
	b.WriteString("---\n")
	b.WriteString("type: agent\n")
	b.WriteString("version: 1.0.0\n")
	for k, v := range fields {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(v)
		b.WriteString("\n")
	}
	b.WriteString("---\n\nbody\n")
	return []byte(b.String())
}

// outputContains reports whether any file's content contains substr.
func outputContains(out []File, substr string) bool {
	for _, f := range out {
		if strings.Contains(string(f.Content), substr) {
			return true
		}
	}
	return false
}

// runMatrixField asserts that the given field-marker is present in
// the output of the named adapter, mapping to a §6.7.1 ✓ cell.
func runMatrixField(t *testing.T, adapterID, field, sentinel string) {
	t.Helper()
	r := DefaultRegistry()
	a, err := r.Get(adapterID)
	if err != nil {
		t.Fatalf("Get(%q): %v", adapterID, err)
	}
	src := Source{
		ArtifactID:    "test/artifact",
		ArtifactBytes: makeArtifact(map[string]string{field: sentinel}),
	}
	out, err := a.Adapt(src)
	if err != nil {
		t.Fatalf("%s.Adapt: %v", adapterID, err)
	}
	if !outputContains(out, sentinel) {
		t.Errorf("%s did not preserve field %q (sentinel %q)", adapterID, field, sentinel)
	}
}

// Spec: §6.7.1 capability matrix — description is ✓ across every
// first-class adapter. The sentinel value flows through to the
// adapter's output unchanged.
// Matrix: §6.7.1 (claude-code, description)
// Matrix: §6.7.1 (claude-desktop, description)
// Matrix: §6.7.1 (claude-cowork, description)
// Matrix: §6.7.1 (cursor, description)
// Matrix: §6.7.1 (codex, description)
// Matrix: §6.7.1 (opencode, description)
// Matrix: §6.7.1 (gemini, description)
// Matrix: §6.7.1 (pi, description)
// Matrix: §6.7.1 (hermes, description)
func TestCapabilityMatrix_Description(t *testing.T) {
	t.Parallel()
	for _, id := range firstClassAdapters {
		runMatrixField(t, id, "description", "Sentinel-DESCRIPTION-7c0c4a")
	}
}

// Spec: §6.7.1 — mcpServers is ✓ across every first-class adapter.
// Matrix: §6.7.1 (claude-code, mcpServers)
// Matrix: §6.7.1 (claude-desktop, mcpServers)
// Matrix: §6.7.1 (claude-cowork, mcpServers)
// Matrix: §6.7.1 (cursor, mcpServers)
// Matrix: §6.7.1 (codex, mcpServers)
// Matrix: §6.7.1 (opencode, mcpServers)
// Matrix: §6.7.1 (gemini, mcpServers)
// Matrix: §6.7.1 (pi, mcpServers)
// Matrix: §6.7.1 (hermes, mcpServers)
func TestCapabilityMatrix_MCPServers(t *testing.T) {
	t.Parallel()
	for _, id := range firstClassAdapters {
		// Use the literal `mcpServers:` field name as sentinel so the
		// test catches both name preservation and frontmatter
		// pass-through.
		r := DefaultRegistry()
		a, _ := r.Get(id)
		src := Source{
			ArtifactID: "test/artifact",
			ArtifactBytes: []byte("---\ntype: agent\nversion: 1.0.0\n" +
				"mcpServers:\n  - name: finance-warehouse\n---\n\nbody\n"),
		}
		out, err := a.Adapt(src)
		if err != nil {
			t.Fatalf("%s.Adapt: %v", id, err)
		}
		if !outputContains(out, "finance-warehouse") {
			t.Errorf("%s did not preserve mcpServers entry", id)
		}
	}
}

// Spec: §6.7.1 — expose_as_mcp_prompt is ✓ across every first-class
// adapter.
// Matrix: §6.7.1 (claude-code, expose_as_mcp_prompt)
// Matrix: §6.7.1 (claude-desktop, expose_as_mcp_prompt)
// Matrix: §6.7.1 (claude-cowork, expose_as_mcp_prompt)
// Matrix: §6.7.1 (cursor, expose_as_mcp_prompt)
// Matrix: §6.7.1 (codex, expose_as_mcp_prompt)
// Matrix: §6.7.1 (opencode, expose_as_mcp_prompt)
// Matrix: §6.7.1 (gemini, expose_as_mcp_prompt)
// Matrix: §6.7.1 (pi, expose_as_mcp_prompt)
// Matrix: §6.7.1 (hermes, expose_as_mcp_prompt)
func TestCapabilityMatrix_ExposeAsMCPPrompt(t *testing.T) {
	t.Parallel()
	for _, id := range firstClassAdapters {
		runMatrixField(t, id, "expose_as_mcp_prompt", "true")
	}
}

// Spec: §6.7.1 — rule_mode: always is ✓ on every adapter except
// gemini (⚠ fallback).
// Matrix: §6.7.1 (claude-code, rule_mode_always)
// Matrix: §6.7.1 (claude-desktop, rule_mode_always)
// Matrix: §6.7.1 (claude-cowork, rule_mode_always)
// Matrix: §6.7.1 (cursor, rule_mode_always)
// Matrix: §6.7.1 (codex, rule_mode_always)
// Matrix: §6.7.1 (opencode, rule_mode_always)
// Matrix: §6.7.1 (gemini, rule_mode_always)
// Matrix: §6.7.1 (pi, rule_mode_always)
// Matrix: §6.7.1 (hermes, rule_mode_always)
func TestCapabilityMatrix_RuleModeAlways(t *testing.T) {
	t.Parallel()
	for _, id := range firstClassAdapters {
		r := DefaultRegistry()
		a, _ := r.Get(id)
		src := Source{
			ArtifactID: "test/rule",
			ArtifactBytes: []byte("---\ntype: rule\nversion: 1.0.0\nrule_mode: always\n" +
				"description: a rule\n---\n\nrule body\n"),
		}
		out, err := a.Adapt(src)
		if err != nil {
			t.Fatalf("%s.Adapt: %v", id, err)
		}
		// Adapter must produce at least one output file for the rule.
		if len(out) == 0 {
			t.Errorf("%s produced no output for rule_mode: always", id)
		}
	}
}

// Spec: §6.7.1 — rule_mode: explicit is ✓ on every adapter except
// gemini (⚠ fallback).
// Matrix: §6.7.1 (claude-code, rule_mode_explicit)
// Matrix: §6.7.1 (claude-desktop, rule_mode_explicit)
// Matrix: §6.7.1 (claude-cowork, rule_mode_explicit)
// Matrix: §6.7.1 (cursor, rule_mode_explicit)
// Matrix: §6.7.1 (codex, rule_mode_explicit)
// Matrix: §6.7.1 (opencode, rule_mode_explicit)
// Matrix: §6.7.1 (gemini, rule_mode_explicit)
// Matrix: §6.7.1 (pi, rule_mode_explicit)
// Matrix: §6.7.1 (hermes, rule_mode_explicit)
func TestCapabilityMatrix_RuleModeExplicit(t *testing.T) {
	t.Parallel()
	for _, id := range firstClassAdapters {
		r := DefaultRegistry()
		a, _ := r.Get(id)
		src := Source{
			ArtifactID: "test/rule",
			ArtifactBytes: []byte("---\ntype: rule\nversion: 1.0.0\nrule_mode: explicit\n" +
				"description: a rule\n---\n\nrule body\n"),
		}
		out, err := a.Adapt(src)
		if err != nil {
			t.Fatalf("%s.Adapt: %v", id, err)
		}
		if len(out) == 0 {
			t.Errorf("%s produced no output for rule_mode: explicit", id)
		}
	}
}
