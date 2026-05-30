//go:build windows

package main

import "os"

// hangupSignals returns no signals on Windows, which has no SIGHUP. The
// file watch and the per-call fresh read still satisfy the §6.3.2.1
// rotation contract there.
func hangupSignals() []os.Signal { return nil }
