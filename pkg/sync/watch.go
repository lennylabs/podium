package sync

import (
	"context"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// WatchOptions configures the long-running --watch mode (§7.5).
//
// Watch reruns Run whenever the registry path or overlay path
// changes. Detection is poll-based (no fsnotify dependency); the
// poller computes a content-fingerprint over each watched tree and
// reruns the sync when the fingerprint moves. Debounce coalesces
// bursts of edits into a single sync.
type WatchOptions struct {
	// Sync supplies the underlying Run options. RegistryPath is
	// always watched; Target receives the materialized output.
	Sync Options
	// OverlayPath, when non-empty, is watched alongside the
	// registry path so workspace overlay edits trigger a rerun.
	OverlayPath string
	// Period is the poll interval. Defaults to 500ms.
	Period time.Duration
	// Debounce delays a rerun until at least this duration has
	// passed since the last detected change. Defaults to 200ms.
	Debounce time.Duration
}

// WatchEvent reports one Run invocation triggered by Watch. Callers
// receive an event for the initial sync and for every subsequent
// rerun. Err is non-nil when the run itself failed; the watcher
// continues running.
type WatchEvent struct {
	Result *Result
	Err    error
}

// Watch runs an initial sync, then polls the registry and overlay
// paths and reruns Run on every change. Stops when ctx is canceled,
// returning ctx.Err() (typically context.Canceled).
//
// Each rerun emits one WatchEvent on the returned channel. The
// channel is closed when the watcher exits.
func Watch(ctx context.Context, opts WatchOptions) (<-chan WatchEvent, error) {
	if opts.Sync.RegistryPath == "" {
		return nil, ErrNoRegistry
	}
	if opts.Period <= 0 {
		opts.Period = 500 * time.Millisecond
	}
	if opts.Debounce <= 0 {
		opts.Debounce = 200 * time.Millisecond
	}

	events := make(chan WatchEvent, 4)
	go runWatch(ctx, opts, events)
	return events, nil
}

// runWatch is the watcher goroutine. It owns the events channel and
// closes it on exit.
func runWatch(ctx context.Context, opts WatchOptions, events chan<- WatchEvent) {
	defer close(events)

	emit := func(res *Result, err error) {
		select {
		case events <- WatchEvent{Result: res, Err: err}:
		case <-ctx.Done():
		}
	}

	// Initial sync.
	res, err := Run(opts.Sync)
	emit(res, err)
	lastSig := watchSignature(opts.Sync.RegistryPath, opts.OverlayPath)

	ticker := time.NewTicker(opts.Period)
	defer ticker.Stop()

	pending := false
	var pendingSince time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sig := watchSignature(opts.Sync.RegistryPath, opts.OverlayPath)
			if sig != lastSig {
				lastSig = sig
				pending = true
				pendingSince = time.Now()
			}
			if pending && time.Since(pendingSince) >= opts.Debounce {
				pending = false
				res, err := Run(opts.Sync)
				emit(res, err)
			}
		}
	}
}

// watchSignature returns a stable fingerprint of the watched paths.
// Two signatures are equal iff every (path, modtime, size) tuple
// matches across calls. Walks that fail (e.g., transient permission
// errors) contribute their error to the signature so callers see
// later success as a change.
func watchSignature(paths ...string) string {
	h := fnv.New64a()
	for _, p := range paths {
		if p == "" {
			continue
		}
		signOne(h, p)
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

func signOne(h interface{ Write([]byte) (int, error) }, root string) {
	type entry struct {
		path string
		mod  int64
		size int64
		dir  bool
	}
	entries := []entry{}
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			entries = append(entries, entry{path: path + ":" + err.Error()})
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			entries = append(entries, entry{path: path + ":" + ierr.Error()})
			return nil
		}
		entries = append(entries, entry{
			path: path, mod: info.ModTime().UnixNano(),
			size: info.Size(), dir: d.IsDir(),
		})
		return nil
	})
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	for _, e := range entries {
		fmt.Fprintf(stringWriter{h}, "%s\x00%d\x00%d\x00%t\n",
			e.path, e.mod, e.size, e.dir)
	}
	if walkErr != nil && !os.IsNotExist(walkErr) {
		fmt.Fprintf(stringWriter{h}, "walkerr:%s\n", walkErr.Error())
	}
}

type stringWriter struct {
	w interface{ Write([]byte) (int, error) }
}

func (s stringWriter) Write(p []byte) (int, error) { return s.w.Write(p) }
