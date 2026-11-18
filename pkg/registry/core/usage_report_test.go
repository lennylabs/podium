package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// seedUsage stores rows for the fixture: counts for both visible skills, for
// the hidden one, and for an artifact the team layer no longer serves.
func seedUsage(t *testing.T, f *usageFixture) {
	t.Helper()
	row := func(day, lay, id, kind, harness string, loads, syncs, hits int64) store.UsageCount {
		return store.UsageCount{Day: day, Layer: lay, ArtifactID: id, ClientKind: kind, Harness: harness, Loads: loads, Syncs: syncs, SearchHits: hits}
	}
	if err := f.st.AddUsageCounts(context.Background(), "t", []store.UsageCount{
		row("2026-10-25", "team", "ops/greet", "mcp", "claude-code", 1, 0, 0),
		row("2026-11-01", "team", "ops/greet", "mcp", "cursor", 2, 0, 3),
		row("2026-11-10", "team", "ops/greet", "sync", "claude-code", 0, 4, 0),
		row("2026-11-18", "team", "ops/farewell", "mcp", "claude-code", 3, 0, 1),
		row("2026-11-17", "team", "ops/retired", "cli", "none", 5, 0, 0),
		row("2026-11-16", "secret", "ops/hidden", "mcp", "claude-code", 7, 0, 0),
	}); err != nil {
		t.Fatal(err)
	}
}

func usageReport(t *testing.T, f *usageFixture, id func() core.UsageQuery, who string) *core.UsageReport {
	t.Helper()
	ident := usageAlice
	if who == "bob" {
		ident = usageBob
	}
	rep, err := f.reg.UsageReport(context.Background(), ident, id())
	if err != nil {
		t.Fatalf("UsageReport: %v", err)
	}
	return rep
}

func q(mod func(*core.UsageQuery)) func() core.UsageQuery {
	return func() core.UsageQuery {
		u := core.UsageQuery{Retention: 365}
		if mod != nil {
			mod(&u)
		}
		return u
	}
}

func reportIDs(rep *core.UsageReport) []string {
	out := []string{}
	for _, a := range rep.Artifacts {
		out = append(out, a.ArtifactID)
	}
	return out
}

func layerIDs(rep *core.UsageReport) []string {
	out := []string{}
	for _, l := range rep.Layers {
		out = append(out, l.Layer)
	}
	return out
}

// Spec: §8.7 — the default report covers the last 30 days ending today, sums
// only rows of the caller's effective layers, lists every visible artifact
// with its counts, breakdowns, and last days, includes rows of an artifact the
// layer no longer serves in the layer total only, and orders artifacts by
// loads, syncs, search hits, and ID.
func TestUsageReport_TotalsAndVisibility(t *testing.T) {
	t.Parallel()
	f := newUsageFixture(t)
	seedUsage(t, f)
	rep := usageReport(t, f, q(nil), "alice")
	if rep.Days != 30 || rep.From != "2026-10-20" || rep.To != "2026-11-18" {
		t.Errorf("window = %d %s..%s, want 30 2026-10-20..2026-11-18", rep.Days, rep.From, rep.To)
	}
	if got := (core.UsageCounts{Loads: 11, Syncs: 4, SearchHits: 4}); rep.Totals.UsageCounts != got {
		t.Errorf("totals = %+v, want %+v (the hidden layer's 7 loads excluded)", rep.Totals.UsageCounts, got)
	}
	if !reflect.DeepEqual(layerIDs(rep), []string{"team"}) || rep.Layers[0].UsageCounts != rep.Totals.UsageCounts {
		t.Errorf("layers = %+v, want team alone with the totals", rep.Layers)
	}
	if !reflect.DeepEqual(reportIDs(rep), []string{"ops/greet", "ops/farewell"}) {
		t.Errorf("artifacts = %v, want greet (3 loads, 4 syncs) before farewell (3 loads)", reportIDs(rep))
	}
	g := rep.Artifacts[0]
	if g.Loads != 3 || g.Syncs != 4 || g.SearchHits != 3 || *g.LastLoaded != "2026-11-01" || *g.LastSynced != "2026-11-10" ||
		g.ByClient["sync"].Syncs != 4 || g.ByHarness["cursor"].Loads != 2 || g.ByHarness["claude-code"].Loads != 1 || g.Series != nil {
		t.Errorf("greet entry = %+v", g)
	}
	if fw := rep.Artifacts[1]; fw.LastSynced != nil || len(fw.ByClient) != 1 {
		t.Errorf("farewell entry = %+v, want no last_synced and one client kind", fw)
	}
	bob := usageReport(t, f, q(nil), "bob")
	if !reflect.DeepEqual(layerIDs(bob), []string{"team", "secret"}) || bob.Totals.Loads != 18 || len(bob.Artifacts) != 3 {
		t.Errorf("bob sees layers %v, %d loads, %d artifacts; want team and secret, 18, 3", layerIDs(bob), bob.Totals.Loads, len(bob.Artifacts))
	}
}

