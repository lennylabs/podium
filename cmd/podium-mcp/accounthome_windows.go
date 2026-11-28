//go:build windows

package main

import (
	"errors"
	"os/user"
)

// accountHome returns the profile directory of the process token, which
// user.Current reads on Windows. It never reads USERPROFILE, which a
// project configuration can set for the MCP server.
//
// Spec: §4.7.11 (User home)
func accountHome() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	if u.HomeDir == "" {
		return "", errors.New("the process token names no profile directory")
	}
	return u.HomeDir, nil
}
