//go:build windows

package audit

// ignoreSIGPIPE does nothing on Windows, which raises no SIGPIPE: a write
// to a closed pipe returns an error there.
//
// Spec: §8.3
func ignoreSIGPIPE() {}
