package ingest

import (
	"bytes"
	"io"
	"io/fs"
	"sync"
)

// readOnceFS gives a cycle one read of each path of its snapshot. A `local`
// source exposes a live directory, so a second read of a path can return
// other bytes than the first, and a cycle that digested a file for the layer
// digest and then stored a later read would serve bytes the layer step never
// covered. Every file read, directory listing, and stat the cycle makes goes
// through this view, which answers a repeated request with the result of the
// first one.
//
// Spec: §4.7.11 (Snapshot reads)
type readOnceFS struct {
	fsys fs.FS

	// mu guards the three caches. Each holds the first result for a path,
	// error included, so a path that failed once fails every time.
	mu    sync.Mutex
	files map[string]readResult
	dirs  map[string]dirResult
	stats map[string]statResult
}

type readResult struct {
	data []byte
	err  error
}

type dirResult struct {
	entries []fs.DirEntry
	err     error
}

type statResult struct {
	info fs.FileInfo
	err  error
}

// readOnce wraps fsys. A view that is already a readOnceFS is returned as it
// is, so wrapping twice shares the caches.
func readOnce(fsys fs.FS) fs.FS {
	if r, ok := fsys.(*readOnceFS); ok {
		return r
	}
	return &readOnceFS{fsys: fsys, files: map[string]readResult{}, dirs: map[string]dirResult{}, stats: map[string]statResult{}}
}

// ReadFile returns a copy of the bytes of the first read of name, so a caller
// that edits the slice changes no later read.
func (r *readOnceFS) ReadFile(name string) ([]byte, error) {
	r.mu.Lock()
	res, ok := r.files[name]
	if !ok {
		data, err := fs.ReadFile(r.fsys, name)
		res = readResult{data: data, err: err}
		r.files[name] = res
	}
	r.mu.Unlock()
	if res.err != nil {
		return nil, res.err
	}
	return bytes.Clone(res.data), nil
}

// ReadDir returns the first listing of name.
func (r *readOnceFS) ReadDir(name string) ([]fs.DirEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, ok := r.dirs[name]
	if !ok {
		entries, err := fs.ReadDir(r.fsys, name)
		res = dirResult{entries: entries, err: err}
		r.dirs[name] = res
	}
	return append([]fs.DirEntry(nil), res.entries...), res.err
}

// Stat returns the first stat of name. A caller that checks a size against
// a limit checks the length of the bytes it read as well, because the file
// can change between the stat and the read.
func (r *readOnceFS) Stat(name string) (fs.FileInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, ok := r.stats[name]
	if !ok {
		info, err := fs.Stat(r.fsys, name)
		res = statResult{info: info, err: err}
		r.stats[name] = res
	}
	return res.info, res.err
}

// Open serves a directory from its first listing and a file from its first
// read.
func (r *readOnceFS) Open(name string) (fs.File, error) {
	info, err := r.Stat(name)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		entries, err := r.ReadDir(name)
		if err != nil {
			return nil, err
		}
		return &onceDir{info: info, entries: entries}, nil
	}
	data, err := r.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return &onceFile{info: sizedInfo{FileInfo: info, size: int64(len(data))}, r: bytes.NewReader(data)}, nil
}

// sizedInfo reports the length of the bytes a file serves.
type sizedInfo struct {
	fs.FileInfo
	size int64
}

func (s sizedInfo) Size() int64 { return s.size }

// onceFile is a file served from the bytes of its first read.
type onceFile struct {
	info fs.FileInfo
	r    *bytes.Reader
}

func (f *onceFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *onceFile) Read(p []byte) (int, error) { return f.r.Read(p) }
func (f *onceFile) Close() error               { return nil }

// onceDir is a directory served from its first listing.
type onceDir struct {
	info    fs.FileInfo
	entries []fs.DirEntry
	offset  int
}

func (d *onceDir) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *onceDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.info.Name(), Err: fs.ErrInvalid}
}
func (d *onceDir) Close() error { return nil }

// ReadDir follows the fs.ReadDirFile contract: n <= 0 returns the rest, and
// n > 0 returns at most n entries and io.EOF at the end.
func (d *onceDir) ReadDir(n int) ([]fs.DirEntry, error) {
	rest := d.entries[d.offset:]
	if n <= 0 {
		d.offset = len(d.entries)
		return rest, nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	if n > len(rest) {
		n = len(rest)
	}
	d.offset += n
	return rest[:n], nil
}
