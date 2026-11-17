package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/sync"
)

// Spec: §13.11.3, §7.5.2 — an --on-unparseable, PODIUM_SYNC_ON_UNPARSEABLE,
// or defaults.on_unparseable value other than keep or remove makes podium
// sync, podium sync --config, and podium sync override exit 2 with a message
// naming the source, and the override records no toggle.
func TestSync_OnUnparseableUsageError(t *testing.T) {
	registry := t.TempDir()
	testharness.WriteTree(t, registry, testharness.WriteTreeOption{
		Path: "tools/a/SKILL.md", Content: "---\nname: a\ndescription: The a skill.\n---\n\nbody\n",
	})
	run := func(t *testing.T, fn func() int) (int, string) {
		t.Helper()
		var rc int
		stderr := captureStderr(t, func() {
			captureStdout(t, func() { rc = fn() })
		})
		return rc, stderr
	}
	isolate := func(t *testing.T) string {
		t.Helper()
		t.Setenv("HOME", t.TempDir())
		t.Setenv(sync.OnUnparseableEnv, "")
		ws := t.TempDir()
		t.Chdir(ws)
		return ws
	}
	wantUsage := func(t *testing.T, rc int, stderr, msg string) {
		t.Helper()
		if rc != 2 || !strings.Contains(stderr, msg) {
			t.Errorf("rc = %d, stderr = %q; want 2 and %q", rc, stderr, msg)
		}
	}

	t.Run("sync flag", func(t *testing.T) {
		isolate(t)
		target := t.TempDir()
		rc, stderr := run(t, func() int {
			return syncCmd([]string{"--registry", registry, "--target", target, "--on-unparseable", "discard"})
		})
		wantUsage(t, rc, stderr, `error: --on-unparseable must be keep | remove, got "discard"`)
		if _, err := os.Stat(sync.LockFilePath(target)); !os.IsNotExist(err) {
			t.Errorf("sync wrote a lock before refusing: %v", err)
		}
	})

	t.Run("sync.yaml", func(t *testing.T) {
		ws := isolate(t)
		testharness.WriteTree(t, ws, testharness.WriteTreeOption{
			Path: ".podium/sync.yaml", Content: "defaults:\n  on_unparseable: discard\n",
		})
		rc, stderr := run(t, func() int {
			return syncCmd([]string{"--registry", registry, "--target", t.TempDir()})
		})
		wantUsage(t, rc, stderr, `error: defaults.on_unparseable must be keep | remove, got "discard"`)
	})

	t.Run("config", func(t *testing.T) {
		ws := isolate(t)
		testharness.WriteTree(t, ws, testharness.WriteTreeOption{
			Path: ".podium/sync.yaml",
			Content: "defaults:\n  registry: " + registry + "\ntargets:\n  - id: ws\n    kind: workspace\n    harness: none\n" +
				"    target: " + filepath.Join(ws, "out") + "\n",
		})
		t.Setenv(sync.OnUnparseableEnv, "drop")
		rc, stderr := run(t, func() int {
			return runMultiTargetSync(filepath.Join(ws, ".podium", "sync.yaml"), "", "", false, false, false, false)
		})
		wantUsage(t, rc, stderr, `error: PODIUM_SYNC_ON_UNPARSEABLE must be keep | remove, got "drop"`)
		if _, err := os.Stat(filepath.Join(ws, "out")); !os.IsNotExist(err) {
			t.Errorf("--config ran a target before refusing: %v", err)
		}
	})

	t.Run("override", func(t *testing.T) {
		isolate(t)
		target := t.TempDir()
		rc, stderr := run(t, func() int {
			return syncOverrideCmd([]string{"--target", target, "--add", "tools/a", "--registry", registry, "--on-unparseable", "discard"})
		})
		wantUsage(t, rc, stderr, `error: --on-unparseable must be keep | remove, got "discard"`)
		if _, err := os.Stat(sync.LockFilePath(target)); !os.IsNotExist(err) {
			t.Errorf("override recorded a toggle before refusing: %v", err)
		}
	})
}
