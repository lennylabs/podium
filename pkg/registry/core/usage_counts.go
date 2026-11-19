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

// UsageUnknown is the client and the harness §8.7 records for a request that
// does not provide one, and UsageOther the value a client or harness beyond
// the per-day cap is recorded under.
const (
	UsageUnknown = "unknown"
	UsageOther   = "other"
)

// DefaultUsageMaxClientKeys is the §13.12 PODIUM_USAGE_MAX_CLIENT_KEYS
// default: the most distinct clients, and the most distinct harnesses, one
// tenant records per UTC day.
const DefaultUsageMaxClientKeys = 32

// usageClientCtxKey carries the §7.2.2 client name, harness, and purpose of
// the request a call serves.
type usageClientCtxKey struct{}

// usageClient is the request's normalized client name and harness, and
// whether it carries purpose=sync.
type usageClient struct {
	name, harness string
	sync          bool
}

// ContextWithUsageClient returns ctx carrying the request's §7.2.2 client
// name, harness, and purpose, after normalization, for the §8.7 usage counts.
// An empty name or harness is recorded as UsageUnknown, and purpose sync makes
// a resolved load count as a sync. The server sets it from the X-Podium-Client
// header; the value is advisory and feeds no access decision.
//
// Spec: §8.7, §7.2.2
func ContextWithUsageClient(ctx context.Context, name, harness, purpose string) context.Context {
	return context.WithValue(ctx, usageClientCtxKey{}, usageClient{name: name, harness: harness, sync: purpose == "sync"})
}

// usageClientFrom returns the client name, harness, and sync purpose ctx
// carries, with UsageUnknown for a name or harness not provided.
func usageClientFrom(ctx context.Context) (name, harness string, sync bool) {
	c, _ := ctx.Value(usageClientCtxKey{}).(usageClient)
	name, harness = c.name, c.harness
	if name == "" {
		name = UsageUnknown
	}
	if harness == "" {
		harness = UsageUnknown
	}
	return name, harness, c.sync
}

// usageBufferKey identifies one buffered §8.7 row.
type usageBufferKey struct {
	tenant, day, layer, artifact, client, harness string
}

// usageDayKeys is the set of clients and the set of harnesses one tenant has
// recorded on one UTC day, not counting UsageUnknown and UsageOther.
type usageDayKeys struct {
	clients, harnesses map[string]bool
}

// UsageCounter buffers the §8.7 usage counts of the calls one replica serves
// and adds them to the RegistryStore on Flush. Adding is commutative, so
// replicas that flush to one store produce totals across every replica.
//
// mu guards pending; every method takes it, so the counter is safe for
// concurrent use by request goroutines and the flush loop. flushing, a
// one-slot semaphore, serializes Flush: the shutdown flush runs while a
// periodic flush the shutdown cancelled may still be waiting on the store,
// and that flush puts its counts back only when its add fails. Without the
// wait, the shutdown flush would find the buffer empty and the restored
// counts would be lost at exit. A channel rather than a mutex lets the wait
// end at the flush's deadline, because a store call that ignores
// cancellation would otherwise hold the shutdown open.
//
// Spec: §8.7
type UsageCounter struct {
	clock    clock.Clock
	flushing chan struct{}
	mu       sync.Mutex
	pending  map[usageBufferKey]store.UsageCount
	// maxKeys is the §8.7 per-tenant, per-day cap on distinct clients and on
	// distinct harnesses.
	maxKeys int
	// known holds the keys each tenant has recorded per day, keyed by tenant
	// and day. Only Flush reads and writes it, and the flushing semaphore
	// serializes Flush, so it needs no lock of its own.
	known map[[2]string]*usageDayKeys
}

// NewUsageCounter returns an empty counter that dates each count by c in UTC.
// A nil c uses the real clock.
func NewUsageCounter(c clock.Clock) *UsageCounter {
	if c == nil {
		c = clock.Real{}
	}
	return &UsageCounter{
		clock: c, flushing: make(chan struct{}, 1), pending: map[usageBufferKey]store.UsageCount{},
		maxKeys: DefaultUsageMaxClientKeys, known: map[[2]string]*usageDayKeys{},
	}
}

// WithMaxClientKeys sets the §8.7 per-tenant, per-day cap on distinct clients
// and harnesses. A value below 1 keeps the default.
func (u *UsageCounter) WithMaxClientKeys(n int) *UsageCounter {
	if n >= 1 {
		u.maxKeys = n
	}
	return u
}

// add buffers one count for the call's artifact under today's UTC date.
func (u *UsageCounter) add(ctx context.Context, tenant, layer, artifact string, loads, syncs, hits int64) {
	if u == nil || layer == "" || artifact == "" {
		return
	}
	client, harness, _ := usageClientFrom(ctx)
	day := u.clock.Now().UTC().Format(store.UsageDayLayout)
	k := usageBufferKey{tenant: tenant, day: day, layer: layer, artifact: artifact, client: client, harness: harness}
	u.mu.Lock()
	defer u.mu.Unlock()
	c, ok := u.pending[k]
	if !ok {
		c = store.UsageCount{TenantID: tenant, Day: day, Layer: layer, ArtifactID: artifact, Client: client, Harness: harness}
	}
	c.Loads += loads
	c.Syncs += syncs
	c.SearchHits += hits
	u.pending[k] = c
}

