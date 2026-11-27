//go:build !podiumtest

package main

import "flag"

// addGitHubAPITestFlag defines the flag that redirects the GitHub API in a
// binary built with the podiumtest tag. A release build defines none, so no
// flag or environment variable of a shipped binary sends the token in
// GH_TOKEN or GITHUB_TOKEN to a host other than api.github.com.
//
// Spec: §7.3.1 (Trust preset)
func addGitHubAPITestFlag(*flag.FlagSet) *string { return nil }
