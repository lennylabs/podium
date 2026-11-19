package core

import (
	"context"
	"sort"
	"sync"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/audit"
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
	return context.WithValue(ctx, usageClientCtxKey{}, usageClient{name: name, harness: harness, sync: purpose == audit.PurposeSync})
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

// pendingCount is one buffered §8.7 row and the order of the call that first
// buffered it. Flush caps keys in that order, so a client or harness beyond the
// per-day cap is the one whose first call came later, as §8.7 states.
type pendingCount struct {
	store.UsageCount
	seq uint64
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
// mu guards pending, next, and held; every method takes it, so the counter
// is safe for concurrent use by request goroutines and the flush loop. flushing, a
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
	pending  map[usageBufferKey]pendingCount
	// next is the sequence number add gives the next new row; mu guards it.
	next uint64
	// maxKeys is the §8.7 per-tenant, per-day cap on distinct clients and on
	// distinct harnesses.
	maxKeys int
	// known holds the keys each tenant has recorded per day, keyed by tenant
	// and day. Only Flush reads and writes it, and the flushing semaphore
	// serializes Flush, so it needs no lock of its own.
	known map[[2]string]*usageDayKeys
	// held holds, per tenant and day, the clients and harnesses add may buffer
	// under their own names: the values add has counted and the values a
	// flush has read or admitted as recorded. add records any other value
	// under UsageOther once a set holds maxKeys values, so pending names at
	// most maxKeys values beyond those read as recorded, however many
	// distinct names callers send between flushes. Flush still applies the
	// exact cap; held only bounds what waits for it. mu guards it, because
	// add reads it on the request path.
	held map[[2]string]*usageDayKeys
}

// NewUsageCounter returns an empty counter that dates each count by c in UTC.
// A nil c uses the real clock.
func NewUsageCounter(c clock.Clock) *UsageCounter {
	if c == nil {
		c = clock.Real{}
	}
	return &UsageCounter{
		clock: c, flushing: make(chan struct{}, 1), pending: map[usageBufferKey]pendingCount{},
		maxKeys: DefaultUsageMaxClientKeys, known: map[[2]string]*usageDayKeys{}, held: map[[2]string]*usageDayKeys{},
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

// add buffers one count for the call's artifact under today's UTC date. A
// client or a harness the day's held set has no room for is buffered under
// UsageOther.
//
// Spec: §8.7
func (u *UsageCounter) add(ctx context.Context, tenant, layer, artifact string, loads, syncs, hits int64) {
	if u == nil || layer == "" || artifact == "" {
		return
	}
	client, harness, _ := usageClientFrom(ctx)
	day := u.clock.Now().UTC().Format(store.UsageDayLayout)
	u.mu.Lock()
	defer u.mu.Unlock()
	held := dayKeysIn(u.held, tenant, day)
	client = admitKey(held.clients, client, u.maxKeys)
	harness = admitKey(held.harnesses, harness, u.maxKeys)
	k := usageBufferKey{tenant: tenant, day: day, layer: layer, artifact: artifact, client: client, harness: harness}
	c, ok := u.pending[k]
	if !ok {
		c = pendingCount{
			UsageCount: store.UsageCount{TenantID: tenant, Day: day, Layer: layer, ArtifactID: artifact, Client: client, Harness: harness},
			seq:        u.next,
		}
		u.next++
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
	u.pending = map[usageBufferKey]pendingCount{}
	u.mu.Unlock()

	byTenant := map[string][]pendingCount{}
	for _, c := range taken {
		byTenant[c.TenantID] = append(byTenant[c.TenantID], c)
	}
	var first error
	for tenant, counts := range byTenant {
		sort.Slice(counts, func(i, j int) bool { return counts[i].seq < counts[j].seq })
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
	u.holdKnown()
	u.forgetOldDays()
	return first
}

// holdKnown merges every day's known keys into the held sets, so add keeps
// buffering a value a flush read or admitted as recorded under its own name
// once the held set is full. A value a flush recorded under UsageOther stays
// held; its later counts are buffered under its name and capped again by
// the next flush, which records them under UsageOther as before.
func (u *UsageCounter) holdKnown() {
	u.mu.Lock()
	defer u.mu.Unlock()
	for k, keys := range u.known {
		held := dayKeysIn(u.held, k[0], k[1])
		for v := range keys.clients {
			held.clients[v] = true
		}
		for v := range keys.harnesses {
			held.harnesses[v] = true
		}
	}
}

// dayKeysIn returns the keys sets holds for tenant on day, adding empty
// ones when it holds none.
func dayKeysIn(sets map[[2]string]*usageDayKeys, tenant, day string) *usageDayKeys {
	k := sets[[2]string{tenant, day}]
	if k == nil {
		k = &usageDayKeys{clients: map[string]bool{}, harnesses: map[string]bool{}}
		sets[[2]string{tenant, day}] = k
	}
	return k
}

// capKeys returns counts, in the order given, with each client, and each
// harness, beyond the tenant's per-day cap replaced by UsageOther.
// UsageUnknown and UsageOther never count toward the cap. The known keys of a
// day are read from the rows the store holds for it the first time a flush
// meets the day, and read again, at most once per flush, before admitting a
// key this replica has not seen while the set has room: another replica may
// have recorded keys since, and §8.7 lets replicas exceed the cap only until
// each reads the others'. A full set admits nothing, so it needs no read. A
// store read that fails returns its error, and the caller keeps the counts
// buffered.
//
// Spec: §8.7, §13.12
func (u *UsageCounter) capKeys(ctx context.Context, st store.Store, tenant string, counts []pendingCount) ([]store.UsageCount, error) {
	out := make([]store.UsageCount, 0, len(counts))
	read := map[string]bool{}
	for _, pc := range counts {
		c := pc.UsageCount
		keys := u.known[[2]string{tenant, c.Day}]
		if keys == nil || !read[c.Day] && keys.wouldAdd(c, u.maxKeys) {
			var err error
			if keys, err = u.readDayKeys(ctx, st, tenant, c.Day); err != nil {
				return nil, err
			}
			read[c.Day] = true
		}
		c.Client = admitKey(keys.clients, c.Client, u.maxKeys)
		c.Harness = admitKey(keys.harnesses, c.Harness, u.maxKeys)
		out = append(out, c)
	}
	return out, nil
}

// readDayKeys merges the clients and harnesses of the store's rows for tenant
// on day into the day's known keys, creating them on the first read, and
// returns them. The merge is unbounded: a key the store holds is recorded
// already, so it keeps its name. It reads the day's distinct client and
// harness pairs rather than its rows, which grow with the catalog.
func (u *UsageCounter) readDayKeys(ctx context.Context, st store.Store, tenant, day string) (*usageDayKeys, error) {
	rows, err := st.SumUsage(ctx, tenant, store.UsageSumQuery{
		From: day, To: day, GroupBy: []store.UsageDim{store.UsageByClient, store.UsageByHarness},
	})
	if err != nil {
		return nil, err
	}
	k := dayKeysIn(u.known, tenant, day)
	for _, r := range rows {
		if counted(r.Client) {
			k.clients[r.Client] = true
		}
		if counted(r.Harness) {
			k.harnesses[r.Harness] = true
		}
	}
	return k, nil
}

// wouldAdd reports whether capping c would add a client or a harness to k.
func (k *usageDayKeys) wouldAdd(c store.UsageCount, max int) bool {
	return admits(k.clients, c.Client, max) || admits(k.harnesses, c.Harness, max)
}

// counted reports whether key counts toward the cap: every value except
// UsageUnknown and UsageOther.
func counted(key string) bool {
	return key != UsageUnknown && key != UsageOther
}

// admits reports whether admitKey would add key to set.
func admits(set map[string]bool, key string, max int) bool {
	return counted(key) && !set[key] && len(set) < max
}

// admitKey returns key when the set already holds it, when it is UsageUnknown
// or UsageOther, or when the set holds fewer than max keys, adding it in the
// last case; otherwise it returns UsageOther.
func admitKey(set map[string]bool, key string, max int) string {
	if !counted(key) || set[key] {
		return key
	}
	if len(set) >= max {
		return UsageOther
	}
	set[key] = true
	return key
}

// forgetOldDays drops the known and held keys of days before yesterday,
// which no count flushed from now on carries except a count a failed flush
// restored, and that count's day is reseeded from the store.
func (u *UsageCounter) forgetOldDays() {
	cutoff := u.clock.Now().UTC().AddDate(0, 0, -1).Format(store.UsageDayLayout)
	for k := range u.known {
		if k[1] < cutoff {
			delete(u.known, k)
		}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	for k := range u.held {
		if k[1] < cutoff {
			delete(u.held, k)
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

// restore merges counts back into the buffer after a failed flush. A merged
// row keeps the restored row's sequence number, which is the earlier one,
// so the cap still meets it in call order.
func (u *UsageCounter) restore(counts []pendingCount) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, c := range counts {
		k := usageBufferKey{tenant: c.TenantID, day: c.Day, layer: c.Layer, artifact: c.ArtifactID, client: c.Client, harness: c.Harness}
		p, ok := u.pending[k]
		if !ok {
			u.pending[k] = c
			continue
		}
		c.Loads += p.Loads
		c.Syncs += p.Syncs
		c.SearchHits += p.SearchHits
		u.pending[k] = c
	}
}

// WithUsageCounts wires the §8.7 usage counter. A nil counter records nothing,
// and the usage report then reads only rows another replica stored.
func (r *Registry) WithUsageCounts(u *UsageCounter) *Registry {
	r.usageCounts = u
	return r
}
