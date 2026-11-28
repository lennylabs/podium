//go:build unix

package sync

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// MachineConfigDir returns the directory of the machine file on Unix.
//
// Spec: §4.7.11 (Machine file)
func MachineConfigDir() (string, error) { return "/etc/podium", nil }

// maxLinkHops bounds the symbolic links one resolution follows, as the
// kernel's own limit does.
const maxLinkHops = 40

// openMachineFile resolves dir/sync.yaml component by component and checks
// each directory of the resolved path, each symbolic link on the way, and
// the directory that holds each link: root or a trusted user owns it, and
// no other user can write it, where a group write bit counts as such a
// write unless the group is gid 0. It then opens the resolved file without
// following a final link and checks the owner and the mode of the open
// descriptor. The check reads no POSIX ACL. A nil file and no error mean
// the file is absent.
//
// Spec: §4.7.11 (Machine file, Check on Unix)
func openMachineFile(dir string, opts MachineFileOptions) (*os.File, string, error) {
	path := filepath.Join(dir, MachineConfigName)
	// An absent file raises nothing, whatever the directories on the way.
	if _, err := os.Stat(path); absentFile(err) {
		return nil, path, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, path, unsafeFile(path, "resolve: %v", err)
	}
	resolved, err := resolveChecked(abs, opts, 0)
	if absentFile(err) {
		return nil, path, nil
	}
	if err != nil {
		return nil, path, err
	}
	f, err := os.OpenFile(resolved, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if absentFile(err) {
		return nil, path, nil
	}
	if err != nil {
		return nil, path, unsafeFile(resolved, "open: %v", err)
	}
	fi, err := f.Stat()
	if err == nil {
		err = checkOpenFile(fi, opts)
	}
	if err != nil {
		_ = f.Close()
		return nil, path, unsafeFile(resolved, "%v", err)
	}
	return f, path, nil
}

// resolveChecked walks p from the root. A component that is a symbolic
// link is checked and replaced by its target, and the walk starts again
// from the root, so every directory of the target's path is checked as
// well. It returns the resolved path of the file, whose own checks run on
// the open descriptor.
func resolveChecked(p string, opts MachineFileOptions, hops int) (string, error) {
	if hops > maxLinkHops {
		return "", unsafeFile(p, "too many symbolic links")
	}
	cur := string(filepath.Separator)
	if err := checkPathDir(cur, opts); err != nil {
		return "", err
	}
	comps := strings.Split(strings.TrimPrefix(filepath.Clean(p), cur), string(filepath.Separator))
	for i, c := range comps {
		next := filepath.Join(cur, c)
		fi, err := os.Lstat(next)
		if err != nil {
			if absentFile(err) {
				return "", err
			}
			return "", unsafeFile(next, "%v", err)
		}
		last := i == len(comps)-1
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			// The directory that holds the link was checked as cur.
			if err := checkOwner(next, fi, opts); err != nil {
				return "", err
			}
			target, err := os.Readlink(next)
			if err != nil {
				return "", unsafeFile(next, "read link: %v", err)
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(cur, target)
			}
			rest := append([]string{target}, comps[i+1:]...)
			return resolveChecked(filepath.Join(rest...), opts, hops+1)
		case last:
			return next, nil
		case !fi.IsDir():
			return "", unsafeFile(next, "is not a directory")
		}
		if err := checkPathDir(next, opts); err != nil {
			return "", err
		}
		cur = next
	}
	return cur, nil
}

// checkPathDir checks one directory on the way to the file.
func checkPathDir(dir string, opts MachineFileOptions) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return unsafeFile(dir, "%v", err)
	}
	if err := checkOwner(dir, fi, opts); err != nil {
		return err
	}
	st, _ := fi.Sys().(*syscall.Stat_t)
	if othersWrite(fi.Mode(), st.Gid) && (!opts.AllowSticky || fi.Mode()&fs.ModeSticky == 0) {
		return unsafeFile(dir, "another user can write the directory (mode %s)", fi.Mode().Perm())
	}
	return nil
}

// checkOwner checks that root or a trusted user owns a path.
func checkOwner(path string, fi fs.FileInfo, opts MachineFileOptions) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return unsafeFile(path, "has no owner")
	}
	if !trustedOwner(st.Uid, opts) {
		return unsafeFile(path, "is owned by user %d, not by root", st.Uid)
	}
	return nil
}

// checkOpenFile checks the descriptor of the machine file: a regular file
// that root or a trusted user owns and that no other user can write.
func checkOpenFile(fi fs.FileInfo, opts MachineFileOptions) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	switch {
	case !fi.Mode().IsRegular():
		return fmt.Errorf("is not a regular file")
	case !ok:
		return fmt.Errorf("has no owner")
	case !trustedOwner(st.Uid, opts):
		return fmt.Errorf("is owned by user %d, not by root", st.Uid)
	case othersWrite(fi.Mode(), st.Gid):
		return fmt.Errorf("another user can write the file (mode %s)", fi.Mode().Perm())
	}
	return nil
}

// trustedOwner reports whether uid is root or a trusted user.
func trustedOwner(uid uint32, opts MachineFileOptions) bool {
	return uid == 0 || slices.Contains(opts.TrustedUIDs, uid)
}

// othersWrite reports whether a user other than the owner can write a file
// of mode m and group gid: the other write bit, or the group write bit of a
// group other than gid 0.
func othersWrite(m fs.FileMode, gid uint32) bool {
	return m&0o002 != 0 || m&0o020 != 0 && gid != 0
}
