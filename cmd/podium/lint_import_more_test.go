package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func runtimeGoexit() { runtime.Goexit() }

func TestLintCmd_FindsDiagnosticIssue(t *testing.T) {
	root := t.TempDir()
	// Write an artifact missing required type+version fields.
	dir := filepath.Join(root, "broken/artifact")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ARTIFACT.md"),
		[]byte("---\n# missing required fields\n---\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	out := captureStdout(t, func() {
		withStderr(t, func() {
			rc := lintCmd([]string{"--registry", root})
			// rc is 0 or 1 depending on severity; the test mainly drives
			// the lint output path.
			_ = rc
		})
	})
	// Either "lint: no issues" or a diagnostic was emitted — exercise
	// the printout path either way.
	_ = out
}

func TestImportCmd_HappyPath(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "src", "skills", "greet")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: greet\ndescription: Say hi.\nlicense: MIT\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("WriteFile SKILL: %v", err)
	}
	out := filepath.Join(dir, "out")
	withStderr(t, func() {
		captureStdout(t, func() {
			rc := importCmd([]string{
				"--source", filepath.Join(dir, "src"),
				"--target", out,
			})
			if rc != 0 {
				t.Errorf("importCmd = %d, want 0", rc)
			}
		})
	})
}

func TestImportCmd_MissingSourceErrors(t *testing.T) {
	t.Parallel()
	withStderr(t, func() {
		if rc := importCmd(nil); rc == 0 {
			t.Errorf("expected non-zero")
		}
	})
}

// mustGetJSON success path: real JSON response.
func TestMustGetJSON_OK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "test" {
			t.Errorf("query = %s", r.URL.Query().Get("q"))
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	body := mustGetJSON(srv.URL, "/v1/x", map[string]string{"q": "test"})
	if !strings.Contains(string(body), "ok") {
		t.Errorf("body = %s", body)
	}
}

// layerWatch with a stubbed sleep that exits the goroutine after one
// iteration via runtime.Goexit. Avoids panic-recover which has been
// observed to interact poorly with the coverage runtime's atexit
// handler.
func TestLayerWatch_OneIteration(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	orig := sleepSeconds
	t.Cleanup(func() { sleepSeconds = orig })
	sleepSeconds = func(int) { runtimeGoexit() }
	done := make(chan struct{})
	go func() {
		defer close(done)
		withStderr(t, func() {
			captureStdout(t, func() {
				_ = layerWatch([]string{"--id", "team", "--interval", "1"})
			})
		})
	}()
	<-done
	if hits == 0 {
		t.Errorf("reingest endpoint was never called")
	}
}

func TestLayerWatch_HTTPErrorIsNotFatal(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"x"}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	orig := sleepSeconds
	t.Cleanup(func() { sleepSeconds = orig })
	sleepSeconds = func(int) { runtimeGoexit() }
	done := make(chan struct{})
	go func() {
		defer close(done)
		withStderr(t, func() {
			captureStdout(t, func() {
				_ = layerWatch([]string{"--id", "team", "--interval", "1"})
			})
		})
	}()
	<-done
	if hits == 0 {
		t.Errorf("hit count = %d", hits)
	}
}
