package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSample sets up a self-contained module with a tiny package whose
// coverage.out matches its real source lines. This lets `go tool cover
// -func` resolve every block and report a clean total: line.
func writeSample(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	mustWrite(filepath.Join(dir, "go.mod"), "module example.test/cov\n\ngo 1.21\n")
	mustWrite(filepath.Join(dir, "pkg/foo/a.go"),
		"package foo\n\nfunc A1() int { return 1 }\nfunc A2() int { return 2 }\n")
	mustWrite(filepath.Join(dir, "pkg/bar/b.go"),
		"package bar\n\nfunc B1() int { return 1 }\nfunc B2() int { return 2 }\n")
	mustWrite(filepath.Join(dir, "coverage.out"),
		"mode: set\n"+
			"example.test/cov/pkg/foo/a.go:3.16,3.27 1 1\n"+
			"example.test/cov/pkg/foo/a.go:4.16,4.27 1 0\n"+
			"example.test/cov/pkg/bar/b.go:3.16,3.27 1 1\n"+
			"example.test/cov/pkg/bar/b.go:4.16,4.27 1 1\n")
	return dir
}

func TestPackageCoverage_AggregatesByImportPath(t *testing.T) {
	t.Parallel()
	dir := writeSample(t)
	got, err := packageCoverage(dir)
	if err != nil {
		t.Fatalf("packageCoverage: %v", err)
	}
	const fooPkg = "example.test/cov/pkg/foo"
	const barPkg = "example.test/cov/pkg/bar"
	if v := got[fooPkg]; v < 49 || v > 51 {
		t.Errorf("foo coverage = %.1f, want ~50", v)
	}
	if v := got[barPkg]; v < 99 || v > 101 {
		t.Errorf("bar coverage = %.1f, want 100", v)
	}
}

func TestPackageCoverage_MissingFileErrors(t *testing.T) {
	t.Parallel()
	if _, err := packageCoverage(t.TempDir()); err == nil {
		t.Errorf("expected error for missing coverage.out")
	}
}

func TestOverallCoverage_ParsesTotalLine(t *testing.T) {
	t.Parallel()
	dir := writeSample(t)
	pct, err := overallCoverage(dir)
	if err != nil {
		t.Fatalf("overallCoverage: %v", err)
	}
	// 3 hit / 4 total = 75%
	if pct < 70 || pct > 80 {
		t.Errorf("pct = %.1f, want ~75", pct)
	}
}

func TestResolveRepoRoot_WalksUp(t *testing.T) {
	t.Parallel()
	if got, err := resolveRepoRoot("/explicit/path"); err != nil || got != "/explicit/path" {
		t.Errorf("explicit: got %q, err %v", got, err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	sub := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	orig, _ := os.Getwd()
	if err := os.Chdir(sub); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(orig)
	got, err := resolveRepoRoot("")
	if err != nil {
		t.Fatalf("resolveRepoRoot: %v", err)
	}
	// macOS path returns from t.TempDir() can be /private/var/... so
	// we just check the suffix.
	if filepath.Base(got) != filepath.Base(dir) {
		// Resolve symlinks on macOS so /var vs /private/var lines up.
		if r1, _ := filepath.EvalSymlinks(got); r1 != "" {
			if r2, _ := filepath.EvalSymlinks(dir); r2 != "" && r1 == r2 {
				return
			}
		}
		t.Errorf("got %q, want %q (or symlink-equivalent)", got, dir)
	}
}
