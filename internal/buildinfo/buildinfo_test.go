package buildinfo

import (
	"strings"
	"testing"
)

func TestString_DefaultOnlyVersion(t *testing.T) {
	withGlobals(t, "0.0.0-dev", "unknown", "unknown")

	got := String()
	if got != "0.0.0-dev" {
		t.Fatalf("default String() = %q, want %q", got, "0.0.0-dev")
	}
}

func TestString_PopulatedCommitAndDate(t *testing.T) {
	withGlobals(t, "1.2.3", "abc123", "2026-01-01T00:00:00Z")

	got := String()
	for _, want := range []string{"1.2.3", "abc123", "2026-01-01T00:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q; missing %q", got, want)
		}
	}
}

func TestString_OnlyOneOverrideKeepsDefaultShape(t *testing.T) {
	// If only Commit is populated, the string still shows the full form
	// (Commit != "unknown" trips the branch even with Date == "unknown").
	withGlobals(t, "1.2.3", "abc123", "unknown")

	got := String()
	if !strings.Contains(got, "abc123") || !strings.Contains(got, "unknown") {
		t.Errorf("String() = %q; expected commit and date to appear", got)
	}
}

// withGlobals overrides the package-level Version/Commit/Date variables
// for the duration of a test and restores them on cleanup.
func withGlobals(t *testing.T, v, c, d string) {
	t.Helper()
	oV, oC, oD := Version, Commit, Date
	Version, Commit, Date = v, c, d
	t.Cleanup(func() {
		Version, Commit, Date = oV, oC, oD
	})
}
