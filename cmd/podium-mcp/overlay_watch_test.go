package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
)

// Spec: §6.4 / §6.4.1 / F-6.4.1 — reindexOverlay re-resolves the overlay so
// an artifact added while the bridge runs becomes visible, and a removed
// overlay clears the in-memory records (the §6.9 "warn once" path) instead of
// leaving stale drafts. The pre-fix server loaded the overlay once at startup
// and never refreshed it.
func TestReindexOverlay_PicksUpAddedAndRemoved(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	overlayDir := filepath.Join(ws, ".podium", "overlay")
	testharness.WriteTree(t, ws, testharness.WriteTreeOption{
		Path:    ".podium/overlay/first/ARTIFACT.md",
		Content: "---\ntype: context\nversion: 0.1.0\n---\n\nfirst body\n",
	})

	s := newTestServer(t, &config{overlayPath: overlayDir})
	s.reindexOverlay(overlayDir)
	if got := len(s.overlaySnapshot()); got != 1 {
		t.Fatalf("after initial reindex: %d records, want 1", got)
	}

	// A second draft added at runtime becomes visible on the next reindex.
	testharness.WriteTree(t, ws, testharness.WriteTreeOption{
		Path:    ".podium/overlay/second/ARTIFACT.md",
		Content: "---\ntype: context\nversion: 0.1.0\n---\n\nsecond body\n",
	})
	s.reindexOverlay(overlayDir)
	if got := len(s.overlaySnapshot()); got != 2 {
		t.Fatalf("after adding a draft: %d records, want 2", got)
	}
	if s.overlayMatch("second") == nil {
		t.Errorf("added overlay draft 'second' not visible after reindex")
	}

	// Removing the overlay directory clears the records (warn-once path).
	if err := os.RemoveAll(overlayDir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	s.reindexOverlay(overlayDir)
	if got := len(s.overlaySnapshot()); got != 0 {
		t.Errorf("after removing the overlay: %d records, want 0", got)
	}
}

// Spec: §6.4.1 / F-6.4.1 — the poll loop fires reindex when the watched
// tree's content fingerprint changes.
func TestOverlayWatchLoop_FiresOnContentChange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	calls := make(chan string, 4)
	done := make(chan struct{})
	go overlayWatchLoop(10*time.Millisecond, func() string { return dir }, func(p string) { calls <- p }, done)
	defer close(done)

	// Let the loop capture its baseline before mutating.
	time.Sleep(40 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	select {
	case got := <-calls:
		if got != dir {
			t.Errorf("reindex path = %q, want %q", got, dir)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not reindex on a content change")
	}
}

// Spec: §6.4 step 2 / F-6.4.1 — the loop consults pathOf every tick, so an
// overlay path established after startup (the roots/list reply) triggers an
// initial reindex.
func TestOverlayWatchLoop_FiresOnPathChange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var path atomicString
	calls := make(chan string, 4)
	done := make(chan struct{})
	go overlayWatchLoop(10*time.Millisecond, path.Load, func(p string) { calls <- p }, done)
	defer close(done)

	time.Sleep(40 * time.Millisecond)
	path.Store(dir) // path resolved after startup
	select {
	case got := <-calls:
		if got != dir {
			t.Errorf("reindex path = %q, want %q", got, dir)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not reindex when the path changed")
	}
}

// Spec: §6.4 / §14.7 / F-6.4.3 / F-14.7.1 — the overlay watcher uses fsnotify,
// not a poll loop, so a content change is picked up promptly even when the
// path re-check interval is long. A long interval starves the polling
// fallback: this test reindexes within the fsnotify debounce, which the
// previous 500ms-poll implementation could not do with a multi-second
// interval. It fails against a poll-only watcher and passes with fsnotify.
func TestOverlayWatchLoop_UsesFsnotifyNotPolling(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	calls := make(chan string, 4)
	done := make(chan struct{})
	// A 5s path-re-check interval means a poll-only watcher would not observe
	// the content change for up to 5s; fsnotify observes it within the
	// 200ms debounce.
	go overlayWatchLoop(5*time.Second, func() string { return dir }, func(p string) { calls <- p }, done)
	defer close(done)

	// Give the fsnotify watcher time to register before mutating.
	time.Sleep(150 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	select {
	case got := <-calls:
		if got != dir {
			t.Errorf("reindex path = %q, want %q", got, dir)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fsnotify watcher did not reindex within 2s of a content change (poll fallback would need the 5s interval)")
	}
}

// Spec: §6.4 / F-6.4.3 — a newly created subdirectory under the overlay is
// itself watched, so an artifact package added at runtime (a new directory
// holding ARTIFACT.md) is observed even though fsnotify is not recursive.
func TestOverlayWatchLoop_WatchesNewSubdirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	calls := make(chan string, 8)
	done := make(chan struct{})
	go overlayWatchLoop(5*time.Second, func() string { return dir }, func(p string) { calls <- p }, done)
	defer close(done)

	time.Sleep(150 * time.Millisecond)
	// Create a new package directory, then drop a file into it. The file
	// write only surfaces if the watcher added the new directory.
	pkg := filepath.Join(dir, "new-pkg")
	if err := os.Mkdir(pkg, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Drain the event from the directory creation.
	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not reindex on subdirectory creation")
	}
	if err := os.WriteFile(filepath.Join(pkg, "ARTIFACT.md"), []byte("---\ntype: context\n---\n"), 0o644); err != nil {
		t.Fatalf("write into new pkg: %v", err)
	}
	select {
	case got := <-calls:
		if got != dir {
			t.Errorf("reindex path = %q, want %q", got, dir)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not observe a file written into a newly created subdirectory")
	}
}

// Spec: §6.4 / F-6.4.3 — retarget opens a real fsnotify watch over an existing
// directory and reports it active; an empty path leaves the watch inactive so
// the loop parks the event cases and (when applicable) polls instead.
func TestOverlayContentWatch_Retarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := &overlayContentWatch{}
	defer w.close()

	w.retarget(dir)
	if !w.fsnotifyActive() {
		t.Fatal("retarget(existing dir) did not activate fsnotify")
	}
	if w.events() == nil || w.errs() == nil {
		t.Fatal("active watch must expose non-nil event and error channels")
	}

	w.retarget("")
	if w.fsnotifyActive() {
		t.Error("retarget(\"\") left fsnotify active")
	}
	if w.events() != nil || w.errs() != nil {
		t.Error("inactive watch must expose nil channels so the select case parks")
	}
}

// atomicString is a tiny goroutine-safe string cell for the path-change test.
type atomicString struct {
	mu sync.Mutex
	v  string
}

func (a *atomicString) Load() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.v
}

func (a *atomicString) Store(s string) {
	a.mu.Lock()
	a.v = s
	a.mu.Unlock()
}
