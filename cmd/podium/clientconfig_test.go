package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// spec: §7.7 (F-7.7.1) — config show prints the merged sync.yaml with
// per-key provenance: each defaults key annotated with the winning scope.
func TestConfigClientShow_Provenance(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	mustWrite(t, filepath.Join(home, ".podium", "sync.yaml"), "defaults:\n  harness: claude-code\n")
	mustWrite(t, filepath.Join(ws, ".podium", "sync.yaml"), "defaults:\n  registry: https://podium.acme.com\n  target: .claude/\n")

	out := captureStdout(t, func() {
		if rc := configClientShowAt(ws, home, false, ""); rc != 0 {
			t.Errorf("rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "https://podium.acme.com") {
		t.Errorf("missing registry value:\n%s", out)
	}
	if !strings.Contains(out, "defaults.harness") || !strings.Contains(out, "claude-code") {
		t.Errorf("missing harness from user-global scope:\n%s", out)
	}
	// Provenance: the registry comes from the project-shared scope, the
	// harness from the user-global scope.
	if !strings.Contains(out, "<ws>/.podium/sync.yaml") || !strings.Contains(out, "~/.podium/sync.yaml") {
		t.Errorf("missing per-key provenance labels:\n%s", out)
	}
}

// spec: §7.7 (F-7.7.3), §7.5.2 — config show surfaces profile-name
// collisions across scopes for debugging.
func TestConfigClientShow_ProfileCollision(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, ".podium", "sync.yaml"),
		"profiles:\n  staging:\n    include: [\"a/**\"]\n")
	mustWrite(t, filepath.Join(ws, ".podium", "sync.local.yaml"),
		"profiles:\n  staging:\n    include: [\"b/**\"]\n")

	out := captureStdout(t, func() {
		_ = configClientShowAt(ws, home, false, "")
	})
	if !strings.Contains(out, "Profile collisions: 1") {
		t.Errorf("missing collision summary:\n%s", out)
	}
	if !strings.Contains(out, "staging") || !strings.Contains(out, "wins") {
		t.Errorf("collision line should name the profile and winner:\n%s", out)
	}
}

// spec: §7.7 (F-7.7.2) — --explain prints one key's full resolution
// chain: the value at each scope and which won.
func TestConfigClientShow_Explain(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	mustWrite(t, filepath.Join(home, ".podium", "sync.yaml"), "defaults:\n  registry: https://home-reg\n")
	mustWrite(t, filepath.Join(ws, ".podium", "sync.yaml"), "defaults:\n  registry: https://ws-reg\n")

	out := captureStdout(t, func() {
		if rc := configClientShowAt(ws, home, false, "registry"); rc != 0 {
			t.Errorf("rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "https://home-reg") || !strings.Contains(out, "https://ws-reg") {
		t.Errorf("explain should list every scope's value:\n%s", out)
	}
	// The project-shared scope outranks user-global.
	if !strings.Contains(out, "resolved: https://ws-reg") {
		t.Errorf("explain should report the winning value:\n%s", out)
	}
}

// spec: §6.10 — JSON output stays structured for tooling.
func TestConfigClientShow_JSON(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, ".podium", "sync.yaml"), "defaults:\n  registry: https://json-reg\n")

	out := captureStdout(t, func() {
		_ = configClientShowAt(ws, home, true, "")
	})
	var payload struct {
		Defaults map[string]struct {
			Value string `json:"value"`
			From  string `json:"from"`
		} `json:"defaults"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, out)
	}
	if payload.Defaults["registry"].Value != "https://json-reg" {
		t.Errorf("defaults.registry = %+v", payload.Defaults["registry"])
	}
}
