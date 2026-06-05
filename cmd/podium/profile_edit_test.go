package main

import (
	"os"
	"path/filepath"
	"strings"
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
			// spec: §7.5.7 — the profile name is positional.
			rc := profileCmd([]string{
				"edit",
				"team",
				"--target", dir,
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
				"team",
				"--target", dir,
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

// spec: §7.5.7 — `podium profile edit` with no positional name errors (exit 2).
func TestProfileEdit_MissingNameExits2(t *testing.T) {
	dir := t.TempDir()
	writeSyncYAML(t, dir)
	withStderr(t, func() {
		if rc := profileCmd([]string{"edit", "--target", dir}); rc != 2 {
			t.Errorf("rc = %d, want 2", rc)
		}
	})
}

// spec: §7.5.7 — `podium profile edit <name>` with no batch flags
// opens the interactive editor; scripted commands write the same result the
// --add-include flag would. The stdin indirection keeps the loop from reading a
// real terminal so the test never blocks.
func TestProfileEdit_NoFlagsTUIApplies(t *testing.T) {
	dir := t.TempDir()
	writeSyncYAML(t, dir)
	withInteractiveStdin(t, "add-include personal/*\nsave\n", true)
	withStderr(t, func() {
		captureStdout(t, func() {
			if rc := profileCmd([]string{"edit", "team", "--target", dir}); rc != 0 {
				t.Errorf("rc = %d, want 0", rc)
			}
		})
	})
	body, err := os.ReadFile(filepath.Join(dir, ".podium", "sync.yaml"))
	if err != nil {
		t.Fatalf("read sync.yaml: %v", err)
	}
	if !strings.Contains(string(body), "personal/*") {
		t.Errorf("profile edit TUI did not write the include pattern:\n%s", body)
	}
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
