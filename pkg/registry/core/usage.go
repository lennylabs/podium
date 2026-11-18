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
type usageSnapshot struct {
	readAt     time.Time
	loads      map[usageRef]int64
	refreshing bool
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
		loads := snap.loads
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
		return snap.loads
	}
	snap.loads = loads
	return loads
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
	stored := r.usageRank.stored(ctx, r.store, tenant)
	pending := r.usageCounts.pendingLoads(tenant)
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
