// Package clock is the only time source production code is allowed to consume.
// Tests inject a controllable Clock; production wires Real.
package clock

import (
	"sync"
	"time"
)

// Clock is the abstraction every package depends on instead of time.Now.
type Clock interface {
	Now() time.Time
}

// Real is a Clock backed by the operating system clock.
type Real struct{}

// Now returns the current system time.
func (Real) Now() time.Time { return time.Now().UTC() }

// Frozen is a Clock fixed at a specific time. Tests use it to make every
// time-dependent behavior deterministic.
type Frozen struct {
	mu sync.Mutex
	t  time.Time
}

// NewFrozen returns a Frozen Clock initialized to t.
func NewFrozen(t time.Time) *Frozen { return &Frozen{t: t.UTC()} }

// Now returns the frozen time.
func (f *Frozen) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

// Set replaces the frozen time.
func (f *Frozen) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = t.UTC()
}

// Advance moves the frozen time forward by d.
func (f *Frozen) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d).UTC()
}
