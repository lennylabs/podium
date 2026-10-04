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

// Spec: §4.7.10 — the manifest body is the bytes after the closing "---" with
// leading CR and LF removed, and a document with no frontmatter block has an
// empty body. No YAML is parsed, so malformed frontmatter still yields a body.
func TestManifestBodyOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, doc, want string
	}{
		{"lf", "---\nname: a\n---\nbody\n", "body\n"},
		{"crlf", "---\r\nname: a\r\n---\r\n\r\nbody\r\n", "body\r\n"},
		{"leading blank lines", "---\nname: a\n---\n\n\nbody", "body"},
		{"no trailing newline", "---\nname: a\n---", ""},
		{"text after close", "---\nname: a\n---body\n", "body\n"},
		{"earliest close", "---\na\n---\nb\n---\nc", "b\n---\nc"},
		{"malformed yaml", "---\n: : [\n---\nbody", "body"},
		{"no frontmatter", "body only\n", ""},
		{"unclosed", "---\nname: a\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ManifestBodyOf([]byte(tc.doc))
			if err != nil {
				t.Fatalf("ManifestBodyOf: %v", err)
			}
			if got != tc.want {
				t.Errorf("ManifestBodyOf = %q, want %q", got, tc.want)
			}
		})
	}
}
