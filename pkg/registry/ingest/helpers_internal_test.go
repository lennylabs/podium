package ingest

import (
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
)

func TestStripPin(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"foo/bar@1.0.0":         "foo/bar",
		"foo/bar@sha256:abc":    "foo/bar",
		"foo/bar":               "foo/bar",
		"":                      "",
	}
	for in, want := range cases {
		if got := stripPin(in); got != want {
			t.Errorf("stripPin(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitRef(t *testing.T) {
	t.Parallel()
	id, pin := splitRef("foo/bar@1.0.0")
	if id != "foo/bar" || pin != "1.0.0" {
		t.Errorf("got %q %q", id, pin)
	}
	id, pin = splitRef("foo/bar")
	if id != "foo/bar" || pin != "" {
		t.Errorf("got %q %q", id, pin)
	}
}

func TestDirOf(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"a/b/c": "a/b",
		"x":     ".",
		"":      ".",
		"/abs":  "",
	}
	for in, want := range cases {
		if got := dirOf(in); got != want {
			t.Errorf("dirOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		dir, name, want string
	}{
		{"", "x", "x"},
		{".", "x", "x"},
		{"a", "x", "a/x"},
		{"a/b", "x", "a/b/x"},
	}
	for _, c := range cases {
		if got := joinPath(c.dir, c.name); got != c.want {
			t.Errorf("joinPath(%q, %q) = %q, want %q", c.dir, c.name, got, c.want)
		}
	}
}

func TestDirToCanonical(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":    "",
		".":   "",
		"foo": "foo",
		"a/b": "a/b",
	}
	for in, want := range cases {
		if got := dirToCanonical(in); got != want {
			t.Errorf("dirToCanonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRejectsSensitivity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		floor, actual manifest.Sensitivity
		want          bool
	}{
		{"", manifest.SensitivityHigh, false},
		{manifest.SensitivityHigh, manifest.SensitivityLow, false},
		{manifest.SensitivityLow, manifest.SensitivityHigh, true},
		{manifest.SensitivityMedium, manifest.SensitivityMedium, true},
		{manifest.SensitivityMedium, manifest.SensitivityHigh, true},
		{manifest.SensitivityMedium, manifest.SensitivityLow, false},
	}
	for _, c := range cases {
		if got := rejectsSensitivity(c.floor, c.actual); got != c.want {
			t.Errorf("rejectsSensitivity(%v, %v) = %v, want %v",
				c.floor, c.actual, got, c.want)
		}
	}
}
