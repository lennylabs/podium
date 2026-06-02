package manifest

import "testing"

// Spec: 04-artifact-model.md §4.3 — rule_mode is the closed enumeration
// "always | glob | auto | explicit". Every value is recognized, the list
// is in spec-table order, and the accessor returns a defensive copy.
func TestCanonicalRuleModes(t *testing.T) {
	t.Parallel()
	want := []string{"always", "glob", "auto", "explicit"}
	got := CanonicalRuleModes()
	if len(got) != len(want) {
		t.Fatalf("CanonicalRuleModes len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("CanonicalRuleModes[%d] = %q, want %q", i, got[i], want[i])
		}
		if !IsCanonicalRuleMode(RuleMode(want[i])) {
			t.Errorf("IsCanonicalRuleMode(%q) = false, want true", want[i])
		}
	}
	got[0] = "mutated"
	if CanonicalRuleModes()[0] == "mutated" {
		t.Errorf("CanonicalRuleModes returned a shared backing array")
	}
}

// Spec: 04-artifact-model.md §4.3 — values outside the enumeration
// (misspellings, the empty value, mixed case) are not canonical. The
// empty value is handled by the §4.3 default (always) at materialization,
// not by membership.
func TestIsCanonicalRuleMode_Rejects(t *testing.T) {
	t.Parallel()
	for _, m := range []string{"", "sometimes", "Always", "glob,auto", "manual"} {
		if IsCanonicalRuleMode(RuleMode(m)) {
			t.Errorf("IsCanonicalRuleMode(%q) = true, want false", m)
		}
	}
}
