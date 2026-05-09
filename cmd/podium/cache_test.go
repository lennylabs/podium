package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Spec: §6.5 — `podium cache prune --days N` removes content
// buckets whose newest file mtime is older than N days. Younger
// buckets stay.
func TestCachePrune_RemovesOldBuckets(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "sha256-old")
	young := filepath.Join(dir, "sha256-young")
	for _, b := range []string{old, young} {
		if err := os.MkdirAll(b, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(b, "frontmatter"), []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	// Backdate "old" by 60 days.
	past := time.Now().Add(-60 * 24 * time.Hour)
	_ = os.Chtimes(filepath.Join(old, "frontmatter"), past, past)
	_ = os.Chtimes(old, past, past)

	rc := cachePrune([]string{
		"--dir", dir,
		"--days", "30",
	})
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old bucket survived: %v", err)
	}
	if _, err := os.Stat(young); err != nil {
		t.Errorf("young bucket removed: %v", err)
	}
}

// Spec: §6.5 — --dry-run reports without removing.
func TestCachePrune_DryRun(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "sha256-old")
	_ = os.MkdirAll(old, 0o755)
	_ = os.WriteFile(filepath.Join(old, "x"), []byte("data"), 0o644)
	past := time.Now().Add(-60 * 24 * time.Hour)
	_ = os.Chtimes(filepath.Join(old, "x"), past, past)
	_ = os.Chtimes(old, past, past)

	rc := cachePrune([]string{"--dir", dir, "--days", "30", "--dry-run"})
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if _, err := os.Stat(old); err != nil {
		t.Errorf("dry-run removed bucket: %v", err)
	}
}

// Spec: §6.5 — pruning a missing cache dir is a no-op success.
func TestCachePrune_MissingDirIsNoop(t *testing.T) {
	rc := cachePrune([]string{"--dir", filepath.Join(t.TempDir(), "absent"), "--days", "30"})
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
}
