package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/lennylabs/podium/pkg/overlay"
	synccfg "github.com/lennylabs/podium/pkg/sync"
)

// overlayWatchInterval is the cadence at which the watcher re-checks the
// resolved overlay path. §6.4 step 2 lets the path be established or changed
// at runtime via the roots/list reply, a transition that carries no
// filesystem event, so the path is re-read on this interval. Content changes
// under an established path are detected by fsnotify (§6.4: "The MCP server
// watches the resolved path via fsnotify and re-indexes on change"); this
// interval also drives the polling fallback used when fsnotify cannot
// initialize.
const overlayWatchInterval = 500 * time.Millisecond

// overlayWatchDebounce coalesces a burst of filesystem events into a single
// re-index, mirroring the `podium sync --watch` debounce (pkg/sync).
const overlayWatchDebounce = 200 * time.Millisecond

// startOverlayWatch installs the §6.4 overlay watcher: it watches the
// resolved overlay path via fsnotify and re-resolves the records whenever the
// tree (or the path itself) changes, atomically swapping s.overlay so request
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
		s.setOverlay(nil, nil)
		return
	}
	records, domains, err := resolveOverlayAll(path)
	if err != nil {
		if !errors.Is(err, overlay.ErrNoOverlay) {
			fmt.Fprintf(os.Stderr, "WARN: workspace overlay path %q became unavailable (%v); overlay disabled\n", path, err)
		} else {
			fmt.Fprintf(os.Stderr, "WARN: workspace overlay path %q no longer exists; overlay disabled\n", path)
		}
		s.setOverlay(nil, nil)
		return
	}
	s.setOverlay(records, domains)
}

// startOverlayWatch wires the testable watch loop and returns a stop function.
// pathOf is consulted each tick so a path established after startup (via the
// roots/list reply) is picked up; reindex is invoked whenever the path or its
// content changes.
func startOverlayWatch(interval time.Duration, pathOf func() string, reindex func(path string)) func() {
	done := make(chan struct{})
	go overlayWatchLoop(interval, pathOf, reindex, done)
	return func() { close(done) }
}

// overlayWatchLoop is the testable core of the overlay watcher. It watches the
// resolved overlay path via fsnotify (§6.4) and fires reindex, debounced to
// overlayWatchDebounce, on every change under the tree. The resolved path is
// re-read on each tick of interval so a path established or changed after
// startup (roots/list) re-targets the watcher and triggers an initial
// reindex. When fsnotify cannot initialize for the current path, it falls
// back to a content-fingerprint poll on the same interval, mirroring
// pkg/sync/watch.go. It exits when done is closed.
func overlayWatchLoop(interval time.Duration, pathOf func() string, reindex func(path string), done <-chan struct{}) {
	if interval <= 0 {
		interval = overlayWatchInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	w := &overlayContentWatch{}
	defer w.close()

	// Watch whatever path is resolved at startup. A path present at startup is
	// watched but not reindexed (the serve loop already loaded it once); only
	// a later change fires reindex.
	curPath := pathOf()
	w.retarget(curPath)
	lastSig := synccfg.PathSignature(curPath)

	var (
		debTimer *time.Timer
		debC     <-chan time.Time
	)
	arm := func() {
		if debTimer == nil {
			debTimer = time.NewTimer(overlayWatchDebounce)
		} else {
			if !debTimer.Stop() {
				select {
				case <-debTimer.C:
				default:
				}
			}
			debTimer.Reset(overlayWatchDebounce)
		}
		debC = debTimer.C
	}
	defer func() {
		if debTimer != nil {
			debTimer.Stop()
		}
	}()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			path := pathOf()
			if path != curPath {
				// A path established or changed via roots/list. Re-target the
				// fsnotify watch and reindex against the new path. Drop any
				// pending debounce for the old path.
				curPath = path
				w.retarget(path)
				lastSig = synccfg.PathSignature(path)
				debC = nil
				reindex(path)
				continue
			}
			// fsnotify-unavailable fallback: poll the content fingerprint so
			// the behavioral contract ("re-indexes on change") still holds.
			if !w.fsnotifyActive() {
				sig := synccfg.PathSignature(path)
				if sig != lastSig {
					lastSig = sig
					reindex(path)
				}
			}
		case ev, ok := <-w.events():
			if !ok {
				continue
			}
			// A newly created directory must itself be watched because
			// fsnotify is not recursive; without this, edits inside a freshly
			// added overlay package would go unobserved.
			if ev.Op&fsnotify.Create != 0 {
				if info, serr := os.Stat(ev.Name); serr == nil && info.IsDir() {
					w.addTree(ev.Name)
				}
			}
			arm()
		case _, ok := <-w.errs():
			if !ok {
				continue
			}
			// A transient fsnotify error (for example a dropped event on an
			// overflowed queue) is non-fatal; keep watching. The next event
			// rearms the debounce and a full reindex reconciles any miss.
		case <-debC:
			debC = nil
			reindex(curPath)
		}
	}
}

// overlayContentWatch wraps a recursive fsnotify watcher (pkg/sync.TreeWatcher)
// over the current overlay path. When fsnotify cannot initialize, fsw is nil
// and the watch loop falls back to fingerprint polling. The zero value is an
// inactive watch.
type overlayContentWatch struct {
	fsw *synccfg.TreeWatcher
}

// retarget closes any existing fsnotify watch and opens a fresh one over path.
// An empty path leaves the watch inactive; an fsnotify init failure leaves it
// inactive so the caller polls instead.
func (w *overlayContentWatch) retarget(path string) {
	w.close()
	if path == "" {
		return
	}
	if tw, err := synccfg.NewTreeWatcher(path); err == nil {
		w.fsw = tw
	}
}

func (w *overlayContentWatch) fsnotifyActive() bool { return w.fsw != nil }

// events returns the fsnotify event channel, or nil when no watch is active.
// A nil channel parks the corresponding select case, which is the intended
// behavior on the polling fallback.
func (w *overlayContentWatch) events() <-chan fsnotify.Event {
	if w.fsw == nil {
		return nil
	}
	return w.fsw.Events()
}

func (w *overlayContentWatch) errs() <-chan error {
	if w.fsw == nil {
		return nil
	}
	return w.fsw.Errors()
}

func (w *overlayContentWatch) addTree(path string) {
	if w.fsw != nil {
		w.fsw.AddTree(path)
	}
}

func (w *overlayContentWatch) close() {
	if w.fsw != nil {
		_ = w.fsw.Close()
		w.fsw = nil
	}
}
