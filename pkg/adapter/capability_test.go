package adapter

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
)

// wantGrid is an independent transcription of the spec §6.7.1 capability
// matrix, in the same column order as firstClassHarnessOrder. It is
// written out by hand so the test cross-checks the production matrix
// rather than reusing its decoder. N native (✓), F fallback (⚠),
// X unsupported (✗).
//
// spec: §6.7.1 capability matrix
var wantGrid = map[Capability]string{
	{Field: "type", Value: "skill"}:         "NXNNNNNNX",
	{Field: "type", Value: "agent"}:         "NXNNNNNXX",
	{Field: "type", Value: "context"}:       "NXNNNNNNN",
	{Field: "type", Value: "command"}:       "NXNNXNNNX",
	{Field: "type", Value: "mcp-server"}:    "NXNNNNNXX",
	{Field: "description"}:                  "NXNNNNNXX",
	{Field: "mcpServers"}:                   "NXNNXNNXX",
	{Field: "delegates_to"}:                 "NXNNXNNXX",
	{Field: "requiresApproval"}:             "NXNNXNNXX",
	{Field: "sandbox_profile"}:              "NXNNXNNXX",
	{Field: "rule_mode", Value: "always"}:   "NXFNNNNNN",
	{Field: "rule_mode", Value: "glob"}:     "FXFNFFFFN",
	{Field: "rule_mode", Value: "auto"}:     "FXFNFFFFN",
	{Field: "rule_mode", Value: "explicit"}: "NXFNFFFFN",
	{Field: "hook_event"}:                   "NXNFNXNXX",
}

func wantSupport(r byte) Support {
	switch r {
	case 'N':
		return SupportNative
	case 'F':
		return SupportFallback
	case 'X':
		return SupportUnsupported
	}
	panic("bad rune")
}

// Spec: §6.7.1 — Cell returns the documented support level for every
// (field, harness) cell in the capability matrix.
func TestCapability_CellMatchesSpecGrid(t *testing.T) {
	t.Parallel()
	harnesses := FirstClassHarnesses()
	for cap, gridRow := range wantGrid {
		if len(gridRow) != len(harnesses) {
			t.Fatalf("%s: grid has %d cells, want %d", cap, len(gridRow), len(harnesses))
		}
		for i, h := range harnesses {
			got, ok := Cell(cap, h)
			if !ok {
				t.Errorf("Cell(%s, %s): ok=false, want a graded cell", cap, h)
				continue
			}
			if want := wantSupport(gridRow[i]); got != want {
				t.Errorf("Cell(%s, %s) = %d, want %d", cap, h, got, want)
			}
		}
	}
}

// Spec: §6.7.1 — a harness outside the matrix (none, custom) and an
// unknown field carry no contract: Cell reports ok=false.
func TestCapability_CellUnknown(t *testing.T) {
	t.Parallel()
	if _, ok := Cell(Capability{Field: "sandbox_profile"}, "none"); ok {
		t.Errorf("Cell(sandbox_profile, none): ok=true, want false")
	}
	if _, ok := Cell(Capability{Field: "sandbox_profile"}, "made-up"); ok {
		t.Errorf("Cell(sandbox_profile, made-up): ok=true, want false")
	}
	if _, ok := Cell(Capability{Field: "not_a_field"}, "cursor"); ok {
		t.Errorf("Cell(not_a_field, cursor): ok=true, want false")
	}
}

// Spec: §6.7.1 mitigation 1 — the core feature set is the cells native on
// every first-class harness. Claude Desktop has no project-level
// materialization surface (every cell ✗), so no capability is native on
// every harness and the core set is empty.
func TestCapability_CoreFeatureSet(t *testing.T) {
	t.Parallel()
	if got := CoreFeatureSet(); len(got) != 0 {
		t.Errorf("CoreFeatureSet() = %v, want empty (claude-desktop materializes nothing)", got)
	}
	// context is native on every harness except claude-desktop, so it is not
	// in the core set under the literal "every harness" definition.
	if InCoreFeatureSet(Capability{Field: "type", Value: "context"}) {
		t.Errorf("InCoreFeatureSet(type: context) = true; claude-desktop is ✗")
	}
	if InCoreFeatureSet(Capability{Field: "sandbox_profile"}) {
		t.Errorf("InCoreFeatureSet(sandbox_profile) = true")
	}
}

