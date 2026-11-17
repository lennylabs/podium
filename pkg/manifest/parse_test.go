package manifest

import (
	"encoding/binary"
	"errors"
	"reflect"
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

// Spec: §4.1, §4.3.4 — a sequence or a mapping under a metadata key does not
// make the frontmatter unparseable. ParseSkill keeps every scalar value in
// Metadata and lists each nested value in NonStringMetadata, in document
// order with merged keys after direct ones, through aliases, alias keys, and
// merge keys. It fails against a parser that refuses the nested value, drops
// the scalar siblings, or loses the key order.
func TestParseSkill_NonStringMetadataParses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		fm         string
		wantNested []NonStringMetadata
		wantScalar map[string]string
	}{
		{
			name: "block mapping and sequence under unread keys",
			fm:   "name: x\ndescription: d\nmetadata:\n  requires:\n    bins: [\"lark-cli\"]\n  cliHelp: \"lark-cli docs --help\"\n  credits:\n    - alice\n    - bob\n",
			wantNested: []NonStringMetadata{
				{Key: "requires", Kind: "mapping", Value: `{bins: ["lark-cli"]}`},
				{Key: "credits", Kind: "sequence", Value: "[alice, bob]"},
			},
			wantScalar: map[string]string{"cliHelp": "lark-cli docs --help"},
		},
		{
			name:       "flow-style metadata",
			fm:         "name: x\ndescription: d\nmetadata: {\"openclaw\": {\"emoji\": \"x\", \"requires\": {\"bins\": [\"uv\"]}}, \"version\": \"1.0.0\"}\n",
			wantNested: []NonStringMetadata{{Key: "openclaw", Kind: "mapping", Value: `{"emoji": "x", "requires": {"bins": ["uv"]}}`}},
			wantScalar: map[string]string{"version": "1.0.0"},
		},
		{
			name:       "mapping under version",
			fm:         "name: x\nmetadata:\n  version:\n    major: 1\n",
			wantNested: []NonStringMetadata{{Key: "version", Kind: "mapping", Value: "{major: 1}"}},
		},
		{
			name:       "sequence under tags",
			fm:         "name: x\nmetadata:\n  tags: [a, b]\n",
			wantNested: []NonStringMetadata{{Key: "tags", Kind: "sequence", Value: "[a, b]"}},
		},
		{
			name:       "alias to a sequence",
			fm:         "name: x\nlist: &l [a, b]\nmetadata:\n  tags: *l\n",
			wantNested: []NonStringMetadata{{Key: "tags", Kind: "sequence", Value: "&l [a, b]"}},
		},
		{
			name:       "aliased metadata mapping",
			fm:         "name: x\nbase: &m\n  owner: [a]\nmetadata: *m\n",
			wantNested: []NonStringMetadata{{Key: "owner", Kind: "sequence", Value: "[a]"}},
		},
		{
			name:       "alias key names the resolved key",
			fm:         "name: x\nmetadata:\n  owner: &k tags\n  *k : [a, b]\n",
			wantNested: []NonStringMetadata{{Key: "tags", Kind: "sequence", Value: "[a, b]"}},
			wantScalar: map[string]string{"owner": "tags"},
		},
		{
			name:       "merge key brings a sequence after the direct keys",
			fm:         "name: x\nmetadata:\n  <<: {tags: [a, b]}\n  owner: {team: x}\n",
			wantNested: []NonStringMetadata{{Key: "owner", Kind: "mapping", Value: "{team: x}"}, {Key: "tags", Kind: "sequence", Value: "[a, b]"}},
		},
		{
			name:       "merge sequence skips an overridden key",
			fm:         "name: x\nmetadata:\n  owner: a\n  <<: [{owner: [x]}, {team: {a: b}}]\n",
			wantNested: []NonStringMetadata{{Key: "team", Kind: "mapping", Value: "{a: b}"}},
			wantScalar: map[string]string{"owner": "a"},
		},
		{
			name:       "top-level merge key brings metadata",
			fm:         "name: x\n<<: {metadata: {tags: [a, b]}}\n",
			wantNested: []NonStringMetadata{{Key: "tags", Kind: "sequence", Value: "[a, b]"}},
		},
		{
			name:       "top-level alias key names metadata",
			fm:         "name: x\nk: &k metadata\n*k : {tags: [a, b]}\n",
			wantNested: []NonStringMetadata{{Key: "tags", Kind: "sequence", Value: "[a, b]"}},
		},
		{
			name:       "direct metadata takes precedence over a merged one",
			fm:         "name: x\nmetadata: {owner: [a]}\n<<: {metadata: {tags: [a]}}\n",
			wantNested: []NonStringMetadata{{Key: "owner", Kind: "sequence", Value: "[a]"}},
		},
		{
			name:       "a merge source listed twice is walked once",
			fm:         "name: x\nbase: &b {owner: [a]}\nmetadata:\n  <<: [*b, *b]\n",
			wantNested: []NonStringMetadata{{Key: "owner", Kind: "sequence", Value: "[a]"}},
		},
		{
			name:       "a null key is dropped as yaml.v3 drops it",
			fm:         "name: x\nmetadata:\n  ~: [a]\n  owner: alice\n",
			wantScalar: map[string]string{"owner": "alice"},
		},
		{
			name:       "comments are not part of the named value",
			fm:         "name: x\nmetadata:\n  requires: # tools\n    bins: # list\n      - uv # runner\n",
			wantNested: []NonStringMetadata{{Key: "requires", Kind: "mapping", Value: "{bins: [uv]}"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, err := ParseSkill([]byte("---\n" + tc.fm + "---\nBody.\n"))
			if err != nil {
				t.Fatalf("ParseSkill: %v", err)
			}
			if !reflect.DeepEqual(s.NonStringMetadata, tc.wantNested) {
				t.Errorf("NonStringMetadata = %+v, want %+v", s.NonStringMetadata, tc.wantNested)
			}
			for _, nv := range tc.wantNested {
				if _, ok := s.Metadata[nv.Key]; ok {
					t.Errorf("Metadata holds the nested key %q", nv.Key)
				}
			}
			for k, v := range tc.wantScalar {
				if s.Metadata[k] != v {
					t.Errorf("Metadata[%q] = %q, want %q", k, s.Metadata[k], v)
				}
			}
			if s.Body != "Body.\n" {
				t.Errorf("Body = %q", s.Body)
			}
		})
	}
}

