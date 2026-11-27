package attest

import (
	"errors"
	"testing"
)

// excludedRanges are the §4.7.6 subject name exclusions, written from the
// spec's list rather than from excludedRune.
var excludedRanges = [][2]rune{
	{0x0000, 0x001F}, {0x007F, 0x009F}, {0x0020, 0x0020}, {0x00A0, 0x00A0},
	{0x1680, 0x1680}, {0x2000, 0x200A}, {0x2028, 0x2028}, {0x2029, 0x2029},
	{0x202F, 0x202F}, {0x205F, 0x205F}, {0x3000, 0x3000}, {0xFEFF, 0xFEFF},
	{0x005C, 0x005C},
}

// Spec: §4.7.6 — every excluded character, at the start, in the middle, and
// at the end of an ID, leaves the package with no subject name, and so does
// an ID that is not valid UTF-8. Every other code point up to U+FFFF passes,
// including the format characters the rule does not name, such as U+200B,
// U+200C, and U+2060, and U+FFFD itself.
func TestSubjectName_CharacterRule(t *testing.T) {
	t.Parallel()
	excluded := map[rune]bool{}
	for _, r := range excludedRanges {
		for c := r[0]; c <= r[1]; c++ {
			excluded[c] = true
			for _, id := range []string{string(c) + "a/b", "a/" + string(c) + "b", "a/b" + string(c)} {
				if _, err := SubjectName(id, "1.0.0"); !errors.Is(err, ErrNoSubjectName) {
					t.Errorf("SubjectName(%q) = %v, want ErrNoSubjectName", id, err)
				}
			}
		}
	}
	for c := rune(0); c <= 0xFFFF; c++ {
		if excluded[c] || (c >= 0xD800 && c <= 0xDFFF) {
			continue
		}
		if err := CheckSubjectNameCharacters("a/" + string(c)); err != nil {
			t.Errorf("U+%04X refused: %v", c, err)
		}
	}
	for _, id := range []string{"a/\xff", "a/\xc3", "\xed\xa0\x80"} {
		if _, err := SubjectName(id, ""); !errors.Is(err, ErrNoSubjectName) {
			t.Errorf("SubjectName(%q) = %v, want ErrNoSubjectName for invalid UTF-8", id, err)
		}
	}
	if err := CheckSubjectNameCharacters("a/\U0001F600"); err != nil {
		t.Errorf("a supplementary-plane character is refused: %v", err)
	}
}

// Spec: §4.7.6 — the name carries the authored version only in
// major.minor.patch form, a bare ID otherwise, and is split at its last @.
// An ID under the reserved .podium/ prefix gets no name.
func TestSubjectName_VersionForms(t *testing.T) {
	t.Parallel()
	cases := []struct{ version, want string }{
		{"1.4.0", "finance/close-books@1.4.0"},
		{"10.20.30", "finance/close-books@10.20.30"},
		{"", "finance/close-books"},
		{"1.4", "finance/close-books"},
		{"v1.4.0", "finance/close-books"},
		{"1.4.0-rc.1", "finance/close-books"},
		{"0.0.0-auto.3", "finance/close-books"},
		{" 1.4.0", "finance/close-books"},
		{"１.4.0", "finance/close-books"},
	}
	for _, tc := range cases {
		got, err := SubjectName("finance/close-books", tc.version)
		if err != nil || got != tc.want {
			t.Errorf("SubjectName(%q) = %q, %v, want %q", tc.version, got, err, tc.want)
			continue
		}
		id, v := ParseSubjectName(got)
		wantV := ""
		if got != "finance/close-books" {
			wantV = tc.version
		}
		if id != "finance/close-books" || v != wantV {
			t.Errorf("ParseSubjectName(%q) = %q, %q", got, id, v)
		}
	}
	if id, v := ParseSubjectName("a@b@1.0.0"); id != "a@b" || v != "1.0.0" {
		t.Errorf("ParseSubjectName splits at the first @: %q %q", id, v)
	}
	if _, err := SubjectName(".podium/layer", ""); !errors.Is(err, ErrReservedName) {
		t.Errorf("SubjectName(.podium/layer) = %v, want ErrReservedName", err)
	}
}

// Spec: §4.7.6 — the layer subject name trims the leading and trailing /
// characters of root and applies no other normalization, and a root that
// breaks the character rule gives the layer no name.
func TestLayerSubjectName(t *testing.T) {
	t.Parallel()
	cases := []struct{ root, want string }{
		{"", ".podium/layer"},
		{"/", ".podium/layer"},
		{"//", ".podium/layer"},
		{"skills", ".podium/layer/skills"},
		{"/skills/", ".podium/layer/skills"},
		{"//.claude/skills//", ".podium/layer/.claude/skills"},
		{"a//b", ".podium/layer/a//b"},
		{"a/./b", ".podium/layer/a/./b"},
		{"../b", ".podium/layer/../b"},
	}
	for _, tc := range cases {
		got, err := LayerSubjectName(tc.root)
		if err != nil || got != tc.want {
			t.Errorf("LayerSubjectName(%q) = %q, %v, want %q", tc.root, got, err, tc.want)
		}
		if !IsLayerSubjectName(got) || IsReservedName(got) {
			t.Errorf("%q is not read as a layer subject name", got)
		}
	}
	for _, root := range []string{"skills\n", "my skills", "a\\b", string(rune(0xFEFF)) + "skills"} {
		if _, err := LayerSubjectName(root); !errors.Is(err, ErrNoSubjectName) {
			t.Errorf("LayerSubjectName(%q) = %v, want ErrNoSubjectName", root, err)
		}
	}
}

// Spec: §4.7.6 — a name under .podium/ that is no layer subject name is
// reserved, and a statement that lists a name under .podium/signed-at/ is not
// accepted.
func TestReservedAndSignedAtNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{".podium/", ".podium/layer/", ".podium/layerx", ".podium/signed-at/2026-11-26T00:00:00Z", ".podium/other"} {
		if !IsReservedName(name) {
			t.Errorf("IsReservedName(%q) = false", name)
		}
	}
	for _, name := range []string{"finance/close-books@1.4.0", ".podium", "podium/layer", ".podium/layer", ".podium/layer/skills"} {
		if IsReservedName(name) {
			t.Errorf("IsReservedName(%q) = true", name)
		}
	}
	ok := []string{".podium/layer", "finance/close-books@1.4.0", ".podium/signed-at"}
	if err := CheckStatementSubjects(ok); err != nil {
		t.Errorf("CheckStatementSubjects(%q) = %v", ok, err)
	}
	bad := append(ok, ".podium/signed-at/1764115200")
	if err := CheckStatementSubjects(bad); !errors.Is(err, ErrSignedAtSubject) {
		t.Errorf("CheckStatementSubjects(%q) = %v, want ErrSignedAtSubject", bad, err)
	}
}

// Spec: §4.7.6 — a package file path that is not valid UTF-8 leaves its
// package with no subject name; any valid UTF-8 path passes, white space
// included, because the character rule applies to names alone.
func TestCheckEntryPath(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"a.txt", "dir/é.md", "with space.txt", "\U0001F600"} {
		if err := CheckEntryPath(p); err != nil {
			t.Errorf("CheckEntryPath(%q) = %v", p, err)
		}
	}
	for _, p := range []string{"bad-\xff.txt", "dir/\xc3", "\xed\xa0\x80"} {
		if err := CheckEntryPath(p); !errors.Is(err, ErrNoSubjectName) {
			t.Errorf("CheckEntryPath(%q) = %v, want ErrNoSubjectName", p, err)
		}
	}
}
