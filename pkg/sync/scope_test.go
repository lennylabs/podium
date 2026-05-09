package sync

import (
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// Spec: §4.5.2 Imports and Globs — `*` matches a single segment.
// Phase: 3
func TestMatchGlob_SingleSegment(t *testing.T) {
	testharness.RequirePhase(t, 3)
	t.Parallel()
	cases := []struct {
		pattern, id string
		want        bool
	}{
		{"finance/*", "finance/ap", true},
		{"finance/*", "finance/ap/pay", false},
		{"finance/*/pay", "finance/ap/pay", true},
	}
	for _, c := range cases {
		if got := matchGlob(c.pattern, c.id); got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", c.pattern, c.id, got, c.want)
		}
	}
}

// Spec: §4.5.2 — `**` matches zero or more path segments.
// Phase: 3
func TestMatchGlob_RecursiveDoubleStar(t *testing.T) {
	testharness.RequirePhase(t, 3)
	t.Parallel()
	cases := []struct {
		pattern, id string
		want        bool
	}{
		{"finance/**", "finance/ap/pay", true},
		{"finance/**", "finance", false},
		{"finance/**/pay", "finance/ap/pay", true},
		{"finance/**/pay", "finance/pay", true},
		{"**/pay", "finance/ap/pay", true},
	}
	for _, c := range cases {
		if got := matchGlob(c.pattern, c.id); got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", c.pattern, c.id, got, c.want)
		}
	}
}

// Spec: §4.5.2 — brace alternation expands the pattern.
// Phase: 3
func TestMatchGlob_BraceAlternation(t *testing.T) {
	testharness.RequirePhase(t, 3)
	t.Parallel()
	cases := []struct {
		pattern, id string
		want        bool
	}{
		{"finance/{ap,ar}/pay", "finance/ap/pay", true},
		{"finance/{ap,ar}/pay", "finance/ar/pay", true},
		{"finance/{ap,ar}/pay", "finance/close/pay", false},
	}
	for _, c := range cases {
		if got := matchGlob(c.pattern, c.id); got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", c.pattern, c.id, got, c.want)
		}
	}
}

// Spec: §7.5.1 Scope Filters — Include narrows; without Include, every
// record passes through (subject to Exclude / Types).
// Phase: 3
func TestScopeFilter_Apply(t *testing.T) {
	testharness.RequirePhase(t, 3)
	t.Parallel()
	records := mkRecords("finance/ap/pay", "finance/ar/refund", "notes/glossary")

	got := ScopeFilter{Include: []string{"finance/**"}}.Apply(records)
	if len(got) != 2 {
		t.Errorf("Include finance/** matched %d, want 2", len(got))
	}
	got = ScopeFilter{Include: []string{"finance/**"}, Exclude: []string{"finance/ar/**"}}.Apply(records)
	if len(got) != 1 || got[0].ID != "finance/ap/pay" {
		t.Errorf("Include + Exclude got %+v", idsOfRecords(got))
	}
	got = ScopeFilter{}.Apply(records)
	if len(got) != 3 {
		t.Errorf("empty filter passed %d, want 3", len(got))
	}
}

// Spec: §7.5.1 — type filter narrows to listed types only.
// Phase: 3
func TestScopeFilter_TypeFilter(t *testing.T) {
	testharness.RequirePhase(t, 3)
	t.Parallel()
	records := mkRecordsWithTypes(
		[2]string{"finance/run/skill", "skill"},
		[2]string{"notes/x", "context"},
	)
	got := ScopeFilter{Types: []string{"skill"}}.Apply(records)
	if len(got) != 1 || got[0].ID != "finance/run/skill" {
		t.Errorf("type filter got %+v", got)
	}
}
