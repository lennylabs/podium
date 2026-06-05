package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// TestPodiumSync_CodexSessionEndHook covers
// Spec: §6.7.1 — the codex hook_event cell is graded ✓, which requires the
// adapter to config-merge every common event including session_end. A
// session_end hook synced for codex must inject a [[hooks.SessionEnd]] table
// into .codex/config.toml; before the fix codex omitted session_end and the
// hook produced no output.
func TestPodiumSync_CodexSessionEndHook(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	target := t.TempDir()
	testharness.WriteTree(t, registry, testharness.WriteTreeOption{
		Path: "audit/teardown/ARTIFACT.md",
		Content: "---\ntype: hook\nversion: 1.0.0\ndescription: teardown.\n" +
			"hook_event: session_end\nhook_action: |\n  echo bye\n---\n\nbody\n",
	})
	res := cmdharness.Run(t, "podium", "",
		"sync", "--registry", registry, "--target", target, "--harness", "codex")
	if res.ExitCode != 0 {
		t.Fatalf("sync exit=%d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	cfg, err := os.ReadFile(filepath.Join(target, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("read .codex/config.toml: %v", err)
	}
	if !strings.Contains(string(cfg), "[[hooks.SessionEnd]]") {
		t.Errorf("session_end did not config-merge a SessionEnd table:\n%s", cfg)
	}
}

// TestPodiumSync_RefusesBelowMinServerVersion covers
// Spec: §6.7 "Versioning" — a profile or harness combination that needs newer
// adapter behavior pins a minimum binary version via min_server_version; an
// older binary refuses to start. podium sync runs the versioned adapters, so a
// pin above this binary refuses the run.
func TestPodiumSync_RefusesBelowMinServerVersion(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	testharness.WriteTree(t, registry, testharness.WriteTreeOption{
		Path:    "glossary/ARTIFACT.md",
		Content: contextArtifact,
	})
	ws := t.TempDir()
	// The min_server_version pin lives in the workspace sync.yaml and is read
	// from the merged config regardless of how the registry resolves, so the
	// registry is passed explicitly to keep the test independent of any
	// PODIUM_REGISTRY in the test environment.
	writeSyncYAML(t, ws, "defaults:\n  min_server_version: \"99.0.0\"\n")
	res := cmdharness.Run(t, "podium", ws, "sync", "--registry", registry)
	if res.ExitCode != 2 {
		t.Fatalf("sync exit=%d, want 2\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "config.server_version_too_old") {
		t.Errorf("stderr missing config.server_version_too_old:\n%s", res.Stderr)
	}
}

// TestPodiumSync_AllowsAtOrAboveMinServerVersion covers a pin at or
// below the binary version does not block the run.
func TestPodiumSync_AllowsAtOrAboveMinServerVersion(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	testharness.WriteTree(t, registry, testharness.WriteTreeOption{
		Path:    "glossary/ARTIFACT.md",
		Content: contextArtifact,
	})
	ws := t.TempDir()
	target := t.TempDir()
	writeSyncYAML(t, ws, "defaults:\n  min_server_version: \"0.0.1\"\n")
	res := cmdharness.Run(t, "podium", ws, "sync", "--registry", registry, "--target", target, "--harness", "none")
	if res.ExitCode != 0 {
		t.Fatalf("sync exit=%d, want 0\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if _, err := os.Stat(filepath.Join(target, "glossary", "ARTIFACT.md")); err != nil {
		t.Errorf("expected glossary/ARTIFACT.md to materialize: %v", err)
	}
}

// TestPodiumSync_ProfilePinRefusesOlderAdapter covers
// Spec: §12 "Harness adapter drift" — "Adapters are versioned with the MCP
// server binary; profiles can pin a minimum version." §6.7 establishes that
// adapter behavior versions alongside the binary, so a profile that needs newer
// adapter behavior pins min_server_version; `podium sync` against that profile
// refuses an older binary rather than materializing with a stale adapter.
func TestPodiumSync_ProfilePinRefusesOlderAdapter(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	testharness.WriteTree(t, registry, testharness.WriteTreeOption{
		Path:    "glossary/ARTIFACT.md",
		Content: contextArtifact,
	})
	ws := t.TempDir()
	// The pin lives on the profile, not defaults, and the run selects it.
	writeSyncYAML(t, ws, "profiles:\n  prod:\n    min_server_version: \"99.0.0\"\n    include: [\"glossary\"]\n")
	res := cmdharness.Run(t, "podium", ws, "sync", "--registry", registry, "--profile", "prod")
	if res.ExitCode != 2 {
		t.Fatalf("sync exit=%d, want 2\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "config.server_version_too_old") {
		t.Errorf("stderr missing config.server_version_too_old:\n%s", res.Stderr)
	}
}

// writeSyncYAML writes <ws>/.podium/sync.yaml with the given body.
func writeSyncYAML(t *testing.T, ws, body string) {
	t.Helper()
	dir := filepath.Join(ws, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .podium: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sync.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write sync.yaml: %v", err)
	}
}
