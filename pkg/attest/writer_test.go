package attest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func hexOf(c byte) string { return strings.Repeat(string(c), 64) }

func sub(name string, c byte) Subject { return Subject{Name: name, Digest: "sha256:" + hexOf(c)} }

// Spec: §7.3.5 — the checksums format is one line per subject in the format
// of the sha256sum tool: 64 lowercase hexadecimal digits, two spaces, and the
// name, ended by a line feed, in the order given.
func TestWriteChecksums_Format(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	subjects := []Subject{sub(".podium/layer/skills", 'a'), sub("finance/close-books@1.4.0", 'b'), sub("finance/ledger", 'c')}
	if err := WriteChecksums(&b, subjects); err != nil {
		t.Fatalf("WriteChecksums: %v", err)
	}
	want := hexOf('a') + "  .podium/layer/skills\n" + hexOf('b') + "  finance/close-books@1.4.0\n" + hexOf('c') + "  finance/ledger\n"
	if b.String() != want {
		t.Errorf("checksums =\n%s\nwant\n%s", b.String(), want)
	}
}

// Spec: §7.3.5 — the statement format is one in-toto Statement v1 document
// with the members in the order _type, subject, predicateType, predicate, an
// empty predicate, each subject's digest under sha256 without its prefix,
// and a line feed after it. A name with < or & is written as itself.
func TestWriteStatement_Format(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := (Output{Format: FormatStatement}).Write(&b, []Subject{sub(".podium/layer", 'a'), sub("team/a<b&c@1.0.0", 'b')}); err != nil {
		t.Fatalf("WriteStatement: %v", err)
	}
	want := `{"_type":"https://in-toto.io/Statement/v1","subject":[` +
		`{"name":".podium/layer","digest":{"sha256":"` + hexOf('a') + `"}},` +
		`{"name":"team/a<b&c@1.0.0","digest":{"sha256":"` + hexOf('b') + `"}}],` +
		`"predicateType":"https://lennylabs.github.io/podium/attestation/subjects/v1","predicate":{}}` + "\n"
	if b.String() != want {
		t.Errorf("statement =\n%s\nwant\n%s", b.String(), want)
	}
	var doc struct {
		Type      string          `json:"_type"`
		Predicate json.RawMessage `json:"predicate"`
	}
	if err := json.Unmarshal(b.Bytes(), &doc); err != nil || doc.Type != StatementType || string(doc.Predicate) != "{}" {
		t.Errorf("statement decodes to %+v, %v", doc, err)
	}
}

// Spec: §4.7.6, §7.3.5 — both writers refuse a subject whose name breaks the
// character rule, which would put a line feed or a backslash into a
// checksums line, and a digest that is not sha256: with 64 lowercase
// hexadecimal digits. A refused write writes nothing.
func TestWriters_RefuseBadSubjects(t *testing.T) {
	t.Parallel()
	bad := []Subject{
		sub("team/a\nb", 'a'),
		sub(`team\b`, 'a'),
		sub("team/a b", 'a'),
		{Name: "team/x", Digest: "sha256:" + strings.Repeat("A", 64)},
		{Name: "team/x", Digest: "sha512:" + hexOf('a')},
		{Name: "team/x", Digest: "sha256:" + hexOf('a')[:63]},
		{Name: "team/x", Digest: hexOf('a')},
	}
	for _, s := range bad {
		for _, f := range []Format{FormatChecksums, FormatStatement} {
			var b bytes.Buffer
			if err := (Output{Format: f}).Write(&b, []Subject{sub(".podium/layer", 'a'), s}); err == nil {
				t.Errorf("%s writer accepted %+v", f, s)
			}
			if b.Len() != 0 {
				t.Errorf("%s writer wrote %q for a refused subject", f, b.String())
			}
		}
	}
	if err := WriteChecksums(&bytes.Buffer{}, []Subject{sub("team/a\nb", 'a')}); !errors.Is(err, ErrNoSubjectName) {
		t.Errorf("line feed in a name = %v, want ErrNoSubjectName", err)
	}
}

// failingWriter fails every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

// Spec: §7.3.5 — a failed write is reported.
func TestWriters_ReportWriteFailure(t *testing.T) {
	t.Parallel()
	for _, f := range []Format{FormatChecksums, FormatStatement} {
		if err := (Output{Format: f}).Write(failingWriter{}, []Subject{sub(".podium/layer", 'a')}); err == nil {
			t.Errorf("%s writer ignored a failed write", f)
		}
	}
}

