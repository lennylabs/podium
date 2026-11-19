package storetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

func usageRow(day, layer, id, kind, harness string, loads, syncs, hits int64) store.UsageCount {
	return store.UsageCount{Day: day, Layer: layer, ArtifactID: id, Client: kind, Harness: harness, Loads: loads, Syncs: syncs, SearchHits: hits}
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
			r.TenantID, r.Day, r.Layer, r.ArtifactID, r.Client, r.Harness, r.Loads, r.Syncs, r.SearchHits))
	}
	return strings.Join(parts, " ")
}

// Spec: §8.7 — adding counts adds to the stored row of each key, so two adds
// of one key, in one call or in two, sum. A key that differs in any member is
// its own row, a row whose counters are all zero is not stored, and rows come
// back ordered by day, layer, artifact, client, and harness within the
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

// Spec: §8.7 — a count with no layer, artifact, client, or harness, a day
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

// Spec: §8.7 — purging a soft-deleted layer at the end of its recovery
// window deletes the layer's usage rows in the same step. A layer still
// inside its window, a live layer, and a layer of the same identifier in
// another tenant keep theirs.
func usageCountsPurgedWithLayer(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	mustCreateTenant(t, s, "b")
	now := time.Now().UTC()
	expired, recent := now.Add(-40*24*time.Hour), now.Add(-5*24*time.Hour)
	for _, l := range []store.LayerConfig{
		{TenantID: "a", ID: "gone", SourceType: "local", LocalPath: "/g", DeletedAt: &expired},
		{TenantID: "a", ID: "recent", SourceType: "local", LocalPath: "/r", DeletedAt: &recent},
		{TenantID: "a", ID: "live", SourceType: "local", LocalPath: "/l"},
		{TenantID: "b", ID: "gone", SourceType: "local", LocalPath: "/g"},
	} {
		must(t, s.PutLayerConfig(ctx, l))
	}
	for _, tenant := range []string{"a", "b"} {
		must(t, s.AddUsageCounts(ctx, tenant, []store.UsageCount{
			usageRow("2026-11-02", "gone", "x", "mcp", "none", 1, 0, 0),
			usageRow("2026-11-02", "recent", "x", "mcp", "none", 2, 0, 0),
			usageRow("2026-11-02", "live", "x", "mcp", "none", 3, 0, 0),
		}))
	}
	_, err := s.PurgeExpiredLayerDeletions(ctx, now.Add(-30*24*time.Hour))
	must(t, err)
	got := usageSummary(t, s, "a", "2026-11-01", "2026-11-03") + " ; " + usageSummary(t, s, "b", "2026-11-01", "2026-11-03")
	want := "a|2026-11-02|live|x|mcp|none=3/0/0 a|2026-11-02|recent|x|mcp|none=2/0/0 ; " +
		"b|2026-11-02|gone|x|mcp|none=1/0/0 b|2026-11-02|live|x|mcp|none=3/0/0 b|2026-11-02|recent|x|mcp|none=2/0/0"
	if got != want {
		t.Errorf("usage rows after purge:\n got %s\nwant %s", got, want)
	}
}

// Spec: §3.3, §8.7 — summing loads adds a layer and artifact's loads across
// the days of the range, clients, and harnesses; a pair with only syncs
// or search hits, a day outside the range, and another tenant's rows are left
// out; and pairs come back ordered by layer and artifact.
func usageLoadsSummed(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	mustCreateTenant(t, s, "b")
	must(t, s.AddUsageCounts(ctx, "a", []store.UsageCount{
		usageRow("2026-11-01", "team", "x", "mcp", "claude-code", 2, 0, 1),
		usageRow("2026-11-02", "team", "x", "sdk", "none", 3, 0, 0),
		usageRow("2026-11-02", "org", "x", "mcp", "cursor", 1, 0, 0),
		usageRow("2026-11-02", "team", "y", "sync", "claude-code", 0, 4, 2),
		usageRow("2026-10-31", "team", "z", "mcp", "none", 9, 0, 0),
	}))
	must(t, s.AddUsageCounts(ctx, "b", []store.UsageCount{usageRow("2026-11-02", "team", "x", "mcp", "none", 7, 0, 0)}))
	got, err := s.SumUsageLoads(ctx, "a", "2026-11-01", "2026-11-02")
	must(t, err)
	parts := make([]string, 0, len(got))
	for _, l := range got {
		parts = append(parts, fmt.Sprintf("%s|%s=%d", l.Layer, l.ArtifactID, l.Loads))
	}
	if want := "org|x=1 team|x=5"; strings.Join(parts, " ") != want {
		t.Errorf("summed loads = %q, want %q", strings.Join(parts, " "), want)
	}
}

