package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec: §13.2.2 — `podium status` surfaces the registry's mode
// (public / read_only / ready) so operators can detect a public-
// mode deployment without inspecting startup config.
func TestStatus_SurfacesRegistryMode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mode":"public","ready":true}`))
	}))
	defer ts.Close()
	t.Setenv("PODIUM_REGISTRY", ts.URL)

	out := captureStdout(t, func() {
		if rc := statusCmd(nil); rc != 0 {
			t.Errorf("rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "registry mode:      public") {
		t.Errorf("status output missing public-mode marker:\n%s", out)
	}
	if !strings.Contains(out, "reachability:       OK") {
		t.Errorf("status output missing reachability OK:\n%s", out)
	}
}

// Spec: §13.2.2 / §13.9 — read_only mode also surfaces through
// status.
func TestStatus_SurfacesReadOnlyMode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"mode":"read_only","ready":true}`))
	}))
	defer ts.Close()
	t.Setenv("PODIUM_REGISTRY", ts.URL)

	out := captureStdout(t, func() {
		_ = statusCmd(nil)
	})
	if !strings.Contains(out, "registry mode:      read_only") {
		t.Errorf("status output missing read_only marker:\n%s", out)
	}
}

// Spec: §13.10 — when the registry is unreachable, status
// reports the failure cleanly without a panic.
func TestStatus_UnreachableRegistry(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1") // unbound port
	out := captureStdout(t, func() {
		_ = statusCmd(nil)
	})
	if !strings.Contains(out, "UNREACHABLE") {
		t.Errorf("status output missing UNREACHABLE marker:\n%s", out)
	}
}

// Spec: §7.5.2 / §7.7 — `podium status` resolves the registry and harness from
// the merged sync.yaml (not only the environment), so the diagnostic reflects
// what a sync would actually use. A filesystem-source registry is marked as
// such and skips the HTTP reachability probe.
func TestStatus_ResolvesRegistryAndHarnessFromSyncYAML(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "")
	t.Setenv("PODIUM_HARNESS", "")
	t.Setenv("HOME", t.TempDir()) // isolate the user-global config scope
	t.Setenv("USERPROFILE", t.TempDir())

	dir := t.TempDir()
	regDir := filepath.Join(dir, "reg")
	if err := os.MkdirAll(filepath.Join(dir, ".podium"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "defaults:\n  registry: " + regDir + "\n  harness: claude-code\n"
	if err := os.WriteFile(filepath.Join(dir, ".podium", "sync.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	out := captureStdout(t, func() {
		if rc := statusCmd(nil); rc != 0 {
			t.Errorf("rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "registry:           "+regDir) {
		t.Errorf("status did not resolve registry from sync.yaml:\n%s", out)
	}
	if !strings.Contains(out, "harness:            claude-code") {
		t.Errorf("status did not resolve harness from sync.yaml:\n%s", out)
	}
	if !strings.Contains(out, "source:             filesystem") {
		t.Errorf("status did not mark the filesystem registry:\n%s", out)
	}
}
