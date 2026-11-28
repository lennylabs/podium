//go:build windows

package sync

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// Spec: §4.7.11 (Machine file, Check on Windows) — a podium folder that a
// non-administrator created, whose DACL inherits from its parent, and a
// file whose DACL grants the test user write access each fail the check;
// an absent file raises nothing.
func TestReadMachineConfig_WindowsRefusals(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "podium")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if mf, err := ReadMachineConfig(MachineFileOptions{Dir: dir}); mf != nil || err != nil {
		t.Fatalf("absent file: %v, %v", mf, err)
	}
	if err := os.WriteFile(filepath.Join(dir, MachineConfigName), []byte("defaults: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadMachineConfig(MachineFileOptions{Dir: dir})
	if !errors.Is(err, ErrMachineFileUnsafe) {
		t.Fatalf("err = %v, want ErrMachineFileUnsafe", err)
	}
	if !strings.Contains(err.Error(), "owned by") && !strings.Contains(err.Error(), "inherits") && !strings.Contains(err.Error(), "grants") {
		t.Errorf("err = %v names no failed condition", err)
	}
}

// Spec: §4.7.11 (Machine file) — the directory is podium under the
// ProgramData known folder.
func TestMachineConfigDir_ProgramData(t *testing.T) {
	t.Parallel()
	pd, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		t.Fatal(err)
	}
	if d, err := MachineConfigDir(); err != nil || d != filepath.Join(pd, "podium") {
		t.Errorf("MachineConfigDir = %q, %v", d, err)
	}
}

// Spec: §4.7.11 (Check on Windows) — generic rights map to the file rights
// they stand for before the check.
func TestMapGeneric(t *testing.T) {
	t.Parallel()
	if mapGeneric(windows.GENERIC_WRITE)&windows.FILE_WRITE_DATA == 0 || mapGeneric(windows.GENERIC_ALL)&windows.WRITE_DAC == 0 {
		t.Error("a generic right does not map to its write rights")
	}
	if mapGeneric(windows.GENERIC_READ)&fileWriteRights != 0 {
		t.Error("GENERIC_READ maps to a write right")
	}
}
