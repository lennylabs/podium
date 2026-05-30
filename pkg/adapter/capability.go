package adapter

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lennylabs/podium/pkg/manifest"
)

// This file encodes the spec §6.7.1 capability matrix as a production data
// structure "maintained alongside the adapters" (§6.7.1 mitigation 2), and
// the queries the ingest lint (§6.7.1 / §4.3.5) and the §6.9
// "Adapter cannot translate an artifact" materialization guard run against
// it. The matrix and the documented core feature set (§6.7.1 mitigation 1)
// share a single source so they cannot drift apart.

// Support is the per-(field, harness) capability level from the §6.7.1
// matrix: ✓ native, ⚠ fallback, ✗ unsupported.
type Support int

// Support levels, mirroring the §6.7.1 legend.
const (
	// SupportNative is ✓: the adapter maps the field to a native harness
	// equivalent. No lint diagnostic.
	SupportNative Support = iota
	// SupportFallback is ⚠: the adapter emits a degraded fallback. The
	// capability lint warns when an artifact's target_harnesses names a
	// harness whose cell is ⚠.
	SupportFallback
	// SupportUnsupported is ✗: the harness has no equivalent. The
	// capability lint errors when an artifact's target_harnesses names a
	// harness whose cell is ✗, and §6.9 fails materialization for it.
	SupportUnsupported
)

// firstClassHarnessOrder is the column order of the §6.7.1 capability
// matrix. The capability rows below carry one Support per harness in this
// order.
var firstClassHarnessOrder = []string{
	"claude-code", "claude-desktop", "claude-cowork",
	"cursor", "codex", "opencode", "gemini", "pi", "hermes",
}

// harnessColumn maps a harness ID to its column index in
// firstClassHarnessOrder.
var harnessColumn = func() map[string]int {
	m := make(map[string]int, len(firstClassHarnessOrder))
	for i, h := range firstClassHarnessOrder {
		m[h] = i
	}
	return m
}()

// Capability identifies one row of the §6.7.1 matrix: a canonical field,
// optionally qualified by a value. Plain fields leave Value empty; the
// rule_mode modes (always / glob / auto / explicit) are distinct rows, so
// they set Value.
type Capability struct {
	Field string
	Value string
}

// String renders the capability the way the spec rows name it, e.g.
// "sandbox_profile" or "rule_mode: glob".
func (c Capability) String() string {
	if c.Value != "" {
		return c.Field + ": " + c.Value
	}
	return c.Field
}

// capabilityMatrix transcribes spec §6.7.1 verbatim. Each value is one
// Support per harness in firstClassHarnessOrder, decoded from a 9-rune
// string so the source reads like the spec table: N native (✓),
// F fallback (⚠), X unsupported (✗).
//
//	column order: claude-code claude-desktop claude-cowork cursor codex
//	              opencode gemini pi hermes
var capabilityMatrix = map[Capability][]Support{
	{Field: "description"}:                  row("NNNNNNNNN"),
	{Field: "mcpServers"}:                   row("NNNNNNNNN"),
	{Field: "delegates_to"}:                 row("NFNXFNXNN"),
	{Field: "requiresApproval"}:             row("NFNXNNXFF"),
	{Field: "sandbox_profile"}:              row("NFFXNNXFF"),
	{Field: "expose_as_mcp_prompt"}:         row("NNNNNNNNN"),
	{Field: "rule_mode", Value: "always"}:   row("NNNNNNFNN"),
	{Field: "rule_mode", Value: "glob"}:     row("FXFNFFXFN"),
	{Field: "rule_mode", Value: "auto"}:     row("FXFNXXXXF"),
	{Field: "rule_mode", Value: "explicit"}: row("NNNNNNFNN"),
	{Field: "hook_event"}:                   row("NXFNNFFFF"),
}

// row decodes a 9-rune capability string into per-harness Support values.
// It panics on a malformed row so a bad edit to capabilityMatrix fails at
// package init rather than silently mis-grading a cell.
func row(s string) []Support {
	if len(s) != len(firstClassHarnessOrder) {
		panic(fmt.Sprintf("capability row %q: want %d cells, got %d", s, len(firstClassHarnessOrder), len(s)))
	}
	out := make([]Support, len(s))
	for i, r := range s {
		switch r {
		case 'N':
			out[i] = SupportNative
		case 'F':
			out[i] = SupportFallback
		case 'X':
			out[i] = SupportUnsupported
		default:
			panic(fmt.Sprintf("capability row %q: bad rune %q", s, string(r)))
		}
	}
	return out
}

