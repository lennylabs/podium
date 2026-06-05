package main

import (
	"testing"

	"go.uber.org/goleak"
)

// Spec: §6.4 / §6.3.2.1 — the stdio bridge's serve loop starts two
// long-running goroutines for the lifetime of the session: the overlay
// watcher (startOverlayWatch) and the session-token watcher (startTokenWatch),
// plus an optional metrics listener. Each returns a stop function the serve
// loop defers, so the goroutines, their fsnotify watchers, and the signal
// registration are released when stdin reaches EOF and serve returns. This
// goroutine-leak guard asserts the package's tests leave no goroutine running
// past their scope, so a regression that drops a stop on one of the watch
// loops (a genuine leak) fails the test binary instead of surviving silently.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
