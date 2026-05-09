package vuln

import (
	"errors"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// Spec: §4.7.7 — PURLs in CVE feeds and SBOM components are parsed
// into Type / Namespace / Name / Version.
// Phase: 17
func TestParsePURL_RoundTrip(t *testing.T) {
	testharness.RequirePhase(t, 17)
	t.Parallel()
	cases := []struct {
		in   string
		want PURL
	}{
		{"pkg:npm/lodash@4.17.20", PURL{Type: "npm", Name: "lodash", Version: "4.17.20"}},
		{"pkg:pypi/django@4.0.0", PURL{Type: "pypi", Name: "django", Version: "4.0.0"}},
		{"pkg:maven/org.apache.commons/commons-lang3@3.12.0", PURL{
			Type: "maven", Namespace: "org.apache.commons", Name: "commons-lang3", Version: "3.12.0",
		}},
		{"pkg:npm/lodash", PURL{Type: "npm", Name: "lodash"}},
		{"pkg:npm/@scoped/pkg@1.0.0?qual=v#sub", PURL{
			Type: "npm", Namespace: "@scoped", Name: "pkg", Version: "1.0.0",
		}},
	}
	for _, c := range cases {
		got, err := ParsePURL(c.in)
		if err != nil {
			t.Errorf("ParsePURL(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParsePURL(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// Spec: §4.7.7 — invalid PURL strings return ErrInvalidPURL.
// Phase: 17
func TestParsePURL_RejectsInvalid(t *testing.T) {
	testharness.RequirePhase(t, 17)
	t.Parallel()
	for _, in := range []string{
		"",
		"npm/lodash@1.0.0",
		"pkg:",
		"pkg:type",
		"pkg:type/",
	} {
		_, err := ParsePURL(in)
		if !errors.Is(err, ErrInvalidPURL) {
			t.Errorf("ParsePURL(%q) = %v, want ErrInvalidPURL", in, err)
		}
	}
}

// Spec: §4.7.7 — SamePackage compares type + namespace + name,
// ignoring version.
// Phase: 17
func TestPURL_SamePackage(t *testing.T) {
	testharness.RequirePhase(t, 17)
	t.Parallel()
	a, _ := ParsePURL("pkg:npm/lodash@4.17.20")
	b, _ := ParsePURL("pkg:npm/lodash@4.17.21")
	if !a.SamePackage(b) {
		t.Errorf("expected same package")
	}
	c, _ := ParsePURL("pkg:pypi/lodash@4.17.20")
	if a.SamePackage(c) {
		t.Errorf("npm vs pypi should differ")
	}
}

// Spec: §4.7.7 Match — a CVE referencing a parsed PURL matches an
// SBOM component with the same package + version.
// Phase: 17
func TestMatch_PURLBasedExact(t *testing.T) {
	testharness.RequirePhase(t, 17)
	t.Parallel()
	cves := []CVE{{
		ID: "CVE-2025-X", Severity: SeverityHigh,
		Affected: []string{"pkg:npm/lodash@4.17.20"},
	}}
	sbom := SBOMRef{Components: []SBOMComponent{
		{Name: "lodash", Version: "4.17.20", PURL: "pkg:npm/lodash@4.17.20"},
	}}
	if got := Match(cves, sbom); len(got) != 1 {
		t.Errorf("got %d matches, want 1", len(got))
	}
}

// Spec: §4.7.7 — a CVE without a version specifier matches every
// version of the same package.
// Phase: 17
func TestMatch_VersionlessMatchesAll(t *testing.T) {
	testharness.RequirePhase(t, 17)
	t.Parallel()
	cves := []CVE{{
		ID: "CVE-2025-Y", Severity: SeverityCritical,
		Affected: []string{"pkg:npm/lodash"},
	}}
	sbom := SBOMRef{Components: []SBOMComponent{
		{Name: "lodash", Version: "4.0.0", PURL: "pkg:npm/lodash@4.0.0"},
	}}
	if got := Match(cves, sbom); len(got) != 1 {
		t.Errorf("got %d matches, want 1", len(got))
	}
}

// Spec: §4.7.7 — a CVE for a different package does not match.
// Phase: 17
func TestMatch_DifferentPackageNoMatch(t *testing.T) {
	testharness.RequirePhase(t, 17)
	t.Parallel()
	cves := []CVE{{
		ID:       "CVE-Z",
		Affected: []string{"pkg:pypi/django@4.0.0"},
	}}
	sbom := SBOMRef{Components: []SBOMComponent{
		{Name: "lodash", Version: "4.17.20", PURL: "pkg:npm/lodash@4.17.20"},
	}}
	if got := Match(cves, sbom); len(got) != 0 {
		t.Errorf("got %d matches, want 0", len(got))
	}
}
