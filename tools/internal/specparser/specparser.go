// Package specparser scans a Podium checkout for spec citations and produces
// the data the speccov and phasegate tools format.
//
// Two inputs:
//
//   - Spec sections, parsed from spec/*.md by reading every line that starts
//     with one or more "#" characters and matches a §-style numbering pattern.
//     The result is a deduplicated, ordered list of (id, title) pairs.
//   - Test functions, parsed from *_test.go (Go), test_*.py / *_test.py
//     (Python), and *.test.ts / *.test.tsx (TypeScript). Each test's
//     immediately-preceding comment block is scanned for `Spec:` and `Phase:`
//     annotations.
//
// The parser is intentionally minimal so the bootstrap stage does not depend
// on third-party AST libraries. Go uses go/parser; Python and TypeScript use
// regex-based scans. Both are sufficient for citation extraction (the file
// content above each test function is comment text, not arbitrary code).
package specparser

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Section is one spec section parsed from spec/*.md.
type Section struct {
	ID    string // e.g., "§4.6" or "§4.6.3"
	Title string // text after the ID on the heading line
	File  string // relative path of the source markdown
	Line  int    // line number in the source markdown
}

// Citation is a spec reference parsed from a test annotation.
type Citation struct {
	SectionID string // "§4.6", "§4.6.3", "n/a", or ""
	Note      string // assertion text after the section title
}

// MatrixCell is a per-cell tag attached to a test, asserting that the
// test verifies one cell of a documented spec matrix (§6.7.1 capability
// matrix, §6.10 error codes, §6.9 failure modes, §4.6 visibility unions).
type MatrixCell struct {
	// Matrix identifies the matrix (e.g., "§6.7.1").
	Matrix string
	// Keys identifies the specific cell within the matrix
	// (e.g., ["claude-code", "rule_mode_glob"]).
	Keys []string
}

// Test represents a single parsed test function.
type Test struct {
	Name     string
	File     string
	Line     int
	Phase    int
	Citation Citation
	Matrix   []MatrixCell
	Language string // "go" | "python" | "typescript"
}

// LoadSpecSections walks the given directory and returns every spec section
// it finds, in stable lexicographic order by section ID. A section ID is the
// first whitespace-delimited token on a heading that matches one of:
//
//	## 4. Title           -> §4
//	## 4.6 Title          -> §4.6
//	### 4.6.3 Title       -> §4.6.3
//	## §4.6 Title         -> §4.6 (already decorated)
func LoadSpecSections(dir string) ([]Section, error) {
	var sections []Section
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		for i, line := range strings.Split(string(data), "\n") {
			s, ok := parseSpecHeading(line)
			if !ok {
				continue
			}
			s.File = rel
			s.Line = i + 1
			sections = append(sections, s)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(sections, func(i, j int) bool {
		return sectionLess(sections[i].ID, sections[j].ID)
	})
	return dedupeSections(sections), nil
}

// headingRegex matches lines like "## 4.6 Layers and Visibility",
// "### 4.6.3 Subsection", or "## 4. Artifact Model" with optional § prefix
// and optional trailing period after the number.
var headingRegex = regexp.MustCompile(`^#+\s+§?(\d+(?:\.\d+)*)\.?\s+(.+?)\s*$`)

func parseSpecHeading(line string) (Section, bool) {
	m := headingRegex.FindStringSubmatch(line)
	if m == nil {
		return Section{}, false
	}
	return Section{
		ID:    "§" + m[1],
		Title: strings.TrimSpace(m[2]),
	}, true
}

func dedupeSections(in []Section) []Section {
	seen := map[string]bool{}
	out := []Section{}
	for _, s := range in {
		if seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		out = append(out, s)
	}
	return out
}

// sectionLess compares two section IDs in numeric order: §4 < §4.5 < §4.10 <
// §5. Lexicographic order on the strings would put §4.10 before §4.2.
func sectionLess(a, b string) bool {
	ap := splitSection(a)
	bp := splitSection(b)
	for i := 0; i < len(ap) && i < len(bp); i++ {
		if ap[i] != bp[i] {
			return ap[i] < bp[i]
		}
	}
	return len(ap) < len(bp)
}

func splitSection(id string) []int {
	id = strings.TrimPrefix(id, "§")
	parts := strings.Split(id, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out
}

// WalkTests walks the repository rooted at root and returns every parsed
// test function with its citation. Vendored / generated trees are skipped.
func WalkTests(root string) ([]Test, error) {
	skip := map[string]bool{
		"vendor":       true,
		"node_modules": true,
		".git":         true,
		"testdata":     true,
		"tmp":          true,
	}
	var tests []Test
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skip[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		base := info.Name()
		switch {
		case strings.HasSuffix(base, "_test.go"):
			t, perr := parseGoFile(path)
			if perr != nil {
				return perr
			}
			tests = append(tests, t...)
		case strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"):
			tests = append(tests, parsePyFile(path)...)
		case strings.HasSuffix(base, "_test.py"):
			tests = append(tests, parsePyFile(path)...)
		case strings.HasSuffix(base, ".test.ts") || strings.HasSuffix(base, ".test.tsx"):
			tests = append(tests, parseTSFile(path)...)
		}
		return nil
	})
	return tests, err
}

func parseGoFile(path string) ([]Test, error) {
	fset := token.NewFileSet()
	f, err := goparser.ParseFile(fset, path, nil, goparser.ParseComments)
	if err != nil {
		return nil, err
	}
	var out []Test
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := fn.Name.Name
		if !strings.HasPrefix(name, "Test") || fn.Recv != nil {
			continue
		}
		pos := fset.Position(fn.Pos())
		t := Test{
			Name:     name,
			File:     path,
			Line:     pos.Line,
			Language: "go",
			Phase:    -1,
		}
		if fn.Doc != nil {
			t.Citation, t.Phase, t.Matrix = parseAnnotations(fn.Doc.Text())
		}
		out = append(out, t)
	}
	return out, nil
}

var (
	pyTestRegex = regexp.MustCompile(`^(?:async\s+)?def\s+(test_\w+)\s*\(`)
	tsTestRegex = regexp.MustCompile(`^\s*(?:test|it)\s*\(\s*['"]([^'"]+)['"]`)
)

// parsePyFile uses a line scanner: for every `def test_*` line, it walks
// backwards collecting contiguous comment lines (`#`) and parses them.
func parsePyFile(path string) []Test {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	var out []Test
	for i, line := range lines {
		m := pyTestRegex.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		comment := collectPrecedingComments(lines, i, "#")
		c, p, mat := parseAnnotations(comment)
		out = append(out, Test{
			Name:     m[1],
			File:     path,
			Line:     i + 1,
			Language: "python",
			Phase:    p,
			Citation: c,
			Matrix:   mat,
		})
	}
	return out
}

func parseTSFile(path string) []Test {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	var out []Test
	for i, line := range lines {
		m := tsTestRegex.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		comment := collectPrecedingComments(lines, i, "//")
		c, p, mat := parseAnnotations(comment)
		out = append(out, Test{
			Name:     m[1],
			File:     path,
			Line:     i + 1,
			Language: "typescript",
			Phase:    p,
			Citation: c,
			Matrix:   mat,
		})
	}
	return out
}

// collectPrecedingComments returns the contiguous block of comment lines
// immediately above index, with prefix stripped. prefix is "//" for TS or
// "#" for Python.
func collectPrecedingComments(lines []string, index int, prefix string) string {
	var b strings.Builder
	for j := index - 1; j >= 0; j-- {
		trim := strings.TrimSpace(lines[j])
		if !strings.HasPrefix(trim, prefix) {
			break
		}
		body := strings.TrimSpace(strings.TrimPrefix(trim, prefix))
		// Build top-down so subsequent line concatenation is in order.
		b.WriteString(body)
		b.WriteString("\n")
	}
	// reverse line order (we walked backwards)
	parts := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "\n")
}

// parseAnnotations extracts the spec citation, phase number, and any
// Matrix cell tags from a comment block.
//
// Recognized lines (case-insensitive on the keyword):
//
//	Spec:   §4.6 short title — assertion text.
//	Spec:   n/a — reason for not citing the spec.
//	Phase:  0
//	Matrix: §6.7.1 (claude-code, rule_mode_glob)
//
// A `Spec:` annotation can wrap across multiple comment lines; lines that
// follow without a new keyword are treated as continuations of the
// preceding Spec note. The first Spec and Phase annotations win; every
// Matrix annotation is collected.
func parseAnnotations(comment string) (Citation, int, []MatrixCell) {
	c := Citation{}
	phase := -1
	var matrix []MatrixCell
	lines := strings.Split(comment, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		switch {
		case startsWithCI(line, "Spec:"):
			c = parseSpecLine(line)
			// Collect continuation lines until the next keyword or
			// blank line.
			for j := i + 1; j < len(lines); j++ {
				next := strings.TrimSpace(lines[j])
				if next == "" || isAnnotationKeyword(next) {
					break
				}
				if c.Note == "" {
					c.Note = next
				} else {
					c.Note += " " + next
				}
				i = j
			}
		case startsWithCI(line, "Phase:"):
			n, err := strconv.Atoi(strings.TrimSpace(line[len("Phase:"):]))
			if err == nil && phase == -1 {
				phase = n
			}
		case startsWithCI(line, "Matrix:"):
			if cell, ok := parseMatrixLine(line); ok {
				matrix = append(matrix, cell)
			}
		}
	}
	return c, phase, matrix
}

func isAnnotationKeyword(line string) bool {
	for _, kw := range []string{"Spec:", "Phase:", "Matrix:"} {
		if startsWithCI(line, kw) {
			return true
		}
	}
	return false
}

// matrixLineRegex captures `Matrix: §X.Y (k1, k2, ...)`.
var matrixLineRegex = regexp.MustCompile(`^[Mm]atrix:\s*(§\d+(?:\.\d+)*)\s*\(([^)]*)\)\s*$`)

func parseMatrixLine(line string) (MatrixCell, bool) {
	m := matrixLineRegex.FindStringSubmatch(line)
	if m == nil {
		return MatrixCell{}, false
	}
	rawKeys := strings.Split(m[2], ",")
	keys := make([]string, 0, len(rawKeys))
	for _, k := range rawKeys {
		k = strings.TrimSpace(k)
		if k != "" {
			keys = append(keys, k)
		}
	}
	return MatrixCell{Matrix: m[1], Keys: keys}, true
}

func startsWithCI(line, prefix string) bool {
	return len(line) >= len(prefix) &&
		strings.EqualFold(line[:len(prefix)], prefix)
}

// specLineRegex captures "§X.Y[.Z...]" or "n/a" as the section identifier.
// Anything after the identifier (separator and prose, in any combination) is
// captured verbatim as the Note.
var specLineRegex = regexp.MustCompile(`^[Ss]pec:\s*(§\d+(?:\.\d+)*|n/a)(?:\s+(.+))?$`)

// noteSplitRegex separates an annotation note's optional short title from
// its assertion text using a Unicode em-dash, en-dash, or " - " hyphen.
var noteSplitRegex = regexp.MustCompile(`\s+[—–-]\s+`)

func parseSpecLine(line string) Citation {
	m := specLineRegex.FindStringSubmatch(line)
	if m == nil {
		return Citation{}
	}
	c := Citation{SectionID: m[1]}
	if len(m) >= 3 {
		c.Note = strings.TrimSpace(m[2])
	}
	return c
}

// SplitNote returns the (title, assertion) pair for an annotation note.
// When the note contains an em-dash / en-dash / hyphen separator surrounded
// by spaces, the parts before and after the first such separator are
// returned. Otherwise the whole note is returned as the assertion with an
// empty title. Used by reporters that want to distinguish the two halves.
func SplitNote(note string) (title, assertion string) {
	if note == "" {
		return "", ""
	}
	loc := noteSplitRegex.FindStringIndex(note)
	if loc == nil {
		return "", note
	}
	return strings.TrimSpace(note[:loc[0]]), strings.TrimSpace(note[loc[1]:])
}
