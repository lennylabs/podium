package storetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

// overlapAt is the computed time every overlap conformance record carries, so
// a round trip can compare it exactly.
var overlapAt = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func overlapRec(a, va, b, vb string, sim float64) store.OverlapRecord {
	return store.OverlapRecord{ArtifactA: a, VersionA: va, ArtifactB: b, VersionB: vb, Similarity: sim, ModelID: "m1", ComputedAt: overlapAt}
}

// overlapSummary renders the listed records in one comparable string, so a
// case asserts the whole stored state at once.
func overlapSummary(t *testing.T, s store.Store, tenant string) string {
	t.Helper()
	recs, err := s.ListOverlapRecords(context.Background(), tenant)
	must(t, err)
	parts := make([]string, 0, len(recs))
	for _, r := range recs {
		parts = append(parts, fmt.Sprintf("%s|%s@%s~%s@%s=%.2f/%s/%s",
			r.TenantID, r.ArtifactA, r.VersionA, r.ArtifactB, r.VersionB, r.Similarity, r.ModelID, r.ComputedAt.UTC().Format(time.RFC3339)))
	}
	return strings.Join(parts, " ")
}

// Spec: §7.3.1 — computing an artifact removes every stored record that
// names it and stores one record per neighbour, keyed by the unordered pair,
// so a later computation from either side replaces it. Records come back
// ordered by pair with the smaller identifier first, and a record that does
// not name the computed artifact is refused without a change.
func overlapRecordsReplaceByArtifact(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	// Computing "b" stores (a, b) from b's side and (b, c).
	must(t, s.ReplaceOverlapRecords(ctx, "a", "b", []store.OverlapRecord{
		overlapRec("b", "2.0.0", "a", "1.0.0", 0.91),
		overlapRec("b", "2.0.0", "c", "1.0.0", 0.40),
	}))
	// Computing "a" replaces (a, b) by key and adds (a, d).
	must(t, s.ReplaceOverlapRecords(ctx, "a", "a", []store.OverlapRecord{
		overlapRec("a", "1.0.0", "b", "2.0.0", 0.93),
		overlapRec("a", "1.0.0", "d", "1.0.0", 0.70),
	}))
	want := "a|a@1.0.0~b@2.0.0=0.93/m1/2026-11-17T12:00:00Z a|a@1.0.0~d@1.0.0=0.70/m1/2026-11-17T12:00:00Z a|b@2.0.0~c@1.0.0=0.40/m1/2026-11-17T12:00:00Z"
	got := overlapSummary(t, s, "a")
	// Recomputing "b" with no neighbours removes every record naming b and
	// leaves (a, d); a record that does not name the computed artifact is
	// refused and changes nothing.
	must(t, s.ReplaceOverlapRecords(ctx, "a", "b", nil))
	bad := s.ReplaceOverlapRecords(ctx, "a", "d", []store.OverlapRecord{overlapRec("a", "1.0.0", "c", "1.0.0", 0.5)})
	after := overlapSummary(t, s, "a")
	if got != want || after != "a|a@1.0.0~d@1.0.0=0.70/m1/2026-11-17T12:00:00Z" || !errors.Is(bad, store.ErrInvalidOverlapRecord) {
		t.Errorf("overlap records:\n got     %s\n want    %s\n after   %s\n refusal %v", got, want, after, bad)
	}
}

// Spec: §4.7.1, §7.3.1 — overlap records live in the tenant's store, so one
// tenant's computation neither reads nor removes another tenant's records.
func overlapRecordsScopedToTenant(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	mustCreateTenant(t, s, "b")
	must(t, s.ReplaceOverlapRecords(ctx, "a", "x", []store.OverlapRecord{overlapRec("x", "1", "y", "1", 0.9)}))
	must(t, s.ReplaceOverlapRecords(ctx, "b", "x", []store.OverlapRecord{overlapRec("x", "1", "z", "1", 0.8)}))
	must(t, s.ReplaceOverlapRecords(ctx, "b", "x", nil))
	got := overlapSummary(t, s, "a") + " | " + overlapSummary(t, s, "b")
	if want := "a|x@1~y@1=0.90/m1/2026-11-17T12:00:00Z | "; got != want {
		t.Errorf("tenant-scoped records = %q, want %q", got, want)
	}
}
