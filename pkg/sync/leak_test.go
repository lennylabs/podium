package sync_test

import (
	"testing"

	"go.uber.org/goleak"
)

// Spec: §7.5.4 — `podium sync --watch` subscribes to registry change events and
// runs a watcher goroutine for the lifetime of the context. Every watch loop in
// pkg/sync exits on ctx.Done(), and the server-source watcher tears down its
// nested SSE stream goroutine when the request context is canceled. This
// goroutine-leak guard asserts that the package's tests leave no goroutine
// running past their context, so a regression that drops a ctx.Done() exit (a
// genuine leak) fails the test binary instead of silently surviving.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
