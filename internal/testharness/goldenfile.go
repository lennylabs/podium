package testharness

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const envUpdateGolden = "UPDATE_GOLDEN"

// AssertGoldenFile compares actual against the contents of the golden file at
// path. When the test is run with UPDATE_GOLDEN=1, the golden file is
// (re)written and the assertion passes. Otherwise, mismatches fail the test
// with a unified diff.
//
// Each error path returns explicitly after t.Fatalf so that test doubles
// which record but do not abort behave correctly.
func AssertGoldenFile(t testing.TB, path string, actual []byte) {
	t.Helper()
	if shouldUpdateGolden() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for golden file: %v", err)
			return
		}
		if err := os.WriteFile(path, actual, 0o644); err != nil {
			t.Fatalf("write golden file: %v", err)
			return
		}
		return
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden file %s: %v\nrun with UPDATE_GOLDEN=1 to create it", path, err)
		return
	}
	if bytes.Equal(expected, actual) {
		return
	}
	t.Fatalf("golden file mismatch: %s\n%s", path, unifiedDiff(string(expected), string(actual)))
}

// AssertGoldenString is the string-typed convenience form.
func AssertGoldenString(t testing.TB, path string, actual string) {
	t.Helper()
	AssertGoldenFile(t, path, []byte(actual))
}

func shouldUpdateGolden() bool {
	v := strings.TrimSpace(os.Getenv(envUpdateGolden))
	return v == "1" || strings.EqualFold(v, "true")
}

// unifiedDiff produces a small, dependency-free unified diff for golden-file
// mismatches. It is intentionally compact: line-by-line additions and
// deletions, no context lines or hunk headers. Sufficient for the kind of
// canonicalized output golden files contain.
func unifiedDiff(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	var b strings.Builder
	b.WriteString("--- want\n+++ got\n")
	max := len(wantLines)
	if len(gotLines) > max {
		max = len(gotLines)
	}
	for i := 0; i < max; i++ {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w == g {
			continue
		}
		if i < len(wantLines) {
			b.WriteString("-")
			b.WriteString(w)
			b.WriteString("\n")
		}
		if i < len(gotLines) {
			b.WriteString("+")
			b.WriteString(g)
			b.WriteString("\n")
		}
	}
	return b.String()
}
