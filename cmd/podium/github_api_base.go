package main

import "github.com/lennylabs/podium/pkg/attest/ghactions"

// githubAPIBase returns the GitHub API base URL: def, unless the test flag
// that only a podiumtest build defines names another.
func githubAPIBase(def string, testFlag *string) string {
	if testFlag != nil && *testFlag != "" {
		return *testFlag
	}
	if def == "" {
		return ghactions.APIBase
	}
	return def
}
