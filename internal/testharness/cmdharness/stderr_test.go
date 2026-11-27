package cmdharness

import "testing"

func TestStripCoverageNoise(t *testing.T) {
	t.Parallel()
	const noise = "error: coverage meta-data emit failed: writing /tmp/gocoverdir/covmeta.ab: rename from /tmp/gocoverdir/tmp.covmeta.ab1795770606454255000 failed: no such file or directory\n"
	cases := []struct {
		name, in, want string
	}{
		{"no noise", "artifact show team/stub: manifest body: HTTP 500\n", "artifact show team/stub: manifest body: HTTP 500\n"},
		{"empty", "", ""},
		{"noise then blank then message", noise + "\nartifact show team/stub: manifest body: HTTP 500\n", "artifact show team/stub: manifest body: HTTP 500\n"},
		{"noise alone", noise, ""},
		{"noise without trailing newline", "first\n" + noise[:len(noise)-1], "first\n"},
		{"blank line kept when not after noise", "a\n\nb\n", "a\n\nb\n"},
		{"blank line after a non-writing failure belongs to the program", "error: coverage meta-data emit failed: output directory /x inaccessible\n\nUsage: podium\n", "\nUsage: podium\n"},
		{"noise mid-stream", "a\n" + noise + "b\n", "a\nb\n"},
	}
	for _, c := range cases {
		if got := StripCoverageNoise(c.in); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
