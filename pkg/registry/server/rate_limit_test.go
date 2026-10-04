package server

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7.8 — the search QPS limiter rejects calls beyond the
// configured rate; refill restores capacity over time.
func TestQuotaLimiter_SearchRateLimit(t *testing.T) {
	t.Parallel()
	q := NewQuotaLimiter(QuotaLimits{SearchQPS: 2})
	if !q.AllowSearch("t", store.Quota{}) {
		t.Fatalf("first call denied")
	}
	if !q.AllowSearch("t", store.Quota{}) {
		t.Fatalf("second call denied")
	}
	if q.AllowSearch("t", store.Quota{}) {
		t.Errorf("third call allowed; bucket should be empty")
	}
	// Wait long enough for refill (~1 token).
	time.Sleep(600 * time.Millisecond)
	if !q.AllowSearch("t", store.Quota{}) {
		t.Errorf("refill did not allow next call")
	}
}

// Spec: §4.7.8 — zero limit means no enforcement.
func TestQuotaLimiter_ZeroLimitDisablesCheck(t *testing.T) {
	t.Parallel()
	q := NewQuotaLimiter(QuotaLimits{})
	for i := 0; i < 100; i++ {
		if !q.AllowSearch("t", store.Quota{}) {
			t.Errorf("call %d denied with zero limit", i)
		}
	}
}

// Spec: §4.7.8 — limits are per-tenant; one tenant's traffic
// doesn't drain another tenant's bucket.
func TestQuotaLimiter_TenantsIsolated(t *testing.T) {
	t.Parallel()
	q := NewQuotaLimiter(QuotaLimits{SearchQPS: 1})
	if !q.AllowSearch("a", store.Quota{}) {
		t.Errorf("tenant a first call denied")
	}
	if !q.AllowSearch("b", store.Quota{}) {
		t.Errorf("tenant b first call denied")
	}
	if q.AllowSearch("a", store.Quota{}) {
		t.Errorf("tenant a second call allowed; bucket should be empty")
	}
	if q.AllowSearch("b", store.Quota{}) {
		t.Errorf("tenant b second call allowed; bucket should be empty")
	}
}

// Spec: §4.7.8 — materialize bucket is independent of search.
func TestQuotaLimiter_SearchAndMaterializeIndependent(t *testing.T) {
	t.Parallel()
	q := NewQuotaLimiter(QuotaLimits{SearchQPS: 1, MaterializeRate: 1})
	_ = q.AllowSearch("t", store.Quota{}) // empties search bucket
	if !q.AllowMaterialize("t", store.Quota{}) {
		t.Errorf("materialize bucket drained by search call")
	}
}

// Spec: §4.7.8 — a positive tenant value wins, zero selects the deployment
// default, and a negative value disables the budget for the tenant.
func TestEffectiveLimits_Precedence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		rec, def int
		want     int
	}{
		{"positive record wins", 5, 2, 5},
		{"zero record selects default", 0, 2, 2},
		{"negative record is unenforced", -1, 2, 0},
		{"zero record and zero default is unenforced", 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := store.Quota{SearchQPS: tc.rec, MaterializeRate: tc.rec, AuditVolumePerDay: int64(tc.rec)}
			def := QuotaLimits{SearchQPS: tc.def, MaterializeRate: tc.def, AuditVolumePerDay: int64(tc.def)}
			want := QuotaLimits{SearchQPS: tc.want, MaterializeRate: tc.want, AuditVolumePerDay: int64(tc.want)}
			if got := EffectiveLimits(rec, def); got != want {
				t.Errorf("EffectiveLimits(%d, %d) = %+v, want %+v", tc.rec, tc.def, got, want)
			}
		})
	}
}

// Spec: §4.7.8 — Limits resolves a record against the limiter's defaults.
func TestQuotaLimiter_Limits(t *testing.T) {
	t.Parallel()
	def := QuotaLimits{SearchQPS: 2, MaterializeRate: 4, AuditVolumePerDay: 8}
	q := NewQuotaLimiter(def)
	if got := q.Limits(store.Quota{SearchQPS: 9}).SearchQPS; got != 9 {
		t.Errorf("Limits(SearchQPS 9).SearchQPS = %d, want 9", got)
	}
	if got := q.Limits(store.Quota{}); got != def {
		t.Errorf("Limits(zero) = %+v, want the defaults %+v", got, def)
	}
	if got := q.Limits(store.Quota{SearchQPS: -1}).SearchQPS; got != 0 {
		t.Errorf("Limits(SearchQPS -1).SearchQPS = %d, want 0", got)
	}
}

