//go:build windows

package audit

// ignoreSIGPIPE does nothing on Windows, which raises no SIGPIPE: a write
// to a closed pipe returns an error there. The file builds only for
// Windows, so the macOS and Linux test lanes never compile it, and its
// empty body holds no statement for a coverage profile to count.
//
// Spec: §8.3
func ignoreSIGPIPE() {}
