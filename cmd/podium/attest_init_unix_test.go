//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec: §7.3.5 (Writing the signing workflow) — a symbolic link or an entry
// other than a directory at .github or .github/workflows, a link at the
// workflow file even with --force, and a directory the command cannot
// write are refused with exit 1.
func TestAttestInit_UnwritablePaths(t *testing.T) {
	api := newFakeGitHub(t, map[string]string{"/repos/acme/x": ghRecord("acme/x", "main", "public")})

	linked := initRepo(t, "https://github.com/acme/x", "main")
	if err := os.Symlink(t.TempDir(), filepath.Join(linked, ".github")); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runInit(t, api.URL, nil, linked); code != 1 || !strings.Contains(stderr, "symbolic link or no directory") {
		t.Errorf(".github link: %d %q", code, stderr)
	}

	fileAt := initRepo(t, "https://github.com/acme/x", "main")
	if err := os.MkdirAll(filepath.Join(fileAt, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fileAt, ".github", "workflows"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runInit(t, api.URL, nil, fileAt); code != 1 || !strings.Contains(stderr, "workflows is a symbolic link or no directory") {
		t.Errorf("workflows file: %d %q", code, stderr)
	}

	fileLink := initRepo(t, "https://github.com/acme/x", "main")
	if err := os.MkdirAll(filepath.Join(fileLink, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/null", filepath.Join(fileLink, ".github", "workflows", "podium-attest.yml")); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runInit(t, api.URL, nil, fileLink, "--force"); code != 1 || !strings.Contains(stderr, "which --force does not replace") {
		t.Errorf("file link: %d %q", code, stderr)
	}

	if os.Geteuid() == 0 {
		t.Skip("root writes a read-only directory")
	}
	readOnly := initRepo(t, "https://github.com/acme/x", "main")
	if err := os.Chmod(readOnly, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o755) })
	if code, _, stderr := runInit(t, api.URL, nil, readOnly); code != 1 || !strings.Contains(stderr, "create ") {
		t.Errorf("read-only dir: %d %q", code, stderr)
	}
	ro2 := initRepo(t, "https://github.com/acme/x", "main")
	wf := filepath.Join(ro2, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(wf, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(wf, 0o755) })
	if code, _, stderr := runInit(t, api.URL, nil, ro2); code != 1 || !strings.Contains(stderr, "write ") {
		t.Errorf("read-only workflows dir: %d %q", code, stderr)
	}
}
