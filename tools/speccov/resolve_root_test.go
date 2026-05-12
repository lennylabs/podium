package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveRepoRoot_Explicit(t *testing.T) {
	got, err := resolveRepoRoot("/some/explicit/path")
	if err != nil || got != "/some/explicit/path" {
		t.Errorf("got %q err %v", got, err)
	}
}

func TestResolveRepoRoot_WalksUp(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	sub := filepath.Join(dir, "a")
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
		t.Errorf("got %q, want suffix %q", got, filepath.Base(dir))
	}
}
