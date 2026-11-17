package manifest

import (
	"encoding/binary"
	"errors"
	"strings"
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
		{"block", []byte("---\ntype: agent\nextends: a\n---\n" + body)},
		{"flow mapping", []byte("---\n{type: skill, extends: a}\n---\n" + body)},
		{"quoted key", []byte("---\ntype: agent\n\"extends\": a\n---\n" + body)},
		{"hex escape", []byte("---\ntype: agent\n\"\\x65xtends\": a\n---\n" + body)},
		{"unicode escape", []byte("---\ntype: agent\n\"ext\\u0065nds\": a\n---\n" + body)},
		{"explicit key continuation", []byte("---\ntype: agent\n? \"ext\\\n  ends\"\n: a\n---\n" + body)},
		{"binary implicit", []byte("---\ntype: agent\n!!binary ZXh0ZW5kcw==: a\n---\n" + body)},
		{"binary explicit key", []byte("---\ntype: agent\n? !!binary ZXh0ZW5kcw==\n: a\n---\n" + body)},
		{"binary verbose tag", []byte("---\ntype: agent\n!<tag:yaml.org,2002:binary> ZXh0ZW5kcw==: a\n---\n" + body)},
		{"utf-16le", utf16Frontmatter("type: agent\nextends: a\n", binary.LittleEndian, []byte{0xff, 0xfe})},
		{"utf-16be", utf16Frontmatter("type: agent\nextends: a\n", binary.BigEndian, []byte{0xfe, 0xff})},
		{"empty value", []byte("---\ntype: agent\nextends:\n---\n" + body)},
		{"empty string value", []byte("---\ntype: agent\nextends: \"\"\n---\n" + body)},
		{"merge key", []byte("---\ntype: agent\n<<: {extends: a}\n---\n" + body)},
		{"beside a typed-decode failure", []byte("---\ntype: agent\ndescription: [x]\nextends: a\n---\n" + body)},
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
		{"implicit split quoted key", []byte("---\ntype: agent\n\"ext\\\n  ends\": a\n---\n" + body)},
		{"nested", []byte("---\ntype: agent\nmetadata:\n  extends: a\n---\n" + body)},
		{"value substring", []byte("---\ntype: agent\ndescription: never extends anything\n---\n" + body)},
		{"body only", []byte("---\ntype: agent\n---\n\nextends: a\n")},
		{"no frontmatter", []byte("type: agent\nextends: a\n")},
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

// Spec: §4.1 — command and context are removed types, each with a non-empty
// replacement phrase, and the five remaining types are not. It fails against a
// predicate keyed on the type list rather than on the removed set.
func TestIsRemovedType(t *testing.T) {
	t.Parallel()
	for _, typ := range []ArtifactType{"command", "context"} {
		if !IsRemovedType(typ) {
			t.Errorf("IsRemovedType(%q) = false, want true", typ)
		}
		if RemovedTypeReplacement(typ) == "" {
			t.Errorf("RemovedTypeReplacement(%q) is empty", typ)
		}
	}
	if got := RemovedTypeReplacement("command"); !strings.Contains(got, `metadata.invocation is "user"`) {
		t.Errorf("command replacement = %q, want it to name metadata.invocation", got)
	}
	if got := RemovedTypeReplacement("context"); !strings.Contains(got, "references/") {
		t.Errorf("context replacement = %q, want it to name references/", got)
	}
	for _, typ := range []ArtifactType{TypeSkill, TypeAgent, TypeRule, TypeHook, TypeMCPServer, "Command", ""} {
		if IsRemovedType(typ) {
			t.Errorf("IsRemovedType(%q) = true, want false", typ)
		}
		if got := RemovedTypeReplacement(typ); got != "" {
			t.Errorf("RemovedTypeReplacement(%q) = %q, want empty", typ, got)
		}
	}
}

// Spec: §4.1, §4.6 — a stored row retires when its type is a removed type or
// its frontmatter declares extends:, and serves otherwise. It fails against a
// predicate that reads only DeclaresExtends or only the type.
func TestRetiredRow(t *testing.T) {
	t.Parallel()
	plain := []byte("---\ntype: skill\nversion: 1.0.0\n---\n\nbody\n")
	extends := []byte("---\ntype: agent\nextends: a/b\n---\n\nbody\n")
	cases := []struct {
		name string
		fm   []byte
		typ  string
		want bool
	}{
		{"extends", extends, "agent", true},
		{"command", []byte("---\ntype: command\n---\n\nbody\n"), "command", true},
		{"context", []byte("---\ntype: context\n---\n\nbody\n"), "context", true},
		{"removed type column with empty frontmatter", nil, "context", true},
		{"plain skill", plain, "skill", false},
		{"plain agent", []byte("---\ntype: agent\n---\n\nbody\n"), "agent", false},
		{"empty", nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := RetiredRow(tc.fm, tc.typ); got != tc.want {
				t.Errorf("RetiredRow(%q, %q) = %v, want %v", tc.fm, tc.typ, got, tc.want)
			}
		})
	}
}

