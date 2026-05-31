package sync

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// treeWatcher wraps an fsnotify.Watcher to observe one or more directory
// trees recursively. fsnotify watches individual directories, so the watcher
// adds every existing subdirectory up front and adds newly created
// subdirectories as they appear, keeping the whole registry and overlay
// trees observed. spec: §13.11.4.
type treeWatcher struct {
	w *fsnotify.Watcher
}

// newTreeWatcher creates a recursive watcher over the given paths. An empty
// path is skipped. A path that does not exist contributes no watches; its
// parent directory is watched instead (when present) so the path's creation
// is detected. Returns an error only when fsnotify itself cannot initialize,
// which is the signal for the caller to fall back to polling.
func newTreeWatcher(paths ...string) (*treeWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	tw := &treeWatcher{w: w}
	for _, p := range paths {
		if p == "" {
			continue
		}
		tw.addTree(p)
	}
	return tw, nil
}

// addTree adds root and every subdirectory under it to the watcher. A missing
// root is tolerated: its parent directory is watched (when it exists) so the
// root's later creation surfaces as an event.
func (t *treeWatcher) addTree(root string) {
	info, err := os.Stat(root)
	if err != nil {
		_ = t.w.Add(filepath.Dir(root))
		return
	}
	if !info.IsDir() {
		_ = t.w.Add(filepath.Dir(root))
		return
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		if d.IsDir() {
			_ = t.w.Add(path)
		}
		return nil
	})
}

// Close releases the underlying fsnotify watcher.
func (t *treeWatcher) Close() error { return t.w.Close() }

// runFSNotifyWatch runs an initial sync, then reruns Run whenever fsnotify
// reports a change under the watched trees. A debounce timer coalesces a
// burst of edits into a single rerun. The watcher is already registered
// before the initial sync, so edits made in response to the first event are
// queued by fsnotify and folded into the next rerun rather than lost.
func runFSNotifyWatch(ctx context.Context, opts WatchOptions, tw *treeWatcher, emit func(*Result, error)) {
	res, err := Run(opts.Sync)
	emit(res, err)

	// timer is armed on the first change after a rerun and reset on every
	// subsequent change; a rerun fires when it elapses with no further edit.
	var timer *time.Timer
	var timerC <-chan time.Time
	arm := func() {
		if timer == nil {
			timer = time.NewTimer(opts.Debounce)
		} else {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(opts.Debounce)
		}
		timerC = timer.C
	}
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-tw.w.Events:
			if !ok {
				return
			}
			// A newly created directory must itself be watched because
			// fsnotify is not recursive; without this, edits inside a
			// freshly added artifact package would go unobserved.
			if ev.Op&fsnotify.Create != 0 {
				if info, serr := os.Stat(ev.Name); serr == nil && info.IsDir() {
					tw.addTree(ev.Name)
				}
			}
			arm()
		case _, ok := <-tw.w.Errors:
			if !ok {
				return
			}
			// A transient watcher error (for example a dropped event on an
			// overflowed queue) is non-fatal; keep watching. The next edit
			// rearms the debounce and a full rerun reconciles any missed
			// change.
		case <-timerC:
			timerC = nil
			res, err := Run(opts.Sync)
			emit(res, err)
		}
	}
}
