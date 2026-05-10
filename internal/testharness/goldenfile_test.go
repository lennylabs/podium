package testharness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec: n/a — internal harness primitive (TEST_INFRASTRUCTURE_PLAN.md §6.6).
func TestAssertGoldenFile_PassesOnMatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "expected.txt")
	if err := os.WriteFile(path, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	rec := &fatalRecorder{TB: t}
	AssertGoldenFile(rec, path, []byte("hello\nworld\n"))
	if rec.failed {
		t.Fatalf("expected no failure, got: %s", rec.fatalMsg)
	}
}

// Spec: n/a — internal harness primitive (TEST_INFRASTRUCTURE_PLAN.md §6.6).
func TestAssertGoldenFile_FailsWithDiff(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "expected.txt")
	if err := os.WriteFile(path, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	rec := &fatalRecorder{TB: t}
	AssertGoldenFile(rec, path, []byte("hello\nplanet\n"))
	if !rec.failed {
		t.Fatalf("expected failure on mismatch, got pass")
	}
	if !strings.Contains(rec.fatalMsg, "-world") || !strings.Contains(rec.fatalMsg, "+planet") {
		t.Fatalf("expected diff with -world / +planet, got:\n%s", rec.fatalMsg)
	}
}

// Spec: n/a — internal harness primitive (TEST_INFRASTRUCTURE_PLAN.md §6.6).
func TestAssertGoldenFile_UpdatesWhenFlagSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "expected.txt")

	t.Setenv(envUpdateGolden, "1")
	rec := &fatalRecorder{TB: t}
	AssertGoldenFile(rec, path, []byte("freshly written\n"))
	if rec.failed {
		t.Fatalf("expected no failure when updating, got: %s", rec.fatalMsg)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read updated golden: %v", err)
	}
	if string(got) != "freshly written\n" {
		t.Fatalf("golden contents = %q, want %q", got, "freshly written\n")
	}
}

// Spec: n/a — internal harness primitive (TEST_INFRASTRUCTURE_PLAN.md §6.6).
func TestAssertGoldenFile_MissingFileFailsWithHint(t *testing.T) {
	t.Parallel()
	rec := &fatalRecorder{TB: t}
	AssertGoldenFile(rec, filepath.Join(t.TempDir(), "missing.txt"), []byte("x"))
	if !rec.failed {
		t.Fatalf("expected failure when golden file is missing")
	}
	if !strings.Contains(rec.fatalMsg, "UPDATE_GOLDEN=1") {
		t.Fatalf("expected hint about UPDATE_GOLDEN, got:\n%s", rec.fatalMsg)
	}
}

// fatalRecorder captures Fatalf calls so the goldenfile assertions can be
// tested without actually failing the surrounding test.
type fatalRecorder struct {
	testing.TB
	failed   bool
	fatalMsg string
}

func (r *fatalRecorder) Fatalf(format string, args ...any) {
	r.failed = true
	r.fatalMsg = format
	for _, a := range args {
		if s, ok := a.(string); ok {
			r.fatalMsg += "|" + s
		}
	}
}

func (r *fatalRecorder) Helper() {}
