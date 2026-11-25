package source

import (
	"context"
	"testing"
	"time"
)

// Spec: §7.3.1, §13.12 — a failed snapshot names PODIUM_GIT_FETCH_TIMEOUT
// only when the fetch deadline owns the failure. A caller context that ended,
// or whose own deadline comes before the fetch deadline, keeps its own error.
func TestFetchDeadlinePassed_Owner(t *testing.T) {
	t.Parallel()
	past := time.Now().Add(-time.Second)
	future := time.Now().Add(time.Hour)

	expired, cancelExpired := context.WithDeadline(context.Background(), past)
	defer cancelExpired()
	live, cancelLive := context.WithCancel(context.Background())
	defer cancelLive()

	t.Run("caller context ended", func(t *testing.T) {
		t.Parallel()
		caller, cancel := context.WithCancel(context.Background())
		cancel()
		if fetchDeadlinePassed(caller, expired, past) {
			t.Fatal("a caller context that ended owns the failure")
		}
	})

	t.Run("caller deadline comes first", func(t *testing.T) {
		t.Parallel()
		// The caller's deadline lies before the fetch deadline and has not
		// fired yet. The caller owns the failure, so the clock is not read.
		caller, cancel := context.WithDeadline(context.Background(), future)
		defer cancel()
		if fetchDeadlinePassed(caller, live, future.Add(time.Hour)) {
			t.Fatal("a caller whose deadline comes first owns the failure")
		}
	})

	t.Run("fetch context expired", func(t *testing.T) {
		t.Parallel()
		if !fetchDeadlinePassed(context.Background(), expired, past) {
			t.Fatal("an expired fetch context owns the failure")
		}
	})

	t.Run("clock reached the deadline before the context timer ran", func(t *testing.T) {
		t.Parallel()
		if !fetchDeadlinePassed(context.Background(), live, past) {
			t.Fatal("a failure at or after the fetch deadline is the deadline's")
		}
	})

	t.Run("failure before the deadline", func(t *testing.T) {
		t.Parallel()
		if fetchDeadlinePassed(context.Background(), live, future) {
			t.Fatal("a failure before the fetch deadline keeps its own error")
		}
	})

	t.Run("caller deadline later than the fetch deadline", func(t *testing.T) {
		t.Parallel()
		caller, cancel := context.WithDeadline(context.Background(), future)
		defer cancel()
		if !fetchDeadlinePassed(caller, expired, past) {
			t.Fatal("a fetch deadline that comes first owns the failure")
		}
	})
}
