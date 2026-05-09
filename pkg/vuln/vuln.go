// Package vuln implements vulnerability tracking, SBOM ingestion, and
// the NotificationProvider SPI from spec §4.7.7 / §9.1.
package vuln

import (
	"context"
	"sort"
	"strings"
)

// Severity is the canonical severity scale aligned with CVSS-derived
// classifications (low / medium / high / critical).
type Severity string

// Severity values.
const (
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// CVE represents one CVE entry consumed from a feed.
type CVE struct {
	ID          string
	Severity    Severity
	Description string
	Affected    []string // package@version specs (PURL or similar)
}

// SBOMRef is a reference to an artifact's SBOM declaration (§4.3
// caller-interpreted fields). Phase 17 ships ID + Format; the parsed
// content is fetched by the registry on demand.
type SBOMRef struct {
	ArtifactID string
	Format     string // "cyclonedx-1.5" | "spdx-2.3" | etc.
	Components []SBOMComponent
}

// SBOMComponent is one component identified in the SBOM.
type SBOMComponent struct {
	Name    string
	Version string
	PURL    string
}

// Match returns the CVEs that affect any component in the SBOM.
func Match(cves []CVE, sbom SBOMRef) []CVE {
	if len(cves) == 0 || len(sbom.Components) == 0 {
		return nil
	}
	out := []CVE{}
	for _, c := range cves {
		if affectsAny(c, sbom.Components) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func affectsAny(c CVE, comps []SBOMComponent) bool {
	for _, comp := range comps {
		for _, spec := range c.Affected {
			if matchesPackage(spec, comp) {
				return true
			}
		}
	}
	return false
}

// matchesPackage returns whether spec (e.g., "pkg:npm/lodash@4.17.20")
// applies to the given component. Match strategy:
//
//  1. Exact PURL string match (fast path for identical CVE feeds).
//  2. Parsed PURL: same type + namespace + name; version equal when
//     spec carries a version. A spec without a version matches every
//     version of the same package (broad CVE).
//
// Range matching ("<4.17.21") lands when an OSV / NVD feed adapter
// ships; the parsed PURL gives later commits the structural anchor
// they need.
func matchesPackage(spec string, comp SBOMComponent) bool {
	if spec == comp.PURL {
		return true
	}
	specPURL, err := ParsePURL(spec)
	if err != nil {
		return strings.Contains(spec, comp.Name) &&
			(comp.Version == "" || strings.Contains(spec, comp.Version))
	}
	compPURL, err := ParsePURL(comp.PURL)
	if err != nil {
		return false
	}
	if !specPURL.SamePackage(compPURL) {
		return false
	}
	if specPURL.Version == "" {
		return true
	}
	return specPURL.Version == compPURL.Version
}

// NotificationProvider is the SPI implementations satisfy (§9.1).
type NotificationProvider interface {
	ID() string
	Notify(ctx context.Context, n Notification) error
}

// Notification is one delivery payload.
type Notification struct {
	Severity   Severity
	Subject    string
	Body       string
	Recipients []string
}

// Capture is a NotificationProvider that records every Notify call.
// Tests use it to assert behavior without real outbound IO.
type Capture struct {
	calls []Notification
}

// NewCapture returns a fresh Capture.
func NewCapture() *Capture { return &Capture{} }

// ID returns "capture".
func (Capture) ID() string { return "capture" }

// Notify records the notification.
func (c *Capture) Notify(_ context.Context, n Notification) error {
	c.calls = append(c.calls, n)
	return nil
}

// Notifications returns a copy of the recorded notifications.
func (c *Capture) Notifications() []Notification {
	out := make([]Notification, len(c.calls))
	copy(out, c.calls)
	return out
}
