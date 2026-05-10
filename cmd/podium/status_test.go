package main

import (
	"net/http"
	"net/http/httptest"
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
