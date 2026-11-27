package attest

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// In-toto statement constants of the §7.3.5 statement format.
//
// DefaultPredicateType is the type §7.3.5 defines, whose predicate is the
// empty object: it states that the signer attests the listed subjects and
// nothing else. A well-known type such as SLSA provenance would be wrong here,
// because its schema requires members an empty predicate lacks, so a tool
// that checks the predicate refuses or misreads the statement. Podium reads
// predicateType and never predicate, so the type adds nothing to verification.
const (
	StatementType        = "https://in-toto.io/Statement/v1"
	DefaultPredicateType = "https://lennylabs.github.io/podium/attestation/subjects/v1"
)

// ErrPredicateType reports a --predicate-type that is not an absolute URI,
// or that holds white space or a control character.
var ErrPredicateType = errors.New("attest: the predicate type is not an absolute URI")

// CheckPredicateType returns nil when s is an absolute URI with a scheme that
// holds no white space and no control character, and an error wrapping
// ErrPredicateType otherwise.
//
// Spec: §7.3.5
func CheckPredicateType(s string) error {
	if strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return fmt.Errorf("%w: %q holds white space or a control character", ErrPredicateType, s)
	}
	u, err := url.Parse(s)
	if err != nil || !u.IsAbs() || (u.Host == "" && u.Opaque == "") {
		return fmt.Errorf("%w: %q", ErrPredicateType, s)
	}
	return nil
}

// DefaultMaxSubjects is the §7.3.5 default bound on the subjects of one
// file, the layer subject included: the most subjects GitHub's attestation
// action accepts in one statement.
const DefaultMaxSubjects = 1024

// minSubjects is the smallest bound that leaves room for an artifact beside
// the layer subject every file begins with.
const minSubjects = 2

// ErrMaxSubjects reports a bound on the subjects of one file below
// minSubjects.
var ErrMaxSubjects = errors.New("attest: a file holds at least 2 subjects, the layer subject and one artifact")

// Format is a §7.3.5 output format of `podium attest`.
type Format string

// The output formats.
const (
	FormatChecksums Format = "checksums"
	FormatStatement Format = "statement"
)

// Output file name parts. Each format numbers its files from 1.
const (
	checksumsPrefix = "podium-subjects-"
	checksumsSuffix = ".sha256"
	statementPrefix = "podium-statement-"
	statementSuffix = ".json"
)

// ParseFormat returns the Format named s, or an error naming the formats.
func ParseFormat(s string) (Format, error) {
	switch f := Format(s); f {
	case FormatChecksums, FormatStatement:
		return f, nil
	}
	return "", fmt.Errorf("attest: format %q is neither %s nor %s", s, FormatChecksums, FormatStatement)
}

// FileName returns the name of the n-th file of the format, counting from 1.
//
// Spec: §7.3.5
func (f Format) FileName(n int) string {
	if f == FormatStatement {
		return statementPrefix + strconv.Itoa(n) + statementSuffix
	}
	return checksumsPrefix + strconv.Itoa(n) + checksumsSuffix
}

// OutputFileFormat returns the format whose file name name has, and false
// when name is the name of no file either format writes.
//
// Spec: §7.3.5
func OutputFileFormat(name string) (Format, bool) {
	switch {
	case numbered(name, checksumsPrefix, checksumsSuffix):
		return FormatChecksums, true
	case numbered(name, statementPrefix, statementSuffix):
		return FormatStatement, true
	}
	return "", false
}

// FirstSubjectReadLimit bounds the bytes FirstSubjectName reads. Every file
// `podium attest` writes names its first subject within the first few hundred
// bytes, and the bound keeps an oversized or endless file in the output
// directory from holding a run.
const FirstSubjectReadLimit = 64 << 10

// FirstSubjectName returns the name of the first subject of a file of format
// f read from r, which is the layer subject in every file `podium attest`
// writes, and false when the first FirstSubjectReadLimit bytes do not read as
// such a file. A run reads it to remove only the files an earlier run wrote
// for the same layer, so runs for several layer roots can share one output
// directory.
//
// Spec: §7.3.5
func FirstSubjectName(f Format, r io.Reader) (string, bool) {
	r = io.LimitReader(r, FirstSubjectReadLimit)
	if f == FormatStatement {
		return firstStatementSubject(json.NewDecoder(r))
	}
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil {
		return "", false
	}
	_, name, ok := strings.Cut(strings.TrimSuffix(line, "\n"), "  ")
	return name, ok
}

// firstStatementSubject reads the name of the first element of the subject
// member of the JSON object dec yields, token by token, so a prefix of the
// file is enough and the rest is never read.
func firstStatementSubject(dec *json.Decoder) (string, bool) {
	if !nextDelim(dec, '{') {
		return "", false
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return "", false
		}
		if key != "subject" {
			var skip json.RawMessage
			if dec.Decode(&skip) != nil {
				return "", false
			}
			continue
		}
		if !nextDelim(dec, '[') || !dec.More() {
			return "", false
		}
		var first statementSubject
		if dec.Decode(&first) != nil || first.Name == "" {
			return "", false
		}
		return first.Name, true
	}
	return "", false
}

