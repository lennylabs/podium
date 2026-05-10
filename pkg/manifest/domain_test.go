package manifest

import (
	"strings"
	"testing"
)

// Spec: §4.5.1 DOMAIN.md — frontmatter description, include, exclude,
// discovery, and unlisted round-trip through ParseDomain. Body is preserved.
func TestParseDomain_FullFrontmatter(t *testing.T) {
	t.Parallel()
	src := []byte(`---
unlisted: false
description: "AP-related operations"

discovery:
  max_depth: 4
  fold_below_artifacts: 5
  featured:
    - finance/ap/pay-invoice
  keywords:
    - invoice
    - remittance

include:
  - finance/ap/pay-invoice
  - finance/ap/payments/*

exclude:
  - finance/ap/internal/**
---

# Accounts Payable

Operations for the AP function.
`)
	got, err := ParseDomain(src)
	if err != nil {
		t.Fatalf("ParseDomain: %v", err)
	}
	if got.Unlisted {
		t.Errorf("Unlisted = true, want false")
	}
	if got.Description != "AP-related operations" {
		t.Errorf("Description = %q", got.Description)
	}
	if got.Discovery == nil {
		t.Fatalf("Discovery is nil, want populated")
	}
	if got.Discovery.MaxDepth != 4 {
		t.Errorf("Discovery.MaxDepth = %d, want 4", got.Discovery.MaxDepth)
	}
	if got.Discovery.FoldBelowArtifacts != 5 {
		t.Errorf("Discovery.FoldBelowArtifacts = %d, want 5",
			got.Discovery.FoldBelowArtifacts)
	}
	if len(got.Discovery.Featured) != 1 ||
		got.Discovery.Featured[0] != "finance/ap/pay-invoice" {
		t.Errorf("Discovery.Featured = %v", got.Discovery.Featured)
	}
	if len(got.Discovery.Keywords) != 2 {
		t.Errorf("Discovery.Keywords = %v", got.Discovery.Keywords)
	}
	if len(got.Include) != 2 {
		t.Errorf("Include length = %d, want 2", len(got.Include))
	}
	if len(got.Exclude) != 1 {
		t.Errorf("Exclude length = %d, want 1", len(got.Exclude))
	}
	if !strings.Contains(got.Body, "Accounts Payable") {
		t.Errorf("Body did not include the prose body, got %q", got.Body)
	}
}

// Spec: §4.5.3 Unlisted folders — unlisted: true is preserved through parsing.
func TestParseDomain_Unlisted(t *testing.T) {
	t.Parallel()
	src := []byte(`---
unlisted: true
description: "shared payment helpers"
---

internal-only domain
`)
	got, err := ParseDomain(src)
	if err != nil {
		t.Fatalf("ParseDomain: %v", err)
	}
	if !got.Unlisted {
		t.Errorf("Unlisted = false, want true")
	}
}
