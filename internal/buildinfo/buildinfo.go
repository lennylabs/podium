// Package buildinfo carries the version, commit, and build date that
// each Podium binary reports. The values are set at link time via
// -ldflags "-X" from the Makefile (or a release pipeline). The defaults
// below produce a clearly-marked "-dev" version when someone runs
// `go build` directly without ldflags.
package buildinfo

var (
	// Version is the semver string for this build (e.g., "0.1.0" or
	// "0.1.0-rc.1"). Defaults to a "-dev" marker so a plain
	// `go build` from a working tree is recognisable as unreleased.
	Version = "0.1.0"
	// Commit is the git commit short hash this build was produced
	// from. Set via -ldflags at release time; "unknown" otherwise.
	Commit = "unknown"
	// Date is the build timestamp (RFC 3339). Set via -ldflags at
	// release time; "unknown" otherwise.
	Date = "unknown"
)

// String returns "<Version> (<Commit>, built <Date>)" when commit and
// date are populated, or just Version when they remain the default.
func String() string {
	if Commit == "unknown" && Date == "unknown" {
		return Version
	}
	return Version + " (" + Commit + ", built " + Date + ")"
}