// Spec: §7.3.5 — each file begins with the layer subject and holds at most
// maxSubjects subjects, the artifacts follow in ascending byte-wise order of
// canonical ID, and a layer with no artifact gets one file.
func TestSubjectFiles_Split(t *testing.T) {
	t.Parallel()
	layer := sub(".podium/layer", 'f')
	// Ordered by ID, a < a-b; ordered by name, a-b@1.0.0 < a@2.0.0.
	artifacts := []Subject{sub("z", '1'), sub("a@2.0.0", '2'), sub("a-b@1.0.0", '3'), sub("m", '4'), sub("b", '5')}
	files, err := SubjectFiles(layer, artifacts, 3)
	if err != nil {
		t.Fatalf("SubjectFiles: %v", err)
	}
	var got []string
	for _, f := range files {
		names := make([]string, 0, len(f))
		for _, s := range f {
			names = append(names, s.Name)
		}
		got = append(got, strings.Join(names, ","))
	}
	want := []string{
		".podium/layer,a@2.0.0,a-b@1.0.0",
		".podium/layer,b,m",
		".podium/layer,z",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("files = %v, want %v", got, want)
	}
	if artifacts[0].Name != "z" {
		t.Errorf("SubjectFiles reordered its input: %v", artifacts)
	}

	files, err = SubjectFiles(layer, nil, DefaultMaxSubjects)
	if err != nil || len(files) != 1 || len(files[0]) != 1 || files[0][0] != layer {
		t.Errorf("empty layer = %v, %v, want one file with the layer subject", files, err)
	}
	files, err = SubjectFiles(layer, artifacts[:2], 3)
	if err != nil || len(files) != 1 {
		t.Errorf("a layer that fills one file exactly = %d files, %v, want 1", len(files), err)
	}
	for _, n := range []int{-1, 0, 1} {
		if _, err := SubjectFiles(layer, artifacts, n); !errors.Is(err, ErrMaxSubjects) {
			t.Errorf("SubjectFiles(max %d) = %v, want ErrMaxSubjects", n, err)
		}
	}
}

// Spec: §7.3.5 — the formats, their file names, and the names a run removes.
func TestFormat_NamesAndParse(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"checksums", "statement"} {
		if f, err := ParseFormat(s); err != nil || string(f) != s {
			t.Errorf("ParseFormat(%q) = %q, %v", s, f, err)
		}
	}
	for _, s := range []string{"", "Checksums", "json", "sha256sum"} {
		if _, err := ParseFormat(s); err == nil {
			t.Errorf("ParseFormat(%q) accepted", s)
		}
	}
	if got := FormatChecksums.FileName(1); got != "podium-subjects-1.sha256" {
		t.Errorf("checksums file name = %q", got)
	}
	if got := FormatStatement.FileName(12); got != "podium-statement-12.json" {
		t.Errorf("statement file name = %q", got)
	}
	for n, want := range map[string]Format{"podium-subjects-1.sha256": FormatChecksums, "podium-subjects-007.sha256": FormatChecksums, "podium-statement-3.json": FormatStatement} {
		if f, ok := OutputFileFormat(n); !ok || f != want {
			t.Errorf("OutputFileFormat(%q) = %q, %v, want %q", n, f, ok, want)
		}
	}
	for _, n := range []string{"podium-subjects-.sha256", "podium-subjects-1.sha256.bak", "podium-subjects-a.sha256", "subjects-1.sha256", "podium-statement-1.sha256", "podium-subjects-1.json", "notes.md"} {
		if _, ok := OutputFileFormat(n); ok {
			t.Errorf("OutputFileFormat(%q) matched", n)
		}
	}
}

// Spec: §7.3.5 — the first subject of a file each format writes is read
// back, and a file that does not read as one gives none.
func TestFirstSubjectName(t *testing.T) {
	t.Parallel()
	subjects := []Subject{sub(".podium/layer/a", '1'), sub("team/x@1.0.0", '2')}
	for _, f := range []Format{FormatChecksums, FormatStatement} {
		var b bytes.Buffer
		if err := (Output{Format: f}).Write(&b, subjects); err != nil {
			t.Fatal(err)
		}
		if name, ok := FirstSubjectName(f, bytes.NewReader(b.Bytes())); !ok || name != ".podium/layer/a" {
			t.Errorf("%s: FirstSubjectName = %q, %v", f, name, ok)
		}
	}
	for _, c := range []struct {
		f Format
		b string
	}{
		{FormatChecksums, "stale\n"},
		{FormatChecksums, hexOf('a') + "  .podium/layer"},
		{FormatStatement, `{"subject":[]}`},
		{FormatStatement, `{"subject":[{"digest":{}}]}`},
		{FormatStatement, `{"subject":{}}`},
		{FormatStatement, `{"_type":"x"}`},
		{FormatStatement, `{"_type":`},
		{FormatStatement, `[]`},
		{FormatStatement, "not json"},
	} {
		if name, ok := FirstSubjectName(c.f, strings.NewReader(c.b)); ok {
			t.Errorf("%s: FirstSubjectName(%q) = %q, want none", c.f, c.b, name)
		}
	}
	// A member before subject is skipped, and a truncated tail after the
	// first subject is never read.
	if name, ok := FirstSubjectName(FormatStatement, strings.NewReader(`{"_type":"x","subject":[{"name":".podium/layer","digest":{}},{"na`)); !ok || name != ".podium/layer" {
		t.Errorf("statement prefix = %q, %v, want .podium/layer", name, ok)
	}
}

