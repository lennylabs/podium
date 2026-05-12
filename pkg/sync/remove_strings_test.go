package sync

import (
	"strings"
	"testing"
)

func TestRemoveStrings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		hay, drop, want []string
	}{
		{nil, nil, nil},
		{[]string{"a", "b", "c"}, nil, []string{"a", "b", "c"}},
		{[]string{"a", "b", "c"}, []string{"b"}, []string{"a", "c"}},
		{[]string{"a", "b", "c"}, []string{"a", "c"}, []string{"b"}},
		{[]string{"a", "a", "b"}, []string{"a"}, []string{"b"}},
		{nil, []string{"x"}, nil},
	}
	for i, c := range cases {
		got := removeStrings(c.hay, c.drop)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("case %d: got %v, want %v", i, got, c.want)
		}
	}
}
