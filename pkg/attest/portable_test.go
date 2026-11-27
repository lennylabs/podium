package attest

import (
	"fmt"
	"testing"
)

// Spec: §4.2 — a portable name ends in neither "." nor a space, holds none of
// < > : " | ? *, is no Windows device name alone or before spaces and "." or
// ":", holds no code point HFS+ ignores, and is not in the 8.3 short-name
// form. Each case is written from the spec's list.
func TestIsPortableName(t *testing.T) {
	t.Parallel()
	notPortable := []string{
		"close-books.", "close-books ", ".", "..",
		"a<b", "a>b", "a:b", `a"b`, "a|b", "a?b", "a*b",
		"CON", "con", "Con", "PRN", "AUX", "aux", "NUL", "nul", "CONIN$", "conout$",
		"COM0", "com9", "LPT0", "lpt9", "COM¹", "com²", "LPT³",
		"CON.txt", "CON .txt", "con  .tar.gz", "NUL:stream", "aux.md", "nul.",
		"\u200C.git", "a\u200Db", "a\u200Fb", "a\u202Ab", "a\u202Eb", "a\u206Ab", "a\u206Fb", "\uFEFFgit",
		"GIT~1", "git~1", "PODIUM~1", "CLOSE-~1", "CLOSE-~1.MD", "~1", "ABCDEF~9", "AB~12345", "X~1.a", "X~1.abc",
	}
	for _, seg := range notPortable {
		if IsPortableName(seg) {
			t.Errorf("IsPortableName(%q) = true, want false", seg)
		}
	}
	portable := []string{
		"close-books", "README.md", ".gitignore", "a b", "CONSOLE", "con-tool", "COM10", "LPT", "COM",
		"COMA", "CON x.txt", "xCON", "COM¹x", "CONOUT", "\u200Bzero-width-space",
		"release~2", "release~2.md", "ABCDEFGH~1", "GIT~", "GIT~X", "GIT~1.tar.gz", "GIT~1.json", "a~1b",
		"café", "日本語",
	}
	for _, seg := range portable {
		if !IsPortableName(seg) {
			t.Errorf("IsPortableName(%q) = false, want true", seg)
		}
	}
}

// Spec: §4.2 — NonPortableSegment returns the first segment of a slash path
// that is not portable, in path order.
func TestNonPortableSegment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path, seg string
		ok        bool
	}{
		{"finance/close-books", "", false},
		{"refs/notes.md", "", false},
		{"finance/CON/aux.md", "CON", true},
		{"scripts/GIT~1/config", "GIT~1", true},
		{"a/b./c?", "b.", true},
	}
	for _, c := range cases {
		seg, ok := NonPortableSegment(c.path)
		if seg != c.seg || ok != c.ok {
			t.Errorf("NonPortableSegment(%q) = %q, %v, want %q, %v", c.path, seg, ok, c.seg, c.ok)
		}
	}
}

// Spec: §4.2 — every device number from 0 to 9 is a device for COM and LPT,
// in each letter case, alone and before an extension.
func TestIsPortableName_DeviceNumbers(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{"COM", "com", "Lpt", "LPT"} {
		for n := 0; n <= 9; n++ {
			for _, seg := range []string{fmt.Sprintf("%s%d", prefix, n), fmt.Sprintf("%s%d.log", prefix, n)} {
				if IsPortableName(seg) {
					t.Errorf("IsPortableName(%q) = true, want false", seg)
				}
			}
		}
	}
}