// nextDelim reports whether dec's next token is the delimiter d.
func nextDelim(dec *json.Decoder, d json.Delim) bool {
	tok, err := dec.Token()
	return err == nil && tok == d
}

// numbered reports whether name is prefix, one or more ASCII digits, and
// suffix.
func numbered(name, prefix, suffix string) bool {
	mid, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return false
	}
	mid, ok = strings.CutSuffix(mid, suffix)
	if !ok || mid == "" {
		return false
	}
	return strings.Trim(mid, "0123456789") == ""
}

// Output is the §7.3.5 output of one run: its format and, for the statement
// format, the predicate type, which defaults to DefaultPredicateType.
type Output struct {
	Format        Format
	PredicateType string
}

// Write writes subjects to w in o's format.
//
// Spec: §7.3.5
func (o Output) Write(w io.Writer, subjects []Subject) error {
	if o.Format == FormatStatement {
		pt := o.PredicateType
		if pt == "" {
			pt = DefaultPredicateType
		}
		return WriteStatement(w, pt, subjects)
	}
	return WriteChecksums(w, subjects)
}

// SubjectFiles divides the subjects of one layer into the files of §7.3.5:
// each file begins with the layer subject and continues with at most
// maxSubjects-1 artifact subjects, in ascending byte-wise order of canonical
// ID. A layer with no artifact gets one file that holds the layer subject.
// artifacts is not modified.
//
// Spec: §7.3.5
func SubjectFiles(layer Subject, artifacts []Subject, maxSubjects int) ([][]Subject, error) {
	if maxSubjects < minSubjects {
		return nil, fmt.Errorf("%w: got %d", ErrMaxSubjects, maxSubjects)
	}
	sorted := append([]Subject(nil), artifacts...)
	sort.Slice(sorted, func(i, j int) bool {
		a, _ := ParseSubjectName(sorted[i].Name)
		b, _ := ParseSubjectName(sorted[j].Name)
		return a < b
	})
	per := maxSubjects - 1
	files := make([][]Subject, 0, len(sorted)/per+1)
	for start := 0; start == 0 || start < len(sorted); start += per {
		end := min(start+per, len(sorted))
		files = append(files, append([]Subject{layer}, sorted[start:end]...))
	}
	return files, nil
}

// WriteChecksums writes subjects in the format of the sha256sum tool: one
// line per subject with the 64 lowercase hexadecimal digits of its digest,
// two spaces, and its name. It refuses a subject whose name breaks the
// §4.7.6 character rule, which no line may hold, because a line feed in a
// name would add a subject line of its own, and a subject whose digest is not
// in the sha256:<hex> form. It writes nothing when it refuses.
//
// Spec: §4.7.6, §7.3.5
func WriteChecksums(w io.Writer, subjects []Subject) error {
	var b strings.Builder
	for _, s := range subjects {
		hexDigest, err := checkSubject(s)
		if err != nil {
			return err
		}
		b.WriteString(hexDigest + "  " + s.Name + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// statement is the in-toto Statement v1 document of the statement format.
type statement struct {
	Type          string             `json:"_type"`
	Subject       []statementSubject `json:"subject"`
	PredicateType string             `json:"predicateType"`
	Predicate     struct{}           `json:"predicate"`
}

type statementSubject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// WriteStatement writes subjects as one in-toto Statement v1 document with
// predicateType and an empty predicate, followed by a line feed. It refuses a
// predicate type CheckPredicateType refuses and the subjects WriteChecksums
// refuses, so both formats list one set, and writes nothing when it refuses.
//
// Spec: §7.3.5
func WriteStatement(w io.Writer, predicateType string, subjects []Subject) error {
	if err := CheckPredicateType(predicateType); err != nil {
		return err
	}
	st := statement{Type: StatementType, Subject: make([]statementSubject, 0, len(subjects)), PredicateType: predicateType}
	for _, s := range subjects {
		hexDigest, err := checkSubject(s)
		if err != nil {
			return err
		}
		st.Subject = append(st.Subject, statementSubject{Name: s.Name, Digest: map[string]string{"sha256": hexDigest}})
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	// A subject name may hold <, >, or &, which the default encoder writes
	// as \u escapes. The decoded name is the same either way; the plain form
	// keeps the file readable beside the checksums format.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(st); err != nil {
		return fmt.Errorf("attest: encode statement: %w", err)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// checkSubject returns the hexadecimal part of s's digest, or an error when
// s's name breaks the §4.7.6 character rule or its digest is not sha256:
// followed by 64 lowercase hexadecimal digits.
func checkSubject(s Subject) (string, error) {
	if err := CheckSubjectNameCharacters(s.Name); err != nil {
		return "", err
	}
	hexDigest, ok := strings.CutPrefix(s.Digest, digestPrefix)
	if !ok || len(hexDigest) != 64 || strings.Trim(hexDigest, "0123456789abcdef") != "" {
		return "", fmt.Errorf("attest: subject %q: digest %q is not sha256: and 64 lowercase hexadecimal digits", s.Name, s.Digest)
	}
	return hexDigest, nil
}
