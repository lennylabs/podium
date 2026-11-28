//go:build !unix && !windows

package main

import "errors"

// accountHome reports that the platform has no account database lookup,
// so the MCP server reads no user sync.yaml.
//
// Spec: §4.7.11 (User home)
func accountHome() (string, error) {
	return "", errors.New("this platform has no account database lookup")
}
