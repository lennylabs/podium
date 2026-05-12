package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeMiniModule scaffolds a tiny go module with one test that
// exercises one statement. runWithCover then produces a coverage.out
// that report/budget/perPackage can parse.
func writeMiniModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	mustWrite(filepath.Join(dir, "go.mod"), "module example.test/cov\n\ngo 1.21\n")
	mustWrite(filepath.Join(dir, "pkg/foo/a.go"),
		"package foo\n\nfunc A() int { return 1 }\nfunc B() int { return 2 }\n")
	mustWrite(filepath.Join(dir, "pkg/foo/a_test.go"),
		"package foo\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { _ = A() }\n")
	return dir
}

func TestReport_Passes(t *testing.T) {
	t.Parallel()
	dir := writeMiniModule(t)
	if rc := report(dir); rc != 0 {
		t.Errorf("report rc = %d, want 0", rc)
	}
}

func TestBudget_PassesAndFails(t *testing.T) {
	t.Parallel()
	dir := writeMiniModule(t)
	// 50% test coverage; budget 25 passes.
	if rc := budget(dir, 25); rc != 0 {
		t.Errorf("budget rc = %d, want 0 with low budget", rc)
	}
	// Budget 95 fails.
	if rc := budget(dir, 95); rc != 1 {
		t.Errorf("budget rc = %d, want 1 with high budget", rc)
	}
}

func TestPerPackage_Passes(t *testing.T) {
	t.Parallel()
	dir := writeMiniModule(t)
	if rc := perPackage(dir); rc != 0 {
		t.Errorf("perPackage rc = %d, want 0", rc)
	}
}

func TestRunWithCover_FailureSurfacesError(t *testing.T) {
	t.Parallel()
	// Empty dir (no go.mod) → go test exits non-zero.
	dir := t.TempDir()
	if err := runWithCover(dir); err == nil {
		t.Errorf("expected error from runWithCover in empty dir")
	}
}
