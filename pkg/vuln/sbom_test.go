package vuln

import (
	"errors"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

const cycloneDX = `{
  "bomFormat": "CycloneDX",
  "specVersion": "1.5",
  "components": [
    {"name": "lodash", "version": "4.17.20", "purl": "pkg:npm/lodash@4.17.20"},
    {"name": "django", "version": "4.0.0", "purl": "pkg:pypi/django@4.0.0"}
  ]
}`

const spdx = `{
  "spdxVersion": "SPDX-2.3",
  "packages": [
    {
      "name": "lodash",
      "versionInfo": "4.17.20",
      "externalRefs": [
        {"referenceCategory": "PACKAGE-MANAGER", "referenceType": "purl",
         "referenceLocator": "pkg:npm/lodash@4.17.20"}
      ]
    }
  ]
}`

// Spec: §4.3 — CycloneDX SBOM JSON parses into SBOMRef with PURL
// preserved for matching.
// Phase: 17
func TestParseCycloneDX(t *testing.T) {
	testharness.RequirePhase(t, 17)
	t.Parallel()
	sbom, err := ParseCycloneDX([]byte(cycloneDX))
	if err != nil {
		t.Fatalf("ParseCycloneDX: %v", err)
	}
	if sbom.Format != "cyclonedx-1.5" {
		t.Errorf("Format = %q", sbom.Format)
	}
	if len(sbom.Components) != 2 {
		t.Fatalf("got %d components, want 2", len(sbom.Components))
	}
	if sbom.Components[0].PURL != "pkg:npm/lodash@4.17.20" {
		t.Errorf("PURL = %q", sbom.Components[0].PURL)
	}
}

// Spec: §4.3 — SPDX SBOM JSON parses externalRefs[type=purl] into the
// component's PURL.
// Phase: 17
func TestParseSPDX(t *testing.T) {
	testharness.RequirePhase(t, 17)
	t.Parallel()
	sbom, err := ParseSPDX([]byte(spdx))
	if err != nil {
		t.Fatalf("ParseSPDX: %v", err)
	}
	if sbom.Format != "spdx-2.3" {
		t.Errorf("Format = %q", sbom.Format)
	}
	if len(sbom.Components) != 1 {
		t.Fatalf("got %d components, want 1", len(sbom.Components))
	}
	if sbom.Components[0].PURL != "pkg:npm/lodash@4.17.20" {
		t.Errorf("PURL = %q", sbom.Components[0].PURL)
	}
}

// Spec: §4.3 — ParseSBOM dispatches to the right parser by peeking at
// the top-level keys.
// Phase: 17
func TestParseSBOM_DispatchesByFormat(t *testing.T) {
	testharness.RequirePhase(t, 17)
	t.Parallel()
	cdx, err := ParseSBOM([]byte(cycloneDX))
	if err != nil {
		t.Fatalf("CycloneDX: %v", err)
	}
	if cdx.Format != "cyclonedx-1.5" {
		t.Errorf("CycloneDX dispatch wrong: %q", cdx.Format)
	}
	sdx, err := ParseSBOM([]byte(spdx))
	if err != nil {
		t.Fatalf("SPDX: %v", err)
	}
	if sdx.Format != "spdx-2.3" {
		t.Errorf("SPDX dispatch wrong: %q", sdx.Format)
	}
}

// Spec: §4.3 — unrecognized SBOM formats fail with ErrInvalidSBOM.
// Phase: 17
func TestParseSBOM_RejectsUnrecognized(t *testing.T) {
	testharness.RequirePhase(t, 17)
	t.Parallel()
	_, err := ParseSBOM([]byte(`{"foo":"bar"}`))
	if !errors.Is(err, ErrInvalidSBOM) {
		t.Errorf("got %v, want ErrInvalidSBOM", err)
	}
}

// Spec: §4.7.7 / §4.3 — parsed SBOM round-trips through Match for the
// CVE pipeline.
// Phase: 17
func TestParseCycloneDX_RoundTripsThroughMatch(t *testing.T) {
	testharness.RequirePhase(t, 17)
	t.Parallel()
	sbom, err := ParseCycloneDX([]byte(cycloneDX))
	if err != nil {
		t.Fatalf("ParseCycloneDX: %v", err)
	}
	cves := []CVE{{
		ID: "CVE-2025-X", Severity: SeverityHigh,
		Affected: []string{"pkg:npm/lodash@4.17.20"},
	}}
	matches := Match(cves, *sbom)
	if len(matches) != 1 {
		t.Errorf("got %d matches, want 1", len(matches))
	}
}
