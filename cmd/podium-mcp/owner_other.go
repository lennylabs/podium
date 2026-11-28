//go:build !unix

package main

// ownedByProcessUser reports a file as the user's own on a platform where
// the MCP server reads no file owner, so a workspace file of the working
// directory that does not parse refuses the start, as the fail-closed
// reading of §4.7.11 states.
func ownedByProcessUser(string) bool { return true }