// Spec: §4.1, §4.3.4 — a long nested value is named by a capped single-line
// rendering, so a diagnostic stays one readable line.
func TestParseSkill_NonStringMetadataValueCapped(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("abcdefghij, ", 30)
	s, err := ParseSkill([]byte("---\nname: x\nmetadata:\n  notes: [" + long + "z]\n---\n"))
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}
	v := s.NonStringMetadata[0].Value
	if !strings.HasSuffix(v, "...") || len([]rune(v)) != nonStringValueMaxRunes+3 {
		t.Errorf("Value = %q (%d runes), want %d runes and an ellipsis", v, len([]rune(v)), nonStringValueMaxRunes+3)
	}
}

// Spec: §4.1 — a field whose value has a kind its definition does not allow
// still makes the frontmatter unparseable: a description or a name that is a
// mapping, a metadata that is not a mapping, a root that is not a mapping, and
// a cyclic merge. A nested metadata value beside such a field does not hide
// the error. It fails against a parser that accepts any of them.
func TestParseSkill_KindErrorsStillFail(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		fm       string
		contains string
	}{
		{"description mapping", "name: x\ndescription:\n  text: d\n", "cannot unmarshal !!map into string"},
		{"name sequence beside nested metadata", "name: [x]\nmetadata:\n  tags: [a, b]\n", "line 1: cannot unmarshal !!seq into string"},
		{"metadata sequence", "name: x\nmetadata: [a]\n", "line 2: metadata is not a mapping"},
		{"metadata string", "name: x\nmetadata: a\n", "metadata is not a mapping"},
		{"root sequence", "- a\n- b\n", "cannot unmarshal !!seq into a SKILL.md frontmatter mapping"},
		{"repeated metadata key", "name: x\nmetadata:\n  a: b\n  a: c\n", "already defined"},
		{"scalar that does not decode", "name: x\nmetadata:\n  a: !!binary \"%%%\"\n", "!!binary value contains invalid base64 data"},
		{"self-referencing metadata merge", "name: x\nmetadata: &m\n  <<: *m\n  tags: [a]\n", "contains itself"},
		{"self-referencing top-level merge", "--- &r\nname: [x]\n<<: *r\n", "contains itself"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseSkill([]byte("---\n" + tc.fm + "---\nBody.\n"))
			if !errors.Is(err, ErrInvalidYAML) {
				t.Fatalf("err = %v, want ErrInvalidYAML", err)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("err = %q, want it to contain %q", err, tc.contains)
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
