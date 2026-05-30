package main

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// Spec: §6.3.2.1 — the session-token file is watched and a change triggers
// a re-read. watchLoop is the testable core of the watcher. F-6.3.6.
func TestWatchLoop_FileChangeTriggersReload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := dir + "/token"
	if err := os.WriteFile(file, []byte("v1"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	reasons := make(chan string, 4)
	sighup := make(chan os.Signal, 1)
	done := make(chan struct{})
	defer close(done)
	go watchLoop(file, 10*time.Millisecond, sighup, func(reason string) { reasons <- reason }, done)

	// Give the watcher a moment to record the baseline mtime, then rotate.
	time.Sleep(30 * time.Millisecond)
	if err := os.WriteFile(file, []byte("v2-rotated"), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	select {
	case r := <-reasons:
		if r != "file changed" {
			t.Errorf("reason = %q, want file changed", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("file change did not trigger a reload within deadline")
	}
}

// Spec: §6.3.2.1 — SIGHUP triggers a forced re-read. The watcher's signal
// path is exercised with a controlled channel so the test does not signal
// the real process. F-6.3.6.
func TestWatchLoop_SIGHUPTriggersReload(t *testing.T) {
	t.Parallel()
	reasons := make(chan string, 4)
	sighup := make(chan os.Signal, 1)
	done := make(chan struct{})
	defer close(done)
	go watchLoop("", 0, sighup, func(reason string) { reasons <- reason }, done)

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

// Spec: §6.3.2.1 — startTokenWatch installs and cleanly stops the watcher
// (no goroutine leak, no lingering signal registration). F-6.3.6.
func TestStartTokenWatch_StartStop(t *testing.T) {
	t.Parallel()
	stop := startTokenWatch("", time.Second, func(string) {})
	stop() // must not panic or block
}
