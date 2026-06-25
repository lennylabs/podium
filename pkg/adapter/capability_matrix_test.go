package adapter

import (
	"context"
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

// makeArtifact assembles ARTIFACT.md frontmatter + body containing the
// requested fields, on a type: agent carrier, for the frontmatter-field cells.
func makeArtifact(fields map[string]string) []byte {
	b := strings.Builder{}
	b.WriteString("---\ntype: agent\nversion: 1.0.0\n")
	for k, v := range fields {
		b.WriteString(k + ": " + v + "\n")
	}
	b.WriteString("---\n\nbody\n")
	return []byte(b.String())
}

func outputContains(out []File, substr string) bool {
	for _, f := range out {
		if strings.Contains(string(f.Content), substr) {
			return true
		}
	}
	return false
}

func adaptSrc(t *testing.T, adapterID string, src Source) []File {
	t.Helper()
	a, err := DefaultRegistry().Get(adapterID)
	if err != nil {
		t.Fatalf("Get(%q): %v", adapterID, err)
	}
	out, err := a.Adapt(context.Background(), src)
	if err != nil {
		t.Fatalf("%s.Adapt: %v", adapterID, err)
	}
	return out
}

// assertFieldCell checks a frontmatter-field cell: the marker (field value)
// survives the adapter's output for an N or F grade, and is absent for an X
// grade (dropped in translation, or the carrier type did not materialize).
func assertFieldCell(t *testing.T, adapterID string, cap Capability, frontmatter, marker string) {
	t.Helper()
	out := adaptSrc(t, adapterID, Source{ArtifactID: "test/cell", ArtifactBytes: []byte(frontmatter)})
	grade, ok := Cell(cap, adapterID)
	if !ok {
		t.Fatalf("%s: %v not graded", adapterID, cap)
	}
	present := outputContains(out, marker)
	if grade == SupportUnsupported {
		if present {
			t.Errorf("%s %v: grade ✗ but marker %q present", adapterID, cap, marker)
		}
		return
	}
	if !present {
		t.Errorf("%s %v: grade %v but marker %q absent", adapterID, cap, grade, marker)
	}
}

// assertTypeCell checks a type or rule_mode cell: the adapter produces output
// for an N or F grade and none for an X grade.
func assertTypeCell(t *testing.T, adapterID string, cap Capability, src Source) {
	t.Helper()
	out := adaptSrc(t, adapterID, src)
	grade, ok := Cell(cap, adapterID)
	if !ok {
		t.Fatalf("%s: %v not graded", adapterID, cap)
	}
	if grade == SupportUnsupported {
		if len(out) > 0 {
			t.Errorf("%s %v: grade ✗ but produced %d files", adapterID, cap, len(out))
		}
		return
	}
	if len(out) == 0 {
		t.Errorf("%s %v: grade %v but produced no output", adapterID, cap, grade)
	}
}

// typeSource builds a minimal valid artifact of the given type.
func typeSource(artType string) Source {
	id := "test/" + artType
	switch artType {
	case "skill":
		return Source{
			ArtifactID:    id,
			ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\n---\n"),
			SkillBytes:    []byte("---\nname: " + artType + "\ndescription: a skill\n---\n\nbody\n"),
		}
	case "mcp-server":
		return Source{
			ArtifactID:    id,
			ArtifactBytes: []byte("---\ntype: mcp-server\nversion: 1.0.0\nserver_identifier: npx:@acme/srv\n---\n\nbody\n"),
		}
	default:
		return Source{
			ArtifactID:    id,
			ArtifactBytes: []byte("---\ntype: " + artType + "\nversion: 1.0.0\ndescription: an artifact\n---\n\nbody\n"),
		}
	}
}

// --- type materialization (§6.7) ---------------------------------------------

func TestCapabilityMatrix_Types(t *testing.T) {
	t.Parallel()
	for _, ty := range []string{"skill", "agent", "context", "command", "mcp-server"} {
		src := typeSource(ty)
		for _, id := range firstClassAdapters {
			assertTypeCell(t, id, Capability{Field: "type", Value: ty}, src)
		}
	}
}

// --- frontmatter-field fidelity (carried on a type: agent) -------------------

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
	fm := string(makeArtifact(map[string]string{"description": "Sentinel-DESCRIPTION-7c0c4a"}))
	for _, id := range firstClassAdapters {
		assertFieldCell(t, id, Capability{Field: "description"}, fm, "Sentinel-DESCRIPTION-7c0c4a")
	}
}

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
	const fm = "---\ntype: agent\nversion: 1.0.0\n" +
		"mcpServers:\n  - name: finance-warehouse\n---\n\nbody\n"
	for _, id := range firstClassAdapters {
		assertFieldCell(t, id, Capability{Field: "mcpServers"}, fm, "finance-warehouse")
	}
}

