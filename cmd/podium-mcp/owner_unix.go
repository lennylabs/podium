//go:build unix

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// ownWorkspaceFile reports whether the workspace file at path is the
// process user's own: neither the file nor its .podium directory is a
// symbolic link, and the owner of the file itself, read without following a
// link, is the user of the process. A file that cannot be examined counts
// as the user's own, so the start is refused for it.
//
// Spec: §4.7.11 (User sources of the MCP server, Keys)
func ownWorkspaceFile(path string) bool {
	if linked(filepath.Dir(path)) {
		return false
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return true
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return !ok || int(st.Uid) == os.Getuid()
}

// linked reports whether path is a symbolic link.
func linked(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&fs.ModeSymlink != 0
}
