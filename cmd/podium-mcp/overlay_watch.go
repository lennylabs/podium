package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/lennylabs/podium/pkg/overlay"
	synccfg "github.com/lennylabs/podium/pkg/sync"
)

// overlayWatchInterval is the poll cadence for the §6.4 workspace overlay.
// Detection is poll-based, matching the session-token watch and
// `podium sync --watch` (the project carries no fsnotify dependency). §6.4
// names fsnotify, but the behavioral contract it states is "re-indexes on
// change", which the poller satisfies.
const overlayWatchInterval = 500 * time.Millisecond

// startOverlayWatch installs the §6.4.1 overlay watcher: it polls the
// resolved overlay path and re-resolves the records whenever the tree (or
// the path itself) changes, atomically swapping s.overlay so request
// handlers see the new set on the next call. It returns a stop function the
// serve loop defers so the watcher goroutine is released on exit.
func (s *mcpServer) startOverlayWatch() func() {
	return startOverlayWatch(overlayWatchInterval, s.overlayPath, s.reindexOverlay)
}

// reindexOverlay re-resolves the overlay at path and swaps it into the
// server. An empty path (overlay disabled) or a path that resolves to
// ErrNoOverlay clears the overlay. A path that became unresolvable mid-session
// (removed directory, permission change) warns once per transition per the
// §6.9 "Workspace overlay path missing" failure mode, then disables the layer.
func (s *mcpServer) reindexOverlay(path string) {
	if path == "" {
		s.setOverlay(nil)
		return
	}
	records, err := overlay.Filesystem{Path: path}.Resolve(nil)
	if err != nil {
		if !errors.Is(err, overlay.ErrNoOverlay) {
			fmt.Fprintf(os.Stderr, "WARN: workspace overlay path %q became unavailable (%v); overlay disabled\n", path, err)
		} else {
			fmt.Fprintf(os.Stderr, "WARN: workspace overlay path %q no longer exists; overlay disabled\n", path)
		}
		s.setOverlay(nil)
		return
	}
	s.setOverlay(records)
}

// startOverlayWatch wires the testable poll loop and returns a stop function.
// pathOf is consulted every tick so a path established after startup (via the
// roots/list reply) is picked up; reindex is invoked whenever the path or its
// content fingerprint changes.
func startOverlayWatch(interval time.Duration, pathOf func() string, reindex func(path string)) func() {
	done := make(chan struct{})
	go overlayWatchLoop(interval, pathOf, reindex, done)
	return func() { close(done) }
}

// overlayWatchLoop is the testable core of the overlay watcher. It fires
// reindex on the first observed change to either the resolved path or the
// content fingerprint of the watched tree, debounced implicitly to the poll
// interval. It exits when done is closed.
func overlayWatchLoop(interval time.Duration, pathOf func() string, reindex func(path string), done <-chan struct{}) {
	if interval <= 0 {
		interval = overlayWatchInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	lastPath := pathOf()
	lastSig := synccfg.PathSignature(lastPath)
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			path := pathOf()
			sig := synccfg.PathSignature(path)
			if path != lastPath || sig != lastSig {
				lastPath, lastSig = path, sig
				reindex(path)
			}
		}
	}
}
