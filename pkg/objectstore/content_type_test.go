package objectstore

import "testing"

// GuessContentType maps a path extension to a MIME type. The test
// exercises every listed branch plus the default fallback so the
// data-plane Content-Type stays stable (spec §7.2).
func TestGuessContentType(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"x.json":    "application/json",
		"x.md":      "text/markdown",
		"x.txt":     "text/plain",
		"x.yaml":    "application/yaml",
		"x.yml":     "application/yaml",
		"x.png":     "image/png",
		"x.jpg":     "image/jpeg",
		"x.jpeg":    "image/jpeg",
		"":          "application/octet-stream",
		"x.unknown": "application/octet-stream",
		"plain":     "application/octet-stream",
	}
	for in, want := range cases {
		if got := GuessContentType(in); got != want {
			t.Errorf("GuessContentType(%q) = %q, want %q", in, got, want)
		}
	}
}
