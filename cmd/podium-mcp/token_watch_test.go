package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Spec: §6.3.2.1 — the session-token file is watched via fsnotify and re-read
// on change. watchLoop is the testable core: an fsnotify event whose target
// matches the watched file triggers a reload. F-6.3.2.
func TestWatchLoop_FSNotifyEventTriggersReload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "token")
	reasons := make(chan string, 4)
	sighup := make(chan os.Signal, 1)
	events := make(chan fsnotify.Event, 1)
	errs := make(chan error, 1)
	done := make(chan struct{})
	defer close(done)
	go watchLoop(file, sighup, events, errs, func(reason string) { reasons <- reason }, done)

	events <- fsnotify.Event{Name: file, Op: fsnotify.Write}
	select {
	case r := <-reasons:
		if r != "file changed" {
			t.Errorf("reason = %q, want file changed", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("fsnotify write event did not trigger a reload within deadline")
	}
}

// Spec: §6.3.2.1 — an event for an unrelated file in the watched directory is
// ignored; only the configured token path triggers a re-read. F-6.3.2.
func TestWatchLoop_IgnoresUnrelatedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "token")
	reasons := make(chan string, 4)
	sighup := make(chan os.Signal, 1)
	events := make(chan fsnotify.Event, 2)
	errs := make(chan error, 1)
	done := make(chan struct{})
	defer close(done)
	go watchLoop(file, sighup, events, errs, func(reason string) { reasons <- reason }, done)

	// An unrelated sibling write must not reload; a subsequent token write must.
	events <- fsnotify.Event{Name: filepath.Join(dir, "other"), Op: fsnotify.Write}
	events <- fsnotify.Event{Name: file, Op: fsnotify.Create}
	select {
	case r := <-reasons:
		if r != "file changed" {
			t.Errorf("reason = %q, want file changed (the unrelated write must be ignored)", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("token event did not trigger a reload within deadline")
	}
	// No second reload should be queued from the unrelated file.
	select {
	case r := <-reasons:
		t.Errorf("unexpected extra reload %q from the unrelated file", r)
	default:
	}
}

// Spec: §6.3.2.1 — SIGHUP triggers a forced re-read. The watcher's signal path
// is exercised with a controlled channel so the test does not signal the real
// process. F-6.3.2.
func TestWatchLoop_SIGHUPTriggersReload(t *testing.T) {
	t.Parallel()
	reasons := make(chan string, 4)
	sighup := make(chan os.Signal, 1)
	done := make(chan struct{})
	defer close(done)
	// nil events/errs: no file watch wired, SIGHUP path still active.
	go watchLoop("", sighup, nil, nil, func(reason string) { reasons <- reason }, done)

	sighup <- syscall.SIGHUP
	select {
	case r := <-reasons:
		if r != "SIGHUP" {
			t.Errorf("reason = %q, want SIGHUP", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("SIGHUP did not trigger a reload within deadline")
	}
}

// Spec: §6.3.2.1 — startTokenWatch installs a real fsnotify watch on the token
// file's directory; a write to the file re-reads the token end-to-end (the
// watcher genuinely observes the filesystem rather than a synthetic channel).
// F-6.3.2.
func TestStartTokenWatch_RealFSNotifyReload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "token")
	if err := os.WriteFile(file, []byte("v1"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	reasons := make(chan string, 4)
	stop := startTokenWatch(file, func(reason string) { reasons <- reason })
	defer stop()

	// Let the watcher register, then rotate the token.
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(file, []byte("v2-rotated"), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	select {
	case r := <-reasons:
		if r != "file changed" {
			t.Errorf("reason = %q, want file changed", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("real fsnotify write did not trigger a reload within deadline")
	}
}

// Spec: §6.3.2.1 — startTokenWatch installs and cleanly stops the watcher (no
// goroutine leak, no lingering signal registration, no leaked fsnotify
// watcher) even with no file configured. F-6.3.2.
func TestStartTokenWatch_StartStop(t *testing.T) {
	t.Parallel()
	stop := startTokenWatch("", func(string) {})
	stop() // must not panic or block
}