// Spec: §8.7 — named layers restrict layers, artifacts, and totals; named
// artifacts restrict the artifact list only; a hidden layer or artifact is
// answered exactly as one that names nothing.
func TestUsageReport_LayerAndArtifactFilters(t *testing.T) {
	t.Parallel()
	f := newUsageFixture(t)
	seedUsage(t, f)
	asJSON := func(rep *core.UsageReport) string {
		b, _ := json.Marshal(rep)
		return string(b)
	}
	if bob := usageReport(t, f, q(func(u *core.UsageQuery) { u.LayerIDs = []string{"secret"} }), "bob"); !reflect.DeepEqual(layerIDs(bob), []string{"secret"}) ||
		bob.Totals.Loads != 7 || !reflect.DeepEqual(reportIDs(bob), []string{"ops/hidden"}) {
		t.Errorf("bob layer=secret: layers %v loads %d artifacts %v", layerIDs(bob), bob.Totals.Loads, reportIDs(bob))
	}
	hidden := usageReport(t, f, q(func(u *core.UsageQuery) { u.LayerIDs = []string{"secret"} }), "alice")
	unknown := usageReport(t, f, q(func(u *core.UsageQuery) { u.LayerIDs = []string{"nope"} }), "alice")
	if asJSON(hidden) != asJSON(unknown) || len(hidden.Layers) != 0 || len(hidden.Artifacts) != 0 || hidden.Totals.Loads != 0 {
		t.Errorf("hidden layer = %s\nunknown layer = %s", asJSON(hidden), asJSON(unknown))
	}
	two := usageReport(t, f, q(func(u *core.UsageQuery) { u.ArtifactIDs = []string{"ops/farewell", "ops/greet", "ops/hidden"} }), "alice")
	if !reflect.DeepEqual(reportIDs(two), []string{"ops/greet", "ops/farewell"}) || two.Totals.Loads != 11 {
		t.Errorf("artifact filter: %v with %d total loads, want greet and farewell over the layer totals", reportIDs(two), two.Totals.Loads)
	}
	hiddenA := usageReport(t, f, q(func(u *core.UsageQuery) { u.ArtifactIDs = []string{"ops/hidden"} }), "alice")
	unknownA := usageReport(t, f, q(func(u *core.UsageQuery) { u.ArtifactIDs = []string{"ops/none"} }), "alice")
	if asJSON(hiddenA) != asJSON(unknownA) || len(hiddenA.Artifacts) != 0 {
		t.Errorf("hidden artifact = %s\nunknown artifact = %s", asJSON(hiddenA), asJSON(unknownA))
	}
}

func periods(s []core.UsagePeriod) string {
	out := ""
	for _, p := range s {
		out += fmt.Sprintf("%s..%s=%d/%d/%d ", p.Start, p.End, p.Loads, p.Syncs, p.SearchHits)
	}
	return out
}

