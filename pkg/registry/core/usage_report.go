package core

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/lennylabs/podium/internal/clock"
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
	UsageMaxTypes     = 20
)

// UsageQuery holds the §8.7 report's parameters. From and To are optional
// YYYY-MM-DD dates and Days an optional window length; Days cannot be combined
// with From or To. Retention is the configured PODIUM_USAGE_RETENTION_DAYS,
// which bounds the window. LayerIDs and ArtifactIDs, when non-empty, restrict
// the report to the named layers and artifacts. Granularity is "", "none",
// "day", "week", or "month"; "" is "none". Types, when non-empty, restricts
// the artifact list to artifacts of the named types, and Query to artifacts
// whose ID contains it. Sort and Order choose the list's order, "" for the
// default. Limit is the page size of the
// artifact list, 0 for the default, and Cursor an earlier response's
// next_cursor.
type UsageQuery struct {
	From        string
	To          string
	Days        int
	Granularity string
	Retention   int
	LayerIDs    []string
	ArtifactIDs []string
	Types       []string
	Query       string
	Sort        string
	Order       string
	Limit       int
	Cursor      string
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
	Days int    `json:"days"`
	From string `json:"from"`
	To   string `json:"to"`
	// Granularity is the granularity the series use, which a long window
	// makes coarser than the requested one.
	Granularity string          `json:"granularity"`
	Totals      UsageTotals     `json:"totals"`
	Layers      []UsageLayer    `json:"layers"`
	Artifacts   []UsageArtifact `json:"artifacts"`
	// ArtifactCount is the number of entries the artifact list holds across
	// every page, and NextCursor reads the page after this one, or is nil
	// on the last page.
	ArtifactCount int     `json:"artifact_count"`
	NextCursor    *string `json:"next_cursor"`
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
	if len(q.LayerIDs) > UsageMaxLayers || len(q.ArtifactIDs) > UsageMaxArtifacts || len(q.Types) > UsageMaxTypes {
		return usageWindow{}, fmt.Errorf("%w: at most %d layers, %d artifacts, and %d types", ErrInvalidArgument, UsageMaxLayers, UsageMaxArtifacts, UsageMaxTypes)
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
// The store sums the rows, so the report holds sums per layer, client,
// harness, day, and artifact rather than the window's rows.
//
// Spec: §8.7
func (r *Registry) UsageReport(ctx context.Context, id layer.Identity, q UsageQuery) (*UsageReport, error) {
	w, err := resolveUsageWindow(r.usageNow(), q)
	if err != nil {
		return nil, err
	}
	page, err := parseUsagePage(q)
	if err != nil {
		return nil, err
	}
	visible, err := r.visibleManifests(ctx, id)
	if err != nil {
		return nil, err
	}
	served := dedupeLatest(visible)
	layers := r.usageLayers(ctx, id, served)
	if len(q.LayerIDs) > 0 {
		layers = keepLayers(layers, q.LayerIDs)
	}
	served = keepArtifactsIn(served, layers)
	src := newUsageSums(r.store, r.TenantFor(ctx), w, layers, layer.ParseScopes(id.Scopes))
	return buildUsageReport(ctx, src, w, layers, served, q, page)
}

// usageSums reads the report's sums from the store, restricted to the rows
// the report may sum: those whose layer the report covers and, when the
// caller's token carries read scopes, whose artifact ID the scopes allow.
// visibleManifests narrows the artifact list by the same scopes, so without
// the second restriction the totals, the layer entries, and their breakdowns
// would add up artifacts the caller cannot read, and a layer holding one such
// artifact would give its counts away by subtraction.
//
// Spec: §8.7, §6.3.1
type usageSums struct {
	st     store.Store
	tenant string
	base   store.UsageSumQuery
	// none is set when the caller's view reaches no row: it covers no layer,
	// or its scopes grant no read. The store treats an empty filter as no
	// restriction, so such a report must not query.
	none bool
}

func newUsageSums(st store.Store, tenant string, w usageWindow, layers []string, scopes layer.ScopeSet) usageSums {
	src := usageSums{st: st, tenant: tenant, base: store.UsageSumQuery{From: w.from(), To: w.to(), Layers: layers}}
	if len(layers) == 0 {
		src.none = true
	}
	if scopes.Active() {
		grants := scopes.ReadGrants()
		if len(grants) == 0 {
			src.none = true
		}
		for _, g := range grants {
			src.base.Paths = append(src.base.Paths, store.UsagePath{Path: g.Path, Subtree: g.Subtree})
		}
	}
	return src
}

// sum returns the sums grouped by by, restricted to artifacts when it is
// non-empty.
func (s usageSums) sum(ctx context.Context, by []store.UsageDim, artifacts []string) ([]store.UsageSum, error) {
	if s.none {
		return nil, nil
	}
	q := s.base
	q.GroupBy = by
	q.ArtifactIDs = artifacts
	sums, err := s.st.SumUsage(ctx, s.tenant, q)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return sums, nil
}

// usageNow is the report's clock: the counter's when one is wired, so a test
// that freezes the counter's clock also freezes the window.
func (r *Registry) usageNow() time.Time {
	if r.usageCounts != nil {
		return r.usageCounts.clock.Now()
	}
	return clock.Real{}.Now().UTC()
}

// usageLayers returns the caller's effective layers in §4.6 precedence order.
// A public-mode caller sees every layer. A registry with no layer list, such
// as one serving a filesystem source, lists the layers of the artifacts the
// caller can see.
func (r *Registry) usageLayers(ctx context.Context, id layer.Identity, served []store.ManifestRecord) []string {
	resolved := r.resolveLayers(ctx)
	if r.unfiltered(resolved) {
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

// usageAccumulator gathers one total from the store's sums.
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

// addTotal adds a sum to the total and moves the latest load and sync days.
func (a *usageAccumulator) addTotal(s store.UsageSum) {
	a.totals.add(s.UsageCount)
	if s.LastLoaded > a.lastL {
		a.lastL = s.LastLoaded
	}
	if s.LastSynced > a.lastS {
		a.lastS = s.LastSynced
	}
}

// addSplit adds a sum grouped by client and harness to the breakdowns.
func (a *usageAccumulator) addSplit(s store.UsageSum) {
	c := a.totals.ByClient[s.Client]
	c.add(s.UsageCount)
	a.totals.ByClient[s.Client] = c
	h := a.totals.ByHarness[s.Harness]
	h.add(s.UsageCount)
	a.totals.ByHarness[s.Harness] = h
}

// addDay adds a sum grouped by day to the series' days.
func (a *usageAccumulator) addDay(s store.UsageSum) {
	d := a.daily[s.Day]
	if d == nil {
		d = &UsageCounts{}
		a.daily[s.Day] = d
	}
	d.add(s.UsageCount)
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

// The groupings the report reads. Each result is bounded by the number of
// layers, clients, harnesses, days, and artifacts rather than by the number
// of stored rows.
var (
	usageByLayerSplit    = []store.UsageDim{store.UsageByLayer, store.UsageByClient, store.UsageByHarness}
	usageByLayerDay      = []store.UsageDim{store.UsageByLayer, store.UsageByDay}
	usageByArtifact      = []store.UsageDim{store.UsageByArtifact}
	usageByArtifactSplit = []store.UsageDim{store.UsageByArtifact, store.UsageByClient, store.UsageByHarness}
	usageByArtifactDay   = []store.UsageDim{store.UsageByArtifact, store.UsageByDay}
)

// buildUsageReport reads the sums of src, already restricted to the caller's
// view, into the report's totals, layers, and one page of artifacts.
func buildUsageReport(ctx context.Context, src usageSums, w usageWindow, layers []string, served []store.ManifestRecord, q UsageQuery, page usagePage) (*UsageReport, error) {
	q.Granularity = servedGranularity(q.Granularity, len(w.days))
	series := q.Granularity != "" && q.Granularity != GranularityNone
	total := newUsageAccumulator()
	byLayer := map[string]*usageAccumulator{}
	for _, l := range layers {
		byLayer[l] = newUsageAccumulator()
	}
	if err := addLayerSums(ctx, src, series, total, byLayer); err != nil {
		return nil, err
	}
	byArtifact, err := artifactTotals(ctx, src, served)
	if err != nil {
		return nil, err
	}
	rep := &UsageReport{
		Days:        len(w.days),
		From:        w.from(),
		To:          w.to(),
		Granularity: q.Granularity,
		Totals:      total.result(w, q.Granularity),
		Layers:      make([]UsageLayer, 0, len(layers)),
	}
	for _, l := range layers {
		rep.Layers = append(rep.Layers, UsageLayer{Layer: l, UsageTotals: byLayer[l].result(w, q.Granularity)})
	}
	entries := usageEntries(served, byArtifact, q.ArtifactIDs, q.Types, page)
	rep.ArtifactCount = len(entries)
	selected, next := page.slice(entries)
	rep.NextCursor = next
	// The breakdowns and the series are read for the page alone, so a
	// response's size and the sums it reads follow the limit rather than
	// the catalog.
	if err := addPageSums(ctx, src, series, selected); err != nil {
		return nil, err
	}
	rep.Artifacts = make([]UsageArtifact, 0, len(selected))
	for _, e := range selected {
		rep.Artifacts = append(rep.Artifacts, UsageArtifact{
			ArtifactID:  e.m.ArtifactID,
			Type:        e.m.Type,
			Layer:       e.m.Layer,
			UsageTotals: e.acc.result(w, q.Granularity),
			LastLoaded:  dayOrNil(e.acc.lastL),
			LastSynced:  dayOrNil(e.acc.lastS),
		})
	}
	return rep, nil
}

// addLayerSums adds the per-layer sums to the catalog total and to each
// layer's total, with the per-day sums when the report has series.
func addLayerSums(ctx context.Context, src usageSums, series bool, total *usageAccumulator, byLayer map[string]*usageAccumulator) error {
	splits, err := src.sum(ctx, usageByLayerSplit, nil)
	if err != nil {
		return err
	}
	for _, s := range splits {
		for _, a := range []*usageAccumulator{total, byLayer[s.Layer]} {
			a.addTotal(s)
			a.addSplit(s)
		}
	}
	if !series {
		return nil
	}
	days, err := src.sum(ctx, usageByLayerDay, nil)
	if err != nil {
		return err
	}
	for _, s := range days {
		total.addDay(s)
		byLayer[s.Layer].addDay(s)
	}
	return nil
}

// artifactTotals returns an accumulator per served artifact holding its sums
// and latest days, which order the list. An artifact's rows under any layer
// the report covers count toward it.
func artifactTotals(ctx context.Context, src usageSums, served []store.ManifestRecord) (map[string]*usageAccumulator, error) {
	byArtifact := make(map[string]*usageAccumulator, len(served))
	for _, m := range served {
		byArtifact[m.ArtifactID] = newUsageAccumulator()
	}
	sums, err := src.sum(ctx, usageByArtifact, nil)
	if err != nil {
		return nil, err
	}
	for _, s := range sums {
		if a := byArtifact[s.ArtifactID]; a != nil {
			a.addTotal(s)
		}
	}
	return byArtifact, nil
}

// addPageSums adds the breakdowns, and the per-day sums when the report has
// series, of the page's artifacts.
func addPageSums(ctx context.Context, src usageSums, series bool, page []usageEntry) error {
	if len(page) == 0 {
		return nil
	}
	ids := make([]string, len(page))
	byID := make(map[string]*usageAccumulator, len(page))
	for i, e := range page {
		ids[i] = e.m.ArtifactID
		byID[e.m.ArtifactID] = e.acc
	}
	splits, err := src.sum(ctx, usageByArtifactSplit, ids)
	if err != nil {
		return err
	}
	for _, s := range splits {
		byID[s.ArtifactID].addSplit(s)
	}
	if !series {
		return nil
	}
	days, err := src.sum(ctx, usageByArtifactDay, ids)
	if err != nil {
		return err
	}
	for _, s := range days {
		byID[s.ArtifactID].addDay(s)
	}
	return nil
}

// usageEntries returns the artifacts the list covers, in the page's order:
// the named ones when the request names any, of a named type when it names
// types, whose ID contains the query when it has one, and every condition the
// request sets. A type that no artifact has, including a removed one, matches
// nothing.
//
// Spec: §8.7
func usageEntries(served []store.ManifestRecord, byArtifact map[string]*usageAccumulator, namedIDs, types []string, page usagePage) []usageEntry {
	named := make(map[string]bool, len(namedIDs))
	for _, id := range namedIDs {
		named[id] = true
	}
	typed := make(map[string]bool, len(types))
	for _, t := range types {
		typed[t] = true
	}
	entries := make([]usageEntry, 0, len(served))
	for _, m := range served {
		if (len(named) > 0 && !named[m.ArtifactID]) || (len(typed) > 0 && !typed[m.Type]) || !page.matches(m.ArtifactID) {
			continue
		}
		a := byArtifact[m.ArtifactID]
		entries = append(entries, usageEntry{m: m, acc: a, key: usageKey{
			loads: a.totals.Loads, syncs: a.totals.Syncs, hits: a.totals.SearchHits, lastLoaded: a.lastL, id: m.ArtifactID,
		}})
	}
	page.sortEntries(entries)
	return entries
}

func dayOrNil(day string) *string {
	if day == "" {
		return nil
	}
	return &day
}
