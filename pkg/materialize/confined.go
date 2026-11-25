package materialize

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnconfinedPath signals a path the stale-file cleanup or a sync state
// write refuses: it does not name a regular file reached below the destination
// root under the rule the operation applies. A directory, a symbolic link, or
// another non-regular file at the path is refused, and so is a link that
// resolves outside the root.
var ErrUnconfinedPath = errors.New("materialize: path is not a regular file confined to the destination")

// RemoveStale deletes the regular file at rel below destination and then
// prunes the directories that held it while they are empty. rel is the
// slash-separated path a sync lock recorded, and the lock can come from a
// repository another party pushes to.
//
// The removal accepts the links the writer accepts (§6.7): a directory
// component may be a symbolic link that resolves inside the root, so the
// output the writer put behind such a link is removed when its artifact
// leaves the view. A link that resolves outside the root, a link at the path
// itself, a .podium directory that is a link, and a path whose resolved form
// fails CheckRecordedPath are refused. A directory is never removed
// recursively. The pruning removes only the empty directories the path
// reaches below its last link, deepest first, so it stops at the first
// directory that is not empty, at the directory a link names, or at the root.
// A path that is already absent prunes the same way and returns nil, so a file
// the operator deleted by hand still lets its empty parents go.
//
// Spec: §6.7, §7.5.3
func RemoveStale(destination, rel string) error {
	sp, err := openStale(destination, rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer sp.close()
	err = checkConfinedFile(sp.root, rel, sp.segs)
	switch {
	case err == nil:
		if rerr := sp.root.Remove(filepath.Join(sp.segs...)); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return fmt.Errorf("materialize: remove %s: %w", rel, rerr)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	pruneEmptyDirs(sp.root, sp.segs[:len(sp.segs)-1], sp.keep)
	return nil
}

// ReadStale returns the bytes of the regular file at rel below destination,
// under the rule RemoveStale applies. An absent path returns an error that
// wraps fs.ErrNotExist, and a refused path returns an error that wraps
// ErrUnconfinedPath or ErrUnsafeRecordedPath, so a recorded path cannot direct
// a read at a device, a FIFO, or a file outside the destination.
func ReadStale(destination, rel string) ([]byte, error) {
	sp, err := openStale(destination, rel)
	if err != nil {
		return nil, err
	}
	defer sp.close()
	if err := checkConfinedFile(sp.root, rel, sp.segs); err != nil {
		return nil, err
	}
	data, err := sp.root.ReadFile(filepath.Join(sp.segs...))
	if err != nil {
		return nil, fmt.Errorf("materialize: read %s: %w", rel, err)
	}
	return data, nil
}

// RewriteStale replaces the content of the regular file at rel below
// destination with rewrite applied to its current content, under the rule
// RemoveStale applies. It is the in-place reconciliation of a §6.7
// config-merge or inject file whose last contributing artifact is gone. An
// absent path is a no-op that returns nil.
func RewriteStale(destination, rel string, rewrite func([]byte) []byte) error {
	sp, err := openStale(destination, rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer sp.close()
	if err := checkConfinedFile(sp.root, rel, sp.segs); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	name := filepath.Join(sp.segs...)
	data, err := sp.root.ReadFile(name)
	if err != nil {
		return fmt.Errorf("materialize: read %s: %w", rel, err)
	}
	if err := sp.root.WriteFile(name, rewrite(data), 0o644); err != nil {
		return fmt.Errorf("materialize: write %s: %w", rel, err)
	}
	return nil
}

// stalePath is a recorded path resolved for the stale-file cleanup.
type stalePath struct {
	// root is the destination opened as an os.Root. Every operation goes
	// through it, so a component swapped for a link after the resolution
	// cannot carry the operation outside the destination.
	root *os.Root
	// segs is the path below the root with every directory link resolved.
	// The operations check it again with checkConfinedFile, which refuses any
	// link, so a link planted after the resolution fails the operation.
	segs []string
	// keep is the number of leading directories of segs the pruning leaves in
	// place: the directories up to and including the one the last link names.
	keep int
}

func (sp *stalePath) close() { _ = sp.root.Close() }

// openStale checks rel with CheckRecordedPath, resolves its directory
// components through the links that stay inside the destination, and checks
// the resolved path again. The resolution uses filepath.EvalSymlinks against
// the symlink-resolved root, as the writer's checkSymlinkContainment does, so
// the writer and the cleanup accept the same links. A missing directory
// returns an error wrapping fs.ErrNotExist.
func openStale(destination, rel string) (*stalePath, error) {
	if err := CheckRecordedPath(rel); err != nil {
		return nil, err
	}
	root, segs, err := openConfinedRoot(destination, rel)
	if err != nil {
		return nil, err
	}
	sp, err := resolveStale(root, destination, rel, segs)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	return sp, nil
}

// resolveStale resolves the directory components of rel. See openStale.
func resolveStale(root *os.Root, destination, rel string, segs []string) (*stalePath, error) {
	// filepath.Abs fails only when the working directory cannot be read, and
	// openConfinedRoot already resolved the same destination.
	absDest, err := filepath.Abs(destination)
	if err != nil {
		return nil, err
	}
	realDest, err := filepath.EvalSymlinks(absDest)
	if err != nil {
		return nil, fmt.Errorf("materialize: resolve %s: %w", absDest, err)
	}
	dirs := segs[:len(segs)-1]
	lastLink := -1
	for i := range dirs {
		info, err := root.Lstat(filepath.Join(dirs[:i+1]...))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("materialize: inspect %s: %w", rel, err)
			}
			return nil, fmt.Errorf("%w: %q: %v", ErrUnconfinedPath, rel, err)
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			continue
		}
		if strings.EqualFold(dirs[i], ".podium") {
			return nil, fmt.Errorf("%w: %q: .podium is a symbolic link", ErrUnconfinedPath, rel)
		}
		lastLink = i
	}
	resolvedDir, err := filepath.EvalSymlinks(filepath.Join(realDest, filepath.Join(dirs...)))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("materialize: resolve %s: %w", rel, err)
		}
		return nil, fmt.Errorf("%w: %q: %v", ErrUnconfinedPath, rel, err)
	}
	if !withinRoot(realDest, resolvedDir) {
		return nil, fmt.Errorf("%w: %q resolves outside the destination", ErrUnconfinedPath, rel)
	}
	relDir, err := filepath.Rel(realDest, resolvedDir)
	if err != nil {
		// Unreachable: both paths are absolute and withinRoot placed
		// resolvedDir under realDest. Kept so a future change fails closed.
		return nil, fmt.Errorf("%w: %q: %v", ErrUnconfinedPath, rel, err)
	}
	resolved := segs[len(segs)-1]
	if relDir != "." {
		resolved = filepath.ToSlash(relDir) + "/" + resolved
	}
	if err := CheckRecordedPath(resolved); err != nil {
		return nil, fmt.Errorf("%w: %q resolves to %q: %v", ErrUnconfinedPath, rel, resolved, err)
	}
	rsegs := strings.Split(resolved, "/")
	// The directories after the last link are real directories the path
	// itself created; the pruning stops above them.
	realDirs := len(dirs) - (lastLink + 1)
	return &stalePath{root: root, segs: rsegs, keep: len(rsegs) - 1 - realDirs}, nil
}