// Spec: §4.7.8 — the routing helper carries the tenant ID and its quota
// together, and a context routing never touched carries the zero quota.
func TestContextWithRoutedTenant_CarriesIDAndQuota(t *testing.T) {
	t.Parallel()
	if got := tenantQuotaFrom(context.Background()); got != (store.Quota{}) {
		t.Errorf("tenantQuotaFrom(Background) = %+v, want zero", got)
	}
	q := store.Quota{SearchQPS: 3, MaterializeRate: 4, AuditVolumePerDay: 5}
	ctx := contextWithRoutedTenant(context.Background(), store.Tenant{ID: "acme", Quota: q})
	if got := tenantQuotaFrom(ctx); got != q {
		t.Errorf("tenantQuotaFrom = %+v, want %+v", got, q)
	}
	if got := core.New(store.NewMemory(), "bound", nil).TenantFor(ctx); got != "acme" {
		t.Errorf("TenantFor = %q, want acme", got)
	}
}

// Spec: §4.7.8 — each tenant is charged at its own record's rate.
func TestQuotaLimiter_PerTenantRecord(t *testing.T) {
	t.Parallel()
	q := NewQuotaLimiter(QuotaLimits{})
	acme, globex := store.Quota{SearchQPS: 1}, store.Quota{}
	if !q.AllowSearch("acme", acme) {
		t.Fatal("acme first call denied")
	}
	if q.AllowSearch("acme", acme) {
		t.Error("acme second call allowed at a record of 1")
	}
	for i := 0; i < 10; i++ {
		if !q.AllowSearch("globex", globex) {
			t.Fatalf("globex call %d denied with zero record and zero default", i)
		}
	}
}

// Spec: §4.7.8 — a negative record exempts the tenant from the default.
func TestQuotaLimiter_NegativeRecordExempts(t *testing.T) {
	t.Parallel()
	q := NewQuotaLimiter(QuotaLimits{SearchQPS: 1})
	for i := 0; i < 10; i++ {
		if !q.AllowSearch("acme", store.Quota{SearchQPS: -1}) {
			t.Fatalf("acme call %d denied with a negative record", i)
		}
	}
}

// searchBucket returns the tenant's search bucket under q.mu.
func searchBucket(q *QuotaLimiter, tenant string) *rateBucket {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.searchBuckets[tenant]
}

// bucketState returns the bucket's rate, capacity, and tokens under b.mu.
func bucketState(b *rateBucket) (rate float64, capacity int, tokens float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rate, b.capacity, b.tokens
}

// Spec: §4.7.8 — raising the rate retunes the bucket in place and grants no
// fresh burst (D7).
func TestQuotaLimiter_RaisedRateRetunesInPlace(t *testing.T) {
	t.Parallel()
	q := NewQuotaLimiter(QuotaLimits{})
	if !q.AllowSearch("acme", store.Quota{SearchQPS: 1}) {
		t.Fatal("acme first call denied")
	}
	if q.AllowSearch("acme", store.Quota{SearchQPS: 1}) {
		t.Fatal("acme second call allowed at a record of 1")
	}
	before := searchBucket(q, "acme")
	if q.AllowSearch("acme", store.Quota{SearchQPS: 3}) {
		t.Error("raising the rate to 3 granted a fresh burst")
	}
	after := searchBucket(q, "acme")
	if after != before {
		t.Fatal("the rate change replaced acme's bucket")
	}
	if rate, capacity, _ := bucketState(after); rate != 3 || capacity != 3 {
		t.Errorf("bucket rate/capacity = %v/%d, want 3/3", rate, capacity)
	}
}

// Spec: §4.7.8 — lowering the rate caps the carried tokens at the new
// capacity (D7). The first call creates the bucket so the switch reaches the
// retune rather than newBucket.
func TestQuotaLimiter_LoweredRateClampsTokens(t *testing.T) {
	t.Parallel()
	q := NewQuotaLimiter(QuotaLimits{})
	if !q.AllowSearch("acme", store.Quota{SearchQPS: 3}) {
		t.Fatal("acme first call denied")
	}
	if !q.AllowSearch("acme", store.Quota{SearchQPS: 1}) {
		t.Fatal("first call after lowering the rate denied")
	}
	if q.AllowSearch("acme", store.Quota{SearchQPS: 1}) {
		t.Error("second call after lowering the rate allowed; carried tokens were not clamped")
	}
	if rate, capacity, _ := bucketState(searchBucket(q, "acme")); rate != 1 || capacity != 1 {
		t.Errorf("bucket rate/capacity = %v/%d, want 1/1", rate, capacity)
	}
}