// Matrix: §6.7.1 (claude-code, delegates_to)
// Matrix: §6.7.1 (claude-desktop, delegates_to)
// Matrix: §6.7.1 (claude-cowork, delegates_to)
// Matrix: §6.7.1 (cursor, delegates_to)
// Matrix: §6.7.1 (codex, delegates_to)
// Matrix: §6.7.1 (opencode, delegates_to)
// Matrix: §6.7.1 (gemini, delegates_to)
// Matrix: §6.7.1 (pi, delegates_to)
// Matrix: §6.7.1 (hermes, delegates_to)
func TestCapabilityMatrix_DelegatesTo(t *testing.T) {
	t.Parallel()
	const fm = "---\ntype: agent\nversion: 1.0.0\n" +
		"delegates_to:\n  - finance/sub-agent@1.x\n---\n\nbody\n"
	for _, id := range firstClassAdapters {
		assertFieldCell(t, id, Capability{Field: "delegates_to"}, fm, "finance/sub-agent")
	}
}

// Matrix: §6.7.1 (claude-code, requiresApproval)
// Matrix: §6.7.1 (claude-desktop, requiresApproval)
// Matrix: §6.7.1 (claude-cowork, requiresApproval)
// Matrix: §6.7.1 (cursor, requiresApproval)
// Matrix: §6.7.1 (codex, requiresApproval)
// Matrix: §6.7.1 (opencode, requiresApproval)
// Matrix: §6.7.1 (gemini, requiresApproval)
// Matrix: §6.7.1 (pi, requiresApproval)
// Matrix: §6.7.1 (hermes, requiresApproval)
func TestCapabilityMatrix_RequiresApproval(t *testing.T) {
	t.Parallel()
	const fm = "---\ntype: agent\nversion: 1.0.0\n" +
		"requiresApproval:\n  - tool: payment-submit\n    reason: irreversible\n---\n\nbody\n"
	for _, id := range firstClassAdapters {
		assertFieldCell(t, id, Capability{Field: "requiresApproval"}, fm, "payment-submit")
	}
}

// Matrix: §6.7.1 (claude-code, sandbox_profile)
// Matrix: §6.7.1 (claude-desktop, sandbox_profile)
// Matrix: §6.7.1 (claude-cowork, sandbox_profile)
// Matrix: §6.7.1 (cursor, sandbox_profile)
// Matrix: §6.7.1 (codex, sandbox_profile)
// Matrix: §6.7.1 (opencode, sandbox_profile)
// Matrix: §6.7.1 (gemini, sandbox_profile)
// Matrix: §6.7.1 (pi, sandbox_profile)
// Matrix: §6.7.1 (hermes, sandbox_profile)
func TestCapabilityMatrix_SandboxProfile(t *testing.T) {
	t.Parallel()
	const fm = "---\ntype: agent\nversion: 1.0.0\nsandbox_profile: read-only-fs\n---\n\nbody\n"
	for _, id := range firstClassAdapters {
		assertFieldCell(t, id, Capability{Field: "sandbox_profile"}, fm, "read-only-fs")
	}
}

// --- rule_mode (type: rule) --------------------------------------------------

func ruleSource(mode, extra string) Source {
	return Source{
		ArtifactID:    "test/rule",
		ArtifactBytes: []byte("---\ntype: rule\nversion: 1.0.0\nrule_mode: " + mode + "\n" + extra + "---\n\nrules\n"),
	}
}

// Matrix: §6.7.1 (claude-code, rule_mode_always)
// Matrix: §6.7.1 (claude-desktop, rule_mode_always)
// Matrix: §6.7.1 (claude-cowork, rule_mode_always)
// Matrix: §6.7.1 (cursor, rule_mode_always)
// Matrix: §6.7.1 (codex, rule_mode_always)
// Matrix: §6.7.1 (opencode, rule_mode_always)
// Matrix: §6.7.1 (gemini, rule_mode_always)
// Matrix: §6.7.1 (pi, rule_mode_always)
// Matrix: §6.7.1 (hermes, rule_mode_always)
// Matrix: §4.3 (always, claude-code)
// Matrix: §4.3 (always, claude-desktop)
// Matrix: §4.3 (always, claude-cowork)
// Matrix: §4.3 (always, cursor)
// Matrix: §4.3 (always, codex)
// Matrix: §4.3 (always, opencode)
// Matrix: §4.3 (always, gemini)
// Matrix: §4.3 (always, pi)
// Matrix: §4.3 (always, hermes)
func TestCapabilityMatrix_RuleModeAlways(t *testing.T) {
	t.Parallel()
	for _, id := range firstClassAdapters {
		assertTypeCell(t, id, Capability{Field: "rule_mode", Value: "always"}, ruleSource("always", ""))
	}
}

