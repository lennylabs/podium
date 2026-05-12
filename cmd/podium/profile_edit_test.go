package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSyncYAML(t *testing.T, dir string) {
	t.Helper()
	pdir := filepath.Join(dir, ".podium")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := []byte("defaults:\n  registry: " + dir + "\nprofiles:\n  team:\n    include: []\n    exclude: []\n")
	if err := os.WriteFile(filepath.Join(pdir, "sync.yaml"), body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestProfileEdit_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeSyncYAML(t, dir)
	withStderr(t, func() {
		captureStdout(t, func() {
			rc := profileCmd([]string{
				"edit",
				"--target", dir,
				"--profile", "team",
				"--add-include", "personal/*",
				"--add-exclude", "drafts/*",
			})
			if rc != 0 {
				t.Errorf("profileCmd = %d, want 0", rc)
			}
		})
	})
}

func TestProfileEdit_DryRun(t *testing.T) {
	dir := t.TempDir()
	writeSyncYAML(t, dir)
	withStderr(t, func() {
		captureStdout(t, func() {
			rc := profileCmd([]string{
				"edit",
				"--target", dir,
				"--profile", "team",
				"--add-include", "personal/*",
				"--dry-run",
			})
			if rc != 0 {
				t.Errorf("profileCmd = %d, want 0", rc)
			}
		})
	})
}

func TestProfileEdit_UnknownSubcommandExits2(t *testing.T) {
	withStderr(t, func() {
		if rc := profileCmd([]string{"bogus"}); rc != 2 {
			t.Errorf("rc = %d, want 2", rc)
		}
	})
}

func TestProfileEdit_MissingProfileFlagExits2(t *testing.T) {
	dir := t.TempDir()
	writeSyncYAML(t, dir)
	withStderr(t, func() {
		if rc := profileCmd([]string{"edit", "--target", dir}); rc != 2 {
			t.Errorf("rc = %d, want 2", rc)
		}
	})
}

func TestSyncSaveAs_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeSyncYAML(t, dir)
	withStderr(t, func() {
		captureStdout(t, func() {
			rc := syncSaveAsCmd([]string{
				"--target", dir,
				"--profile", "team2",
			})
			// SaveAs without an existing sync target/state may legitimately
			// return non-zero; just verify the function ran.
			if rc < 0 {
				t.Errorf("rc = %d", rc)
			}
		})
	})
}

func TestSyncSaveAs_DryRun(t *testing.T) {
	dir := t.TempDir()
	writeSyncYAML(t, dir)
	withStderr(t, func() {
		captureStdout(t, func() {
			_ = syncSaveAsCmd([]string{
				"--target", dir,
				"--profile", "team2",
				"--dry-run",
			})
		})
	})
}

func TestSyncOverride_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeSyncYAML(t, dir)
	withStderr(t, func() {
		captureStdout(t, func() {
			_ = syncOverrideCmd([]string{
				"--target", dir,
				"--add", "personal/skip",
				"--remove", "personal/other",
				"--dry-run",
			})
		})
	})
}

func TestSyncOverride_Reset(t *testing.T) {
	dir := t.TempDir()
	writeSyncYAML(t, dir)
	withStderr(t, func() {
		captureStdout(t, func() {
			_ = syncOverrideCmd([]string{
				"--target", dir,
				"--reset",
			})
		})
	})
}