// Spec: §4.3.4 — metadata values are strings, so a sequence or mapping under
// metadata fails ParseSkill, and the error names the key, keeps the YAML text,
// and adds the comma hint only for tags.
func TestParseSkill_NonStringMetadataNamesKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		fm       string
		contains []string
		absent   []string
	}{
		{
			name:     "tags list",
			fm:       "name: x\nmetadata:\n  tags: [a, b]\n",
			contains: []string{"metadata.tags is not a string", "tags is comma-separated", "cannot unmarshal !!seq into string"},
		},
		{
			name:     "mapping value",
			fm:       "name: x\nmetadata:\n  owner: {team: x}\n",
			contains: []string{"metadata.owner is not a string", "cannot unmarshal !!map into string"},
			absent:   []string{"comma-separated"},
		},
		{
			name:     "alias to a sequence",
			fm:       "name: x\nlist: &l [a, b]\nmetadata:\n  tags: *l\n",
			contains: []string{"metadata.tags is not a string"},
		},
		{
			name:     "aliased metadata mapping",
			fm:       "name: x\nbase: &m\n  owner: [a]\nmetadata: *m\n",
			contains: []string{"metadata.owner is not a string"},
		},
		{
			name:     "alias key names the resolved key",
			fm:       "name: x\nmetadata:\n  owner: &k tags\n  *k : [a, b]\n",
			contains: []string{"metadata.tags is not a string", "tags is comma-separated", "cannot unmarshal !!seq into string"},
			absent:   []string{"metadata.k "},
		},
		{
			name:     "merge key brings a sequence",
			fm:       "name: x\nmetadata:\n  owner: a\n  <<: {tags: [a, b]}\n",
			contains: []string{"metadata.tags is not a string", "tags is comma-separated", "cannot unmarshal !!seq into string"},
			absent:   []string{"metadata.<<"},
		},
		{
			name:     "aliased merge mapping",
			fm:       "name: x\nbase: &m {tags: [a]}\nmetadata:\n  <<: *m\n",
			contains: []string{"metadata.tags is not a string", "tags is comma-separated"},
		},
		{
			name:     "merge sequence skips an overridden key",
			fm:       "name: x\nmetadata:\n  owner: a\n  <<: [{owner: [x]}, {team: {a: b}}]\n",
			contains: []string{"metadata.team is not a string", "cannot unmarshal !!map into string"},
			absent:   []string{"metadata.owner"},
		},
		{
			name:   "self-referencing merge keeps the unchanged error",
			fm:     "name: x\ndescription: d\nmetadata: &m\n  <<: *m\n  a: b\n",
			absent: []string{"metadata.", "is not a string"},
		},
		{
			name:   "nested self-referencing merge keeps the unchanged error",
			fm:     "name: x\ndescription: d\nmetadata: &m\n  <<: {<<: *m}\n  a: b\n",
			absent: []string{"metadata.", "is not a string"},
		},
		{
			name:   "merge sequence holding its own mapping keeps the unchanged error",
			fm:     "name: x\ndescription: d\nmetadata: &m\n  <<: [*m]\n  a: b\n",
			absent: []string{"metadata.", "is not a string"},
		},
		{
			name:     "self-referencing merge still names a sequence",
			fm:       "name: x\nmetadata: &m\n  <<: *m\n  tags: [a]\n",
			contains: []string{"metadata.tags is not a string", "tags is comma-separated"},
		},
		{
			name:     "top-level merge key brings metadata",
			fm:       "name: x\n<<: {metadata: {tags: [a, b]}}\n",
			contains: []string{"metadata.tags is not a string", "tags is comma-separated", "cannot unmarshal !!seq into string"},
		},
		{
			name:     "top-level alias key names metadata",
			fm:       "name: x\nk: &k metadata\n*k : {tags: [a, b]}\n",
			contains: []string{"metadata.tags is not a string", "tags is comma-separated", "cannot unmarshal !!seq into string"},
		},
		{
			name:     "top-level merge sequence brings metadata",
			fm:       "name: x\n<<: [{owner: a}, {metadata: {tags: [a, b]}}]\n",
			contains: []string{"metadata.tags is not a string", "tags is comma-separated"},
		},
		{
			name:     "direct metadata takes precedence over a merged one",
			fm:       "name: x\nmetadata: {owner: [a]}\n<<: {metadata: {tags: [a]}}\n",
			contains: []string{"metadata.owner is not a string"},
			absent:   []string{"metadata.tags"},
		},
		{
			name:   "self-referencing top-level merge keeps the unchanged error",
			fm:     "--- &r\nname: [x]\n<<: *r\n",
			absent: []string{"metadata.", "is not a string"},
		},
		{
			name:     "second error stays visible",
			fm:       "name: [x]\nmetadata:\n  tags: [a, b]\n",
			contains: []string{"metadata.tags is not a string", "line 1", "line 3"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseSkill([]byte("---\n" + tc.fm + "---\nBody.\n"))
			if !errors.Is(err, ErrInvalidYAML) {
				t.Fatalf("err = %v, want ErrInvalidYAML", err)
			}
			for _, s := range tc.contains {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("err = %q, want it to contain %q", err, s)
				}
			}
			for _, s := range tc.absent {
				if strings.Contains(err.Error(), s) {
					t.Errorf("err = %q, want no %q", err, s)
				}
			}
		})
	}
}

