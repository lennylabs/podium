package core

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// UsageReportDefaultDays is the §8.7 window the report reads when the request
// names none, capped by the retention.
const UsageReportDefaultDays = 30

// Limits on the §8.7 report's repeated parameters.
const (
	UsageMaxLayers    = 100
	UsageMaxArtifacts = 500
)

// UsageQuery holds the §8.7 report's parameters. From and To are optional
// YYYY-MM-DD dates and Days an optional window length; Days cannot be combined
// with From or To. Retention is the configured PODIUM_USAGE_RETENTION_DAYS,
// which bounds the window. LayerIDs and ArtifactIDs, when non-empty, restrict
// the report to the named layers and artifacts. Granularity is "", "none",
// "day", "week", or "month"; "" is "none".
type UsageQuery struct {
	From        string
	To          string
	Days        int
	Granularity string
	Retention   int
	LayerIDs    []string
	ArtifactIDs []string
}

// UsageCounts is the three §8.7 counters.
type UsageCounts struct {
	Loads      int64 `json:"loads"`
	Syncs      int64 `json:"syncs"`
	SearchHits int64 `json:"search_hits"`
}

func (c *UsageCounts) add(row store.UsageCount) {
	c.Loads += row.Loads
	c.Syncs += row.Syncs
	c.SearchHits += row.SearchHits
}

func (c UsageCounts) zero() bool { return c.Loads == 0 && c.Syncs == 0 && c.SearchHits == 0 }

// UsagePeriod is one period of a §8.7 series: the counts of the UTC days from
// Start to End, both included.
type UsagePeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
	UsageCounts
}

// UsageTotals is a §8.7 total with its client-kind and harness breakdowns and,
// under a granularity other than none, its series.
type UsageTotals struct {
	UsageCounts
	ByClient  map[string]UsageCounts `json:"by_client"`
	ByHarness map[string]UsageCounts `json:"by_harness"`
	Series    []UsagePeriod          `json:"series,omitempty"`
}

// UsageLayer is one layer's §8.7 totals.
type UsageLayer struct {
	Layer string `json:"layer"`
	UsageTotals
}

// UsageArtifact is one artifact's §8.7 totals. LastLoaded and LastSynced are
// the latest days of the window with a non-zero loads or syncs count.
type UsageArtifact struct {
	ArtifactID string `json:"artifact_id"`
	Type       string `json:"type"`
	Layer      string `json:"layer"`
	UsageTotals
	LastLoaded *string `json:"last_loaded"`
	LastSynced *string `json:"last_synced"`
}

// UsageReport is the §8.7 GET /v1/usage response.
type UsageReport struct {
	Days      int             `json:"days"`
	From      string          `json:"from"`
	To        string          `json:"to"`
	Totals    UsageTotals     `json:"totals"`
	Layers    []UsageLayer    `json:"layers"`
	Artifacts []UsageArtifact `json:"artifacts"`
}

// usageWindow is the report's window of UTC days, oldest first.
type usageWindow struct {
	days []string
}

func (w usageWindow) from() string { return w.days[0] }
func (w usageWindow) to() string   { return w.days[len(w.days)-1] }

// windowBetween returns the UTC days from first to last, both included.
func windowBetween(first, last time.Time) usageWindow {
	var days []string
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		days = append(days, d.Format(store.UsageDayLayout))
	}
	return usageWindow{days: days}
}

// resolveUsageWindow returns the window q selects at now, or reports a
// parameter §8.7 rejects: days combined with from or to, a date that does not
// parse or lies outside the retained days, a from after to, a days outside 1
// to the retention, and too many layers or artifacts.
//
// Spec: §8.7
func resolveUsageWindow(now time.Time, q UsageQuery) (usageWindow, error) {
	retention := max(q.Retention, 1)
	if !validGranularity(q.Granularity) {
		return usageWindow{}, fmt.Errorf("%w: granularity must be none, day, week, or month, got %q", ErrInvalidArgument, q.Granularity)
	}
	if len(q.LayerIDs) > UsageMaxLayers || len(q.ArtifactIDs) > UsageMaxArtifacts {
		return usageWindow{}, fmt.Errorf("%w: at most %d layers and %d artifacts", ErrInvalidArgument, UsageMaxLayers, UsageMaxArtifacts)
	}
	today := now.UTC().Truncate(24 * time.Hour)
	firstKept := today.AddDate(0, 0, 1-retention)
	if q.Days != 0 {
		if q.From != "" || q.To != "" {
			return usageWindow{}, fmt.Errorf("%w: days cannot be combined with from or to", ErrInvalidArgument)
		}
		if q.Days < 1 || q.Days > retention {
			return usageWindow{}, fmt.Errorf("%w: days must be an integer from 1 to %d", ErrInvalidArgument, retention)
		}
		return windowBetween(today.AddDate(0, 0, 1-q.Days), today), nil
	}
	last, err := usageDate(q.To, today, firstKept, today)
	if err != nil {
		return usageWindow{}, err
	}
	first := last.AddDate(0, 0, 1-UsageReportDefaultDays)
	if first.Before(firstKept) {
		first = firstKept
	}
	if first, err = usageDate(q.From, first, firstKept, today); err != nil {
		return usageWindow{}, err
	}
	if first.After(last) {
		return usageWindow{}, fmt.Errorf("%w: from %s is after to %s", ErrInvalidArgument, q.From, last.Format(store.UsageDayLayout))
	}
	return windowBetween(first, last), nil
}

