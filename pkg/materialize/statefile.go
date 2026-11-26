package materialize

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ReadStateFile returns the bytes of a file sync keeps for itself below
// destination, such as .podium/sync.lock. A target can be a repository
// checkout whose content another party commits (§7.8), so a .podium directory
// that is a symbolic link is refused, and a link at the file itself is
// followed only when it resolves to a regular file inside the destination
// whose path passes the recorded-path rule, or to another regular file inside
// .podium/ (checkStateLink); a link that resolves outside or into .git is
// refused. The root itself may be a link. An absent file returns an error
// wrapping fs.ErrNotExist.
//
// Spec: §7.5.3, §7.8
func ReadStateFile(destination, rel string) ([]byte, error) {
	rp, err := openResolved(destination, rel, resolveOpts{followLeaf: true})
	if err != nil {
		return nil, err
	}
	defer rp.close()
	return rp.read(rel)
}

// WriteStateFile writes data to a file sync keeps for itself below
// destination, atomically through "<rel>.tmp" and a rename. It creates
// destination and the missing directories of rel, and refuses a directory
// component that is a symbolic link. The temporary is created exclusively, so
// a temporary name that exists as a link is never followed; a leftover regular
// temporary is removed first, and any other file at the temporary name
// refuses the write. The rename replaces a link at rel itself rather than
// writing through it.
//
// Spec: §7.5.3, §7.8
func WriteStateFile(destination, rel string, data []byte, perm os.FileMode) error {
	if destination == "" {
		return ErrEmptyDestination
	}
	segs, err := confinedSegments(rel)
	if err != nil {
		return err
	}
	absDest, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(absDest, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(absDest)
	if err != nil {
		return fmt.Errorf("materialize: open %s: %w", absDest, err)
	}
	defer func() { _ = root.Close() }()
	if err := mkdirConfined(root, rel, segs[:len(segs)-1]); err != nil {
		return err
	}
	name := filepath.Join(segs...)
	tmp := name + ".tmp"
	if err := stage(root, tmp, data, perm); err != nil {
		return err
	}
	if err := root.Rename(tmp, name); err != nil {
		_ = root.Remove(tmp)
		return fmt.Errorf("materialize: rename %s: %w", rel, err)
	}
	return nil
}

// CheckStateFile returns the refusal WriteStateFile would return for rel below
// destination, without writing anything: a directory component, .podium
// included, that is a symbolic link or not a directory, and a temporary name
// that exists and is not a regular file. A caller that writes other output
// before a state file, such as podium-mcp writing an artifact before its
// sandbox profile, calls it first, so a refused state file leaves the
// destination unchanged. An absent destination or directory passes, because
// WriteStateFile creates it.
//
// Spec: §6.6 step 5, §7.5.3
func CheckStateFile(destination, rel string) error {
	if destination == "" {
		return ErrEmptyDestination
	}
	segs, err := confinedSegments(rel)
	if err != nil {
		return err
	}
	absDest, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(absDest)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("materialize: open %s: %w", absDest, err)
	}
	defer func() { _ = root.Close() }()
	dirs := segs[:len(segs)-1]
	for i := range dirs {
		// Below an absent directory nothing exists yet to refuse.
		if absent, err := inspectStateDir(root, rel, dirs, i); absent || err != nil {
			return err
		}
	}
	_, err = inspectTemp(root, filepath.Join(segs...)+".tmp")
	return err
}

// mkdirConfined creates each missing directory of dirs below root and refuses
// a component that exists as a symbolic link or as a non-directory.
func mkdirConfined(root *os.Root, rel string, dirs []string) error {
	for i := range dirs {
		absent, err := inspectStateDir(root, rel, dirs, i)
		if err != nil {
			return err
		}
		if !absent {
			continue
		}
		name := filepath.Join(dirs[:i+1]...)
		if err := root.Mkdir(name, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("materialize: mkdir %s: %w", filepath.ToSlash(name), err)
		}
	}
	return nil
}

// inspectStateDir inspects the directory dirs[:i+1] below root on the way to
// rel. It reports whether the directory is absent, and refuses one that
// exists as a symbolic link or as a non-directory.
func inspectStateDir(root *os.Root, rel string, dirs []string, i int) (bool, error) {
	name := filepath.Join(dirs[:i+1]...)
	info, err := root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("%w: %q: %v", ErrUnconfinedPath, rel, err)
	case !info.IsDir() && i == 0 && strings.EqualFold(dirs[0], ".podium"):
		return false, podiumDirError(rel)
	case !info.IsDir():
		return false, fmt.Errorf("%w: %q: %s is a symbolic link or not a directory", ErrUnconfinedPath, rel, filepath.ToSlash(name))
	}
	return false, nil
}

// stageFS is the part of the filesystem a staging write uses. *os.Root
// satisfies it for a confined write, and hostFS for a write by absolute path.
type stageFS interface {
	Lstat(name string) (fs.FileInfo, error)
	Remove(name string) error
	OpenFile(name string, flag int, perm fs.FileMode) (*os.File, error)
}

// hostFS is stageFS over the host filesystem by absolute path.
type hostFS struct{}

func (hostFS) Lstat(name string) (fs.FileInfo, error) { return os.Lstat(name) }
func (hostFS) Remove(name string) error               { return os.Remove(name) }
func (hostFS) OpenFile(name string, flag int, perm fs.FileMode) (*os.File, error) {
	return os.OpenFile(name, flag, perm)
}

// inspectTemp inspects the temporary name tmp before a staging write. It
// reports whether a leftover regular file is there, and refuses any other
// file at tmp, a symbolic link included, with ErrUnconfinedPath.
func inspectTemp(fsys stageFS, tmp string) (bool, error) {
	info, err := fsys.Lstat(tmp)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("materialize: inspect %s: %w", tmp, err)
	case !info.Mode().IsRegular():
		return false, fmt.Errorf("%w: temporary %q exists and is not a regular file", ErrUnconfinedPath, filepath.ToSlash(tmp))
	}
	return true, nil
}

// stage writes content to the temporary tmp with an exclusive create. A
// temporary name can be committed to a repository checkout as a symbolic link
// to a file outside it; O_CREATE|O_EXCL fails on any existing name, a link
// included, so the write never follows one. A leftover regular file at tmp,
// from an interrupted earlier run, is removed first, which unlinks only that
// name; any other file at tmp refuses the write with ErrUnconfinedPath. A
// temporary that fails part-way is removed.
//
// Spec: §6.7, §7.8
func stage(fsys stageFS, tmp string, content []byte, mode os.FileMode) error {
	leftover, err := inspectTemp(fsys, tmp)
	if err != nil {
		return err
	}
	if leftover {
		if err := fsys.Remove(tmp); err != nil {
			return fmt.Errorf("materialize: remove leftover %s: %w", tmp, err)
		}
	}
	f, err := fsys.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("materialize: create %s: %w", tmp, err)
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		_ = fsys.Remove(tmp)
		return fmt.Errorf("materialize: write %s: %w", tmp, err)
	}
	// Closing a local regular file reports an error only on a filesystem that
	// defers write errors to close, such as NFS, which the test suite has no
	// way to provoke; the branch removes the temporary for that case.
	if err := f.Close(); err != nil {
		_ = fsys.Remove(tmp)
		return fmt.Errorf("materialize: close %s: %w", tmp, err)
	}
	return nil
}
