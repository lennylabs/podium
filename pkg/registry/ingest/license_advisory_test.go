package ingest_test

import (
	"strings"
	"testing"
)

// contextArtifactLicense builds a type:context ARTIFACT.md carrying a license
// and an optional extends reference.
func contextArtifactLicense(version, license, extends string) string {
	src := "---\ntype: context\nversion: " + version +
		"\ndescription: artifact\nsensitivity: low\nlicense: " + license + "\n"
	if extends != "" {
		src += "extends: " + extends + "\n"
	}
	return src + "---\n\nbody\n"
}

// Spec: §4.6 field-semantics table — license is "Scalar; child wins (lint
// warning if changed across layers)". When an extends child carries a license
// that differs from the resolved parent's, ingest emits a warning advisory so
// the publisher sees the cross-layer change.
func TestExtends_LicenseChangedEmitsAdvisory(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	ingestOne(t, st, "org-defaults", "finance/pay", contextArtifactLicense("1.0.0", "Apache-2.0", ""))
	res := ingestOne(t, st, "team-foo", "finance/pay",
		contextArtifactLicense("2.0.0", "MIT", "finance/pay@1.x"))
	if res.Accepted != 1 || len(res.Rejected) != 0 {
		t.Fatalf("accepted=%d rejected=%+v, want a clean accept (a license change is advisory, not blocking)", res.Accepted, res.Rejected)
	}
	var found bool
	for _, d := range res.Advisories {
		if d.Code == "lint.license_changed_across_layers" {
			found = true
			if d.ArtifactID != "finance/pay" {
				t.Errorf("advisory ArtifactID = %q, want finance/pay", d.ArtifactID)
			}
			if string(d.Severity) != "warning" {
				t.Errorf("advisory Severity = %q, want warning", d.Severity)
			}
			if !strings.Contains(d.Message, "MIT") || !strings.Contains(d.Message, "Apache-2.0") {
				t.Errorf("advisory should name both licenses: %q", d.Message)
			}
		}
	}
	if !found {
		t.Errorf("no lint.license_changed_across_layers advisory: %+v", res.Advisories)
	}
}

// Spec: §4.6 — an extends child whose license matches the parent's produces no
// license-change advisory.
func TestExtends_LicenseUnchangedNoAdvisory(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	ingestOne(t, st, "org-defaults", "finance/pay", contextArtifactLicense("1.0.0", "Apache-2.0", ""))
	res := ingestOne(t, st, "team-foo", "finance/pay",
		contextArtifactLicense("2.0.0", "Apache-2.0", "finance/pay@1.x"))
	for _, d := range res.Advisories {
		if d.Code == "lint.license_changed_across_layers" {
			t.Errorf("matching license must not warn: %+v", d)
		}
	}
}

// Spec: §4.6 — "changed across layers" means a parent license existed and the
// child set a different one. A child that introduces a license over a parent
// with none is not a change, so no advisory fires.
func TestExtends_LicenseIntroducedNoAdvisory(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	// Parent has no license field.
	ingestOne(t, st, "org-defaults", "finance/pay", contextArtifact("base"))
	res := ingestOne(t, st, "team-foo", "finance/pay",
		contextArtifactLicense("2.0.0", "MIT", "finance/pay@1.x"))
	for _, d := range res.Advisories {
		if d.Code == "lint.license_changed_across_layers" {
			t.Errorf("introducing a license over a parent with none is not a change: %+v", d)
		}
	}
}
