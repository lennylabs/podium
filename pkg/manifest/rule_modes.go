package manifest

// Canonical rule_mode values per spec 04-artifact-model.md §4.3. A
// type: rule artifact's rule_mode field is constrained to one of these
// values ("rule_mode: always | glob | auto | explicit"); the harness
// adapter (§6.7.1) translates the canonical mode to the harness's native
// rule format at materialization time. An unset rule_mode defaults to
// always.

// canonicalRuleModes is the ordered §4.3 rule_mode enumeration. The order
// matches the spec rule_mode table so CanonicalRuleModes() and lint
// messages list the modes in a stable, document-aligned sequence.
var canonicalRuleModes = []RuleMode{
	RuleModeAlways, RuleModeGlob, RuleModeAuto, RuleModeExplicit,
}

// canonicalRuleModeSet indexes canonicalRuleModes for O(1) membership.
var canonicalRuleModeSet = func() map[RuleMode]struct{} {
	m := make(map[RuleMode]struct{}, len(canonicalRuleModes))
	for _, v := range canonicalRuleModes {
		m[v] = struct{}{}
	}
	return m
}()

// CanonicalRuleModes returns the §4.3 canonical rule_mode values as
// strings in spec-table order. The returned slice is a copy the caller
// may modify.
func CanonicalRuleModes() []string {
	out := make([]string, len(canonicalRuleModes))
	for i, v := range canonicalRuleModes {
		out[i] = string(v)
	}
	return out
}

// IsCanonicalRuleMode reports whether mode is one of the §4.3 canonical
// rule_mode values.
func IsCanonicalRuleMode(mode RuleMode) bool {
	_, ok := canonicalRuleModeSet[mode]
	return ok
}