// Spec: §4.3.4 — an alias to a scalar is a string and parses, and a decode
// failure outside metadata keeps the unchanged error with no metadata key.
func TestParseSkill_MetadataScalarsAndOtherFailures(t *testing.T) {
	t.Parallel()
	s, err := ParseSkill([]byte("---\nname: x\ndescription: &d \"a, b\"\nmetadata:\n  tags: *d\n  <<: {owner: finance}\n---\nBody.\n"))
	if err != nil {
		t.Fatalf("alias to a scalar: %v", err)
	}
	if s.Metadata["tags"] != "a, b" || s.Metadata["owner"] != "finance" {
		t.Errorf("metadata = %v, want tags %q and merged owner", s.Metadata, "a, b")
	}
	for _, fm := range []string{
		"name: [x]\nmetadata:\n  tags: a\n  <<: {owner: finance}\n",
		"name: [x]\nmetadata: [a]\n",
		"name: [x]\n",
		"- a\n- b\n",
		"name: [x\nmetadata:\n  tags: [a, b]\n",
	} {
		_, err := ParseSkill([]byte("---\n" + fm + "---\nBody.\n"))
		if !errors.Is(err, ErrInvalidYAML) {
			t.Fatalf("%q: err = %v, want ErrInvalidYAML", fm, err)
		}
		if strings.Contains(err.Error(), "metadata.") {
			t.Errorf("%q: err = %q, want no metadata key", fm, err)
		}
	}
}
