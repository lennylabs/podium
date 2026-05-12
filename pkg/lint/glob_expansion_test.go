package lint

import "testing"

// expandAlternatives flattens {a,b,c} into individual patterns,
// recursively for nested braces.
func TestExpandAlternatives(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want int // number of expansions expected
	}{
		{"plain/path", 1},
		{"{a,b}/x", 2},
		{"{a,b,c}/{x,y}", 6},
		{"no-close-brace{", 1},
		{"{a}/x", 1}, // single choice still expands
	}
	for _, c := range cases {
		got := expandAlternatives(c.in)
		if len(got) != c.want {
			t.Errorf("expandAlternatives(%q) → %v (len %d), want %d", c.in, got, len(got), c.want)
		}
	}
}

// globMatchesID exercises the public matcher across braces + **.
func TestGlobMatchesID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pattern string
		id      string
		want    bool
	}{
		{"finance/*", "finance/x", true},
		{"finance/*", "ops/x", false},
		{"finance/**", "finance/a/b/c", true},
		{"finance/**/run", "finance/a/run", true},
		{"finance/**/run", "finance/a/b/run", true},
		{"{finance,ops}/x", "finance/x", true},
		{"{finance,ops}/x", "sales/x", false},
		{"finance/**", "ops/y", false},
	}
	for _, c := range cases {
		if got := globMatchesID(c.pattern, c.id); got != c.want {
			t.Errorf("globMatchesID(%q, %q) = %v, want %v", c.pattern, c.id, got, c.want)
		}
	}
}
