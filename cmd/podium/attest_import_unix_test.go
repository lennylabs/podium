//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

// Spec: §4.7.11 (Attestation directory), §7.3.5 — a symbolic link at
// .podium to a directory outside the layer root is refused by import and
// check, which exit 1 naming the path, and the import writes and removes
// nothing there.
func TestAttestImport_LinkedDotPodiumOutsideRoot(t *testing.T) {
	dir := importLayer(t)
	h := sh.New(t)
	outside := t.TempDir()
	victim := filepath.Join(outside, "attestations", "victim.sigstore.json")
	writeAttestLayer(t, outside, map[string]string{"attestations/victim.sigstore.json": "someone else's file"})
	link := filepath.Join(dir, "skills", ".podium")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	in := writeInput(t, ghBundleOf(t, h, importSLSA, layerSubjectsOf(t, dir, "skills")))
	code, _, stderr := runImportT(t, in, "--dir", dir, "--root", "skills")
	if code != 1 || !strings.Contains(stderr, link+" is a symbolic link or no directory") {
		t.Fatalf("import = %d, stderr %q", code, stderr)
	}
	if entries, _ := os.ReadDir(filepath.Dir(victim)); len(entries) != 1 {
		t.Errorf("the import changed the directory outside the root: %v", entries)
	}
	if code, _, stderr := runCheckT(t, nil, dir, "--root", "skills"); code != 1 || !strings.Contains(stderr, link) {
		t.Errorf("check = %d, stderr %q", code, stderr)
	}
}

// Spec: §4.7.11 (Attestation directory), §7.3.5 — a layer whose
// .podium/attestations links to another layer's attestation directory is
// refused, so importing its bundles removes none of the other layer's.
func TestAttestImport_LinkedAttestationsOfAnotherLayer(t *testing.T) {
	dir := t.TempDir()
	writeAttestLayer(t, dir, map[string]string{
		"skills/team/a/ARTIFACT.md": attestRuleMD,
		"other/team/z/ARTIFACT.md":  attestRuleMD,
	})
	h := sh.New(t)
	placeBundles(t, dir, "other", ghBundleOf(t, h, importSLSA, layerSubjectsOf(t, dir, "other")))
	if err := os.MkdirAll(filepath.Join(dir, "skills", ".podium"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "skills", ".podium", "attestations")
	if err := os.Symlink(filepath.Join("..", "..", "other", ".podium", "attestations"), link); err != nil {
		t.Fatal(err)
	}
	in := writeInput(t, ghBundleOf(t, h, importSLSA, layerSubjectsOf(t, dir, "skills")))
	if code, _, stderr := runImportT(t, in, "--dir", dir, "--root", "skills"); code != 1 || !strings.Contains(stderr, link) {
		t.Fatalf("import = %d, stderr %q", code, stderr)
	}
	if code, stdout, _ := runCheckT(t, nil, dir, "--root", "other"); code != 0 {
		t.Errorf("the other layer's bundles changed: check = %d, %s", code, stdout)
	}
}

// Spec: §7.3.5 — the import creates its files with mode 0644 under the
// process umask, as os.WriteFile does.
func TestAttestImport_FileModeFollowsUmask(t *testing.T) {
	dir := importLayer(t)
	h := sh.New(t)
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	in := writeInput(t, ghBundleOf(t, h, importSLSA, layerSubjectsOf(t, dir, "skills")))
	if code, _, stderr := runImportT(t, in, "--dir", dir, "--root", "skills"); code != 0 {
		t.Fatalf("import = %d, stderr %s", code, stderr)
	}
	attDir := filepath.Join(dir, "skills", ".podium", "attestations")
	for _, n := range bundleFiles(t, attDir) {
		info, err := os.Stat(filepath.Join(attDir, n))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v (%v), want 0600 under umask 077", n, info.Mode(), err)
		}
	}
}