// Spec: §8.7 — a granularity adds a series to every total; day, ISO week, and
// calendar month periods cover the window, the first and last are cut to it,
// and a series sums to its total.
func TestUsageReport_Series(t *testing.T) {
	t.Parallel()
	f := newUsageFixture(t)
	seedUsage(t, f)
	week := usageReport(t, f, q(func(u *core.UsageQuery) { u.From, u.Granularity = "2026-11-01", "week" }), "alice")
	if got := periods(week.Totals.Series); got != "2026-11-01..2026-11-01=2/0/3 2026-11-02..2026-11-08=0/0/0 2026-11-09..2026-11-15=0/4/0 2026-11-16..2026-11-18=8/0/1 " {
		t.Errorf("week series = %s", got)
	}
	if len(week.Layers[0].Series) != 4 || len(week.Artifacts[0].Series) != 4 {
		t.Errorf("layer and artifact entries carry no week series: %+v", week)
	}
	month := usageReport(t, f, q(func(u *core.UsageQuery) { u.From, u.To, u.Granularity = "2026-10-25", "2026-11-18", "month" }), "alice")
	if got := periods(month.Totals.Series); got != "2026-10-25..2026-10-31=1/0/0 2026-11-01..2026-11-18=10/4/4 " {
		t.Errorf("month series = %s", got)
	}
	day := usageReport(t, f, q(func(u *core.UsageQuery) { u.Days, u.Granularity = 3, "day" }), "alice")
	if got := periods(day.Totals.Series); got != "2026-11-16..2026-11-16=0/0/0 2026-11-17..2026-11-17=5/0/0 2026-11-18..2026-11-18=3/0/1 " {
		t.Errorf("day series = %s", got)
	}
	var sum core.UsageCounts
	for _, p := range month.Totals.Series {
		sum.Loads, sum.Syncs, sum.SearchHits = sum.Loads+p.Loads, sum.Syncs+p.Syncs, sum.SearchHits+p.SearchHits
	}
	if sum != month.Totals.UsageCounts {
		t.Errorf("month series sums to %+v, totals %+v", sum, month.Totals.UsageCounts)
	}
	if none := usageReport(t, f, q(func(u *core.UsageQuery) { u.Granularity = "none" }), "alice"); none.Totals.Series != nil {
		t.Errorf("granularity none carries a series")
	}
}

// Spec: §8.7 — the window defaults to 30 days or the retention when smaller,
// and a bad window, granularity, or list length is registry.invalid_argument.
func TestUsageReport_Parameters(t *testing.T) {
	t.Parallel()
	f := newUsageFixture(t)
	short := usageReport(t, f, q(func(u *core.UsageQuery) { u.Retention = 10 }), "alice")
	if short.Days != 10 || short.From != "2026-11-09" {
		t.Errorf("retention 10 window = %d from %s, want 10 from 2026-11-09", short.Days, short.From)
	}
	toOnly := usageReport(t, f, q(func(u *core.UsageQuery) { u.To = "2026-11-10" }), "alice")
	if toOnly.From != "2026-10-12" || toOnly.Days != 30 {
		t.Errorf("to-only window = %s, %d days", toOnly.From, toOnly.Days)
	}
	many := make([]string, core.UsageMaxArtifacts+1)
	manyLayers := make([]string, core.UsageMaxLayers+1)
	for name, mod := range map[string]func(*core.UsageQuery){
		"days with from":     func(u *core.UsageQuery) { u.Days, u.From = 3, "2026-11-01" },
		"days with to":       func(u *core.UsageQuery) { u.Days, u.To = 3, "2026-11-01" },
		"days over":          func(u *core.UsageQuery) { u.Days = 366 },
		"bad date":           func(u *core.UsageQuery) { u.From = "11/01/2026" },
		"before retention":   func(u *core.UsageQuery) { u.From = "2025-11-01" },
		"after today":        func(u *core.UsageQuery) { u.To = "2026-11-19" },
		"from after to":      func(u *core.UsageQuery) { u.From, u.To = "2026-11-10", "2026-11-09" },
		"bad granularity":    func(u *core.UsageQuery) { u.Granularity = "year" },
		"too many artifacts": func(u *core.UsageQuery) { u.ArtifactIDs = many },
		"too many layers":    func(u *core.UsageQuery) { u.LayerIDs = manyLayers },
	} {
		if _, err := f.reg.UsageReport(context.Background(), usageAlice, q(mod)()); !errors.Is(err, core.ErrInvalidArgument) {
			t.Errorf("%s: err = %v, want ErrInvalidArgument", name, err)
		}
	}
}
