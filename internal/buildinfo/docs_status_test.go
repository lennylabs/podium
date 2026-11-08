package buildinfo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// releaseSeries matches a release-series string such as "0.5.x".
var releaseSeries = regexp.MustCompile(`\b\d+\.\d+\.x\b`)

// statusPages are the pages that state the release series in prose. The
// release-process rule lists them among the places the version lives, so a
// release that bumps Version without them fails here instead of shipping a
// status line that names an older series.
var statusPages = []string{
	"README.md",
	filepath.Join("docs", "about", "status.md"),
}

// Every release-series string on a status page names the series of Version.
// The README and the status page said 0.3.x through the 0.5 releases; this
// test is the guard against that drift.
func TestStatusPages_NameTheCurrentReleaseSeries(t *testing.T) {
	// A development version such as "0.6.0-dev" (RELEASING.md, "Open the next
	// development cycle") names a series that has not shipped, while the pages
	// still describe the released one. The check applies to release versions,
	// which is when release prep runs it.
	if strings.Contains(Version, "-") {
		t.Skipf("Version %q is a prerelease; the status pages describe the last released series", Version)
	}
	parts := strings.SplitN(Version, ".", 3)
	if len(parts) < 2 {
		t.Fatalf("Version %q has no MAJOR.MINOR", Version)
	}
	want := parts[0] + "." + parts[1] + ".x"
	root := filepath.Join("..", "..")
	for _, page := range statusPages {
		raw, err := os.ReadFile(filepath.Join(root, page))
		if err != nil {
			t.Fatalf("read %s: %v", page, err)
		}
		found := releaseSeries.FindAllString(string(raw), -1)
		if len(found) == 0 {
			t.Errorf("%s names no release series; want %s", page, want)
		}
		for _, got := range found {
			if got != want {
				t.Errorf("%s names release series %s; want %s (buildinfo.Version %s)", page, got, want, Version)
			}
		}
	}
}