// Spec: §7.3.5 — FirstSubjectName reads at most FirstSubjectReadLimit bytes,
// so a file whose first line or first subject lies beyond the bound gives
// none, and an endless reader returns.
func TestFirstSubjectName_Bounded(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", FirstSubjectReadLimit)
	if _, ok := FirstSubjectName(FormatChecksums, strings.NewReader(long+"  .podium/layer\n")); ok {
		t.Error("a first line beyond the bound gave a subject")
	}
	if _, ok := FirstSubjectName(FormatStatement, strings.NewReader(`{"pad":"`+long+`","subject":[{"name":".podium/layer"}]}`)); ok {
		t.Error("a subject beyond the bound gave a name")
	}
	for _, f := range []Format{FormatChecksums, FormatStatement} {
		if _, ok := FirstSubjectName(f, endless{}); ok {
			t.Errorf("%s: an endless reader gave a subject", f)
		}
	}
}

// endless yields spaces without end, as a link to a device can.
type endless struct{}

func (endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

// Spec: §7.3.5 — the statement format writes the default Podium predicate
// type or the one given, which must be an absolute URI with no white space or
// control character; a refused type writes nothing.
func TestOutput_PredicateType(t *testing.T) {
	t.Parallel()
	subjects := []Subject{sub(".podium/layer", '1')}
	for _, pt := range []string{"https://slsa.dev/provenance/v1", "urn:example:subjects"} {
		var b bytes.Buffer
		if err := (Output{Format: FormatStatement, PredicateType: pt}).Write(&b, subjects); err != nil {
			t.Fatalf("predicate type %q: %v", pt, err)
		}
		if !strings.Contains(b.String(), `"predicateType":"`+pt+`","predicate":{}}`) {
			t.Errorf("statement = %s, want predicateType %q and an empty predicate", b.String(), pt)
		}
	}
	for _, pt := range []string{"subjects/v1", "https:", "https://x/a b", "https://x/\nb", "://x", "%zz"} {
		var b bytes.Buffer
		if err := (Output{Format: FormatStatement, PredicateType: pt}).Write(&b, subjects); !errors.Is(err, ErrPredicateType) || b.Len() != 0 {
			t.Errorf("predicate type %q = %v, wrote %q, want ErrPredicateType and nothing", pt, err, b.String())
		}
	}
	if err := CheckPredicateType(DefaultPredicateType); err != nil {
		t.Errorf("the default predicate type is refused: %v", err)
	}
}

// Spec: §4.7.6, §7.3.5 — a layer root loses its leading and trailing /
// characters, and a root that then holds an empty, ".", or ".." segment, or a
// character the subject name rule excludes, is refused.
func TestCheckLayerRoot(t *testing.T) {
	t.Parallel()
	ok := map[string]string{"": "", "/": "", "skills": "skills", "/skills/": "skills", "//a/b//": "a/b", ".claude/skills": ".claude/skills"}
	for in, want := range ok {
		if got, err := CheckLayerRoot(in); err != nil || got != want {
			t.Errorf("CheckLayerRoot(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"a//b", "a/./b", "./a", "a/..", "../a", "."} {
		if _, err := CheckLayerRoot(in); !errors.Is(err, ErrLayerRoot) {
			t.Errorf("CheckLayerRoot(%q) = %v, want ErrLayerRoot", in, err)
		}
	}
	for _, in := range []string{"a b", `a\b`, "a\nb"} {
		if _, err := CheckLayerRoot(in); !errors.Is(err, ErrNoSubjectName) {
			t.Errorf("CheckLayerRoot(%q) = %v, want ErrNoSubjectName", in, err)
		}
	}
}
