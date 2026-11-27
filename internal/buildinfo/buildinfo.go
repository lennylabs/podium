// Package buildinfo carries the version, commit, and build date that
// each Podium binary reports. The values are set at link time via
// -ldflags "-X" from the Makefile (or a release pipeline). The defaults
// below produce a clearly-marked "-dev" version when someone runs
// `go build` directly without ldflags.
package buildinfo

import "time"

var (
	// Version is the semver string for this build (e.g., "0.1.0" or
	// "0.1.0-rc.1"). Defaults to a "-dev" marker so a plain
	// `go build` from a working tree is recognisable as unreleased.
	Version = "0.7.0"
	// Commit is the git commit short hash this build was produced
	// from. Set via -ldflags at release time; "unknown" otherwise.
	Commit = "unknown"
	// Date is the build timestamp (RFC 3339). Set via -ldflags at
	// release time; "unknown" otherwise.
	Date = "unknown"
	// ReleaseDate is the RFC 3339 build time that the release pipeline
	// sets, and only the release pipeline: the Makefile and a plain
	// `go build` leave it empty. The age of the embedded public-good
	// Sigstore root counts from it (§4.7.11), which holds because the
	// release workflow refuses a tag whose embedded root is not the current
	// one. Date cannot serve, because every Makefile build sets it to the
	// time of the build whatever the age of the checkout's embedded root.
	ReleaseDate = ""
)

// ReleaseTime parses ReleaseDate. It reports false for a source build,
// which records no release date, and for a value that does not parse, so
// the caller treats the date as unknown rather than as a time.
//
// Spec: §4.7.11.
func ReleaseTime() (time.Time, bool) {
	if ReleaseDate == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, ReleaseDate)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// String returns "<Version> (<Commit>, built <Date>)" when commit and
// date are populated, or just Version when they remain the default.
func String() string {
	if Commit == "unknown" && Date == "unknown" {
		return Version
	}
	return Version + " (" + Commit + ", built " + Date + ")"
}