// recordLoad buffers one resolved load, as a sync when the request carries
// purpose=sync and as a load otherwise.
//
// Spec: §8.7
func (u *UsageCounter) recordLoad(ctx context.Context, tenant, layer, artifact string) {
	if _, _, sync := usageClientFrom(ctx); sync {
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
// succeeds adds them. A flush that starts while another is in progress waits
// for it, and returns ctx's error, adding nothing, when ctx ends first. It
// returns the first error.
//
// Spec: §8.7
func (u *UsageCounter) Flush(ctx context.Context, st store.Store) error {
	if u == nil {
		return nil
	}
	select {
	case u.flushing <- struct{}{}:
		defer func() { <-u.flushing }()
	case <-ctx.Done():
		return ctx.Err()
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
		capped, err := u.capKeys(ctx, st, tenant, counts)
		if err == nil {
			err = st.AddUsageCounts(ctx, tenant, capped)
		}
		if err != nil {
			if first == nil {
				first = err
			}
			u.restore(counts)
		}
	}
	u.forgetOldDays()
	return first
}

// capKeys returns counts with each client, and each harness, beyond the
// tenant's per-day cap replaced by UsageOther. It seeds a day's known keys
// from the rows the store holds for that day the first time a flush meets
// the day, and adds each key it admits, so later flushes of this replica see
// them. UsageUnknown and UsageOther never count toward the cap. A store read
// that fails returns its error, and the caller keeps the counts buffered.
//
// Spec: §8.7, §13.12
func (u *UsageCounter) capKeys(ctx context.Context, st store.Store, tenant string, counts []store.UsageCount) ([]store.UsageCount, error) {
	out := make([]store.UsageCount, 0, len(counts))
	for _, c := range counts {
		keys, err := u.dayKeys(ctx, st, tenant, c.Day)
		if err != nil {
			return nil, err
		}
		c.Client = admitKey(keys.clients, c.Client, u.maxKeys)
		c.Harness = admitKey(keys.harnesses, c.Harness, u.maxKeys)
		out = append(out, c)
	}
	return out, nil
}

// dayKeys returns the known keys of tenant on day, reading them from the
// store's rows for the day the first time.
func (u *UsageCounter) dayKeys(ctx context.Context, st store.Store, tenant, day string) (*usageDayKeys, error) {
	if k, ok := u.known[[2]string{tenant, day}]; ok {
		return k, nil
	}
	rows, err := st.ListUsageCounts(ctx, tenant, day, day)
	if err != nil {
		return nil, err
	}
	k := &usageDayKeys{clients: map[string]bool{}, harnesses: map[string]bool{}}
	for _, r := range rows {
		admitKey(k.clients, r.Client, int(^uint(0)>>1))
		admitKey(k.harnesses, r.Harness, int(^uint(0)>>1))
	}
	u.known[[2]string{tenant, day}] = k
	return k, nil
}

// admitKey returns key when the set already holds it, when it is UsageUnknown
// or UsageOther, or when the set holds fewer than max keys, adding it in the
// last case; otherwise it returns UsageOther.
func admitKey(set map[string]bool, key string, max int) string {
	if key == UsageUnknown || key == UsageOther || set[key] {
		return key
	}
	if len(set) >= max {
		return UsageOther
	}
	set[key] = true
	return key
}

// forgetOldDays drops the known keys of days before yesterday, which no
// count flushed from now on carries except a count a failed flush restored,
// and that count's day is reseeded from the store.
func (u *UsageCounter) forgetOldDays() {
	cutoff := u.clock.Now().UTC().AddDate(0, 0, -1).Format(store.UsageDayLayout)
	for k := range u.known {
		if k[1] < cutoff {
			delete(u.known, k)
		}
	}
}

// pendingLoads returns the tenant's buffered loads per layer and artifact:
// the loads this replica served since its last successful flush, which the
// §3.3 reranking adds to the stored loads so a load counts at once. A nil
// counter has none.
func (u *UsageCounter) pendingLoads(tenant string) map[usageRef]int64 {
	if u == nil {
		return nil
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	out := map[usageRef]int64{}
	for k, c := range u.pending {
		if k.tenant == tenant && c.Loads > 0 {
			out[usageRef{id: k.artifact, layer: k.layer}] += c.Loads
		}
	}
	return out
}

// restore merges counts back into the buffer after a failed flush.
func (u *UsageCounter) restore(counts []store.UsageCount) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, c := range counts {
		k := usageBufferKey{tenant: c.TenantID, day: c.Day, layer: c.Layer, artifact: c.ArtifactID, client: c.Client, harness: c.Harness}
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
