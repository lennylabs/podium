package server

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/lennylabs/podium/pkg/registry/filesystem"
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

// filesystemResourceFunc returns a ResourceFunc that reads bundled
// resources from the filesystem registry that produced this server.
//
// The artifactID maps to a directory under the artifact's originating
// layer. Stage 6 keeps it simple: walk the layers in reverse precedence
// order and return the first matching path.
func filesystemResourceFunc(reg *filesystem.Registry) ResourceFunc {
	return func(_ context.Context, artifactID, resourcePath string) ([]byte, bool) {
		for i := len(reg.Layers) - 1; i >= 0; i-- {
			full := filepath.Join(reg.Layers[i].Path,
				filepath.FromSlash(artifactID),
				filepath.FromSlash(resourcePath))
			if !strings.HasPrefix(filepath.Clean(full), filepath.Clean(reg.Layers[i].Path)) {
				continue
			}
			data, err := os.ReadFile(full)
			if err != nil {
				continue
			}
			return data, true
		}
		return nil, false
	}
}
