package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withCwd switches the test's working directory to dir for the
// duration of fn so podium init's relative-path logic operates on
// the temp dir.
func withCwd(t *testing.T, dir string, fn func()) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	fn()
}

// Spec: §7.7 — `podium init --registry URL` writes
// <ws>/.podium/sync.yaml with the registry default.
func TestInit_WorkspaceWritesSyncYAML(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir, func() {
		rc := initCmd([]string{"--registry", "https://podium.example/"})
		if rc != 0 {
			t.Fatalf("rc = %d, want 0", rc)
		}
	})
	body, err := os.ReadFile(filepath.Join(dir, ".podium", "sync.yaml"))
	if err != nil {
		t.Fatalf("read sync.yaml: %v", err)
	}
	if !strings.Contains(string(body), "https://podium.example/") {
		t.Errorf("body = %q, missing registry URL", body)
	}
}

// Spec: §7.7 — `--standalone` is a shortcut for
// `--registry http://127.0.0.1:8080`.
func TestInit_StandaloneShortcut(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir, func() {
		rc := initCmd([]string{"--standalone"})
		if rc != 0 {
			t.Fatalf("rc = %d, want 0", rc)
		}
	})
	body, _ := os.ReadFile(filepath.Join(dir, ".podium", "sync.yaml"))
	if !strings.Contains(string(body), "http://127.0.0.1:8080") {
		t.Errorf("body = %q, missing standalone URL", body)
	}
}

// Spec: §7.7 — `--local` targets <ws>/.podium/sync.local.yaml so
// the override is gitignored.
func TestInit_LocalTargetsLocalYAML(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir, func() {
		rc := initCmd([]string{"--local", "--registry", "https://staging.example/"})
		if rc != 0 {
			t.Fatalf("rc = %d, want 0", rc)
		}
	})
	if _, err := os.Stat(filepath.Join(dir, ".podium", "sync.local.yaml")); err != nil {
		t.Errorf("sync.local.yaml missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".podium", "sync.yaml")); err == nil {
		t.Errorf("--local should not write sync.yaml")
	}
}

// Spec: §7.7 — init refuses to overwrite an existing file without
// --force; with --force the file is replaced.
func TestInit_RefusesToOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	pathYAML := filepath.Join(dir, ".podium", "sync.yaml")
	withCwd(t, dir, func() {
		if rc := initCmd([]string{"--registry", "first"}); rc != 0 {
			t.Fatalf("first init rc = %d", rc)
		}
		if rc := initCmd([]string{"--registry", "second"}); rc == 0 {
			t.Errorf("second init without --force should fail")
		}
		body, _ := os.ReadFile(pathYAML)
		if !strings.Contains(string(body), "first") {
			t.Errorf("body = %q, expected first registry to remain", body)
		}
		if rc := initCmd([]string{"--registry", "third", "--force"}); rc != 0 {
			t.Errorf("--force init rc = %d", rc)
		}
		body, _ = os.ReadFile(pathYAML)
		if !strings.Contains(string(body), "third") {
			t.Errorf("body = %q, expected --force to overwrite to third", body)
		}
	})
}

// Spec: §7.7 — workspace init updates .gitignore so the
// gitignored override file and overlay dir don't accidentally
// land in commits.
func TestInit_AddsGitignoreEntries(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir, func() {
		if rc := initCmd([]string{"--registry", "https://example/"}); rc != 0 {
			t.Fatalf("rc = %d", rc)
		}
	})
	body, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	for _, want := range []string{".podium/sync.local.yaml", ".podium/overlay/"} {
		if !strings.Contains(string(body), want) {
			t.Errorf(".gitignore missing %q:\n%s", want, body)
		}
	}
}
