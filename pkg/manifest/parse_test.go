package manifest

import (
	"encoding/binary"
	"errors"
	"testing"
	"unicode/utf16"
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

// utf16Frontmatter returns a document whose frontmatter block is text
// encoded as UTF-16 behind its byte order mark, between ASCII "---" lines.
func utf16Frontmatter(text string, order binary.AppendByteOrder, bom []byte) []byte {
	doc := append([]byte("---\n"), bom...)
	for _, u := range utf16.Encode([]rune(text)) {
		doc = order.AppendUint16(doc, u)
	}
	return append(doc, "\n---\n\nbody\n"...)
}

// Spec: §4.6 — the predicate is true for every top-level extends key the
// 0.5.x typed decoder resolved, whatever its spelling, encoding, or value,
// and false when the key is absent from the decoded frontmatter block.
func TestDeclaresExtends(t *testing.T) {
	t.Parallel()
	const body = "\nbody\n"
	trueCases := []struct {
		name string
		src  []byte
	}{
		{"block", []byte("---\ntype: context\nextends: a\n---\n" + body)},
		{"flow mapping", []byte("---\n{type: skill, extends: a}\n---\n" + body)},
		{"quoted key", []byte("---\ntype: context\n\"extends\": a\n---\n" + body)},
		{"hex escape", []byte("---\ntype: context\n\"\\x65xtends\": a\n---\n" + body)},
		{"unicode escape", []byte("---\ntype: context\n\"ext\\u0065nds\": a\n---\n" + body)},
		{"explicit key continuation", []byte("---\ntype: context\n? \"ext\\\n  ends\"\n: a\n---\n" + body)},
		{"binary implicit", []byte("---\ntype: context\n!!binary ZXh0ZW5kcw==: a\n---\n" + body)},
		{"binary explicit key", []byte("---\ntype: context\n? !!binary ZXh0ZW5kcw==\n: a\n---\n" + body)},
		{"binary verbose tag", []byte("---\ntype: context\n!<tag:yaml.org,2002:binary> ZXh0ZW5kcw==: a\n---\n" + body)},
		{"utf-16le", utf16Frontmatter("type: context\nextends: a\n", binary.LittleEndian, []byte{0xff, 0xfe})},
		{"utf-16be", utf16Frontmatter("type: context\nextends: a\n", binary.BigEndian, []byte{0xfe, 0xff})},
		{"empty value", []byte("---\ntype: context\nextends:\n---\n" + body)},
		{"empty string value", []byte("---\ntype: context\nextends: \"\"\n---\n" + body)},
		{"merge key", []byte("---\ntype: context\n<<: {extends: a}\n---\n" + body)},
		{"beside a typed-decode failure", []byte("---\ntype: context\ndescription: [x]\nextends: a\n---\n" + body)},
	}
	for _, tc := range trueCases {
		t.Run("true/"+tc.name, func(t *testing.T) {
			t.Parallel()
			if !DeclaresExtends(tc.src) {
				t.Errorf("DeclaresExtends(%q) = false, want true", tc.src)
			}
		})
	}
	falseCases := []struct {
		name string
		src  []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"implicit split quoted key", []byte("---\ntype: context\n\"ext\\\n  ends\": a\n---\n" + body)},
		{"nested", []byte("---\ntype: context\nmetadata:\n  extends: a\n---\n" + body)},
		{"value substring", []byte("---\ntype: context\ndescription: never extends anything\n---\n" + body)},
		{"body only", []byte("---\ntype: context\n---\n\nextends: a\n")},
		{"no frontmatter", []byte("type: context\nextends: a\n")},
		{"malformed flow mapping", []byte("---\n{type: skill, extends: a\n---\n" + body)},
	}
	for _, tc := range falseCases {
		t.Run("false/"+tc.name, func(t *testing.T) {
			t.Parallel()
			if DeclaresExtends(tc.src) {
				t.Errorf("DeclaresExtends(%q) = true, want false", tc.src)
			}
		})
	}
}
