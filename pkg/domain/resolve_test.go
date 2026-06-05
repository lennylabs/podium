package domain

import (
	"reflect"
	"testing"
)

// spec: §4.5.2 — glob syntax: * (one segment), ** (recursive),
// {a,b,c} (alternatives).
func TestMatch(t *testing.T) {
	cases := []struct {
		pattern, id string
		want        bool
	}{
		{"finance/ap/pay-invoice", "finance/ap/pay-invoice", true},
		{"finance/ap/pay-invoice", "finance/ap/other", false},
		{"finance/payments/*", "finance/payments/ach", true},
		{"finance/payments/*", "finance/payments/deep/nested", false}, // one segment only
		{"finance/refunds/**", "finance/refunds/partial", true},
		{"finance/refunds/**", "finance/refunds/full/deep", true},
		{"finance/refunds/**", "finance/other", false},
		{"_shared/regex/{ssn,iban,routing-number}", "_shared/regex/ssn", true},
		{"_shared/regex/{ssn,iban,routing-number}", "_shared/regex/iban", true},
		{"_shared/regex/{ssn,iban,routing-number}", "_shared/regex/other", false},
		{"**", "anything/at/all", true},
		{"a/*/c", "a/b/c", true},
		{"a/*/c", "a/b/d", false},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.id); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.id, got, c.want)
		}
	}
}

// spec: §4.5.2 — exclude is applied after include; the result is the
// included set minus the excluded set, in input order.
func TestResolveImports(t *testing.T) {
	ids := []string{
		"finance/ap/pay-invoice",
		"_shared/regex/ssn",
		"_shared/regex/iban",
		"_shared/other/thing",
	}
	got := ResolveImports([]string{"_shared/**"}, []string{"_shared/regex/iban"}, ids)
	want := []string{"_shared/regex/ssn", "_shared/other/thing"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveImports = %v, want %v", got, want)
	}

	// Empty include yields no imports.
	if out := ResolveImports(nil, nil, ids); len(out) != 0 {
		t.Errorf("empty include resolved %v, want none", out)
	}
}

// spec: §4.5.5 — description fallback is the directory basename,
// de-slugged (hyphens/underscores to spaces) and title-cased.
func TestFallbackDescription(t *testing.T) {
	cases := map[string]string{
		"finance/accounts-payable": "Accounts Payable",
		"finance":                  "Finance",
		"_shared/payment_helpers":  "Payment Helpers",
		"ops":                      "Ops",
	}
	for path, want := range cases {
		if got := FallbackDescription(path); got != want {
			t.Errorf("FallbackDescription(%q) = %q, want %q", path, got, want)
		}
	}
}
