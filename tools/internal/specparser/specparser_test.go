package specparser

import (
	"os"
	"path/filepath"
	"testing"
)

// Spec: n/a — internal tooling for the speccov reporter.
// Phase: 0
func TestParseSpecHeading_RecognizesSectionIDs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		line    string
		wantID  string
		wantOK  bool
	}{
		{"## 4. Artifact Model", "§4", true},
		{"## 4.6 Layers and Visibility", "§4.6", true},
		{"### 4.6.3 Sources", "§4.6.3", true},
		{"## §6.10 Error Model", "§6.10", true},
		{"# Top header", "", false},
		{"random text", "", false},
		{"## not a section number", "", false},
	}
	for _, c := range cases {
		got, ok := parseSpecHeading(c.line)
		if ok != c.wantOK {
			t.Errorf("parseSpecHeading(%q) ok = %v, want %v", c.line, ok, c.wantOK)
			continue
		}
		if c.wantOK && got.ID != c.wantID {
			t.Errorf("parseSpecHeading(%q) ID = %q, want %q", c.line, got.ID, c.wantID)
		}
	}
}

// Spec: n/a — internal tooling for the speccov reporter.
// Phase: 0
func TestSectionLess_NumericOrder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want bool
	}{
		{"§4", "§4.6", true},
		{"§4.10", "§4.6", false},
		{"§4.6", "§4.10", true},
		{"§4.6", "§5", true},
		{"§4.6.3", "§4.6", false},
		{"§4.6", "§4.6.3", true},
	}
	for _, c := range cases {
		got := sectionLess(c.a, c.b)
		if got != c.want {
			t.Errorf("sectionLess(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// Spec: n/a — internal tooling for the speccov reporter.
// Phase: 0
func TestParseAnnotations_ExtractsSpecAndPhase(t *testing.T) {
	t.Parallel()
	in := `Spec: §4.6 Layer ordering — admin layers come first.
Phase: 7`
	c, p, _ := parseAnnotations(in)
	if c.SectionID != "§4.6" {
		t.Errorf("SectionID = %q, want §4.6", c.SectionID)
	}
	if c.Note != "Layer ordering — admin layers come first." {
		t.Errorf("Note = %q, want full annotation text", c.Note)
	}
	title, assertion := SplitNote(c.Note)
	if title != "Layer ordering" {
		t.Errorf("SplitNote title = %q, want %q", title, "Layer ordering")
	}
	if assertion != "admin layers come first." {
		t.Errorf("SplitNote assertion = %q, want %q", assertion, "admin layers come first.")
	}
	if p != 7 {
		t.Errorf("Phase = %d, want 7", p)
	}
}

// Spec: n/a — Multi-cite Spec: lines (e.g. "Spec: §8.1 / §4.7.5
// — adapter ...") count toward each cited section. The primary
// section is SectionID; the rest land in Aliases.
func TestParseAnnotations_MultiCiteAliases(t *testing.T) {
	t.Parallel()
	in := `Spec: §8.1 / §4.7.5 — adapter propagates audit events.
Phase: 7`
	c, _, _ := parseAnnotations(in)
	if c.SectionID != "§8.1" {
		t.Errorf("SectionID = %q, want §8.1", c.SectionID)
	}
	if len(c.Aliases) != 1 || c.Aliases[0] != "§4.7.5" {
		t.Errorf("Aliases = %v, want [§4.7.5]", c.Aliases)
	}
	if c.Note != "— adapter propagates audit events." {
		t.Errorf("Note = %q (want it to start at the em-dash)", c.Note)
	}
}

func TestParseAnnotations_MultiCiteThreeSections(t *testing.T) {
	t.Parallel()
	in := `Spec: §1 / §2 / §3 — combined.
Phase: 0`
	c, _, _ := parseAnnotations(in)
	if c.SectionID != "§1" {
		t.Errorf("SectionID = %q", c.SectionID)
	}
	if len(c.Aliases) != 2 || c.Aliases[0] != "§2" || c.Aliases[1] != "§3" {
		t.Errorf("Aliases = %v, want [§2 §3]", c.Aliases)
	}
}

// Spec: n/a — Matrix annotations let tests claim coverage of specific
// cells in spec tables (capability matrix, error codes, failure modes).
// Phase: 0
func TestParseAnnotations_MatrixCells(t *testing.T) {
	t.Parallel()
	in := `Spec: §6.7.1 capability matrix — claude-code supports rule_mode: glob.
Phase: 13
Matrix: §6.7.1 (claude-code, rule_mode_glob)
Matrix: §6.7.1 (claude-code, rule_mode_always)`
	_, _, cells := parseAnnotations(in)
	if len(cells) != 2 {
		t.Fatalf("got %d cells, want 2: %+v", len(cells), cells)
	}
	if cells[0].Matrix != "§6.7.1" {
		t.Errorf("Matrix = %q", cells[0].Matrix)
	}
	if len(cells[0].Keys) != 2 ||
		cells[0].Keys[0] != "claude-code" ||
		cells[0].Keys[1] != "rule_mode_glob" {
		t.Errorf("Keys = %v", cells[0].Keys)
	}
}

// Spec: n/a — internal tooling for the speccov reporter.
// Phase: 0
func TestSplitNote_HandlesSeparatorVariants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in              string
		wantTitle, wantA string
	}{
		{"Layer ordering — admin layers come first.", "Layer ordering", "admin layers come first."},
		{"Layer ordering – assertion with en-dash.", "Layer ordering", "assertion with en-dash."},
		{"Layer ordering - assertion with hyphen.", "Layer ordering", "assertion with hyphen."},
		{"no separator at all", "", "no separator at all"},
		{"", "", ""},
	}
	for _, c := range cases {
		gt, ga := SplitNote(c.in)
		if gt != c.wantTitle || ga != c.wantA {
			t.Errorf("SplitNote(%q) = (%q, %q), want (%q, %q)",
				c.in, gt, ga, c.wantTitle, c.wantA)
		}
	}
}

// Spec: n/a — internal tooling for the speccov reporter.
// Phase: 0
func TestParseAnnotations_AcceptsNotApplicable(t *testing.T) {
	t.Parallel()
	in := "Spec: n/a — internal helper."
	c, _, _ := parseAnnotations(in)
	if c.SectionID != "n/a" {
		t.Errorf("SectionID = %q, want n/a", c.SectionID)
	}
}

// Spec: n/a — internal tooling for the speccov reporter.
// Phase: 0
func TestParseAnnotations_DefaultsPhaseToMinusOne(t *testing.T) {
	t.Parallel()
	c, p, _ := parseAnnotations("Spec: §1.1 something")
	if c.SectionID != "§1.1" {
		t.Errorf("SectionID = %q, want §1.1", c.SectionID)
	}
	if p != -1 {
		t.Errorf("Phase = %d, want -1 (untagged)", p)
	}
}

// Spec: n/a — internal tooling for the speccov reporter.
// Phase: 0
func TestParseGoFile_FindsAnnotatedTests(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := `package x

import "testing"

// Spec: §4.6 Layer ordering — admin layers come first.
// Phase: 7
func TestSomething(t *testing.T) {}

// no annotation here.
func TestNoCitation(t *testing.T) {}
`
	path := filepath.Join(dir, "x_test.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tests, err := parseGoFile(path)
	if err != nil {
		t.Fatalf("parseGoFile: %v", err)
	}
	if len(tests) != 2 {
		t.Fatalf("got %d tests, want 2", len(tests))
	}
	if tests[0].Name != "TestSomething" || tests[0].Citation.SectionID != "§4.6" || tests[0].Phase != 7 {
		t.Errorf("first test = %+v, want TestSomething §4.6 phase=7", tests[0])
	}
	if tests[1].Name != "TestNoCitation" || tests[1].Citation.SectionID != "" {
		t.Errorf("second test = %+v, want TestNoCitation with empty citation", tests[1])
	}
}

// Spec: n/a — internal tooling for the speccov reporter.
// Phase: 0
func TestParsePyFile_FindsCitations(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := `# Spec: §7.6 SDK surface — the Python client exposes load_artifact.
# Phase: 4
def test_load_artifact():
    pass
`
	path := filepath.Join(dir, "test_x.py")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tests := parsePyFile(path)
	if len(tests) != 1 {
		t.Fatalf("got %d tests, want 1", len(tests))
	}
	if tests[0].Citation.SectionID != "§7.6" || tests[0].Phase != 4 {
		t.Errorf("got %+v, want §7.6 phase=4", tests[0].Citation)
	}
}

// Spec: n/a — internal tooling for the speccov reporter.
// Phase: 0
func TestParseTSFile_FindsCitations(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := `// Spec: §7.6 SDK surface — the TS client exposes load_artifact.
// Phase: 14
test('load artifact', () => {})
`
	path := filepath.Join(dir, "x.test.ts")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tests := parseTSFile(path)
	if len(tests) != 1 {
		t.Fatalf("got %d tests, want 1", len(tests))
	}
	if tests[0].Citation.SectionID != "§7.6" || tests[0].Phase != 14 {
		t.Errorf("got %+v, want §7.6 phase=14", tests[0].Citation)
	}
}

// Spec: n/a — internal tooling for the speccov reporter.
// Phase: 0
func TestLoadSpecSections_ReadsMarkdown(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := `# 1. Overview

## 1.1 What
some prose.

## 1.2 Why
more prose.

### 1.2.3 Detail
prose.
`
	if err := os.WriteFile(filepath.Join(dir, "01-overview.md"), []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := LoadSpecSections(dir)
	if err != nil {
		t.Fatalf("LoadSpecSections: %v", err)
	}
	wantIDs := []string{"§1", "§1.1", "§1.2", "§1.2.3"}
	if len(got) != len(wantIDs) {
		t.Fatalf("got %d sections, want %d", len(got), len(wantIDs))
	}
	for i, id := range wantIDs {
		if got[i].ID != id {
			t.Errorf("section %d: got %q, want %q", i, got[i].ID, id)
		}
	}
}
