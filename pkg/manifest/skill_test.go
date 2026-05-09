package manifest

import (
	"errors"
	"strings"
	"testing"
)

// Spec: §4.3.4 SKILL.md compliance — name, description, and license
// round-trip through ParseSkill, and the prose body is preserved.
// Phase: 0
func TestParseSkill_RequiredFields(t *testing.T) {
	t.Parallel()
	src := []byte(`---
name: pay-invoice
description: Pay an approved vendor invoice.
license: MIT
---

Steps to pay a vendor invoice. Begin by reading the policy doc.
`)
	got, err := ParseSkill(src)
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}
	if got.Name != "pay-invoice" {
		t.Errorf("Name = %q, want pay-invoice", got.Name)
	}
	if got.Description == "" {
		t.Errorf("Description is empty")
	}
	if got.License != "MIT" {
		t.Errorf("License = %q, want MIT", got.License)
	}
	if !strings.Contains(got.Body, "Steps to pay") {
		t.Errorf("Body did not contain the prose, got %q", got.Body)
	}
}

// Spec: §4.3.4 SKILL.md compliance — name constraints: 1-64 chars,
// lowercase alphanumeric and hyphens, no leading/trailing hyphen, no
// consecutive hyphens.
// Phase: 0
func TestValidateName_Constraints(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want bool
	}{
		{"pay-invoice", true},
		{"a", true},
		{"pay-invoice-123", true},
		{"PayInvoice", false},      // no uppercase
		{"-leading", false},        // no leading hyphen
		{"trailing-", false},       // no trailing hyphen
		{"double--hyphen", false},  // no consecutive hyphens
		{"", false},                // length 0
		{strings.Repeat("a", 65), false}, // length > 64
		{strings.Repeat("a", 64), true},  // length 64 boundary
		{"under_score", false},     // underscores not allowed
	}
	for _, c := range cases {
		err := ValidateName(c.name)
		got := err == nil
		if got != c.want {
			t.Errorf("ValidateName(%q) = %v (err=%v), want %v",
				c.name, got, err, c.want)
		}
	}
}

// Spec: §4.7.6 Version Resolution — author-chosen versions are
// semver-named (major.minor.patch).
// Phase: 0
func TestValidateVersion_Semver(t *testing.T) {
	t.Parallel()
	cases := []struct {
		v    string
		want bool
	}{
		{"1.0.0", true},
		{"0.0.1", true},
		{"10.20.30", true},
		{"1.0", false},
		{"1", false},
		{"1.0.0-rc1", false}, // pre-release support lands later
		{"v1.0.0", false},
		{"", false},
	}
	for _, c := range cases {
		err := ValidateVersion(c.v)
		got := err == nil
		if got != c.want {
			t.Errorf("ValidateVersion(%q) = %v (err=%v), want %v",
				c.v, got, err, c.want)
		}
	}
}

// Spec: §4.3.4 SKILL.md compliance — missing frontmatter is rejected.
// Phase: 0
func TestParseSkill_NoFrontmatter(t *testing.T) {
	t.Parallel()
	src := []byte("just a body\n")
	_, err := ParseSkill(src)
	if !errors.Is(err, ErrNoFrontmatter) {
		t.Fatalf("expected ErrNoFrontmatter, got %v", err)
	}
}