// openConfinedFile opens destination as a root and checks that rel names a
// regular file reached with no symbolic link at any component below the root.
// It returns the open root and the platform form of rel for the caller's
// operation; the caller closes the root.
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
// is replaced by a symbolic link between a check and the operation. A
// destination that does not exist returns an error wrapping fs.ErrNotExist.
func openConfinedRoot(destination, rel string) (*os.Root, []string, error) {
	if destination == "" {
		return nil, nil, ErrEmptyDestination
	}
	segs, err := confinedSegments(rel)
	if err != nil {
		return nil, nil, err
	}
	// filepath.Abs fails only when the working directory cannot be read. The
	// writer's lexical check cannot fail after confinedSegments refused every
	// "..", "", and absolute form; it is kept so the two checks cannot drift.
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
// segment.
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

// pruneEmptyDirs removes the directory named by dirs and each ancestor above
// it, deepest first, leaving the first keep directories and the root in
// place. Root.Remove deletes only an empty directory, so the walk halts at the
// first ancestor that still holds an operator file or a sibling artifact's
// output, and a component that is missing or is a symbolic link also halts it.
func pruneEmptyDirs(root *os.Root, dirs []string, keep int) {
	for n := len(dirs); n > keep; n-- {
		name := filepath.Join(dirs[:n]...)
		info, err := root.Lstat(name)
		if err != nil || !info.IsDir() {
			return
		}
		if err := root.Remove(name); err != nil {
			return
		}
	}
}
