//go:build unix

package main

import (
	"errors"
	"os"
	"os/user"
	"strconv"
)

// accountHome returns the home directory that the account database records
// for the user ID of the process. It never reads HOME or an XDG_*
// variable, which a project configuration can set for the MCP server.
// user.LookupId uses getpwuid_r with cgo and on macOS, and reads
// /etc/passwd without cgo, with no fallback to $HOME; user.Current is not
// used because its form without cgo falls back to $HOME.
//
// Spec: §4.7.11 (User home)
func accountHome() (string, error) {
	u, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil {
		return "", err
	}
	if u.HomeDir == "" {
		return "", errors.New("the account database records no home directory for the user")
	}
	return u.HomeDir, nil
}
