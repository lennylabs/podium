//go:build !windows

package main

import (
	"os"
	"syscall"
)

// hangupSignals returns the signals that force a §6.3.2.1 token re-read. On
// Unix this is SIGHUP, the conventional "reload configuration" signal.
func hangupSignals() []os.Signal { return []os.Signal{syscall.SIGHUP} }