// Matrix: §6.7.1 (claude-code, rule_mode_glob)
// Matrix: §6.7.1 (claude-desktop, rule_mode_glob)
// Matrix: §6.7.1 (claude-cowork, rule_mode_glob)
// Matrix: §6.7.1 (cursor, rule_mode_glob)
// Matrix: §6.7.1 (codex, rule_mode_glob)
// Matrix: §6.7.1 (opencode, rule_mode_glob)
// Matrix: §6.7.1 (gemini, rule_mode_glob)
// Matrix: §6.7.1 (pi, rule_mode_glob)
// Matrix: §6.7.1 (hermes, rule_mode_glob)
// Matrix: §4.3 (glob, claude-code)
// Matrix: §4.3 (glob, claude-desktop)
// Matrix: §4.3 (glob, claude-cowork)
// Matrix: §4.3 (glob, cursor)
// Matrix: §4.3 (glob, codex)
// Matrix: §4.3 (glob, opencode)
// Matrix: §4.3 (glob, gemini)
// Matrix: §4.3 (glob, pi)
// Matrix: §4.3 (glob, hermes)
func TestCapabilityMatrix_RuleModeGlob(t *testing.T) {
	t.Parallel()
	for _, id := range firstClassAdapters {
		assertTypeCell(t, id, Capability{Field: "rule_mode", Value: "glob"}, ruleSource("glob", "rule_globs: src/**/*.ts\n"))
	}
}

// Matrix: §6.7.1 (claude-code, rule_mode_auto)
// Matrix: §6.7.1 (claude-desktop, rule_mode_auto)
// Matrix: §6.7.1 (claude-cowork, rule_mode_auto)
// Matrix: §6.7.1 (cursor, rule_mode_auto)
// Matrix: §6.7.1 (codex, rule_mode_auto)
// Matrix: §6.7.1 (opencode, rule_mode_auto)
// Matrix: §6.7.1 (gemini, rule_mode_auto)
// Matrix: §6.7.1 (pi, rule_mode_auto)
// Matrix: §6.7.1 (hermes, rule_mode_auto)
// Matrix: §4.3 (auto, claude-code)
// Matrix: §4.3 (auto, claude-desktop)
// Matrix: §4.3 (auto, claude-cowork)
// Matrix: §4.3 (auto, cursor)
// Matrix: §4.3 (auto, codex)
// Matrix: §4.3 (auto, opencode)
// Matrix: §4.3 (auto, gemini)
// Matrix: §4.3 (auto, pi)
// Matrix: §4.3 (auto, hermes)
func TestCapabilityMatrix_RuleModeAuto(t *testing.T) {
	t.Parallel()
	for _, id := range firstClassAdapters {
		assertTypeCell(t, id, Capability{Field: "rule_mode", Value: "auto"}, ruleSource("auto", "rule_description: Apply when migrating.\n"))
	}
}

// Matrix: §6.7.1 (claude-code, rule_mode_explicit)
// Matrix: §6.7.1 (claude-desktop, rule_mode_explicit)
// Matrix: §6.7.1 (claude-cowork, rule_mode_explicit)
// Matrix: §6.7.1 (cursor, rule_mode_explicit)
// Matrix: §6.7.1 (codex, rule_mode_explicit)
// Matrix: §6.7.1 (opencode, rule_mode_explicit)
// Matrix: §6.7.1 (gemini, rule_mode_explicit)
// Matrix: §6.7.1 (pi, rule_mode_explicit)
// Matrix: §6.7.1 (hermes, rule_mode_explicit)
// Matrix: §4.3 (explicit, claude-code)
// Matrix: §4.3 (explicit, claude-desktop)
// Matrix: §4.3 (explicit, claude-cowork)
// Matrix: §4.3 (explicit, cursor)
// Matrix: §4.3 (explicit, codex)
// Matrix: §4.3 (explicit, opencode)
// Matrix: §4.3 (explicit, gemini)
// Matrix: §4.3 (explicit, pi)
// Matrix: §4.3 (explicit, hermes)
func TestCapabilityMatrix_RuleModeExplicit(t *testing.T) {
	t.Parallel()
	for _, id := range firstClassAdapters {
		assertTypeCell(t, id, Capability{Field: "rule_mode", Value: "explicit"}, ruleSource("explicit", ""))
	}
}

// --- hook_event (type: hook) -------------------------------------------------

// Matrix: §6.7.1 (claude-code, hook_event)
// Matrix: §6.7.1 (claude-desktop, hook_event)
// Matrix: §6.7.1 (claude-cowork, hook_event)
// Matrix: §6.7.1 (cursor, hook_event)
// Matrix: §6.7.1 (codex, hook_event)
// Matrix: §6.7.1 (opencode, hook_event)
// Matrix: §6.7.1 (gemini, hook_event)
// Matrix: §6.7.1 (pi, hook_event)
// Matrix: §6.7.1 (hermes, hook_event)
func TestCapabilityMatrix_HookEvent(t *testing.T) {
	t.Parallel()
	// pre_shell_execution maps on every config-merge harness, including Cursor
	// (beforeShellExecution), so the cell measures hook materialization rather
	// than a single harness's event coverage. The generic pre_tool_use has no
	// Cursor-native target, so it would understate Cursor's ⚠ cell.
	src := Source{
		ArtifactID:    "test/hook",
		ArtifactBytes: []byte("---\ntype: hook\nversion: 1.0.0\nhook_event: pre_shell_execution\nhook_action: |\n  echo done\n---\n\n"),
	}
	for _, id := range firstClassAdapters {
		assertTypeCell(t, id, Capability{Field: "hook_event"}, src)
	}
}
