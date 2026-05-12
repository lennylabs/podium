package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureToolStdout swaps os.Stdout, runs fn, then returns whatever
// fn wrote. Used to assert listMatrices/scaffold prints expected
// content.
func captureToolStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan []byte, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.Bytes()
	}()
	defer func() { os.Stdout = orig; _ = r.Close() }()
	fn()
	_ = w.Close()
	return string(<-done)
}

func TestListMatrices_PrintsTable(t *testing.T) {
	matrices := []Matrix{
		{ID: "§A", Title: "First", Axes: [][]string{{"a", "b"}}},
		{ID: "§B", Title: "Second", Axes: [][]string{{"x"}}},
	}
	got := captureToolStdout(t, func() {
		listMatrices(matrices)
	})
	for _, want := range []string{"§A", "§B", "First", "Second", "2 cells", "1 cells"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q: %s", want, got)
		}
	}
}

func TestScaffold_PrintsStubsForUncoveredCells(t *testing.T) {
	matrices := []Matrix{{
		ID: "§A", StubPrefix: "Foo",
		Axes: [][]string{{"a", "b"}},
	}}
	covered := map[string]bool{"§A(a)": true}
	got := captureToolStdout(t, func() {
		scaffold(matrices, covered)
	})
	if !strings.Contains(got, "Test_Foo_B") {
		t.Errorf("scaffold missing stub for uncovered cell: %s", got)
	}
	if strings.Contains(got, "Test_Foo_A") {
		t.Errorf("scaffold emitted stub for covered cell: %s", got)
	}
}

func TestResolveRepoRoot_ExplicitWins(t *testing.T) {
	got, err := resolveRepoRoot("/explicit")
	if err != nil || got != "/explicit" {
		t.Errorf("got %q err %v", got, err)
	}
}

func TestResolveRepoRoot_WalksUpToGoMod(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	sub := filepath.Join(dir, "a", "b")
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
	if !strings.HasSuffix(got, filepath.Base(dir)) {
		t.Errorf("got %q, want path ending in %q", got, filepath.Base(dir))
	}
}
