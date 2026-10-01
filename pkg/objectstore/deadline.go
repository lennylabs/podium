package objectstore

import (
	"context"
	"time"
)

// DefaultReadTimeout is the §13.12 default deadline on each object-storage read
// the §13.4 first-start stored-value rewrite, the sign-stored-rows command, and
// the §13.4 stored-row admission check make. PODIUM_MIGRATION_OBJECT_READ_TIMEOUT overrides it. It lives here
// so the boot path, the registry core, and the filesystem-source server share
// one default without importing one another.
const DefaultReadTimeout = 30 * time.Second

// GetWithDeadline reads one object under a deadline the provider does not have
// to honor. Filesystem.Get discards its context and calls os.ReadFile, and
// filesystem is the default object store, so a hung ReadWriteMany mount would
// otherwise block the caller for the life of the process. The read runs in a
// goroutine that sends on a buffered channel, so the goroutine exits whenever
// the provider returns even though nothing reads its result. A read abandoned
// at the deadline returns context.DeadlineExceeded.
//
// Spec: §13.12 — PODIUM_MIGRATION_OBJECT_READ_TIMEOUT.
func GetWithDeadline(ctx context.Context, p Provider, key string, timeout time.Duration) ([]byte, error) {
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	type result struct {
		body []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		body, err := p.Get(dctx, key)
		done <- result{body: body, err: err}
	}()
	select {
	case r := <-done:
		return r.body, r.err
	case <-dctx.Done():
		return nil, dctx.Err()
	}
}
