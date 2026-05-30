package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writeArtifactTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mkdir := func(p string) {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}
	write := func(p, content string) {
		if err := os.WriteFile(filepath.Join(root, p), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	mkdir("personal/hello/greet")
	write("personal/hello/greet/SKILL.md",
		"---\nname: greet\ndescription: Say hi.\n---\n\nbody\n")
	write("personal/hello/greet/ARTIFACT.md",
		"---\ntype: skill\nversion: 1.0.0\nwhen_to_use:\n  - hello\ntags: [demo]\nsensitivity: low\n---\n\n<!-- body in SKILL.md -->\n")
	return root
}

func TestLintCmd_NoIssues(t *testing.T) {
	root := writeArtifactTree(t)
	captureStdout(t, func() {
		withStderr(t, func() {
			if rc := lintCmd([]string{"--registry", root}); rc != 0 {
				t.Errorf("lintCmd = %d, want 0", rc)
			}
		})
	})
}

func TestLintCmd_BadRegistryPathReturns1(t *testing.T) {
	t.Parallel()
	withStderr(t, func() {
		rc := lintCmd([]string{"--registry", filepath.Join(t.TempDir(), "missing")})
		if rc != 1 {
			t.Errorf("lintCmd(missing) = %d, want 1", rc)
		}
	})
}

func TestSyncCmd_NoRegistryFallsBackToConfig(t *testing.T) {
	// Set CWD into a workspace with .podium/sync.yaml and verify
	// syncCmd reads it instead of erroring.
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".podium"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".podium", "sync.yaml"), []byte(
		"defaults:\n  registry: "+filepath.Join(ws, "registry")+"\n"), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(ws, "registry"), 0o755); err != nil {
		t.Fatalf("mkdir registry: %v", err)
	}
	orig, _ := os.Getwd()
	if err := os.Chdir(ws); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(orig)

	withStderr(t, func() {
		// sync against empty registry should succeed with 0 artifacts.
		if rc := syncCmd([]string{"--target", t.TempDir()}); rc != 0 {
			t.Errorf("syncCmd = %d, want 0", rc)
		}
	})
}

func TestSyncCmd_HappyPath(t *testing.T) {
	root := writeArtifactTree(t)
	target := t.TempDir()
	out := captureStdout(t, func() {
		withStderr(t, func() {
			rc := syncCmd([]string{
				"--registry", root,
				"--target", target,
				"--harness", "none",
			})
			if rc != 0 {
				t.Errorf("syncCmd = %d, want 0", rc)
			}
		})
	})
	if out == "" {
		t.Errorf("expected stdout")
	}
}

func TestSyncCmd_DryRunJSON(t *testing.T) {
	root := writeArtifactTree(t)
	out := captureStdout(t, func() {
		withStderr(t, func() {
			rc := syncCmd([]string{
				"--registry", root,
				"--target", t.TempDir(),
				"--harness", "none",
				"--dry-run", "--json",
			})
			if rc != 0 {
				t.Errorf("syncCmd = %d, want 0", rc)
			}
		})
	})
	if out == "" {
		t.Errorf("expected stdout")
	}
}



func TestSearchCmd_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/search_artifacts" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"total_matched":1,"results":[{"id":"a","type":"context"}]}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	out := captureStdout(t, func() {
		withStderr(t, func() {
			if rc := searchCmd([]string{"--type", "context", "--scope", "x", "query"}); rc != 0 {
				t.Errorf("rc = %d", rc)
			}
		})
	})
	if out == "" {
		t.Errorf("empty stdout")
	}
}

func TestSearchCmd_JSONFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"total_matched":0,"results":[]}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	withStderr(t, func() {
		if rc := searchCmd([]string{"--json", "q"}); rc != 0 {
			t.Errorf("rc = %d", rc)
		}
	})
}

func TestSearchCmd_NoQueryExits2(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	withStderr(t, func() {
		if rc := searchCmd(nil); rc != 2 {
			t.Errorf("rc = %d", rc)
		}
	})
}