// Spec: §6.7.1 — UsedCapabilities maps an artifact's fields to the matrix
// rows it exercises, scoping rule_mode / hook_event to their owning type.
func TestCapability_UsedCapabilities(t *testing.T) {
	t.Parallel()
	rule := &manifest.Artifact{Type: manifest.TypeRule, RuleMode: manifest.RuleModeGlob, RuleGlobs: "src/**"}
	if got := UsedCapabilities(rule); len(got) != 1 || got[0] != (Capability{Field: "rule_mode", Value: "glob"}) {
		t.Errorf("UsedCapabilities(rule glob) = %v", got)
	}
	hook := &manifest.Artifact{Type: manifest.TypeHook, HookEvent: "stop"}
	if got := UsedCapabilities(hook); len(got) != 1 || got[0] != (Capability{Field: "hook_event"}) {
		t.Errorf("UsedCapabilities(hook) = %v", got)
	}
	agent := &manifest.Artifact{
		Type:             manifest.TypeAgent,
		SandboxProfile:   "read-only-fs",
		DelegatesTo:      []string{"x/y"},
		RequiresApproval: []manifest.ApprovalRequirement{{}},
	}
	// type: agent emits its type row plus the three frontmatter-field rows.
	if got := UsedCapabilities(agent); len(got) != 4 {
		t.Errorf("UsedCapabilities(agent) = %v, want 4 (type + 3 fields)", got)
	}
	// A type with a type row emits it; a stray rule_mode on a non-rule type is
	// ignored (the hygiene rule covers it).
	ctx := &manifest.Artifact{Type: manifest.TypeContext, RuleMode: manifest.RuleModeGlob}
	if got := UsedCapabilities(ctx); len(got) != 1 || got[0] != (Capability{Field: "type", Value: "context"}) {
		t.Errorf("UsedCapabilities(context) = %v, want [type: context]", got)
	}
	if got := UsedCapabilities(nil); got != nil {
		t.Errorf("UsedCapabilities(nil) = %v", got)
	}
}

// Spec: §6.7.1 — Evaluate returns the ⚠ and ✗ cells an artifact exercises
// against a declared harness set, and nothing for ✓ cells.
func TestCapability_Evaluate(t *testing.T) {
	t.Parallel()
	rule := &manifest.Artifact{Type: manifest.TypeRule, RuleMode: manifest.RuleModeGlob}
	// cursor is ✓ for glob -> no mismatch; claude-desktop is ✗; claude-code is ⚠.
	got := Evaluate(rule, []string{"cursor", "claude-desktop", "claude-code"})
	if len(got) != 2 {
		t.Fatalf("Evaluate = %v, want 2 mismatches", got)
	}
	if got[0].Harness != "claude-desktop" || got[0].Support != SupportUnsupported {
		t.Errorf("Evaluate[0] = %+v, want claude-desktop ✗", got[0])
	}
	if got[1].Harness != "claude-code" || got[1].Support != SupportFallback {
		t.Errorf("Evaluate[1] = %+v, want claude-code ⚠", got[1])
	}
	// "none" is not a matrix harness -> skipped.
	if got := Evaluate(rule, []string{"none"}); len(got) != 0 {
		t.Errorf("Evaluate(none) = %v, want empty", got)
	}
}

// Spec: §6.9 "Adapter cannot translate an artifact" — TranslationError
// fails materialization for a ✗ cell, names the field, and never fails for
// harness none, ⚠ cells, or ✓ cells.
func TestCapability_TranslationError(t *testing.T) {
	t.Parallel()
	rule := &manifest.Artifact{Type: manifest.TypeRule, RuleMode: manifest.RuleModeGlob}
	// claude-desktop ✗ for glob -> error naming the field.
	if err := TranslationError("claude-desktop", rule); err == nil ||
		!containsAll(err.Error(), "claude-desktop", "rule_mode: glob", "harness: none") {
		t.Errorf("TranslationError(claude-desktop, glob) = %v", err)
	}
	// claude-code ⚠ for glob -> no error (fallback, not unsupported).
	if err := TranslationError("claude-code", rule); err != nil {
		t.Errorf("TranslationError(claude-code, glob) = %v, want nil", err)
	}
	// cursor ✓ for glob -> no error.
	if err := TranslationError("cursor", rule); err != nil {
		t.Errorf("TranslationError(cursor, glob) = %v, want nil", err)
	}
	// none and unknown harness never fail.
	agent := &manifest.Artifact{Type: manifest.TypeAgent, SandboxProfile: "read-only-fs"}
	if err := TranslationError("none", agent); err != nil {
		t.Errorf("TranslationError(none) = %v, want nil", err)
	}
	if err := TranslationError("custom-harness", agent); err != nil {
		t.Errorf("TranslationError(custom) = %v, want nil", err)
	}
	// codex ✗ for sandbox_profile (the TOML agent translation drops it) -> error.
	if err := TranslationError("codex", agent); err == nil || !containsAll(err.Error(), "codex", "sandbox_profile") {
		t.Errorf("TranslationError(codex, sandbox_profile) = %v", err)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
