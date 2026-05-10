package lint

import (
	"context"
	"time"
)

// contextWithTimeout is the package-local helper the URL HEAD
// path uses; isolated so tests can swap the clock.
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