// usageDate parses raw as a YYYY-MM-DD date within [lo, hi], or returns def
// for an empty raw.
func usageDate(raw string, def, lo, hi time.Time) (time.Time, error) {
	if raw == "" {
		return def, nil
	}
	d, err := time.Parse(store.UsageDayLayout, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %q is not a YYYY-MM-DD date", ErrInvalidArgument, raw)
	}
	if d.Before(lo) || d.After(hi) {
		return time.Time{}, fmt.Errorf("%w: %s lies outside the retained days %s to %s", ErrInvalidArgument,
			raw, lo.Format(store.UsageDayLayout), hi.Format(store.UsageDayLayout))
	}
	return d, nil
}

// UsageReport answers the §8.7 usage report for id. Every total sums only
// rows whose layer is in the caller's effective view, the artifact list holds
// every artifact the caller can see at its current served version, and a
// layer or artifact the caller cannot see answers as one that names nothing.
//
// Spec: §8.7
func (r *Registry) UsageReport(ctx context.Context, id layer.Identity, q UsageQuery) (*UsageReport, error) {
	w, err := resolveUsageWindow(r.usageNow(), q)
	if err != nil {
		return nil, err
	}
	visible, err := r.visibleManifests(ctx, id)
	if err != nil {
		return nil, err
	}
	served := dedupeLatest(visible)
	layers := r.usageLayers(ctx, id, served)
	rows, err := r.store.ListUsageCounts(ctx, r.TenantFor(ctx), w.from(), w.to())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if len(q.LayerIDs) > 0 {
		layers = keepLayers(layers, q.LayerIDs)
	}
	served = keepArtifactsIn(served, layers)
	allowed := make(map[string]bool, len(layers))
	for _, l := range layers {
		allowed[l] = true
	}
	kept := rows[:0:0]
	for _, row := range rows {
		if allowed[row.Layer] {
			kept = append(kept, row)
		}
	}
	return buildUsageReport(w, layers, served, kept, q), nil
}

// usageNow is the report's clock: the counter's when one is wired, so a test
// that freezes the counter's clock also freezes the window.
func (r *Registry) usageNow() time.Time {
	if r.usageCounts != nil {
		return r.usageCounts.clock.Now()
	}
	return time.Now().UTC()
}

// usageLayers returns the caller's effective layers in §4.6 precedence order.
// A public-mode caller sees every layer. A registry with no layer list, such
// as one serving a filesystem source, lists the layers of the artifacts the
// caller can see.
func (r *Registry) usageLayers(ctx context.Context, id layer.Identity, served []store.ManifestRecord) []string {
	resolved := r.resolveLayers(ctx)
	if len(resolved) == 0 {
		seen := map[string]bool{}
		var out []string
		for _, m := range served {
			if m.Layer != "" && !seen[m.Layer] {
				seen[m.Layer] = true
				out = append(out, m.Layer)
			}
		}
		sort.Strings(out)
		return out
	}
	eff := resolved
	if !id.IsPublic {
		eff = layer.EffectiveLayersWith(resolved, id, r.resolveGroup)
	}
	out := make([]string, len(eff))
	for i, l := range eff {
		out[i] = l.ID
	}
	return out
}

// keepLayers returns the entries of layers that named holds, in the order of
// layers, so a hidden or unknown name drops out.
func keepLayers(layers, named []string) []string {
	want := make(map[string]bool, len(named))
	for _, l := range named {
		want[l] = true
	}
	out := []string{}
	for _, l := range layers {
		if want[l] {
			out = append(out, l)
		}
	}
	return out
}

