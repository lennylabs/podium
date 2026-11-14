package audit

import (
	"testing"

	"go.uber.org/goleak"
)

// Spec: §8.3 — every HTTP and OTLP destination runs one sender goroutine
// that Close stops and waits for, and the retention and integrity
// schedulers exit when their context ends. This guard fails the test
// binary when a test leaves any of them running, so a regression that
// drops a sender's exit or a scheduler's ctx.Done() case surfaces as a
// leak instead of passing silently.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
