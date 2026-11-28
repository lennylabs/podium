//go:build !unix

package main

import (
	"io/fs"
	"os"
	"path/filepath"
)

// ownWorkspaceFile reports a workspace file as the user's own unless it or
// its .podium directory is a symbolic link. The MCP server reads no file
// owner on this platform, so a file of the working directory that fails
// refuses the start, the fail-closed reading of §4.7.11.
//
// Spec: §4.7.11 (User sources of the MCP server, Keys)
func ownWorkspaceFile(path string) bool {
	for _, p := range []string{filepath.Dir(path), path} {
		if fi, err := os.Lstat(p); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			return false
		}
	}
	return true
}
