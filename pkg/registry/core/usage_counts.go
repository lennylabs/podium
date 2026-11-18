package core

import (
	"context"
	"sync"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/store"
)

// usageSearchHitDepth is how many leading results of a search count a search
// hit. §8.7 fixes it at 10 so the count does not depend on the top_k a caller
// requests.
const usageSearchHitDepth = 10

// UsageNone is the client kind and the harness §8.7 records for a request
// that reports none.
const UsageNone = "none"

// usageClientCtxKey carries the §7.2.2 client kind and harness of the request
// a call serves.
type usageClientCtxKey struct{}

// usageClient is the request's normalized client kind and harness.
type usageClient struct {
	kind, harness string
}

// ContextWithUsageClient returns ctx carrying the request's §7.2.2 client kind
// and harness, after normalization, for the §8.7 usage counts. An empty value
// is recorded as UsageNone. The server sets it from the X-Podium-Client
// header; the value is advisory and feeds no access decision.
//
// Spec: §8.7, §7.2.2
func ContextWithUsageClient(ctx context.Context, kind, harness string) context.Context {
	return context.WithValue(ctx, usageClientCtxKey{}, usageClient{kind: kind, harness: harness})
}

// usageClientFrom returns the client kind and harness ctx carries, with
// UsageNone for an absent or empty value.
func usageClientFrom(ctx context.Context) (kind, harness string) {
	c, _ := ctx.Value(usageClientCtxKey{}).(usageClient)
	kind, harness = c.kind, c.harness
	if kind == "" {
		kind = UsageNone
	}
	if harness == "" {
		harness = UsageNone
	}
	return kind, harness
}

// usageBufferKey identifies one buffered §8.7 row.
type usageBufferKey struct {
	tenant, day, layer, artifact, kind, harness string
}

// UsageCounter buffers the §8.7 usage counts of the calls one replica serves
// and adds them to the RegistryStore on Flush. Adding is commutative, so
// replicas that flush to one store produce totals across every replica.
//
// mu guards pending; every method takes it, so the counter is safe for
// concurrent use by request goroutines and the flush loop.
//
// Spec: §8.7
type UsageCounter struct {
	clock   clock.Clock
	mu      sync.Mutex
	pending map[usageBufferKey]store.UsageCount
}

// NewUsageCounter returns an empty counter that dates each count by c in UTC.
// A nil c uses the real clock.
func NewUsageCounter(c clock.Clock) *UsageCounter {
	if c == nil {
		c = clock.Real{}
	}
	return &UsageCounter{clock: c, pending: map[usageBufferKey]store.UsageCount{}}
}

// add buffers one count for the call's artifact under today's UTC date.
func (u *UsageCounter) add(ctx context.Context, tenant, layer, artifact string, loads, syncs, hits int64) {
	if u == nil || layer == "" || artifact == "" {
		return
	}
	kind, harness := usageClientFrom(ctx)
	day := u.clock.Now().UTC().Format(store.UsageDayLayout)
	k := usageBufferKey{tenant: tenant, day: day, layer: layer, artifact: artifact, kind: kind, harness: harness}
	u.mu.Lock()
	defer u.mu.Unlock()
	c, ok := u.pending[k]
	if !ok {
		c = store.UsageCount{TenantID: tenant, Day: day, Layer: layer, ArtifactID: artifact, ClientKind: kind, Harness: harness}
	}
	c.Loads += loads
	c.Syncs += syncs
	c.SearchHits += hits
	u.pending[k] = c
}

// recordLoad buffers one resolved load, as a sync when the request's client
// kind is sync and as a load otherwise.
//
// Spec: §8.7
func (u *UsageCounter) recordLoad(ctx context.Context, tenant, layer, artifact string) {
	if kind, _ := usageClientFrom(ctx); kind == "sync" {
		u.add(ctx, tenant, layer, artifact, 0, 1, 0)
		return
	}
	u.add(ctx, tenant, layer, artifact, 1, 0, 0)
}

// recordSearchHit buffers one search hit.
func (u *UsageCounter) recordSearchHit(ctx context.Context, tenant, layer, artifact string) {
	u.add(ctx, tenant, layer, artifact, 0, 0, 1)
}

// Flush adds the buffered counts to st, one AddUsageCounts call per tenant,
// and clears what it added. A tenant whose add fails keeps its counts in the
// buffer, merged with the counts buffered since, so the next flush that
// succeeds adds them. It returns the first error.
//
// Spec: §8.7
func (u *UsageCounter) Flush(ctx context.Context, st store.Store) error {
	if u == nil {
		return nil
	}
	u.mu.Lock()
	taken := u.pending
	u.pending = map[usageBufferKey]store.UsageCount{}
	u.mu.Unlock()

	byTenant := map[string][]store.UsageCount{}
	for _, c := range taken {
		byTenant[c.TenantID] = append(byTenant[c.TenantID], c)
	}
	var first error
	for tenant, counts := range byTenant {
		if err := st.AddUsageCounts(ctx, tenant, counts); err != nil {
			if first == nil {
				first = err
			}
			u.restore(counts)
		}
	}
	return first
}

// restore merges counts back into the buffer after a failed flush.
func (u *UsageCounter) restore(counts []store.UsageCount) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, c := range counts {
		k := usageBufferKey{tenant: c.TenantID, day: c.Day, layer: c.Layer, artifact: c.ArtifactID, kind: c.ClientKind, harness: c.Harness}
		p, ok := u.pending[k]
		if !ok {
			u.pending[k] = c
			continue
		}
		p.Loads += c.Loads
		p.Syncs += c.Syncs
		p.SearchHits += c.SearchHits
		u.pending[k] = p
	}
}

// WithUsageCounts wires the §8.7 usage counter. A nil counter records nothing,
// and the usage report then reads only rows another replica stored.
func (r *Registry) WithUsageCounts(u *UsageCounter) *Registry {
	r.usageCounts = u
	return r
}
