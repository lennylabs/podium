package sync_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/sync"
)

// makeRegistry writes a minimal filesystem-source registry under
// dir/registry and returns its path. Adds one artifact under
// finance/intro.
func makeRegistry(t *testing.T, dir string) string {
	t.Helper()
	root := filepath.Join(dir, "registry")
	if err := os.MkdirAll(filepath.Join(root, "_default", "finance", "intro"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	configBody := "layer_order:\n- _default\n"
	if err := os.WriteFile(filepath.Join(root, ".registry-config"), []byte(configBody), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	body := "---\ntype: context\nversion: 1.0.0\ndescription: intro\nsensitivity: low\n---\n\nbody\n"
	if err := os.WriteFile(
		filepath.Join(root, "_default", "finance", "intro", "ARTIFACT.md"),
		[]byte(body), 0o644,
	); err != nil {
		t.Fatalf("WriteFile artifact: %v", err)
	}
	return root
}

// Spec: §7.5 / §13.11.4 — Watch runs an initial sync, then reruns
// Run after edits to the registry. Each rerun emits a WatchEvent
// on the channel; the channel closes when ctx is canceled.
// §13.11.4 covers the filesystem-specific watch shape.
func TestWatch_RerunsAfterRegistryEdit(t *testing.T) {
	// Intentionally not parallel: the poll-based watcher's ticker
	// can be starved when this test runs alongside dozens of
	// disk-bound tests in the same process. Serializing within the
	// package keeps the test reliable without hiding real watcher
	// regressions.
	dir := t.TempDir()
	registry := makeRegistry(t, dir)
	target := filepath.Join(dir, "out")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := sync.Watch(ctx, sync.WatchOptions{
		Sync: sync.Options{
			RegistryPath: registry,
			Target:       target,
			AdapterID:    "none",
		},
		Period:   50 * time.Millisecond,
		Debounce: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	// Initial sync event.
	first := waitFor(t, events, 5*time.Second)
	if first.Err != nil {
		t.Fatalf("initial sync: %v", first.Err)
	}
	if len(first.Result.Artifacts) != 1 {
		t.Errorf("initial Artifacts = %d, want 1", len(first.Result.Artifacts))
	}
	// Edit: add a second artifact.
	if err := os.MkdirAll(filepath.Join(registry, "_default", "marketing", "deck"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := "---\ntype: context\nversion: 1.0.0\ndescription: deck\nsensitivity: low\n---\n\nbody\n"
	if err := os.WriteFile(
		filepath.Join(registry, "_default", "marketing", "deck", "ARTIFACT.md"),
		[]byte(body), 0o644,
	); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// The watcher's debounce + period must exceed the edit settle
	// time before it reruns; allow a generous deadline so the test
	// stays stable when the suite runs hundreds of goroutines in
	// parallel and the ticker channel may be served late. Coverage
	// instrumentation slows every test by ~5×, so the deadline has
	// to absorb that on top of normal scheduling jitter — anything
	// under 30s here flaked under `make coverage-budget` in CI.
	second := waitFor(t, events, 30*time.Second)
	if second.Err != nil {
		t.Fatalf("rerun: %v", second.Err)
	}
	if len(second.Result.Artifacts) != 2 {
		t.Errorf("rerun Artifacts = %d, want 2", len(second.Result.Artifacts))
	}
}

// Spec: §7.5 — canceling the watcher's context closes the events
// channel; the goroutine exits cleanly.
func TestWatch_CancelClosesChannel(t *testing.T) {
	// Intentionally not parallel: the poll-based watcher's ticker
	// can be starved when this test runs alongside dozens of
	// disk-bound tests in the same process. Serializing within the
	// package keeps the test reliable without hiding real watcher
	// regressions.
	dir := t.TempDir()
	registry := makeRegistry(t, dir)
	target := filepath.Join(dir, "out")
	ctx, cancel := context.WithCancel(context.Background())
	events, err := sync.Watch(ctx, sync.WatchOptions{
		Sync: sync.Options{
			RegistryPath: registry,
			Target:       target,
			AdapterID:    "none",
		},
		Period: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	// Drain the initial event.
	_ = waitFor(t, events, 5*time.Second)
	cancel()
	// Channel should close within a few periods.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Error("events channel did not close after cancel")
}

// Spec: §7.5.4 — watch mode materializes "profile + toggles from the lock
// file" on startup. A toggles.remove recorded by a prior override survives the
// watcher event and the removed artifact is not materialized; the toggle
// persists in the rewritten lock.
func TestWatch_AppliesLockTogglesOnStartup(t *testing.T) {
	// Not parallel: shares the package's poll-based watcher timing budget.
	dir := t.TempDir()
	registry := makeRegistry(t, dir)
	target := filepath.Join(dir, "out")

	// Seed a lock with a remove toggle for the only artifact.
	if err := sync.WriteLock(target, &sync.LockFile{
		Version: 1, Target: target,
		Toggles: sync.LockToggles{Remove: []sync.LockToggle{{ID: "_default/finance/intro"}}},
	}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := sync.Watch(ctx, sync.WatchOptions{
		Sync: sync.Options{
			RegistryPath: registry,
			Target:       target,
			AdapterID:    "none",
		},
		Period:   50 * time.Millisecond,
		Debounce: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	first := waitFor(t, events, 5*time.Second)
	if first.Err != nil {
		t.Fatalf("initial sync: %v", first.Err)
	}
	if len(first.Result.Artifacts) != 0 {
		t.Errorf("removed artifact must not materialize, got %d artifacts", len(first.Result.Artifacts))
	}
	cancel()
	// The toggle survives the watcher's lock rewrite.
	lock, err := sync.ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if len(lock.Toggles.Remove) != 1 || lock.Toggles.Remove[0].ID != "_default/finance/intro" {
		t.Errorf("remove toggle not preserved across watch: %+v", lock.Toggles)
	}
}

func waitFor(t *testing.T, ch <-chan sync.WatchEvent, timeout time.Duration) sync.WatchEvent {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before event arrived")
		}
		return ev
	case <-time.After(timeout):
		t.Fatal("timed out waiting for watch event")
	}
	return sync.WatchEvent{}
}
