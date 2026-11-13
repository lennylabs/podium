//go:build !windows

package audit

import (
	"os/signal"
	"syscall"
)

// ignoreSIGPIPE makes a write to a broken standard output fail with EPIPE.
// A Go process that does not ignore SIGPIPE dies when a write to fd 1 hits
// a pipe whose reader has gone, so a vanished log collector would kill the
// registry rather than surface as a write_error drop (§8.3).
//
// Spec: §8.3
func ignoreSIGPIPE() {
	signal.Ignore(syscall.SIGPIPE)
}
