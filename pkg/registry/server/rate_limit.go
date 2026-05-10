package server

import (
	"net/http"
	"sync"
	"time"
)

// QuotaLimits is the per-tenant rate-limit envelope. Mirrors the
// §4.7.8 limit fields the spec lists (search QPS, materialize
// rate). Storage and audit volume are enforced elsewhere
// (ingest, audit retention scheduler).
type QuotaLimits struct {
	SearchQPS       int
	MaterializeRate int
}

// rateBucket is a leaky token bucket. Capacity is the burst size;
// the bucket refills at Rate tokens per second.
type rateBucket struct {
	mu       sync.Mutex
	capacity int
	tokens   float64
	rate     float64
	last     time.Time
}

func newBucket(rate int) *rateBucket {
	if rate <= 0 {
		return nil
	}
	cap := rate
	if cap < 1 {
		cap = 1
	}
	return &rateBucket{
		capacity: cap,
		tokens:   float64(cap),
		rate:     float64(rate),
		last:     time.Now(),
	}
}

func (b *rateBucket) allow() bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens = b.tokens + elapsed*b.rate
	if b.tokens > float64(b.capacity) {
		b.tokens = float64(b.capacity)
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// QuotaLimiter holds the per-tenant rate limiters and exposes
// allowSearch / allowMaterialize methods that handlers consult
// before doing real work. Zero limits skip the check.
type QuotaLimiter struct {
	mu             sync.Mutex
	searchBuckets  map[string]*rateBucket
	matBuckets     map[string]*rateBucket
	searchQPS      int
	matRate        int
}

// NewQuotaLimiter returns a limiter configured with the supplied
// limits. Tenant buckets initialize lazily on first use so the
// limiter scales to the active tenant set.
func NewQuotaLimiter(limits QuotaLimits) *QuotaLimiter {
	return &QuotaLimiter{
		searchBuckets: map[string]*rateBucket{},
		matBuckets:    map[string]*rateBucket{},
		searchQPS:     limits.SearchQPS,
		matRate:       limits.MaterializeRate,
	}
}

// AllowSearch returns true when the tenant's search QPS budget
// permits the request, false when it should be rejected with
// quota.search_qps_exceeded.
func (q *QuotaLimiter) AllowSearch(tenantID string) bool {
	if q == nil || q.searchQPS <= 0 {
		return true
	}
	q.mu.Lock()
	bucket, ok := q.searchBuckets[tenantID]
	if !ok {
		bucket = newBucket(q.searchQPS)
		q.searchBuckets[tenantID] = bucket
	}
	q.mu.Unlock()
	return bucket.allow()
}

// AllowMaterialize returns true when the tenant's materialization
// rate permits the request.
func (q *QuotaLimiter) AllowMaterialize(tenantID string) bool {
	if q == nil || q.matRate <= 0 {
		return true
	}
	q.mu.Lock()
	bucket, ok := q.matBuckets[tenantID]
	if !ok {
		bucket = newBucket(q.matRate)
		q.matBuckets[tenantID] = bucket
	}
	q.mu.Unlock()
	return bucket.allow()
}

// writeQuotaError emits the §6.10 structured error envelope for
// rate-limited requests.
func writeQuotaError(w http.ResponseWriter, code, message string) {
	writeError(w, http.StatusTooManyRequests, code, message)
}
