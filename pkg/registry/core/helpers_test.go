package core

import "testing"

func TestMostRestrictiveSensitivity(t *testing.T) {
	t.Parallel()
	cases := []struct{ a, b, want string }{
		{"", "", ""},
		{"low", "", "low"},
		{"", "low", "low"},
		{"low", "medium", "medium"},
		{"medium", "low", "medium"},
		{"medium", "high", "high"},
		{"high", "low", "high"},
		{"high", "high", "high"},
		{"unknown", "low", "low"},
	}
	for _, c := range cases {
		if got := mostRestrictiveSensitivity(c.a, c.b); got != c.want {
			t.Errorf("mostRestrictive(%q, %q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

func TestSplitParentRef(t *testing.T) {
	t.Parallel()
	id, ver := splitParentRef("foo/bar@1.0.0")
	if id != "foo/bar" || ver != "1.0.0" {
		t.Errorf("got id=%q ver=%q", id, ver)
	}
	id, ver = splitParentRef("foo/bar")
	if id != "foo/bar" || ver != "" {
		t.Errorf("got id=%q ver=%q", id, ver)
	}
	id, ver = splitParentRef("@1.0.0")
	if id != "" || ver != "1.0.0" {
		t.Errorf("got id=%q ver=%q", id, ver)
	}
}