// keepArtifactsIn returns the records served from one of layers.
func keepArtifactsIn(served []store.ManifestRecord, layers []string) []store.ManifestRecord {
	in := make(map[string]bool, len(layers))
	for _, l := range layers {
		in[l] = true
	}
	out := served[:0:0]
	for _, m := range served {
		if in[m.Layer] {
			out = append(out, m)
		}
	}
	return out
}

// usageAccumulator gathers one total from rows.
type usageAccumulator struct {
	totals UsageTotals
	daily  map[string]*UsageCounts
	lastL  string
	lastS  string
}

func newUsageAccumulator() *usageAccumulator {
	return &usageAccumulator{
		totals: UsageTotals{ByClient: map[string]UsageCounts{}, ByHarness: map[string]UsageCounts{}},
		daily:  map[string]*UsageCounts{},
	}
}

func (a *usageAccumulator) add(row store.UsageCount) {
	a.totals.add(row)
	c := a.totals.ByClient[row.ClientKind]
	c.add(row)
	a.totals.ByClient[row.ClientKind] = c
	h := a.totals.ByHarness[row.Harness]
	h.add(row)
	a.totals.ByHarness[row.Harness] = h
	d := a.daily[row.Day]
	if d == nil {
		d = &UsageCounts{}
		a.daily[row.Day] = d
	}
	d.add(row)
	if row.Loads > 0 && row.Day > a.lastL {
		a.lastL = row.Day
	}
	if row.Syncs > 0 && row.Day > a.lastS {
		a.lastS = row.Day
	}
}

// result returns the total, with a series over w at granularity unless it is
// none. A breakdown entry whose counts are all zero is dropped.
func (a *usageAccumulator) result(w usageWindow, granularity string) UsageTotals {
	t := a.totals
	for k, v := range t.ByClient {
		if v.zero() {
			delete(t.ByClient, k)
		}
	}
	for k, v := range t.ByHarness {
		if v.zero() {
			delete(t.ByHarness, k)
		}
	}
	t.Series = usageSeries(w, granularity, a.daily)
	return t
}

// buildUsageReport aggregates rows, already restricted to the caller's view,
// into the report's totals, layers, and artifacts.
func buildUsageReport(w usageWindow, layers []string, served []store.ManifestRecord, rows []store.UsageCount, q UsageQuery) *UsageReport {
	total := newUsageAccumulator()
	byLayer := map[string]*usageAccumulator{}
	byArtifact := map[string]*usageAccumulator{}
	for _, l := range layers {
		byLayer[l] = newUsageAccumulator()
	}
	for _, m := range served {
		byArtifact[m.ArtifactID] = newUsageAccumulator()
	}
	for _, row := range rows {
		total.add(row)
		byLayer[row.Layer].add(row)
		if a := byArtifact[row.ArtifactID]; a != nil {
			a.add(row)
		}
	}
	rep := &UsageReport{
		Days:      len(w.days),
		From:      w.from(),
		To:        w.to(),
		Totals:    total.result(w, q.Granularity),
		Layers:    make([]UsageLayer, 0, len(layers)),
		Artifacts: make([]UsageArtifact, 0, len(served)),
	}
	for _, l := range layers {
		rep.Layers = append(rep.Layers, UsageLayer{Layer: l, UsageTotals: byLayer[l].result(w, q.Granularity)})
	}
	named := make(map[string]bool, len(q.ArtifactIDs))
	for _, id := range q.ArtifactIDs {
		named[id] = true
	}
	for _, m := range served {
		if len(named) > 0 && !named[m.ArtifactID] {
			continue
		}
		a := byArtifact[m.ArtifactID]
		rep.Artifacts = append(rep.Artifacts, UsageArtifact{
			ArtifactID:  m.ArtifactID,
			Type:        m.Type,
			Layer:       m.Layer,
			UsageTotals: a.result(w, q.Granularity),
			LastLoaded:  dayOrNil(a.lastL),
			LastSynced:  dayOrNil(a.lastS),
		})
	}
	sortUsageArtifacts(rep.Artifacts)
	return rep
}

func dayOrNil(day string) *string {
	if day == "" {
		return nil
	}
	return &day
}

// sortUsageArtifacts orders the list by loads, syncs, and search hits, each
// descending, then by artifact ID.
func sortUsageArtifacts(list []UsageArtifact) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		switch {
		case a.Loads != b.Loads:
			return a.Loads > b.Loads
		case a.Syncs != b.Syncs:
			return a.Syncs > b.Syncs
		case a.SearchHits != b.SearchHits:
			return a.SearchHits > b.SearchHits
		}
		return a.ArtifactID < b.ArtifactID
	})
}