// usageSumsRendered renders sums in one comparable string: the grouped
// columns, the counters, and the latest load and sync days.
func usageSumsRendered(sums []store.UsageSum, by []store.UsageDim) string {
	parts := make([]string, 0, len(sums))
	for _, s := range sums {
		keys := make([]string, 0, len(by))
		for _, d := range by {
			keys = append(keys, map[store.UsageDim]string{
				store.UsageByDay: s.Day, store.UsageByLayer: s.Layer, store.UsageByArtifact: s.ArtifactID,
				store.UsageByClient: s.Client, store.UsageByHarness: s.Harness,
			}[d])
		}
		parts = append(parts, fmt.Sprintf("%s=%d/%d/%d@%s/%s",
			strings.Join(keys, "|"), s.Loads, s.Syncs, s.SearchHits, s.LastLoaded, s.LastSynced))
	}
	return strings.Join(parts, " ")
}

// Spec: §8.7 — a usage sum adds the counters of the selected rows per group
// of the named key columns, with each group's latest day of loads and of
// syncs. It selects by tenant, by the days from From to To with both ends
// included, and by the layers, artifacts, and read-scope paths it names. An
// exact path matches one ID; a subtree path matches the ID and the IDs
// beneath it, and a path holding % or _ matches those characters literally.
// Sums come back in byte order of the grouped columns, an ungrouped sum over
// no rows returns nothing, and an unknown dimension is an error.
func usageSummed(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	mustCreateTenant(t, s, "b")
	must(t, s.AddUsageCounts(ctx, "a", []store.UsageCount{
		usageRow("2026-11-01", "team", "ops/greet", "mcp", "claude-code", 2, 0, 1),
		usageRow("2026-11-02", "team", "ops/greet", "sync", "cursor", 0, 3, 0),
		usageRow("2026-11-02", "team", "ops/greeter", "mcp", "cursor", 1, 0, 0),
		usageRow("2026-11-03", "org", "ops/greet", "sdk", "none", 4, 0, 2),
		usageRow("2026-11-03", "org", "pct%/a", "mcp", "claude-code", 1, 0, 0),
		usageRow("2026-11-03", "org", "pctX/a", "mcp", "claude-code", 5, 0, 0),
		usageRow("2026-11-04", "team", "opsx/a", "cli", "none", 0, 0, 3),
		usageRow("2026-10-31", "team", "ops/greet", "mcp", "claude-code", 9, 0, 0),
		usageRow("2026-11-02", "team", "ops", "mcp", "claude-code", 1, 0, 0),
	}))
	must(t, s.AddUsageCounts(ctx, "b", []store.UsageCount{usageRow("2026-11-02", "team", "ops/greet", "mcp", "claude-code", 7, 0, 0)}))
	split := []store.UsageDim{store.UsageByLayer, store.UsageByClient, store.UsageByHarness}
	byArtifact := []store.UsageDim{store.UsageByArtifact}
	cases := []struct {
		name   string
		tenant string
		q      store.UsageSumQuery
	}{
		{"layer split", "a", store.UsageSumQuery{Layers: []string{"team", "org"}, GroupBy: split}},
		{"artifact", "a", store.UsageSumQuery{Layers: []string{"team"}, GroupBy: byArtifact}},
		{"layer day", "a", store.UsageSumQuery{From: "2026-11-02", To: "2026-11-03", Layers: []string{"org", "team"}, GroupBy: []store.UsageDim{store.UsageByLayer, store.UsageByDay}}},
		{"named artifacts", "a", store.UsageSumQuery{ArtifactIDs: []string{"ops/greet", "pct%/a"}, GroupBy: []store.UsageDim{store.UsageByArtifact, store.UsageByClient, store.UsageByHarness}}},
		{"exact path", "a", store.UsageSumQuery{Paths: []store.UsagePath{{Path: "ops/greet"}}, GroupBy: byArtifact}},
		{"subtree path", "a", store.UsageSumQuery{Paths: []store.UsagePath{{Path: "ops", Subtree: true}}, GroupBy: byArtifact}},
		{"literal wildcard", "a", store.UsageSumQuery{Paths: []store.UsagePath{{Path: "pct%", Subtree: true}, {Path: "_ps/greet"}}, GroupBy: byArtifact}},
		{"whole catalog", "a", store.UsageSumQuery{Paths: []store.UsagePath{{Subtree: true}}, Layers: []string{"team"}}},
		{"no rows", "a", store.UsageSumQuery{Layers: []string{"absent"}}},
		{"other tenant", "b", store.UsageSumQuery{GroupBy: []store.UsageDim{store.UsageByClient}}},
		{"one day", "a", store.UsageSumQuery{From: "2026-10-31", To: "2026-10-31", GroupBy: byArtifact}},
	}
	var got []string
	for _, c := range cases {
		q := c.q
		if q.From == "" {
			q.From, q.To = "2026-11-01", "2026-11-04"
		}
		sums, err := s.SumUsage(ctx, c.tenant, q)
		must(t, err)
		got = append(got, c.name+": "+usageSumsRendered(sums, q.GroupBy))
	}
	_, err := s.SumUsage(ctx, "a", store.UsageSumQuery{From: "2026-11-01", To: "2026-11-04", GroupBy: []store.UsageDim{99}})
	got = append(got, fmt.Sprintf("unknown dimension refused: %t", err != nil))
	want := []string{
		"layer split: org|mcp|claude-code=6/0/0@2026-11-03/ org|sdk|none=4/0/2@2026-11-03/ team|cli|none=0/0/3@/ team|mcp|claude-code=3/0/1@2026-11-02/ team|mcp|cursor=1/0/0@2026-11-02/ team|sync|cursor=0/3/0@/2026-11-02",
		"artifact: ops=1/0/0@2026-11-02/ ops/greet=2/3/1@2026-11-01/2026-11-02 ops/greeter=1/0/0@2026-11-02/ opsx/a=0/0/3@/",
		"layer day: org|2026-11-03=10/0/2@2026-11-03/ team|2026-11-02=2/3/0@2026-11-02/2026-11-02",
		"named artifacts: ops/greet|mcp|claude-code=2/0/1@2026-11-01/ ops/greet|sdk|none=4/0/2@2026-11-03/ ops/greet|sync|cursor=0/3/0@/2026-11-02 pct%/a|mcp|claude-code=1/0/0@2026-11-03/",
		"exact path: ops/greet=6/3/3@2026-11-03/2026-11-02",
		"subtree path: ops=1/0/0@2026-11-02/ ops/greet=6/3/3@2026-11-03/2026-11-02 ops/greeter=1/0/0@2026-11-02/",
		"literal wildcard: pct%/a=1/0/0@2026-11-03/",
		"whole catalog: =4/3/4@2026-11-02/2026-11-02",
		"no rows: ",
		"other tenant: mcp=7/0/0@2026-11-02/",
		"one day: ops/greet=9/0/0@2026-10-31/",
		"unknown dimension refused: true",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("usage sums:\n got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
