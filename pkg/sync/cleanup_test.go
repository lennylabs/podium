package sync_test

import (
	"os"
	"path/filepath"
	"strings"
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

// Spec: §6.7 / §7.5 — when the last artifact contributing to a shared
// config-merge file is dropped, the file is reconciled (Podium's entries
// stripped) rather than deleted, so the operator's own entries survive.
func TestRun_OrphanedConfigMergeReconciledNotDeleted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	registry := filepath.Join(dir, "registry")
	target := filepath.Join(dir, "out")

	// One hook artifact; claude-code config-merges it into .claude/settings.json.
	if err := os.MkdirAll(filepath.Join(registry, "audit", "stop"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	hook := "---\ntype: hook\nname: stop\nversion: 1.0.0\ndescription: Stop hook.\nhook_event: stop\nhook_action: |\n  echo done\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(registry, "audit", "stop", "ARTIFACT.md"), []byte(hook), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := sync.Run(sync.Options{RegistryPath: registry, Target: target, AdapterID: "claude-code"}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	settings := filepath.Join(target, ".claude", "settings.json")
	if _, err := os.Stat(settings); err != nil {
		t.Fatalf("settings.json not materialized: %v", err)
	}
	// The operator adds their own key to the shared file after the first sync.
	merged := readFileString(t, settings)
	withOperator := merged[:len(merged)-2] + `,"theme":"dark"}` + "\n"
	if err := os.WriteFile(settings, []byte(withOperator), 0o644); err != nil {
		t.Fatalf("seed operator key: %v", err)
	}

	// Drop the hook; the second sync must reconcile settings.json in place.
	if err := os.RemoveAll(filepath.Join(registry, "audit")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := sync.Run(sync.Options{RegistryPath: registry, Target: target, AdapterID: "claude-code"}); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if _, err := os.Stat(settings); err != nil {
		t.Fatalf("settings.json must not be deleted, got err=%v", err)
	}
	got := readFileString(t, settings)
	if !strings.Contains(got, `"theme"`) {
		t.Errorf("operator key lost from settings.json:\n%s", got)
	}
	if strings.Contains(got, "echo done") || strings.Contains(got, "x-podium-id") {
		t.Errorf("Podium hook entry not stripped from orphaned settings.json:\n%s", got)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
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