// FirstClassHarnesses returns the §6.7.1 harness column order. The slice is
// a copy; callers may sort or mutate it freely.
func FirstClassHarnesses() []string {
	return append([]string(nil), firstClassHarnessOrder...)
}

// Cell returns the §6.7.1 support level for capability c on harness. ok is
// false when c is not a matrix row or harness is not a first-class harness
// (for example "none" or a custom adapter), in which case the matrix
// imposes no contract on the cell.
func Cell(c Capability, harness string) (level Support, ok bool) {
	col, hok := harnessColumn[harness]
	if !hok {
		return SupportNative, false
	}
	supports, cok := capabilityMatrix[c]
	if !cok {
		return SupportNative, false
	}
	return supports[col], true
}

// UsedCapabilities returns the §6.7.1 capability rows the artifact
// exercises that can carry a non-native cell. The always-native rows
// (description, mcpServers, expose_as_mcp_prompt) are omitted because they
// never produce a mismatch; the core-feature-set query reads the matrix
// directly. rule_mode and hook_event are scoped to the type that owns them
// so a stray field on the wrong type is left to the dedicated hygiene
// rules.
func UsedCapabilities(art *manifest.Artifact) []Capability {
	if art == nil {
		return nil
	}
	var out []Capability
	if len(art.DelegatesTo) > 0 {
		out = append(out, Capability{Field: "delegates_to"})
	}
	if len(art.RequiresApproval) > 0 {
		out = append(out, Capability{Field: "requiresApproval"})
	}
	if art.SandboxProfile != "" {
		out = append(out, Capability{Field: "sandbox_profile"})
	}
	if art.Type == manifest.TypeRule && art.RuleMode != "" {
		out = append(out, Capability{Field: "rule_mode", Value: string(art.RuleMode)})
	}
	if art.Type == manifest.TypeHook && art.HookEvent != "" {
		out = append(out, Capability{Field: "hook_event"})
	}
	return out
}

// Mismatch is one non-native (field, harness) cell an artifact exercises.
type Mismatch struct {
	Capability Capability
	Harness    string
	Support    Support
}

// Evaluate returns the ⚠ and ✗ cells for every capability the artifact uses
// against every harness in harnesses, in (capability, harness) order.
// Harnesses absent from the §6.7.1 matrix are skipped: they declare their
// own coverage and impose no per-field contract here.
func Evaluate(art *manifest.Artifact, harnesses []string) []Mismatch {
	var out []Mismatch
	for _, c := range UsedCapabilities(art) {
		for _, h := range harnesses {
			level, ok := Cell(c, h)
			if !ok || level == SupportNative {
				continue
			}
			out = append(out, Mismatch{Capability: c, Harness: h, Support: level})
		}
	}
	return out
}

// CoreFeatureSet returns the §6.7.1 mitigation-1 "core feature set": the
// capability cells every built-in adapter supports natively (✓ across all
// first-class harnesses). An artifact that stays within this set
// materializes the same way on every harness. The result is sorted for a
// stable documented reference.
func CoreFeatureSet() []Capability {
	var out []Capability
	for c, supports := range capabilityMatrix {
		core := true
		for _, s := range supports {
			if s != SupportNative {
				core = false
				break
			}
		}
		if core {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Field != out[j].Field {
			return out[i].Field < out[j].Field
		}
		return out[i].Value < out[j].Value
	})
	return out
}

// InCoreFeatureSet reports whether c is a §6.7.1 core-feature-set cell
// (native on every first-class harness).
func InCoreFeatureSet(c Capability) bool {
	supports, ok := capabilityMatrix[c]
	if !ok {
		return false
	}
	for _, s := range supports {
		if s != SupportNative {
			return false
		}
	}
	return true
}

// TranslationError implements the §6.9 "Adapter cannot translate an
// artifact" failure mode for harnessID. It returns a structured error
// naming the untranslatable fields (the ✗ cells the artifact exercises for
// harnessID) and suggesting harness: none, or nil when every used field is
// translatable. harness "none" (raw output) and harnesses absent from the
// §6.7.1 matrix never fail: they carry no per-field contract.
func TranslationError(harnessID string, art *manifest.Artifact) error {
	if harnessID == "" || harnessID == "none" {
		return nil
	}
	var unsupported []string
	for _, c := range UsedCapabilities(art) {
		if level, ok := Cell(c, harnessID); ok && level == SupportUnsupported {
			unsupported = append(unsupported, c.String())
		}
	}
	if len(unsupported) == 0 {
		return nil
	}
	sort.Strings(unsupported)
	return fmt.Errorf("materialize.untranslatable: adapter %q cannot translate %s; use harness: none for raw output",
		harnessID, strings.Join(unsupported, ", "))
}
