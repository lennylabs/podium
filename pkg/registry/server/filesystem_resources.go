package server

import (
	"io/fs"
	"os"
	"path/filepath"
)

// dirFS is a minimal os.DirFS replacement that the ingest pipeline
// consumes via fs.FS. We use it instead of os.DirFS directly so future
// changes (root-relative path normalization, symlink restrictions) are
// applied consistently.
type dirFS struct{ root string }

func newDirFS(root string) fs.FS { return dirFS{root: root} }

// Open implements fs.FS.
func (d dirFS) Open(name string) (fs.File, error) {
	return os.Open(filepath.Join(d.root, filepath.FromSlash(name)))
}

// ReadDir implements fs.ReadDirFS so fs.WalkDir can iterate efficiently.
func (d dirFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return os.ReadDir(filepath.Join(d.root, filepath.FromSlash(name)))
}

// Stat implements fs.StatFS.
func (d dirFS) Stat(name string) (fs.FileInfo, error) {
	return os.Stat(filepath.Join(d.root, filepath.FromSlash(name)))
}
