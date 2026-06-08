package core

import (
	"context"
	"sort"
	"sync"
)

// UsageSignals records and reports the §3.3 learn-from-usage signal: how
// frequently each artifact is accessed through the meta-tools. spec: §12 —
// the "Catalog grows too large" mitigation credits "learn-from-usage
// reranking surfaces signal-based ordering." The recorded signal feeds
// search_artifacts ranking and the load_domain notable ordering so artifacts
// agents actually load rise above equally-relevant but unused ones. A nil
// UsageSignals on the Registry leaves ranking on its lexical, vector, and
// author-curated order alone.
type UsageSignals interface {
	// Record notes one access to (tenantID, artifactID). The sessionID is the
	// §3.3 correlation key the caller threads through; a repeated access in
	// the same session still counts so a heavily reused artifact ranks above a
	// one-off load. An empty artifactID is ignored.
	Record(ctx context.Context, tenantID, artifactID, sessionID string)
	// Ranking returns the tenant's accessed artifact IDs ordered most-accessed
	// first, ties broken by ID for determinism. IDs never accessed are absent,
	// so callers fuse this partial order with the lexical or vector ranks.
	Ranking(ctx context.Context, tenantID string) []string
}

// MemoryUsageSignals is an in-process UsageSignals backed by per-tenant access
// counters. It carries the §3.3 signal for the life of the registry process,
// which is sufficient for the §12 reranking: the signal is advisory ordering,
// not durable state, and a restart simply relearns it from live traffic.
type MemoryUsageSignals struct {
	mu     sync.Mutex
	counts map[string]map[string]int // tenantID -> artifactID -> access count
}

// NewMemoryUsageSignals returns an empty in-process usage-signal store.
func NewMemoryUsageSignals() *MemoryUsageSignals {
	return &MemoryUsageSignals{counts: map[string]map[string]int{}}
}

// Record increments the access counter for (tenantID, artifactID).
func (m *MemoryUsageSignals) Record(_ context.Context, tenantID, artifactID, _ string) {
	if artifactID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant := m.counts[tenantID]
	if tenant == nil {
		tenant = map[string]int{}
		m.counts[tenantID] = tenant
	}
	tenant[artifactID]++
}

// Ranking returns the tenant's artifact IDs ordered by descending access
// count, ties broken alphabetically.
func (m *MemoryUsageSignals) Ranking(_ context.Context, tenantID string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant := m.counts[tenantID]
	if len(tenant) == 0 {
		return nil
	}
	ids := make([]string, 0, len(tenant))
	for id := range tenant {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if tenant[ids[i]] != tenant[ids[j]] {
			return tenant[ids[i]] > tenant[ids[j]]
		}
		return ids[i] < ids[j]
	})
	return ids
}

// recordUsage notes an artifact access against the §3.3 usage signal when one
// is wired. A nil signal store is a no-op.
func (r *Registry) recordUsage(ctx context.Context, artifactID, sessionID string) {
	if r.usage == nil {
		return
	}
	r.usage.Record(ctx, r.tenantFor(ctx), artifactID, sessionID)
}

// usageRanking returns the tenant's learn-from-usage ranking restricted to the
// allowed set, preserving usage order. It returns nil when no signal store is
// wired or nothing in the allowed set has been accessed, so callers cheaply
// skip the rerank. spec: §12.
func (r *Registry) usageRanking(ctx context.Context, allowed map[string]bool) []string {
	if r.usage == nil {
		return nil
	}
	ranked := r.usage.Ranking(ctx, r.tenantFor(ctx))
	if len(ranked) == 0 {
		return nil
	}
	out := make([]string, 0, len(ranked))
	for _, id := range ranked {
		if allowed == nil || allowed[id] {
			out = append(out, id)
		}
	}
	return out
}