// Spec: §4.7.8 — a zero interval leaves the bucket untouched, and returning to
// a positive rate continues from the spent balance (D7). A design that deletes
// or replaces the bucket admits the final call.
func TestQuotaLimiter_RateChangeKeepsBucket(t *testing.T) {
	t.Parallel()
	q := NewQuotaLimiter(QuotaLimits{})
	if !q.AllowSearch("acme", store.Quota{SearchQPS: 1}) {
		t.Fatal("acme first call denied")
	}
	before := searchBucket(q, "acme")
	for i := 0; i < 10; i++ {
		if !q.AllowSearch("acme", store.Quota{}) {
			t.Fatalf("call %d denied with zero record and zero default", i)
		}
	}
	if after := searchBucket(q, "acme"); after != before {
		t.Fatal("the zero interval replaced acme's bucket")
	}
	if rate, _, tokens := bucketState(before); rate != 1 || tokens >= 1 {
		t.Errorf("bucket rate/tokens = %v/%v, want rate 1 and a spent balance", rate, tokens)
	}
	if q.AllowSearch("acme", store.Quota{SearchQPS: 1}) {
		t.Error("returning to a rate of 1 refilled acme's bucket")
	}
}

// Spec: §4.7.8 — a zero record charges the tenant at the deployment default.
func TestQuotaLimiter_ZeroRecordUsesDefault(t *testing.T) {
	t.Parallel()
	q := NewQuotaLimiter(QuotaLimits{SearchQPS: 2})
	for i := 0; i < 2; i++ {
		if !q.AllowSearch("globex", store.Quota{}) {
			t.Fatalf("globex call %d denied under a default of 2", i)
		}
	}
	if q.AllowSearch("globex", store.Quota{}) {
		t.Error("globex third call allowed under a default of 2")
	}
}

// Spec: §4.7.8 — a nil limiter enforces nothing.
func TestQuotaLimiter_NilAllows(t *testing.T) {
	t.Parallel()
	var q *QuotaLimiter
	rec := store.Quota{SearchQPS: 1, MaterializeRate: 1}
	for i := 0; i < 3; i++ {
		if !q.AllowSearch("acme", rec) || !q.AllowMaterialize("acme", rec) {
			t.Fatalf("nil limiter denied call %d", i)
		}
	}
}

// Spec: §4.7.8 — charges at alternating rates, as a concurrent PATCH of the
// tenant record produces, never admit more than the first bucket's 2 tokens
// plus 2 per second of refill. The invariant under test: q.mu guards bucket
// membership, an entry is never replaced, and b.mu serializes the retune with
// the charge, so no charge lands on an orphaned bucket. Run it under
// `go test -race -run QuotaLimiter ./pkg/registry/server/` as well; CI runs no
// -race lane. TestQuotaLimiter_RateChangeKeepsBucket is the deterministic pin
// of the no-replacement invariant.
func TestQuotaLimiter_ConcurrentRateChangeBoundsAdmissions(t *testing.T) {
	t.Parallel()
	q := NewQuotaLimiter(QuotaLimits{})
	const n = 200
	start := time.Now()
	release := make(chan struct{})
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		admitted int
	)
	for i := 0; i < n; i++ {
		rec := store.Quota{SearchQPS: 1 + i%2}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-release
			if q.AllowSearch("acme", rec) {
				mu.Lock()
				admitted++
				mu.Unlock()
			}
		}()
	}
	close(release)
	wg.Wait()
	elapsed := time.Since(start)
	if bound := 2 + int(math.Ceil(2*elapsed.Seconds())); admitted > bound {
		t.Errorf("admitted %d calls, want at most %d over %v", admitted, bound, elapsed)
	}
	q.mu.Lock()
	buckets := len(q.searchBuckets)
	q.mu.Unlock()
	if buckets != 1 {
		t.Fatalf("searchBuckets has %d entries, want 1", buckets)
	}
	if rate, _, _ := bucketState(searchBucket(q, "acme")); rate != 1 && rate != 2 {
		t.Errorf("bucket rate = %v, want 1 or 2", rate)
	}
}

// Spec: §7.6.2 — admission is a prefix: charging stops at the first refusal,
// and an empty batch makes no charge.
func TestAdmitPrefix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		n         int
		answers   []bool
		wantCount int
		wantCalls int
	}{
		{"empty", 0, nil, 0, 0},
		{"all admitted", 3, []bool{true, true, true}, 3, 3},
		{"first refused", 3, []bool{false, true, true}, 0, 1},
		{"middle refused", 4, []bool{true, false, true, true}, 1, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			got := admitPrefix(tc.n, func() bool {
				ok := tc.answers[calls]
				calls++
				return ok
			})
			if got != tc.wantCount || calls != tc.wantCalls {
				t.Errorf("admitPrefix = %d after %d calls, want %d after %d", got, calls, tc.wantCount, tc.wantCalls)
			}
		})
	}
}

// Spec: §7.6.2, §6.10 — a refused bulk-load item carries the enriched
// quota.materialize_rate_exceeded envelope.
func TestMaterializeQuotaEnvelope(t *testing.T) {
	t.Parallel()
	e := materializeQuotaEnvelope()
	if e.Code != "quota.materialize_rate_exceeded" || e.Message != materializeQuotaMessage {
		t.Errorf("envelope = %+v", e)
	}
	if !e.Retryable || e.SuggestedAction == "" {
		t.Errorf("envelope not enriched: %+v", e)
	}
}
