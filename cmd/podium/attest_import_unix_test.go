//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
)

// Spec: §7.3.5 (Pruning) — an entry of the attestation directory that is
// not a regular file, here a symbolic link to /dev/zero, stays unread with
// a warning from the import and counts as no bundle in the check, and a
// bundle file the command cannot open fails both commands.
func TestAttestImport_NonRegularAndUnreadableEntries(t *testing.T) {
	dir := importLayer(t)
	h := sh.New(t)
	subs := layerSubjectsOf(t, dir, "skills")
	attDir := filepath.Join(dir, "skills", ".podium", "attestations")
	if err := os.MkdirAll(attDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(attDir, "zero.sigstore.json")
	if err := os.Symlink("/dev/zero", link); err != nil {
		t.Fatal(err)
	}
	in := writeInput(t, ghBundleOf(t, h, importSLSA, subs))
	code, _, stderr := runImportT(t, in, "--dir", dir, "--root", "skills")
	if code != 0 || !strings.Contains(stderr, "zero.sigstore.json is not a regular file; it stays") {
		t.Fatalf("import = %d, stderr %q", code, stderr)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("the link was removed: %v", err)
	}
	code, _, stderr = runCheckT(t, nil, dir, "--root", "skills")
	if code != 0 || !strings.Contains(stderr, "zero.sigstore.json is not a regular file, so it counts as no bundle") {
		t.Fatalf("check = %d, stderr %q", code, stderr)
	}

	locked := filepath.Join(attDir, "locked.sigstore.json")
	if err := os.WriteFile(locked, []byte("{}"), 0o000); err != nil {
		t.Fatal(err)
	}
	if f, err := os.Open(locked); err == nil {
		_ = f.Close()
		t.Skip("the test runs as a user that reads a file with no permission")
	}
	if code, _, stderr := runCheckT(t, nil, dir, "--root", "skills"); code != 1 || !strings.Contains(stderr, "locked.sigstore.json") {
		t.Errorf("check with an unreadable file = %d, stderr %q", code, stderr)
	}
	if code, _, stderr := runImportT(t, in, "--dir", dir, "--root", "skills"); code != 1 || !strings.Contains(stderr, "locked.sigstore.json") {
		t.Errorf("import with an unreadable file = %d, stderr %q", code, stderr)
	}
}
