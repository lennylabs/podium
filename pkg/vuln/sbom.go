package vuln

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Errors returned by SBOM parsers.
var (
	// ErrInvalidSBOM signals an SBOM that doesn't match either the
	// CycloneDX or SPDX format the spec accepts (§4.3).
	ErrInvalidSBOM = errors.New("vuln: invalid SBOM")
)

// ParseCycloneDX decodes a CycloneDX 1.x JSON document into the
// internal SBOMRef shape pkg/vuln consumes for matching.
func ParseCycloneDX(data []byte) (*SBOMRef, error) {
	var raw struct {
		BOMFormat   string `json:"bomFormat"`
		SpecVersion string `json:"specVersion"`
		Components  []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			PURL    string `json:"purl"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSBOM, err)
	}
	if raw.BOMFormat != "CycloneDX" {
		return nil, fmt.Errorf("%w: bomFormat=%q (want CycloneDX)", ErrInvalidSBOM, raw.BOMFormat)
	}
	out := &SBOMRef{Format: "cyclonedx-" + raw.SpecVersion}
	for _, c := range raw.Components {
		out.Components = append(out.Components, SBOMComponent{
			Name:    c.Name,
			Version: c.Version,
			PURL:    c.PURL,
		})
	}
	return out, nil
}

// ParseSPDX decodes an SPDX 2.x JSON document into the internal
// SBOMRef shape. PURL refs are read from the externalRefs array as
// referenceType=purl entries.
func ParseSPDX(data []byte) (*SBOMRef, error) {
	var raw struct {
		SPDXVersion string `json:"spdxVersion"`
		Packages    []struct {
			Name         string `json:"name"`
			VersionInfo  string `json:"versionInfo"`
			ExternalRefs []struct {
				ReferenceCategory string `json:"referenceCategory"`
				ReferenceType     string `json:"referenceType"`
				ReferenceLocator  string `json:"referenceLocator"`
			} `json:"externalRefs"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSBOM, err)
	}
	if raw.SPDXVersion == "" {
		return nil, fmt.Errorf("%w: spdxVersion missing", ErrInvalidSBOM)
	}
	out := &SBOMRef{Format: "spdx-" + spdxShortVersion(raw.SPDXVersion)}
	for _, p := range raw.Packages {
		comp := SBOMComponent{Name: p.Name, Version: p.VersionInfo}
		for _, ref := range p.ExternalRefs {
			if ref.ReferenceType == "purl" {
				comp.PURL = ref.ReferenceLocator
				break
			}
		}
		out.Components = append(out.Components, comp)
	}
	return out, nil
}

// ParseSBOM dispatches to the right parser based on the document's
// declared format. Useful for callers that read a file from
// `external_resources:` and don't yet know the format.
func ParseSBOM(data []byte) (*SBOMRef, error) {
	// Peek at the JSON top-level keys without committing to either
	// schema.
	var peek map[string]json.RawMessage
	if err := json.Unmarshal(data, &peek); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSBOM, err)
	}
	if _, ok := peek["bomFormat"]; ok {
		return ParseCycloneDX(data)
	}
	if _, ok := peek["spdxVersion"]; ok {
		return ParseSPDX(data)
	}
	return nil, fmt.Errorf("%w: unrecognized format (no bomFormat or spdxVersion)", ErrInvalidSBOM)
}

// spdxShortVersion strips the "SPDX-" prefix common to spdxVersion
// fields ("SPDX-2.3" → "2.3"). The downstream Format field is then
// uniform across CycloneDX / SPDX.
func spdxShortVersion(v string) string {
	const prefix = "SPDX-"
	if len(v) > len(prefix) && v[:len(prefix)] == prefix {
		return v[len(prefix):]
	}
	return v
}
