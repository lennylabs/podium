package sync_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/sync"
)

// Spec: §7.5 — a sync that drops an artifact deletes the files
// the previous sync wrote for it. The lock file diff drives the
// cleanup so artifacts removed from the registry don't linger in
// the target directory.
func TestRun_RemovesFilesForDroppedArtifact(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	registry := filepath.Join(dir, "registry")
	target := filepath.Join(dir, "out")

	// First sync: two artifacts.
	if err := os.MkdirAll(filepath.Join(registry, "alpha"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(registry, "beta"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := "---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: low\n---\n\nbody\n"
	for _, name := range []string{"alpha", "beta"} {
		if err := os.WriteFile(
			filepath.Join(registry, name, "ARTIFACT.md"),
			[]byte(body), 0o644,
		); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	if _, err := sync.Run(sync.Options{
		RegistryPath: registry, Target: target, AdapterID: "none",
	}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	for _, name := range []string{"alpha", "beta"} {
		if _, err := os.Stat(filepath.Join(target, name, "ARTIFACT.md")); err != nil {
			t.Fatalf("expected %s materialized: %v", name, err)
		}
	}

	// Drop beta from the registry; second sync should remove its
	// previously-materialized files.
	if err := os.RemoveAll(filepath.Join(registry, "beta")); err != nil {
		t.Fatalf("RemoveAll beta: %v", err)
	}
	if _, err := sync.Run(sync.Options{
		RegistryPath: registry, Target: target, AdapterID: "none",
	}); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "alpha", "ARTIFACT.md")); err != nil {
		t.Errorf("alpha should still be materialized: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "beta", "ARTIFACT.md")); !os.IsNotExist(err) {
		t.Errorf("beta should have been cleaned up; got err=%v", err)
	}
}

// Spec: §7.5 — the lock file persists every materialized path so
// the next sync can compute a precise diff.
func TestRun_WritesLockFileWithMaterializedPaths(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	registry := filepath.Join(dir, "registry")
	target := filepath.Join(dir, "out")
	if err := os.MkdirAll(filepath.Join(registry, "x"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(registry, "x", "ARTIFACT.md"),
		[]byte("---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: low\n---\n\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := sync.Run(sync.Options{
		RegistryPath: registry, Target: target, AdapterID: "none",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	lock, err := sync.ReadLock(target)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if lock == nil {
		t.Fatal("expected a lock file")
	}
	if len(lock.Artifacts) == 0 {
		t.Errorf("lock has no artifacts")
	}
	found := false
	for _, a := range lock.Artifacts {
		if a.MaterializedPath != "" {
			found = true
		}
	}
	if !found {
		t.Errorf("no MaterializedPath set in lock; %+v", lock.Artifacts)
	}
}
