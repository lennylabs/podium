package server

import (
	"testing"
	"time"
)

// Spec: §4.7.8 — the search QPS limiter rejects calls beyond the
// configured rate; refill restores capacity over time.
func TestQuotaLimiter_SearchRateLimit(t *testing.T) {
	t.Parallel()
	q := NewQuotaLimiter(QuotaLimits{SearchQPS: 2})
	if !q.AllowSearch("t") {
		t.Fatalf("first call denied")
	}
	if !q.AllowSearch("t") {
		t.Fatalf("second call denied")
	}
	if q.AllowSearch("t") {
		t.Errorf("third call allowed; bucket should be empty")
	}
	// Wait long enough for refill (~1 token).
	time.Sleep(600 * time.Millisecond)
	if !q.AllowSearch("t") {
		t.Errorf("refill did not allow next call")
	}
}

// Spec: §4.7.8 — zero limit means no enforcement.
func TestQuotaLimiter_ZeroLimitDisablesCheck(t *testing.T) {
	t.Parallel()
	q := NewQuotaLimiter(QuotaLimits{})
	for i := 0; i < 100; i++ {
		if !q.AllowSearch("t") {
			t.Errorf("call %d denied with zero limit", i)
		}
	}
}

// Spec: §4.7.8 — limits are per-tenant; one tenant's traffic
// doesn't drain another tenant's bucket.
func TestQuotaLimiter_TenantsIsolated(t *testing.T) {
	t.Parallel()
	q := NewQuotaLimiter(QuotaLimits{SearchQPS: 1})
	if !q.AllowSearch("a") {
		t.Errorf("tenant a first call denied")
	}
	if !q.AllowSearch("b") {
		t.Errorf("tenant b first call denied")
	}
	if q.AllowSearch("a") {
		t.Errorf("tenant a second call allowed; bucket should be empty")
	}
	if q.AllowSearch("b") {
		t.Errorf("tenant b second call allowed; bucket should be empty")
	}
}

// Spec: §4.7.8 — materialize bucket is independent of search.
func TestQuotaLimiter_SearchAndMaterializeIndependent(t *testing.T) {
	t.Parallel()
	q := NewQuotaLimiter(QuotaLimits{SearchQPS: 1, MaterializeRate: 1})
	_ = q.AllowSearch("t") // empties search bucket
	if !q.AllowMaterialize("t") {
		t.Errorf("materialize bucket drained by search call")
	}
}
