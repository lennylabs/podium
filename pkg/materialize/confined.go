package materialize

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnconfinedPath signals a path the stale-file cleanup refuses to remove,
// read, or rewrite: it is not a regular file reached below the destination
// root through directories alone. A symbolic link at any component below the
// root, a directory at the path, or another non-regular file at the path is
// refused. The destination root itself may be a symbolic link.
var ErrUnconfinedPath = errors.New("materialize: path is not a regular file reached without a symbolic link")

// RemoveStale deletes the regular file at rel below destination and then
// prunes the directories that held it while they are empty. rel is the
// slash-separated path a sync lock recorded. The lock can come from a
// repository another party pushes to, so the removal is confined: the file
// must be reached with no symbolic link at any component below the root, a
// directory is never removed recursively, and the pruning stops at the first
// directory that is not empty, at a symbolic link, or at the root. A path that
// is already absent prunes the same way and returns nil, so a file the
// operator deleted by hand still lets its empty parents go.
//
// Spec: §6.7 (no writes outside the materialization destination), §7.5.3 (the
// stale-file cleanup of recorded paths).
func RemoveStale(destination, rel string) error {
	root, segs, err := openConfinedRoot(destination, rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer func() { _ = root.Close() }()
	err = checkConfinedFile(root, rel, segs)
	switch {
	case err == nil:
		if rerr := root.Remove(filepath.Join(segs...)); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return fmt.Errorf("materialize: remove %s: %w", rel, rerr)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	pruneEmptyDirs(root, segs[:len(segs)-1])
	return nil
}

// ReadConfined returns the bytes of the regular file at rel below destination,
// under the confinement RemoveStale applies. An absent path returns an error
// that wraps fs.ErrNotExist, and a path that is not a regular file reached
// without a symbolic link returns an error that wraps ErrUnconfinedPath, so a
// recorded path cannot direct a read at a device, a FIFO, or a file outside
// the destination.
func ReadConfined(destination, rel string) ([]byte, error) {
	root, name, err := openConfinedFile(destination, rel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("materialize: read %s: %w", rel, err)
	}
	return data, nil
}

// RewriteConfined replaces the content of the regular file at rel below
// destination with rewrite applied to its current content, under the
// confinement RemoveStale applies. It is the in-place reconciliation of a
// §6.7 config-merge or inject file whose last contributing artifact is gone.
// An absent path is a no-op that returns nil.
func RewriteConfined(destination, rel string, rewrite func([]byte) []byte) error {
	root, name, err := openConfinedFile(destination, rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile(name)
	if err != nil {
		return fmt.Errorf("materialize: read %s: %w", rel, err)
	}
	if err := root.WriteFile(name, rewrite(data), 0o644); err != nil {
		return fmt.Errorf("materialize: write %s: %w", rel, err)
	}
	return nil
}

// openConfinedFile opens destination as a root and checks that rel names a
// regular file reached without a symbolic link. It returns the open root and
// the platform form of rel for the caller's operation; the caller closes the
// root.
func openConfinedFile(destination, rel string) (*os.Root, string, error) {
	root, segs, err := openConfinedRoot(destination, rel)
	if err != nil {
		return nil, "", err
	}
	if err := checkConfinedFile(root, rel, segs); err != nil {
		_ = root.Close()
		return nil, "", err
	}
	return root, filepath.Join(segs...), nil
}

// openConfinedRoot splits rel into its segments, checks it lexically with the
// writer's resolveSandboxedPath, and opens destination as an os.Root. The root
// confines every later operation to destination, including when a component
// is replaced by a symbolic link between the check and the operation. A
// destination that does not exist returns an error wrapping fs.ErrNotExist.
func openConfinedRoot(destination, rel string) (*os.Root, []string, error) {
	if destination == "" {
		return nil, nil, ErrEmptyDestination
	}
	segs, err := confinedSegments(rel)
	if err != nil {
		return nil, nil, err
	}
	absDest, err := filepath.Abs(destination)
	if err != nil {
		return nil, nil, err
	}
	if _, err := resolveSandboxedPath(absDest, filepath.Join(segs...)); err != nil {
		return nil, nil, err
	}
	root, err := os.OpenRoot(absDest)
	if err != nil {
		return nil, nil, fmt.Errorf("materialize: open %s: %w", absDest, err)
	}
	return root, segs, nil
}

// confinedSegments splits a slash-separated relative path into its segments
// and refuses an empty path, an absolute path, and an empty, ".", or ".."
// segment. The sync lock reader applies the full recorded-path rule before
// this point; the check here keeps the primitive safe for any caller.
func confinedSegments(rel string) ([]string, error) {
	if rel == "" || strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) {
		return nil, fmt.Errorf("%w: %q is not a relative path", ErrOutOfDestination, rel)
	}
	segs := strings.Split(rel, "/")
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return nil, fmt.Errorf("%w: %q has an empty, \".\", or \"..\" segment", ErrOutOfDestination, rel)
		}
	}
	return segs, nil
}

// checkConfinedFile confirms that each component of rel below root is a
// directory that is not a symbolic link and that rel itself is a regular file.
// It inspects the components from the root down with Lstat, so no component
// is resolved through a link the walk has not already refused. A missing
// component returns an error wrapping fs.ErrNotExist.
func checkConfinedFile(root *os.Root, rel string, segs []string) error {
	for i := range segs {
		name := filepath.Join(segs[:i+1]...)
		info, err := root.Lstat(name)
		if err != nil {
			return fmt.Errorf("materialize: inspect %s: %w", rel, err)
		}
		last := i == len(segs)-1
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("%w: %q: %s is a symbolic link", ErrUnconfinedPath, rel, filepath.ToSlash(name))
		case !last && !info.IsDir():
			return fmt.Errorf("%w: %q: %s is not a directory", ErrUnconfinedPath, rel, filepath.ToSlash(name))
		case last && !info.Mode().IsRegular():
			return fmt.Errorf("%w: %q is not a regular file", ErrUnconfinedPath, rel)
		}
	}
	return nil
}

// pruneEmptyDirs removes the directory named by segs and each ancestor above
// it, deepest first, stopping before the root. Root.Remove deletes only an
// empty directory, so the walk halts at the first ancestor that still holds an
// operator file or a sibling artifact's output, and a component that is
// missing or is a symbolic link also halts it.
func pruneEmptyDirs(root *os.Root, segs []string) {
	for n := len(segs); n > 0; n-- {
		name := filepath.Join(segs[:n]...)
		info, err := root.Lstat(name)
		if err != nil || !info.IsDir() {
			return
		}
		if err := root.Remove(name); err != nil {
			return
		}
	}
}
