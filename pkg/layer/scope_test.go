package layer_test

import (
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
)

// Spec: §6.3.1 — an inactive scope set (no "podium:*" scopes) narrows
// nothing; the caller keeps full layer visibility. F-6.3.5.
func TestScopeSet_InactiveAllowsAll(t *testing.T) {
	t.Parallel()
	set := layer.ParseScopes([]string{"openid", "profile", "email"})
	if set.Active() {
		t.Fatalf("Active() = true for non-podium scopes")
	}
	if !set.AllowsRead("finance/ap/pay-invoice") {
		t.Errorf("AllowsRead denied under inactive set")
	}
	if !set.AllowsLoad("finance/ap/pay-invoice", "2.0.0") {
		t.Errorf("AllowsLoad denied under inactive set")
	}
}

// Spec: §6.3.1 — "podium:read:finance/*" covers the finance subtree and
// nothing outside it; the smaller surface wins. F-6.3.5.
func TestScopeSet_ReadWildcard(t *testing.T) {
	t.Parallel()
	set := layer.ParseScopes([]string{"podium:read:finance/*"})
	if !set.Active() {
		t.Fatalf("Active() = false")
	}
	for _, id := range []string{"finance", "finance/ap", "finance/ap/pay-invoice"} {
		if !set.AllowsRead(id) {
			t.Errorf("AllowsRead(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"hr", "hr/policies", "financex/foo"} {
		if set.AllowsRead(id) {
			t.Errorf("AllowsRead(%q) = true, want false", id)
		}
	}
	// A read scope does not authorize a load.
	if set.AllowsLoad("finance/ap/pay-invoice", "1.0.0") {
		t.Errorf("read scope authorized a load")
	}
}

// Spec: §6.3.1 — a load grant implies read visibility for the same
// resource (you can see what you may load). F-6.3.5.
func TestScopeSet_LoadImpliesRead(t *testing.T) {
	t.Parallel()
	set := layer.ParseScopes([]string{"podium:load:finance/ap/pay-invoice@1.x"})
	if !set.AllowsRead("finance/ap/pay-invoice") {
		t.Errorf("load grant did not imply read visibility")
	}
	// Outside the granted resource, read is denied.
	if set.AllowsRead("finance/gl/close") {
		t.Errorf("AllowsRead leaked outside the load grant")
	}
}

// Spec: §6.3.1 — "podium:load:.../pay-invoice@1.x" permits loading only
// versions matching the pin; the smaller surface wins. F-6.3.5.
func TestScopeSet_LoadVersionPin(t *testing.T) {
	t.Parallel()
	set := layer.ParseScopes([]string{"podium:load:finance/ap/pay-invoice@1.x"})
	cases := []struct {
		version string
		want    bool
	}{
		{"1.0.0", true},
		{"1.4.2", true},
		{"2.0.0", false},
		{"0.9.0", false},
	}
	for _, tc := range cases {
		if got := set.AllowsLoad("finance/ap/pay-invoice", tc.version); got != tc.want {
			t.Errorf("AllowsLoad(pay-invoice, %s) = %v, want %v", tc.version, got, tc.want)
		}
	}
	// A different artifact is denied even at a matching version.
	if set.AllowsLoad("finance/ap/other", "1.0.0") {
		t.Errorf("load leaked to a different artifact")
	}
}

// Spec: §6.3.1 — exact (non-subtree) load grant matches one artifact only.
func TestScopeSet_ExactPath(t *testing.T) {
	t.Parallel()
	set := layer.ParseScopes([]string{"podium:load:finance/ap/pay-invoice"})
	if !set.AllowsLoad("finance/ap/pay-invoice", "3.1.4") {
		t.Errorf("exact grant denied its own artifact (any version)")
	}
	if set.AllowsLoad("finance/ap/pay-invoice-v2", "1.0.0") {
		t.Errorf("exact grant matched a prefix sibling")
	}
	if set.AllowsRead("finance/ap") {
		t.Errorf("exact grant matched an ancestor")
	}
}

// Spec: §6.3.1 — ScopeMatchesVersion handles "x"/"*" wildcards positionally.
func TestScopeMatchesVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pin, version string
		want         bool
	}{
		{"1.x", "1.4.2", true},
		{"1.2.x", "1.2.9", true},
		{"1.2.x", "1.3.0", false},
		{"1", "1.4.2", true},
		{"1.2.3", "1.2.3", true},
		{"1.2.3", "1.2.4", false},
		{"*", "9.9.9", true},
		{"1.2.3.4", "1.2.3", false}, // pin longer than version
	}
	for _, tc := range cases {
		if got := layer.ScopeMatchesVersion(tc.pin, tc.version); got != tc.want {
			t.Errorf("ScopeMatchesVersion(%q,%q) = %v, want %v", tc.pin, tc.version, got, tc.want)
		}
	}
}

// Spec: §6.3.1 — multiple scopes union: each grant contributes its surface.
func TestScopeSet_MultipleGrantsUnion(t *testing.T) {
	t.Parallel()
	set := layer.ParseScopes([]string{
		"podium:load:finance/*",
		"podium:read:hr/*",
	})
	if !set.AllowsLoad("finance/ap/x", "1.0.0") {
		t.Errorf("finance load denied")
	}
	if set.AllowsLoad("hr/policies", "1.0.0") {
		t.Errorf("hr load allowed under a read-only grant")
	}
	if !set.AllowsRead("hr/policies") {
		t.Errorf("hr read denied")
	}
	if !set.AllowsRead("finance/ap/x") {
		t.Errorf("finance read denied (load implies read)")
	}
}
