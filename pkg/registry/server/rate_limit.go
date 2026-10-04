package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// QuotaLimits is the rate-like part of the §4.7.8 quota envelope: search QPS,
// materialization rate, and audit volume per day. The limiter holds one value
// as the deployment defaults, and EffectiveLimits resolves a tenant's enforced
// values from its store record against them. The storage-bytes quota is
// enforced at ingest (quota.storage_exceeded). The audit-volume quota is
// enforced by AuditVolumeMeter: the audit emitter records every event against
// the emitting request's tenant and the §7.3.1 reingest path refuses new
// writes with quota.audit_volume_exceeded once that tenant's budget is spent.
type QuotaLimits struct {
	SearchQPS         int
	MaterializeRate   int
	AuditVolumePerDay int64
}

// EffectiveLimits resolves the limits enforced against a tenant from its
// stored quota and the deployment defaults. For each field, a positive record
// value is enforced as is, zero selects the default, and a negative value
// disables the budget for the tenant (0, meaning unenforced) whatever the
// default is.
//
// Spec: §4.7.8
func EffectiveLimits(rec store.Quota, def QuotaLimits) QuotaLimits {
	return QuotaLimits{
		SearchQPS:         effectiveLimit(rec.SearchQPS, def.SearchQPS),
		MaterializeRate:   effectiveLimit(rec.MaterializeRate, def.MaterializeRate),
		AuditVolumePerDay: effectiveLimit(rec.AuditVolumePerDay, def.AuditVolumePerDay),
	}
}

// effectiveLimit applies the §4.7.8 precedence to one field.
func effectiveLimit[T int | int64](rec, def T) T {
	switch {
	case rec > 0:
		return rec
	case rec < 0:
		return 0
	default:
		return def
	}
}

// routedQuotaKey is the context key for the tenant quota routing carried.
type routedQuotaKey struct{}

// contextWithRoutedTenant scopes ctx to the routed tenant t: it sets t.ID as
// the request's tenant and carries t.Quota, so the limiter and the quota read
// resolve the tenant's limits without a second store read of the record
// routing already read. Setting both in one helper keeps a routing site from
// setting the tenant ID without its quota, which would charge the tenant at the
// deployment defaults. routeTenant is the only caller.
//
// Spec: §4.7.8, §6.3.1
func contextWithRoutedTenant(ctx context.Context, t store.Tenant) context.Context {
	ctx = core.ContextWithTenant(ctx, t.ID)
	return context.WithValue(ctx, routedQuotaKey{}, t.Quota)
}

// tenantQuotaFrom returns the tenant quota routing carried on ctx, or the zero
// Quota when routing resolved no tenant. A zero quota resolves to the
// deployment defaults under EffectiveLimits, so unrouted and single-tenant
// requests need no presence flag.
//
// Spec: §4.7.8, §6.3.1
func tenantQuotaFrom(ctx context.Context) store.Quota {
	q, _ := ctx.Value(routedQuotaKey{}).(store.Quota)
	return q
}

// rateBucket is a leaky token bucket. Capacity is the burst size; the bucket
// refills at rate tokens per second.
//
// mu guards capacity, tokens, rate, and last. allow is the only method that
// reads or writes them, and it retunes and charges in one critical section, so
// a rate change cannot lose a concurrent charge.
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
	return &rateBucket{
		capacity: rate,
		tokens:   float64(rate),
		rate:     float64(rate),
		last:     time.Now(),
	}
}

// allow charges one token at the given rate. It refills at the stored rate for
// the elapsed time, then retunes the bucket to rate when it differs, keeping
// the unspent tokens up to the new capacity. A raised rate therefore grants no
// fresh burst, and a lowered rate applies to the charge that carries it.
//
// Spec: §4.7.8
func (b *rateBucket) allow(rate int) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens = min(b.tokens+elapsed*b.rate, float64(b.capacity))
	if float64(rate) != b.rate {
		b.rate = float64(rate)
		b.capacity = rate
		b.tokens = min(b.tokens, float64(b.capacity))
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// QuotaLimiter holds the per-tenant §4.7.8 search and materialization buckets
// that handlers consult before doing work. Each charge resolves the tenant's
// rate from the tenant record routing carried on the request and the
// deployment defaults; the limiter reads no tenant record itself.
type QuotaLimiter struct {
	// mu guards which buckets are in searchBuckets and matBuckets. An entry is
	// inserted once, on the tenant's first positive-rate charge, and is never
	// replaced or removed, so a bucket pointer read under mu stays the
	// tenant's live bucket after mu is released. No code holds mu and a
	// bucket's mu at once.
	mu            sync.Mutex
	searchBuckets map[string]*rateBucket
	matBuckets    map[string]*rateBucket
	defaults      QuotaLimits
}

// NewQuotaLimiter returns a limiter whose deployment defaults are defaults.
// Tenant buckets initialize lazily on first use so the limiter scales to the
// active tenant set.
func NewQuotaLimiter(defaults QuotaLimits) *QuotaLimiter {
	return &QuotaLimiter{
		searchBuckets: map[string]*rateBucket{},
		matBuckets:    map[string]*rateBucket{},
		defaults:      defaults,
	}
}

// Limits returns the limits enforced against a tenant whose stored quota is
// rec. The quota read reports it, so the report and the enforcement agree.
//
// Spec: §4.7.8
func (q *QuotaLimiter) Limits(rec store.Quota) QuotaLimits {
	return EffectiveLimits(rec, q.defaults)
}

// AllowSearch returns true when the tenant's search QPS budget, resolved from
// rec and the deployment defaults, permits the request, false when it should
// be rejected with quota.search_qps_exceeded.
//
// Spec: §4.7.8
func (q *QuotaLimiter) AllowSearch(tenantID string, rec store.Quota) bool {
	if q == nil {
		return true
	}
	return q.charge(q.searchBuckets, tenantID, q.Limits(rec).SearchQPS)
}

// AllowMaterialize returns true when the tenant's materialization rate,
// resolved from rec and the deployment defaults, permits the request.
//
// Spec: §4.7.8
func (q *QuotaLimiter) AllowMaterialize(tenantID string, rec store.Quota) bool {
	if q == nil {
		return true
	}
	return q.charge(q.matBuckets, tenantID, q.Limits(rec).MaterializeRate)
}

// charge charges one request against the tenant's bucket in buckets at rate.
// A rate of zero or less is unenforced and touches neither the map nor any
// bucket, so a later positive rate continues from the bucket's balance rather
// than from a full bucket.
func (q *QuotaLimiter) charge(buckets map[string]*rateBucket, tenantID string, rate int) bool {
	if rate <= 0 {
		return true
	}
	q.mu.Lock()
	bucket, ok := buckets[tenantID]
	if !ok {
		bucket = newBucket(rate)
		buckets[tenantID] = bucket
	}
	q.mu.Unlock()
	return bucket.allow(rate)
}

// writeQuotaError emits the §6.10 structured error envelope for
// rate-limited requests.
func writeQuotaError(w http.ResponseWriter, code, message string) {
	writeError(w, http.StatusTooManyRequests, code, message)
}
