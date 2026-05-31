package main

import (
	"io"
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

// Spec: §7.7 workspace-mode step 1 (F-7.7.11) — init walks up from CWD to
// reuse an existing `.podium/` workspace instead of creating a second one
// in a subdirectory.
func TestInit_WalksUpToExistingWorkspace(t *testing.T) {
	root := t.TempDir()
	withCwd(t, root, func() {
		if rc := initCmd([]string{"--registry", "https://podium.acme.com"}); rc != 0 {
			t.Fatalf("root init rc = %d", rc)
		}
	})
	sub := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	withCwd(t, sub, func() {
		// --local from the subdirectory must target the parent workspace's
		// .podium/, not create a new one under the subdirectory.
		if rc := initCmd([]string{"--local", "--registry", "https://staging.acme.com"}); rc != 0 {
			t.Fatalf("sub init rc = %d", rc)
		}
	})
	if _, err := os.Stat(filepath.Join(sub, ".podium")); err == nil {
		t.Errorf("init created a second .podium/ in the subdirectory")
	}
	if _, err := os.Stat(filepath.Join(root, ".podium", "sync.local.yaml")); err != nil {
		t.Errorf("sync.local.yaml not written to the discovered workspace: %v", err)
	}
}

// Spec: §7.7 (F-7.7.12) — with no value flags and an interactive stdin,
// init prompts for the registry (and optional harness/target) and writes
// the answers.
func TestInit_InteractiveWizard(t *testing.T) {
	dir := t.TempDir()
	prevTerm := initIsTerminal
	prevStdin := initStdin
	initIsTerminal = func() bool { return true }
	initStdin = strings.NewReader("https://wizard.acme.com\nclaude-code\n.claude/\n")
	t.Cleanup(func() { initIsTerminal = prevTerm; initStdin = prevStdin })

	withCwd(t, dir, func() {
		if rc := initCmd(nil); rc != 0 {
			t.Fatalf("wizard init rc = %d, want 0", rc)
		}
	})
	body, err := os.ReadFile(filepath.Join(dir, ".podium", "sync.yaml"))
	if err != nil {
		t.Fatalf("read sync.yaml: %v", err)
	}
	for _, want := range []string{"https://wizard.acme.com", "claude-code", ".claude/"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("sync.yaml missing %q:\n%s", want, body)
		}
	}
}

// Spec: §7.7 (F-7.7.12) — a non-terminal stdin skips the wizard and the
// command exits 2 with the required-flag error rather than blocking.
func TestInit_NonTerminalSkipsWizard(t *testing.T) {
	dir := t.TempDir()
	prevTerm := initIsTerminal
	initIsTerminal = func() bool { return false }
	t.Cleanup(func() { initIsTerminal = prevTerm })
	withCwd(t, dir, func() {
		if rc := initCmd(nil); rc != 2 {
			t.Fatalf("rc = %d, want 2 (no wizard, no flags)", rc)
		}
	})
}

// Spec: §7.7 workspace-mode step 4 (F-7.7.13) — the committed default
// scope prints next-step hints to commit the file and run `podium sync`.
func TestInit_PrintsNextStepHints(t *testing.T) {
	dir := t.TempDir()
	var out string
	withCwd(t, dir, func() {
		out = captureStdout(t, func() {
			if rc := initCmd([]string{"--registry", "https://podium.acme.com"}); rc != 0 {
				t.Fatalf("rc = %d", rc)
			}
		})
	})
	if !strings.Contains(out, "commit") {
		t.Errorf("missing commit hint:\n%s", out)
	}
	if !strings.Contains(out, "podium sync") {
		t.Errorf("missing sync hint:\n%s", out)
	}
}

// Spec: §7.7 (F-7.7.12) — the wizard reader collects the registry and the
// optional defaults; blank optional answers stay unset.
func TestRunInitWizard(t *testing.T) {
	res, err := runInitWizard(strings.NewReader("https://podium.acme.com\n\n\n"), io.Discard)
	if err != nil {
		t.Fatalf("runInitWizard: %v", err)
	}
	if res.registry != "https://podium.acme.com" {
		t.Errorf("registry = %q", res.registry)
	}
	if res.harness != "" || res.target != "" {
		t.Errorf("blank answers should stay unset: harness=%q target=%q", res.harness, res.target)
	}
}
