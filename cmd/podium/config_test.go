package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStdout redirects os.Stdout for the duration of fn and
// returns the captured bytes.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan []byte, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.Bytes()
	}()
	defer func() {
		os.Stdout = orig
		_ = r.Close()
	}()
	fn()
	_ = w.Close()
	return string(<-done)
}

// Spec: §13.10 + §13.12 — `podium config show` prints every
// supported configuration knob with the source of its value
// (env var name, registry.yaml, or "default").
func TestConfigShow_PrintsResolvedSettings(t *testing.T) {
	t.Setenv("PODIUM_BIND", "0.0.0.0:9090")
	t.Setenv("PODIUM_DEFAULT_LAYER_VISIBILITY", "organization")
	t.Setenv("PODIUM_CONFIG_FILE", filepath.Join(t.TempDir(), "missing.yaml"))
	out := captureStdout(t, func() {
		if rc := configShow(nil); rc != 0 {
			t.Errorf("rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "0.0.0.0:9090") {
		t.Errorf("output missing bind value: %s", out)
	}
	if !strings.Contains(out, "PODIUM_BIND") {
		t.Errorf("output missing PODIUM_BIND source label: %s", out)
	}
	if !strings.Contains(out, "organization") {
		t.Errorf("output missing default visibility: %s", out)
	}
}

// Spec: §6.10 — JSON output stays stable for tooling.
func TestConfigShow_JSONIsStructured(t *testing.T) {
	t.Setenv("PODIUM_CONFIG_FILE", filepath.Join(t.TempDir(), "missing.yaml"))
	t.Setenv("PODIUM_DEFAULT_LAYER_VISIBILITY", "public")
	out := captureStdout(t, func() {
		if rc := configShow([]string{"--json"}); rc != 0 {
			t.Errorf("rc = %d, want 0", rc)
		}
	})
	var resp struct {
		Settings []struct {
			Name   string `json:"Name"`
			Value  string `json:"Value"`
			Source string `json:"Source"`
		} `json:"settings"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, out)
	}
	found := false
	for _, s := range resp.Settings {
		if s.Name == "layers.default_visibility" {
			if s.Value != "public" {
				t.Errorf("default_visibility = %q, want public", s.Value)
			}
			if s.Source != "PODIUM_DEFAULT_LAYER_VISIBILITY" {
				t.Errorf("source = %q, want PODIUM_DEFAULT_LAYER_VISIBILITY", s.Source)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("layers.default_visibility missing from JSON: %s", out)
	}
}

// Spec: §6.10 — secrets in resolved settings are redacted.
func TestConfigShow_RedactsSecrets(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-very-secret-key-do-not-leak")
	t.Setenv("PODIUM_CONFIG_FILE", filepath.Join(t.TempDir(), "missing.yaml"))
	out := captureStdout(t, func() {
		_ = configShow(nil)
	})
	if strings.Contains(out, "sk-very-secret-key-do-not-leak") {
		t.Errorf("output leaks API key: %s", out)
	}
	if !strings.Contains(out, "<redacted>") {
		t.Errorf("output missing <redacted> placeholder: %s", out)
	}
}
