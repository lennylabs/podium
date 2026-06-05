package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// startTokenWatch installs the §6.3.2.1 rotation mechanisms beyond the
// per-call fresh read currentToken() already performs: a SIGHUP forced re-read
// and an fsnotify watch on PODIUM_SESSION_TOKEN_FILE. It returns a stop
// function the serve loop defers so the watcher goroutine, the fsnotify
// watcher, and the signal registration are all released when the bridge exits.
func (s *mcpServer) startTokenWatch() func() {
	return startTokenWatch(s.cfg.sessionTokenFile, func(reason string) {
		// Re-read the token so a rotation is observed promptly and surfaced to
		// the operator. currentToken() remains the authoritative per-call read;
		// this keeps the contract's two explicit mechanisms wired without
		// changing request behavior.
		_ = s.currentToken()
		fmt.Fprintf(os.Stderr, "podium-mcp: session token re-read (%s)\n", reason)
	})
}

// startTokenWatch wires the SIGHUP signal channel and an fsnotify watch on file
// into watchLoop and returns a stop function. file may be empty (no file
// watch); the SIGHUP handler is always installed where the platform supports
// it.
//
// §6.3.2.1 names fsnotify as the file-watch mechanism. The watcher observes the
// file's parent directory rather than the file node so an atomic
// rename-replace rotation (which swaps the inode the path points to, the common
// way a runtime rewrites a token with restrictive permissions) is still seen.
// If fsnotify cannot initialize, the file watch is skipped; the per-call fresh
// read and SIGHUP remain the rotation path, matching the contract's statement
// that the only obligation is to read fresh on every call.
func startTokenWatch(file string, reload func(reason string)) func() {
	sig := make(chan os.Signal, 1)
	if hs := hangupSignals(); len(hs) > 0 {
		signal.Notify(sig, hs...)
	}
	done := make(chan struct{})

	var watcher *fsnotify.Watcher
	var events <-chan fsnotify.Event
	var errs <-chan error
	if file != "" {
		dir := filepath.Dir(file)
		if w, err := fsnotify.NewWatcher(); err != nil {
			fmt.Fprintf(os.Stderr, "podium-mcp: cannot start fsnotify watcher (%v); relying on SIGHUP and per-call re-read for token rotation\n", err)
		} else if err := w.Add(dir); err != nil {
			_ = w.Close()
			fmt.Fprintf(os.Stderr, "podium-mcp: cannot watch session token directory %q (%v); relying on SIGHUP and per-call re-read for token rotation\n", dir, err)
		} else {
			watcher = w
			events = w.Events
			errs = w.Errors
		}
	}

	go watchLoop(file, sig, events, errs, reload, done)
	return func() {
		signal.Stop(sig)
		close(done)
		if watcher != nil {
			_ = watcher.Close()
		}
	}
}

// watchLoop fires reload on a SIGHUP-channel send and on an fsnotify event that
// affects file. It is the testable core of the watcher: tests drive it with a
// controlled signal channel and a controlled fsnotify event channel. Only
// events whose target basename matches file are acted on, so unrelated churn in
// the watched directory is ignored. A nil events/errs channel (no file watch
// wired) leaves the SIGHUP path active. The loop exits when done is closed.
func watchLoop(file string, sighup <-chan os.Signal, events <-chan fsnotify.Event, errs <-chan error, reload func(reason string), done <-chan struct{}) {
	base := filepath.Base(file)
	for {
		select {
		case <-sighup:
			reload("SIGHUP")
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if file == "" || filepath.Base(ev.Name) != base {
				continue
			}
			// A write, create, rename, or remove of the token path is a
			// rotation; the per-call fresh read picks up the new content on the
			// next request.
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) != 0 {
				reload("file changed")
			}
		case _, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			// A transient watcher error (for example a dropped event on an
			// overflowed queue) is non-fatal; keep watching. The per-call fresh
			// read reconciles any missed change.
		case <-done:
			return
		}
	}
}
