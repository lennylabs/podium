package manifest

import (
	"errors"
	"testing"
)

// Spec: §4.3 Artifact Manifest Schema — SplitFrontmatter splits on the
// leading "---" and the next "---" delimiter; the prose body is everything
// after, with leading whitespace trimmed.
func TestSplitFrontmatter_RoundTrip(t *testing.T) {
	t.Parallel()
	src := []byte(`---
type: skill
name: x
---

prose body
spans multiple lines
`)
	fm, body, err := SplitFrontmatter(src)
	if err != nil {
		t.Fatalf("SplitFrontmatter: %v", err)
	}
	if string(fm) != "type: skill\nname: x" {
		t.Errorf("frontmatter = %q", fm)
	}
	if string(body) != "prose body\nspans multiple lines\n" {
		t.Errorf("body = %q", body)
	}
}

// Spec: §4.3 — without a leading "---" the input is rejected.
func TestSplitFrontmatter_RejectsMissingLeadingDelimiter(t *testing.T) {
	t.Parallel()
	src := []byte(`type: skill
name: x
---

body
`)
	_, _, err := SplitFrontmatter(src)
	if !errors.Is(err, ErrNoFrontmatter) {
		t.Fatalf("got %v, want ErrNoFrontmatter", err)
	}
}

// Spec: §4.2 Registry Layout on Disk — canonical artifact IDs use forward
// slashes regardless of the host OS.
func TestJoinCanonicalPath_UsesForwardSlash(t *testing.T) {
	t.Parallel()
	got := JoinCanonicalPath("finance", "ap", "pay-invoice")
	if got != "finance/ap/pay-invoice" {
		t.Errorf("JoinCanonicalPath = %q, want finance/ap/pay-invoice", got)
	}
}
