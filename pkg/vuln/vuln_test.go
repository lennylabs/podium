package vuln

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// Spec: §4.7.7 Vulnerability Tracking — Match returns CVEs affecting
// SBOM components.
// Phase: 17
func TestMatch_AffectingComponents(t *testing.T) {
	testharness.RequirePhase(t, 17)
	t.Parallel()
	cves := []CVE{
		{ID: "CVE-2025-0001", Severity: SeverityHigh, Affected: []string{"pkg:npm/lodash@4.17.20"}},
		{ID: "CVE-2025-0002", Severity: SeverityLow, Affected: []string{"pkg:pypi/django@4.0.0"}},
	}
	sbom := SBOMRef{
		ArtifactID: "x",
		Components: []SBOMComponent{
			{Name: "lodash", Version: "4.17.20", PURL: "pkg:npm/lodash@4.17.20"},
		},
	}
	got := Match(cves, sbom)
	if len(got) != 1 || got[0].ID != "CVE-2025-0001" {
		t.Errorf("got %+v, want one CVE-2025-0001", got)
	}
}

// Spec: §9.1 NotificationProvider — Capture records every Notify call
// for assertion in tests.
// Phase: 17
func TestCapture_RecordsNotifications(t *testing.T) {
	testharness.RequirePhase(t, 17)
	t.Parallel()
	c := NewCapture()
	ctx := context.Background()
	_ = c.Notify(ctx, Notification{Severity: SeverityHigh, Subject: "alert"})
	_ = c.Notify(ctx, Notification{Severity: SeverityLow, Subject: "info"})

	got := c.Notifications()
	if len(got) != 2 {
		t.Fatalf("got %d notifications, want 2", len(got))
	}
	if got[0].Severity != SeverityHigh {
		t.Errorf("[0].Severity = %s", got[0].Severity)
	}
}
