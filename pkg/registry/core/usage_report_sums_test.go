package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// sumOnlyStore answers usage sums from the fixture's store and refuses the
// read of raw usage rows, so a report that holds the window's rows fails. It
// counts the sums it is asked for, and failSums makes each one fail.
type sumOnlyStore struct {
	*store.Memory
	sums     atomic.Int32
	failSums bool
}

func (s *sumOnlyStore) ListUsageCounts(context.Context, string, string, string) ([]store.UsageCount, error) {
	return nil, errors.New("the report read the raw usage rows")
}

func (s *sumOnlyStore) SumUsage(ctx context.Context, tenant string, q store.UsageSumQuery) ([]store.UsageSum, error) {
	s.sums.Add(1)
	if s.failSums {
		return nil, errors.New("store unreachable")
	}
	return s.Memory.SumUsage(ctx, tenant, q)
}

// reportOver returns a registry over st with the fixture's layers.
func reportOver(f *usageFixture, st store.Store) *core.Registry {
	return core.New(st, "t", []layer.Layer{
		{ID: "team", Visibility: layer.Visibility{Public: true}, Precedence: 1},
		{ID: "secret", Visibility: layer.Visibility{Users: []string{"bob"}}, Precedence: 2},
	}).WithUsageCounts(f.counter)
}

func reportJSON(t *testing.T, rep *core.UsageReport) string {
	t.Helper()
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Spec: §8.7 — the report sums the stored daily rows in the store rather
// than reading them: over a store that refuses the raw row read it answers
// the same report, series and breakdowns included, as over the store itself.
func TestUsageReport_SumsInTheStore(t *testing.T) {
	t.Parallel()
	f := newUsageFixture(t)
	seedUsage(t, f)
	st := &sumOnlyStore{Memory: f.st}
	for _, who := range []layer.Identity{usageAlice, usageBob} {
		query := q(func(u *core.UsageQuery) { u.Granularity = "week" })()
		want, err := f.reg.UsageReport(context.Background(), who, query)
		if err != nil {
			t.Fatal(err)
		}
		got, err := reportOver(f, st).UsageReport(context.Background(), who, query)
		if err != nil {
			t.Fatalf("%s: report over sums alone: %v", who.Sub, err)
		}
		if reportJSON(t, got) != reportJSON(t, want) {
			t.Errorf("%s: report over sums alone =\n%s\nwant\n%s", who.Sub, reportJSON(t, got), reportJSON(t, want))
		}
	}
}

// Spec: §8.7, §6.10 — a store that cannot answer the report's sums fails the
// report with registry.unavailable.
func TestUsageReport_StoreFailureIsUnavailable(t *testing.T) {
	t.Parallel()
	f := newUsageFixture(t)
	seedUsage(t, f)
	_, err := reportOver(f, &sumOnlyStore{Memory: f.st, failSums: true}).UsageReport(context.Background(), usageAlice, q(nil)())
	if !errors.Is(err, core.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

// Spec: §8.7, §6.3.1 — a caller whose view reaches no row, because its scopes
// grant no read or because it names only layers it cannot see, gets a report
// of zero totals, and the report asks the store for nothing, so an empty
// filter is never read as no restriction.
func TestUsageReport_NoReachableRowsReadsNothing(t *testing.T) {
	t.Parallel()
	f := newUsageFixture(t)
	seedUsage(t, f)
	writeOnly := usageAlice
	writeOnly.Scopes = []string{"podium:write:ops/*"}
	cases := []struct {
		name  string
		who   layer.Identity
		query core.UsageQuery
	}{
		{"scopes grant no read", writeOnly, q(func(u *core.UsageQuery) { u.Granularity = "day" })()},
		{"only hidden layers named", usageAlice, q(func(u *core.UsageQuery) { u.LayerIDs = []string{"secret"} })()},
	}
	for _, c := range cases {
		st := &sumOnlyStore{Memory: f.st}
		rep, err := reportOver(f, st).UsageReport(context.Background(), c.who, c.query)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if n := st.sums.Load(); n != 0 || rep.Totals.UsageCounts != (core.UsageCounts{}) || len(rep.Artifacts) != 0 {
			t.Errorf("%s: %d sums read, totals %+v, %d artifacts; want none, zero, none", c.name, n, rep.Totals.UsageCounts, len(rep.Artifacts))
		}
	}
}
