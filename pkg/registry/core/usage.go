package core

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/store"
)

// UsageRankingWindowDays is the §3.3 learn-from-usage window: the loads of the
// last 30 UTC days, counting today, rank search results and notable entries.
const UsageRankingWindowDays = 30

// usageRef names one candidate of a rerank by its artifact ID and the layer
// that serves it, which is the layer its §8.7 counts are recorded under.
type usageRef struct {
	id, layer string
}

// UsageRanking reads the §8.7 stored load counts that drive the §3.3
// learn-from-usage reranking, and keeps one snapshot per tenant so a search
// reads the store at most once per refresh interval. The stored counts change
// only when a replica flushes, so the flush interval is the natural refresh.
//
// mu guards tenants. A snapshot's loads map is replaced, never mutated, so a
// caller reads the map it was handed without the lock.
//
// Spec: §3.3
type UsageRanking struct {
	clock     clock.Clock
	refresh   time.Duration
	retention int
	mu        sync.Mutex
	tenants   map[string]*usageSnapshot
}

// usageSnapshot is one tenant's stored loads at readAt. refreshing marks a
// read in progress, so concurrent searches keep using the previous loads
// rather than each reading the store.
//
// carried holds the loads this replica's flushes added to the store after
// the last read began, which that read does not include; carriedDuring holds
// those added while a read is in progress, which that read may not include.
// A read that succeeds replaces carried with carriedDuring, and one that
// fails merges the two. Without them, a flush emptied the replica's buffer
// and the loads it added stayed out of the ranking until the next read, up
// to a whole refresh interval later.
type usageSnapshot struct {
	readAt        time.Time
	loads         map[usageRef]int64
	refreshing    bool
	carried       map[usageRef]int64
	carriedDuring map[usageRef]int64
}

// NewUsageRanking returns a ranking that reads the stored counts again once a
// tenant's snapshot is older than refresh, over the §3.3 window capped by
// retentionDays. A nil c uses the real clock.
func NewUsageRanking(c clock.Clock, refresh time.Duration, retentionDays int) *UsageRanking {
	if c == nil {
		c = clock.Real{}
	}
	return &UsageRanking{clock: c, refresh: refresh, retention: retentionDays, tenants: map[string]*usageSnapshot{}}
}

// WithUsageRanking wires the §3.3 learn-from-usage reranking. Nil leaves
// search and load_domain on their lexical, vector, and author-curated order.
func (r *Registry) WithUsageRanking(u *UsageRanking) *Registry {
	r.usageRank = u
	r.usageCounts.withRanking(u)
	return r
}

// stored returns the tenant's stored loads, reading them again when the
// snapshot is older than the refresh interval. A failed read keeps the last
// loads and still counts as a read, so a store outage costs one attempt per
// interval rather than one per search: the ranking is advisory ordering.
//
// Spec: §3.3
func (u *UsageRanking) stored(ctx context.Context, st store.Store, tenant string) map[usageRef]int64 {
	now := u.clock.Now()
	u.mu.Lock()
	snap := u.tenants[tenant]
	if snap != nil && (snap.refreshing || now.Sub(snap.readAt) < u.refresh) {
		loads := snap.withCarried()
		u.mu.Unlock()
		return loads
	}
	if snap == nil {
		snap = &usageSnapshot{}
		u.tenants[tenant] = snap
	}
	snap.refreshing = true
	u.mu.Unlock()

	loads, err := u.read(ctx, st, tenant, now)

	u.mu.Lock()
	defer u.mu.Unlock()
	snap.refreshing = false
	snap.readAt = now
	if err != nil {
		log.Printf("usage ranking: read stored loads for tenant %s: %v; ranking by the last loads read", tenant, err)
		snap.carried = mergeLoads(snap.carried, snap.carriedDuring)
		snap.carriedDuring = nil
		return snap.withCarried()
	}
	snap.loads = loads
	snap.carried, snap.carriedDuring = snap.carriedDuring, nil
	return snap.withCarried()
}

// carry records loads a flush of this replica has just added to the store
// for tenant, which the ranking adds to the stored loads until a read that
// began after the add replaces them. A tenant with no snapshot needs
// nothing: its first read begins after the add.
//
// Spec: §3.3
func (u *UsageRanking) carry(tenant string, loads map[usageRef]int64) {
	if len(loads) == 0 {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	snap := u.tenants[tenant]
	switch {
	case snap == nil:
	case snap.refreshing:
		snap.carriedDuring = mergeLoads(snap.carriedDuring, loads)
	default:
		snap.carried = mergeLoads(snap.carried, loads)
	}
}

// withCarried returns the snapshot's stored loads plus its carried loads. It
// returns the stored map itself when nothing is carried; the caller holds
// UsageRanking.mu.
func (s *usageSnapshot) withCarried() map[usageRef]int64 {
	if len(s.carried) == 0 && len(s.carriedDuring) == 0 {
		return s.loads
	}
	return mergeLoads(mergeLoads(mergeLoads(nil, s.loads), s.carried), s.carriedDuring)
}

// mergeLoads adds src into dst, allocating dst when it is nil, and returns it.
func mergeLoads(dst, src map[usageRef]int64) map[usageRef]int64 {
	if dst == nil && len(src) > 0 {
		dst = make(map[usageRef]int64, len(src))
	}
	for k, n := range src {
		dst[k] += n
	}
	return dst
}

// read sums the tenant's stored loads over the window ending today.
func (u *UsageRanking) read(ctx context.Context, st store.Store, tenant string, now time.Time) (map[usageRef]int64, error) {
	days := min(UsageRankingWindowDays, max(u.retention, 1))
	today := now.UTC().Truncate(24 * time.Hour)
	rows, err := st.SumUsageLoads(ctx, tenant, today.AddDate(0, 0, 1-days).Format(store.UsageDayLayout), today.Format(store.UsageDayLayout))
	if err != nil {
		return nil, err
	}
	loads := make(map[usageRef]int64, len(rows))
	for _, row := range rows {
		loads[usageRef{id: row.ArtifactID, layer: row.Layer}] = row.Loads
	}
	return loads, nil
}

// usageRanking returns the candidates' IDs that have loads, most loaded
// first and ties by ID, counting each candidate's stored and unflushed loads
// under the layer that serves it. Counts recorded under another layer, which
// may be one the caller cannot see, never move a candidate. It returns nil
// when no ranking is wired or no candidate has loads, so callers skip the
// rerank.
//
// Spec: §3.3
func (r *Registry) usageRanking(ctx context.Context, refs []usageRef) []string {
	if r.usageRank == nil {
		return nil
	}
	tenant := r.TenantFor(ctx)
	// The buffer is read before the snapshot: a flush hands its loads to the
	// snapshot before it drops them from the buffer, so in this order a load
	// is counted in one of the two, or briefly in both, and never in neither.
	pending := r.usageCounts.pendingLoads(tenant)
	stored := r.usageRank.stored(ctx, r.store, tenant)
	type ranked struct {
		id    string
		loads int64
	}
	list := make([]ranked, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if seen[ref.id] {
			continue
		}
		seen[ref.id] = true
		if n := stored[ref] + pending[ref]; n > 0 {
			list = append(list, ranked{id: ref.id, loads: n})
		}
	}
	if len(list) == 0 {
		return nil
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].loads != list[j].loads {
			return list[i].loads > list[j].loads
		}
		return list[i].id < list[j].id
	})
	out := make([]string, len(list))
	for i, e := range list {
		out[i] = e.id
	}
	return out
}
