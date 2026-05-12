package main

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/tools/internal/specparser"
)

func TestMatrix_Cells_CartesianProduct(t *testing.T) {
	t.Parallel()
	m := Matrix{
		Axes: [][]string{
			{"a", "b"},
			{"1", "2", "3"},
		},
	}
	cells := m.Cells()
	if len(cells) != 6 {
		t.Fatalf("got %d cells, want 6", len(cells))
	}
	// Build a set of joined strings to make assertions order-agnostic.
	set := map[string]bool{}
	for _, c := range cells {
		set[strings.Join(c, "/")] = true
	}
	for _, want := range []string{"a/1", "a/2", "a/3", "b/1", "b/2", "b/3"} {
		if !set[want] {
			t.Errorf("missing cell %q in %v", want, set)
		}
	}
}

func TestMatrix_Cells_EmptyAxes(t *testing.T) {
	t.Parallel()
	if cells := (Matrix{}).Cells(); cells != nil {
		t.Errorf("empty matrix cells = %v, want nil", cells)
	}
}

func TestKnownMatrices_ReturnsCells(t *testing.T) {
	t.Parallel()
	got := KnownMatrices()
	if len(got) == 0 {
		t.Fatal("KnownMatrices returned 0 matrices")
	}
	for _, m := range got {
		if m.ID == "" {
			t.Errorf("matrix missing ID: %+v", m)
		}
		if len(m.Cells()) == 0 {
			t.Errorf("matrix %s has no cells", m.ID)
		}
	}
}

func TestSanitize(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":                "",
		"a":               "A",
		"foo-bar":         "FooBar",
		"foo_bar":         "FooBar",
		"alpha:beta":      "AlphaBeta",
		"alreadyCamel":    "AlreadyCamel",
		"with-1-numbers":  "With1Numbers",
		"!!keep-letters!": "KeepLetters",
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStubName_BuildsTestPrefix(t *testing.T) {
	t.Parallel()
	m := Matrix{ID: "§4.6", StubPrefix: "Visibility_Union"}
	got := stubName(m, []string{"public"})
	if got != "Test_Visibility_Union_Public" {
		t.Errorf("stubName = %q", got)
	}
	got = stubName(m, []string{"public", "organization"})
	if got != "Test_Visibility_Union_Public_Organization" {
		t.Errorf("stubName multi = %q", got)
	}
}

func TestCellKey_AndFormatCell(t *testing.T) {
	t.Parallel()
	if got := cellKey("§6.10", []string{"a", "b"}); got != "§6.10(a,b)" {
		t.Errorf("cellKey = %q", got)
	}
	if got := formatCell("§6.10", []string{"a", "b"}); got != "§6.10(a, b)" {
		t.Errorf("formatCell = %q", got)
	}
}

func TestIndexCoveredCells(t *testing.T) {
	t.Parallel()
	tests := []specparser.Test{
		{
			Matrix: []specparser.MatrixCell{
				{Matrix: "§6.10", Keys: []string{"config.invalid"}},
				{Matrix: "§4.6", Keys: []string{"public", "organization"}},
			},
		},
	}
	got := indexCoveredCells(tests)
	if !got["§6.10(config.invalid)"] {
		t.Errorf("missing §6.10 entry: %v", got)
	}
	if !got["§4.6(public,organization)"] {
		t.Errorf("missing §4.6 entry: %v", got)
	}
}

func TestAudit_AllCovered_ReturnsZero(t *testing.T) {
	t.Parallel()
	matrices := []Matrix{{
		ID: "§X", StubPrefix: "X",
		Axes: [][]string{{"only"}},
	}}
	covered := map[string]bool{"§X(only)": true}
	if rc := audit(matrices, covered); rc != 0 {
		t.Errorf("audit rc = %d, want 0", rc)
	}
}

func TestAudit_AnyMissing_ReturnsOne(t *testing.T) {
	t.Parallel()
	matrices := []Matrix{{
		ID: "§X", StubPrefix: "X",
		Axes: [][]string{{"only"}},
	}}
	covered := map[string]bool{}
	if rc := audit(matrices, covered); rc != 1 {
		t.Errorf("audit rc = %d, want 1", rc)
	}
}

func TestParentDir(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/a/b/c": "/a/b",
		"/a":     "/",
		"/":      "/",
	}
	for in, want := range cases {
		if got := parentDir(in); got != want {
			t.Errorf("parentDir(%q) = %q, want %q", in, got, want)
		}
	}
}
