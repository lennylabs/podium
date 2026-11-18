package storetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

func usageRow(day, layer, id, kind, harness string, loads, syncs, hits int64) store.UsageCount {
	return store.UsageCount{Day: day, Layer: layer, ArtifactID: id, ClientKind: kind, Harness: harness, Loads: loads, Syncs: syncs, SearchHits: hits}
}

// usageSummary renders a tenant's listed rows in one comparable string, so a
// case asserts the whole stored state at once.
func usageSummary(t *testing.T, s store.Store, tenant, from, to string) string {
	t.Helper()
	rows, err := s.ListUsageCounts(context.Background(), tenant, from, to)
	must(t, err)
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		parts = append(parts, fmt.Sprintf("%s|%s|%s|%s|%s|%s=%d/%d/%d",
			r.TenantID, r.Day, r.Layer, r.ArtifactID, r.ClientKind, r.Harness, r.Loads, r.Syncs, r.SearchHits))
	}
	return strings.Join(parts, " ")
}

// Spec: §8.7 — adding counts adds to the stored row of each key, so two adds
// of one key, in one call or in two, sum. A key that differs in any member is
// its own row, a row whose counters are all zero is not stored, and rows come
// back ordered by day, layer, artifact, client kind, and harness within the
// requested days, both ends included.
func usageCountsAdd(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	must(t, s.AddUsageCounts(ctx, "a", []store.UsageCount{
		usageRow("2026-11-02", "team", "x", "mcp", "claude-code", 2, 0, 1),
		usageRow("2026-11-02", "team", "x", "mcp", "claude-code", 1, 0, 0),
		usageRow("2026-11-02", "team", "x", "sync", "cursor", 0, 3, 0),
		usageRow("2026-11-01", "org", "y", "none", "none", 1, 0, 4),
		usageRow("2026-11-03", "team", "x", "mcp", "claude-code", 0, 0, 0),
		usageRow("2026-11-05", "team", "x", "cli", "none", 9, 0, 0),
	}))
	must(t, s.AddUsageCounts(ctx, "a", []store.UsageCount{usageRow("2026-11-02", "team", "x", "mcp", "claude-code", 4, 0, 2)}))
	got := usageSummary(t, s, "a", "2026-11-01", "2026-11-04")
	want := "a|2026-11-01|org|y|none|none=1/0/4 a|2026-11-02|team|x|mcp|claude-code=7/0/3 a|2026-11-02|team|x|sync|cursor=0/3/0"
	if got != want {
		t.Errorf("usage rows:\n got %s\nwant %s", got, want)
	}
}

// Spec: §8.7 — counts are per tenant: one tenant's adds, lists, and rows are
// invisible to another.
func usageCountsScopedToTenant(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	mustCreateTenant(t, s, "b")
	must(t, s.AddUsageCounts(ctx, "a", []store.UsageCount{usageRow("2026-11-02", "team", "x", "mcp", "none", 1, 0, 0)}))
	must(t, s.AddUsageCounts(ctx, "b", []store.UsageCount{usageRow("2026-11-02", "team", "x", "mcp", "none", 5, 0, 0)}))
	if a, b := usageSummary(t, s, "a", "2026-11-01", "2026-11-30"), usageSummary(t, s, "b", "2026-11-01", "2026-11-30"); a != "a|2026-11-02|team|x|mcp|none=1/0/0" || b != "b|2026-11-02|team|x|mcp|none=5/0/0" {
		t.Errorf("tenant rows: a=%q b=%q", a, b)
	}
}

// Spec: §8.7 — a count with no layer, artifact, client kind, or harness, a day
// that is not a YYYY-MM-DD date, or a negative counter is refused with
// ErrInvalidUsageCount, and the call stores none of its counts.
func usageCountsInvalid(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	good := usageRow("2026-11-02", "team", "x", "mcp", "none", 1, 0, 0)
	for _, bad := range []store.UsageCount{
		usageRow("2026-11-02", "", "x", "mcp", "none", 1, 0, 0),
		usageRow("2026-11-02", "team", "", "mcp", "none", 1, 0, 0),
		usageRow("2026-11-02", "team", "x", "", "none", 1, 0, 0),
		usageRow("2026-11-02", "team", "x", "mcp", "", 1, 0, 0),
		usageRow("11/02/2026", "team", "x", "mcp", "none", 1, 0, 0),
		usageRow("2026-11-02", "team", "x", "mcp", "none", -1, 0, 0),
	} {
		if err := s.AddUsageCounts(ctx, "a", []store.UsageCount{good, bad}); !errors.Is(err, store.ErrInvalidUsageCount) {
			t.Errorf("AddUsageCounts(%+v) = %v, want ErrInvalidUsageCount", bad, err)
		}
	}
	if got := usageSummary(t, s, "a", "2026-01-01", "2026-12-31"); got != "" {
		t.Errorf("a refused call stored rows: %s", got)
	}
}

// Spec: §8.7 — retention deletes the rows of every tenant dated before the
// given day and keeps that day and later ones.
func usageCountsDeleteBefore(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	mustCreateTenant(t, s, "b")
	for _, tenant := range []string{"a", "b"} {
		must(t, s.AddUsageCounts(ctx, tenant, []store.UsageCount{
			usageRow("2026-10-30", "team", "x", "mcp", "none", 1, 0, 0),
			usageRow("2026-10-31", "team", "x", "mcp", "none", 2, 0, 0),
			usageRow("2026-11-01", "team", "x", "mcp", "none", 3, 0, 0),
		}))
	}
	n, err := s.DeleteUsageBefore(ctx, "2026-10-31")
	must(t, err)
	a, b := usageSummary(t, s, "a", "2026-01-01", "2026-12-31"), usageSummary(t, s, "b", "2026-01-01", "2026-12-31")
	if n != 2 || a != "a|2026-10-31|team|x|mcp|none=2/0/0 a|2026-11-01|team|x|mcp|none=3/0/0" || b != strings.ReplaceAll(a, "a|", "b|") {
		t.Errorf("DeleteUsageBefore removed %d; a=%q b=%q", n, a, b)
	}
}

// Spec: §8.7 — adding is commutative, so concurrent adds to one key, as two
// replicas flushing at once produce, all land.
func usageCountsConcurrentAdd(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	const workers = 8
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = s.AddUsageCounts(ctx, "a", []store.UsageCount{usageRow("2026-11-02", "team", "x", "mcp", "none", 1, 1, 1)})
		}(i)
	}
	wg.Wait()
	must(t, errors.Join(errs...))
	if got := usageSummary(t, s, "a", "2026-11-02", "2026-11-02"); got != "a|2026-11-02|team|x|mcp|none=8/8/8" {
		t.Errorf("concurrent adds: %s, want 8/8/8", got)
	}
}
