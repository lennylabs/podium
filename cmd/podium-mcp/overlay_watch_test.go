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
