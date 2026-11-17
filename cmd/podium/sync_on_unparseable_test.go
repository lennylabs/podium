package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/sync"
)

// Spec: §13.11.3 — the rejected line of an unparseable artifact whose
// previous output the sync keeps says the output was kept, and under --dry-run,
// which writes nothing, says it would be kept. A drop not marked kept carries
// neither note.
func TestReportDropped_KeptOutputWording(t *testing.T) {
	kept := layer.Drop{ArtifactID: "tools/a", Code: layer.InvalidArtifactCode, Reason: "broken", PreviousOutputKept: true}
	plain := layer.Drop{ArtifactID: "tools/b", Code: layer.InvalidArtifactCode, Reason: "broken"}
	for _, tc := range []struct {
		dryRun bool
		want   string
	}{
		{false, "rejected: tools/a (ingest.invalid_artifact): broken; the previous sync output was kept\n"},
		{true, "rejected: tools/a (ingest.invalid_artifact): broken; the previous sync output would be kept\n"},
	} {
		var buf strings.Builder
		if !reportDropped(&buf, []layer.Drop{kept, plain}, tc.dryRun) {
			t.Fatalf("dryRun=%v: reportDropped reported no drop", tc.dryRun)
		}
		want := tc.want + "rejected: tools/b (ingest.invalid_artifact): broken\n"
		if got := buf.String(); got != want {
			t.Errorf("dryRun=%v: output = %q, want %q", tc.dryRun, got, want)
		}
	}
}

// Spec: §13.11.3 — podium sync --dry-run over a target whose lock records a
// now-unparseable artifact says its previous output would be kept, and the
// sync that follows says it was kept.
func TestSync_DryRunSaysOutputWouldBeKept(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(sync.OnUnparseableEnv, "")
	t.Chdir(t.TempDir())
	registry := t.TempDir()
	target := t.TempDir()
	testharness.WriteTree(t, registry, testharness.WriteTreeOption{
		Path: "tools/a/SKILL.md", Content: "---\nname: a\ndescription: The a skill.\n---\n\nbody\n",
	})
	run := func(args ...string) (int, string) {
		var rc int
		stderr := captureStderr(t, func() {
			captureStdout(t, func() {
				rc = syncCmd(append([]string{"--registry", registry, "--target", target}, args...))
			})
		})
		return rc, stderr
	}
	if rc, stderr := run(); rc != 0 {
		t.Fatalf("clean sync rc = %d, stderr = %q", rc, stderr)
	}
	testharness.WriteTree(t, registry, testharness.WriteTreeOption{
		Path: "tools/a/SKILL.md", Content: "---\nname: a\ndescription:\n  text: A mapping.\n---\n\nbody\n",
	})
	rc, stderr := run("--dry-run")
	if rc != 1 || !strings.Contains(stderr, "; the previous sync output would be kept\n") || strings.Contains(stderr, "was kept") {
		t.Errorf("dry run rc = %d, stderr = %q; want 1 and the would-be-kept note", rc, stderr)
	}
	rc, stderr = run()
	if rc != 1 || !strings.Contains(stderr, "; the previous sync output was kept\n") {
		t.Errorf("sync rc = %d, stderr = %q; want 1 and the kept note", rc, stderr)
	}
}

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
