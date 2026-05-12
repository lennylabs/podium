package core

import (
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
)

func TestInPrefix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id, prefix string
		want       bool
	}{
		{"foo/bar", "", true},
		{"foo/bar", "foo", true},
		{"foo/bar", "foo/bar", true},
		{"foo/bar/baz", "foo/bar", true},
		{"foobar", "foo", false},
		{"x/y", "foo", false},
		{"foo", "foo/bar", false},
	}
	for _, c := range cases {
		if got := inPrefix(c.id, c.prefix); got != c.want {
			t.Errorf("inPrefix(%q, %q) = %v, want %v", c.id, c.prefix, got, c.want)
		}
	}
}

func TestCallerOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id   layer.Identity
		want string
	}{
		{layer.Identity{IsPublic: true}, "system:public"},
		{layer.Identity{IsAuthenticated: false}, "system:public"},
		{layer.Identity{Sub: "alice", IsAuthenticated: true}, "alice"},
		{layer.Identity{IsAuthenticated: true}, "system:public"}, // authed but no Sub
	}
	for _, c := range cases {
		if got := callerOf(c.id); got != c.want {
			t.Errorf("callerOf(%+v) = %q, want %q", c.id, got, c.want)
		}
	}
}
