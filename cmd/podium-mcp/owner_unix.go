//go:build unix

package main

import (
	"os"
	"syscall"
)

// ownedByProcessUser reports whether the user of the process owns path.
func ownedByProcessUser(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		// A file that cannot be read is the working directory's own file
		// when it sits there; refuse rather than ignore it.
		return true
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return !ok || int(st.Uid) == os.Getuid()
}
