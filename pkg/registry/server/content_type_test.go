package server

import "testing"

// guessContentType maps extension → MIME. The test exercises every
// listed branch plus the default fallback.
func TestGuessContentType(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"x.json":     "application/json",
		"x.md":       "text/markdown",
		"x.txt":      "text/plain",
		"x.yaml":     "application/yaml",
		"x.yml":      "application/yaml",
		"x.png":      "image/png",
		"x.jpg":      "image/jpeg",
		"x.jpeg":     "image/jpeg",
		"":           "application/octet-stream",
		"x.unknown":  "application/octet-stream",
		"plain":      "application/octet-stream",
	}
	for in, want := range cases {
		if got := guessContentType(in); got != want {
			t.Errorf("guessContentType(%q) = %q, want %q", in, got, want)
		}
	}
}
