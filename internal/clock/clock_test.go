package clock

import (
	"testing"
	"time"
)

// Spec: n/a — internal harness primitive (see TEST_INFRASTRUCTURE_PLAN.md §15).
func TestReal_NowIsRecent(t *testing.T) {
	t.Parallel()
	c := Real{}
	got := c.Now()
	if time.Since(got) > 5*time.Second {
		t.Fatalf("Real.Now() returned %v, expected within 5s of system time", got)
	}
	if got.Location() != time.UTC {
		t.Fatalf("Real.Now() returned %v, expected UTC", got.Location())
	}
}

// Spec: n/a — internal harness primitive (see TEST_INFRASTRUCTURE_PLAN.md §15).
func TestFrozen_NowReturnsFrozenInstant(t *testing.T) {
	t.Parallel()
	want := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	c := NewFrozen(want)
	if got := c.Now(); !got.Equal(want) {
		t.Fatalf("Frozen.Now() = %v, want %v", got, want)
	}
}

// Spec: n/a — internal harness primitive (see TEST_INFRASTRUCTURE_PLAN.md §15).
func TestFrozen_AdvanceMovesForward(t *testing.T) {
	t.Parallel()
	c := NewFrozen(time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC))
	c.Advance(2 * time.Hour)
	want := time.Date(2026, 5, 8, 14, 0, 0, 0, time.UTC)
	if got := c.Now(); !got.Equal(want) {
		t.Fatalf("after Advance(2h), Now() = %v, want %v", got, want)
	}
}

// Spec: n/a — internal harness primitive (see TEST_INFRASTRUCTURE_PLAN.md §15).
func TestFrozen_SetReplacesInstant(t *testing.T) {
	t.Parallel()
	c := NewFrozen(time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC))
	want := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	c.Set(want)
	if got := c.Now(); !got.Equal(want) {
		t.Fatalf("after Set, Now() = %v, want %v", got, want)
	}
}

// Spec: n/a — concurrency safety check.
func TestFrozen_ConcurrentAccessIsSafe(t *testing.T) {
	t.Parallel()
	c := NewFrozen(time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC))
	done := make(chan struct{})
	for i := 0; i < 16; i++ {
		go func() {
			for j := 0; j < 1000; j++ {
				c.Advance(time.Nanosecond)
				_ = c.Now()
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 16; i++ {
		<-done
	}
}
