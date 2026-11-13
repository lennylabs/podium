package audit

import (
	"context"
	"testing"
	"time"
)

// manualTicker is a ticker that fires only when the test sends on c.
type manualTicker struct{ c chan time.Time }

func (m manualTicker) C() <-chan time.Time { return m.c }
func (m manualTicker) Stop()               {}

// stubTicker replaces newTicker for one test and returns a function that
// fires the tick of the forwarder opened after the call. The tick channel
// holds one tick, as a time.Ticker does, so fire never blocks. Callers do
// not call t.Parallel.
func stubTicker(t *testing.T) (fire func()) {
	t.Helper()
	c := make(chan time.Time, 1)
	prev := newTicker
	newTicker = func(time.Duration) ticker { return manualTicker{c: c} }
	t.Cleanup(func() { newTicker = prev })
	return func() {
		select {
		case c <- time.Time{}:
		default:
		}
	}
}

// stubSleep replaces the backoff sleep for one test. Callers do not call
// t.Parallel.
func stubSleep(t *testing.T, fn func(ctx context.Context, d time.Duration) error) {
	t.Helper()
	prev := sleep
	sleep = fn
	t.Cleanup(func() { sleep = prev })
}

// stubRequestContext replaces the request deadline hook for one test.
// Callers do not call t.Parallel.
func stubRequestContext(t *testing.T, fn func(parent context.Context, d time.Duration) (context.Context, context.CancelFunc)) {
	t.Helper()
	prev := requestContext
	requestContext = fn
	t.Cleanup(func() { requestContext = prev })
}
