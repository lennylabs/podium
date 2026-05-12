package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lennylabs/podium/tools/internal/specparser"
)

func sampleSections() []specparser.Section {
	return []specparser.Section{
		{ID: "§1", Title: "First"},
		{ID: "§2", Title: "Second"},
		{ID: "§3", Title: "Third"},
	}
}

func sampleTests() []specparser.Test {
	return []specparser.Test{
		{Name: "TestA", File: "a_test.go", Citation: specparser.Citation{SectionID: "§1"}},
		{Name: "TestB", File: "b_test.go", Citation: specparser.Citation{SectionID: "§2", Aliases: []string{"§3"}}},
	}
}

func TestGroupBySection(t *testing.T) {
	t.Parallel()
	got := groupBySection(sampleTests())
	if len(got["§1"]) != 1 || got["§1"][0].Name != "TestA" {
		t.Errorf("§1 = %+v", got["§1"])
	}
	if len(got["§2"]) != 1 {
		t.Errorf("§2 = %+v", got["§2"])
	}
	if len(got["§3"]) != 1 {
		t.Errorf("alias §3 = %+v", got["§3"])
	}
}

func TestGroupBySection_SkipsNoneCitations(t *testing.T) {
	t.Parallel()
	tests := []specparser.Test{
		{Name: "X", Citation: specparser.Citation{SectionID: "n/a"}},
		{Name: "Y", Citation: specparser.Citation{}},
	}
	got := groupBySection(tests)
	if len(got) != 0 {
		t.Errorf("got %d entries, want 0", len(got))
	}
}

func TestCoverageStats(t *testing.T) {
	t.Parallel()
	sections := sampleSections()
	bySection := groupBySection(sampleTests())
	covered, total := coverageStats(sections, bySection)
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if covered != 3 {
		t.Errorf("covered = %d, want 3 (alias §3 counts)", covered)
	}
}

func TestReport_WritesTable(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	rc := report(&buf, sampleSections(), sampleTests())
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	out := buf.String()
	for _, want := range []string{"section", "§1", "§2", "§3", "First", "Second"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q\n--- got ---\n%s", want, out)
		}
	}
}

func TestUncovered_ReportsMissingSections(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	tests := []specparser.Test{
		{Citation: specparser.Citation{SectionID: "§1"}},
	}
	rc := uncovered(&buf, sampleSections(), tests)
	if rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	out := buf.String()
	if !strings.Contains(out, "§2") || !strings.Contains(out, "§3") {
		t.Errorf("missing §2/§3 in output:\n%s", out)
	}
}

func TestUncovered_AllCoveredReturnsZero(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	rc := uncovered(&buf, sampleSections(), sampleTests())
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(buf.String(), "all spec sections") {
		t.Errorf("got:\n%s", buf.String())
	}
}

func TestDrift_DetectsRogueCitations(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	tests := []specparser.Test{
		{Name: "ok", Citation: specparser.Citation{SectionID: "§1"}},
		{Name: "rogue", File: "rogue.go", Citation: specparser.Citation{SectionID: "§deleted"}},
	}
	rc := drift(&buf, sampleSections(), tests)
	if rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	if !strings.Contains(buf.String(), "§deleted") || !strings.Contains(buf.String(), "rogue") {
		t.Errorf("drift output missing rogue entry:\n%s", buf.String())
	}
}

func TestDrift_NoRogueReturnsZero(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	rc := drift(&buf, sampleSections(), sampleTests())
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
}

func TestListTests_SortsByName(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	tests := []specparser.Test{
		{Name: "Z", Citation: specparser.Citation{SectionID: "§1"}},
		{Name: "A"}, // no citation
	}
	rc := listTests(&buf, tests)
	if rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	out := buf.String()
	aIdx := strings.Index(out, "A")
	zIdx := strings.Index(out, "Z")
	if aIdx <= 0 || zIdx <= 0 || aIdx >= zIdx {
		t.Errorf("expected A before Z in output:\n%s", out)
	}
	if !strings.Contains(out, "(none)") {
		t.Errorf("expected (none) marker for missing citation:\n%s", out)
	}
}

func TestJoinPath_HandlesTrailingSeparator(t *testing.T) {
	t.Parallel()
	if got := joinPath("/a", "b", "c"); !strings.HasSuffix(got, "c") || !strings.Contains(got, "a") {
		t.Errorf("joinPath = %q", got)
	}
}

func TestParentDir(t *testing.T) {
	t.Parallel()
	if got := parentDir("/a/b/c"); !strings.HasSuffix(got, "b") {
		t.Errorf("parentDir = %q", got)
	}
}
