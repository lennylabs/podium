package lint

import (
	"context"
	"time"
)

// contextWithTimeout is the package-local helper the URL HEAD
// path uses; isolated so tests can swap the clock. It derives from
// the caller's context so a cancelled lint run aborts the probe
// (spec: §9.3 "Cancellable").
func contextWithTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d)
}
