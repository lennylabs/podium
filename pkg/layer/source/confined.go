package source

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ConfinedFS returns a read-only tree rooted at root that refuses any path
// resolving outside it. os.DirFS validates a path string and confines no
// resolution, so a symbolic link stored inside the tree reads its target
// wherever that target lives; every ingest walk treats such an entry as an
// ordinary bundled file and reads it through. A relative link resolving inside
// the tree still reads; os.Root refuses a link whose target is absolute
// whatever that target names, because it resolves a target from the root
// rather than from the filesystem, so an absolute link inside the tree stops
// being readable here. Every failure other than fs.ErrNotExist is returned
// wrapping ErrSourceUnreachable, which is what classifies a refusal by the time
// it reaches the reingest endpoint; an absent path keeps fs.ErrNotExist
// unchanged, because the ingest's SKILL.md read distinguishes a file that is
// not there from one it may not read.
//
// Spec: §7.3.1 (local-source ingest confinement)
func ConfinedFS(root string) fs.FS { return rootFS{root: root} }

// rootFS satisfies fs.StatFS, fs.ReadDirFS, and fs.ReadFileFS, which are the
// interfaces the ingest walks take. A symlinked root is admitted: os.OpenInRoot
// resolves its own root argument normally and confines only what lies beneath.
type rootFS struct{ root string }

// classify discriminates on the one condition Go exports for this boundary. An
// os.OpenInRoot refusal wraps the unexported errPathEscapes, which satisfies
// neither fs.ErrNotExist, nor fs.ErrPermission, nor os.ErrInvalid, so no
// errors.Is test isolates it. An absent path is returned unchanged so the
// ingest's required-file reads keep distinguishing it; every other failure, a
// confinement refusal and an in-root permission failure alike, carries
// ErrSourceUnreachable. The message names the path relative to the root, so no
// host path reaches a client.
//
// Spec: §7.3.1, §6.10 (ingest.source_unreachable)
func classify(op, name string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return fmt.Errorf("%w: %s %s: refused", ErrSourceUnreachable, op, name)
}

// open resolves one name inside the root. The name is an fs path, so it is
// converted to the host separator before os.OpenInRoot sees it. A name fs.FS
// rejects is classified like every other failure rather than returned raw: the
// rooted and dot-dot names it covers are the population os.OpenInRoot would
// itself refuse as an escape, and an unclassified error reaches the reingest
// endpoint's default arm as registry.unavailable rather than
// ingest.source_unreachable.
func (f rootFS) open(op, name string) (*os.File, error) {
	if !fs.ValidPath(name) {
		return nil, classify(op, name, &fs.PathError{Op: op, Path: name, Err: fs.ErrInvalid})
	}
	file, err := os.OpenInRoot(f.root, filepath.FromSlash(name))
	if err != nil {
		return nil, classify(op, name, err)
	}
	return file, nil
}

// Open implements fs.FS. A directory is returned as a value satisfying
// fs.ReadDirFile, which is what fs.WalkDir needs.
func (f rootFS) Open(name string) (fs.File, error) {
	file, err := f.open("open", name)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// Stat implements fs.StatFS.
func (f rootFS) Stat(name string) (fs.FileInfo, error) {
	file, err := f.open("stat", name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	// A fstat on a descriptor the open just returned fails only where the
	// descriptor was revoked between the two calls, which no test can
	// arrange; the guard classifies it rather than returning it raw.
	info, err := file.Stat()
	if err != nil {
		return nil, classify("stat", name, err)
	}
	return info, nil
}

// ReadDir implements fs.ReadDirFS so fs.WalkDir iterates without opening each
// entry twice.
func (f rootFS) ReadDir(name string) ([]fs.DirEntry, error) {
	file, err := f.open("readdir", name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	// The same holds for the directory read: the open succeeded, so a
	// failure here is a revoked descriptor or an I/O fault.
	entries, err := file.ReadDir(-1)
	if err != nil {
		return nil, classify("readdir", name, err)
	}
	// fs.ReadDir promises entries sorted by filename, and a ReadDirFS
	// implementation answers that call directly, so the sort is this
	// method's to do.
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})
	return entries, nil
}

// ReadFile implements fs.ReadFileFS.
func (f rootFS) ReadFile(name string) ([]byte, error) {
	file, err := f.open("read", name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, classify("read", name, err)
	}
	return data, nil
}
