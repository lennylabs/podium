//go:build podiumtest

package main

import "flag"

// addGitHubAPITestFlag defines --test-github-api, which replaces the GitHub
// API base URL so an end-to-end test answers the request from an in-process
// server. internal/testharness/cmdharness builds the binaries of the
// end-to-end tests with the podiumtest tag, and the release workflow refuses
// a binary built with it.
func addGitHubAPITestFlag(fs *flag.FlagSet) *string {
	return fs.String("test-github-api", "", "test build only: the GitHub API base URL")
}
