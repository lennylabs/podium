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

func overlapRec(owner, ov, peer, pv string, sim float64) store.OverlapRecord {
	return store.OverlapRecord{Owner: owner, OwnerVersion: ov, Peer: peer, PeerVersion: pv, Similarity: sim, ModelID: "m1", ComputedAt: overlapAt}
}

func overlapRun(id, version, model string) store.OverlapComputation {
	return store.OverlapComputation{ArtifactID: id, Version: version, ModelID: model, ComputedAt: overlapAt}
}

// overlapSummary renders the listed records and computation entries in one
// comparable string, so a case asserts the whole stored state at once.
func overlapSummary(t *testing.T, s store.Store, tenant string) string {
	t.Helper()
	recs, err := s.ListOverlapRecords(context.Background(), tenant)
	must(t, err)
	runs, err := s.ListOverlapComputations(context.Background(), tenant)
	must(t, err)
	parts := make([]string, 0, len(recs)+len(runs))
	for _, r := range recs {
		parts = append(parts, fmt.Sprintf("%s|%s@%s>%s@%s=%.2f/%s/%s",
			r.TenantID, r.Owner, r.OwnerVersion, r.Peer, r.PeerVersion, r.Similarity, r.ModelID, r.ComputedAt.UTC().Format(time.RFC3339)))
	}
	for _, c := range runs {
		parts = append(parts, fmt.Sprintf("%s|run:%s@%s/%s/%s",
			c.TenantID, c.ArtifactID, c.Version, c.ModelID, c.ComputedAt.UTC().Format(time.RFC3339)))
	}
	return strings.Join(parts, " ")
}

// Spec: §7.3.1 — a record is keyed by owner and peer, and computing an
// artifact replaces only the records it owns, so a pair one side found
// survives the other side's recomputation. Records come back ordered by owner
// and peer, and a record not owned by the computed artifact, or naming it as
// its own peer, is refused without a change.
func overlapRecordsReplaceByOwner(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	// b owns (b, a) and (b, c); a owns (a, b) and (a, d).
	must(t, s.ReplaceOverlapRecords(ctx, "a", overlapRun("b", "2.0.0", "m1"), []store.OverlapRecord{
		overlapRec("b", "2.0.0", "a", "1.0.0", 0.91),
		overlapRec("b", "2.0.0", "c", "1.0.0", 0.40),
	}))
	must(t, s.ReplaceOverlapRecords(ctx, "a", overlapRun("a", "1.0.0", "m1"), []store.OverlapRecord{
		overlapRec("a", "1.0.0", "b", "2.0.0", 0.93),
		overlapRec("a", "1.0.0", "d", "1.0.0", 0.70),
	}))
	// Recomputing a with one peer replaces a's records and keeps b's.
	must(t, s.ReplaceOverlapRecords(ctx, "a", overlapRun("a", "1.0.0", "m1"), []store.OverlapRecord{
		overlapRec("a", "1.0.0", "d", "1.0.0", 0.72),
	}))
	got := overlapSummary(t, s, "a")
	bad := []error{
		s.ReplaceOverlapRecords(ctx, "a", overlapRun("d", "1.0.0", "m1"), []store.OverlapRecord{overlapRec("a", "1.0.0", "c", "1.0.0", 0.5)}),
		s.ReplaceOverlapRecords(ctx, "a", overlapRun("d", "1.0.0", "m1"), []store.OverlapRecord{overlapRec("d", "1.0.0", "d", "1.0.0", 0.5)}),
		s.ReplaceOverlapRecords(ctx, "a", overlapRun("", "1.0.0", "m1"), nil),
	}
	after := overlapSummary(t, s, "a")
	want := "a|a@1.0.0>d@1.0.0=0.72/m1/2026-11-17T12:00:00Z a|b@2.0.0>a@1.0.0=0.91/m1/2026-11-17T12:00:00Z a|b@2.0.0>c@1.0.0=0.40/m1/2026-11-17T12:00:00Z " +
		"a|run:a@1.0.0/m1/2026-11-17T12:00:00Z a|run:b@2.0.0/m1/2026-11-17T12:00:00Z"
	refused := errors.Is(bad[0], store.ErrInvalidOverlapRecord) && errors.Is(bad[1], store.ErrInvalidOverlapRecord) && errors.Is(bad[2], store.ErrInvalidOverlapRecord)
	if got != want || after != want || !refused {
		t.Errorf("overlap records:\n got     %s\n want    %s\n after   %s\n refusal %v", got, want, after, bad)
	}
}

// Spec: §7.3.1 — each computation records the version and the model of the
// artifact's last successful computation, also when it found no peer, and a
// later computation replaces the entry. Deleting an artifact that is no
// longer served removes the records it owns and its entry, and leaves the
// records other artifacts own, including one naming it as a peer.
func overlapComputationEntries(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	must(t, s.ReplaceOverlapRecords(ctx, "a", overlapRun("lonely", "1.0.0", "m1"), nil))
	must(t, s.ReplaceOverlapRecords(ctx, "a", overlapRun("x", "1.0.0", "m0"), []store.OverlapRecord{overlapRec("x", "1.0.0", "y", "1.0.0", 0.8)}))
	must(t, s.ReplaceOverlapRecords(ctx, "a", overlapRun("x", "2.0.0", "m1"), []store.OverlapRecord{overlapRec("x", "2.0.0", "y", "1.0.0", 0.9)}))
	must(t, s.ReplaceOverlapRecords(ctx, "a", overlapRun("y", "1.0.0", "m1"), []store.OverlapRecord{overlapRec("y", "1.0.0", "x", "2.0.0", 0.9)}))
	before := overlapSummary(t, s, "a")
	must(t, s.DeleteOverlapArtifacts(ctx, "a", []string{"x", "gone"}))
	must(t, s.DeleteOverlapArtifacts(ctx, "a", nil))
	got := overlapSummary(t, s, "a")
	wantBefore := "a|x@2.0.0>y@1.0.0=0.90/m1/2026-11-17T12:00:00Z a|y@1.0.0>x@2.0.0=0.90/m1/2026-11-17T12:00:00Z " +
		"a|run:lonely@1.0.0/m1/2026-11-17T12:00:00Z a|run:x@2.0.0/m1/2026-11-17T12:00:00Z a|run:y@1.0.0/m1/2026-11-17T12:00:00Z"
	want := "a|y@1.0.0>x@2.0.0=0.90/m1/2026-11-17T12:00:00Z a|run:lonely@1.0.0/m1/2026-11-17T12:00:00Z a|run:y@1.0.0/m1/2026-11-17T12:00:00Z"
	if before != wantBefore || got != want {
		t.Errorf("computation entries:\n before %s\n want   %s\n after  %s\n want   %s", before, wantBefore, got, want)
	}
}

// Spec: §4.7.1, §7.3.1 — overlap records and computation entries live in the
// tenant's store, so one tenant's computation or deletion neither reads nor
// removes another tenant's rows.
func overlapRecordsScopedToTenant(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	mustCreateTenant(t, s, "b")
	must(t, s.ReplaceOverlapRecords(ctx, "a", overlapRun("x", "1", "m1"), []store.OverlapRecord{overlapRec("x", "1", "y", "1", 0.9)}))
	must(t, s.ReplaceOverlapRecords(ctx, "b", overlapRun("x", "1", "m1"), []store.OverlapRecord{overlapRec("x", "1", "z", "1", 0.8)}))
	must(t, s.ReplaceOverlapRecords(ctx, "b", overlapRun("x", "1", "m1"), nil))
	must(t, s.DeleteOverlapArtifacts(ctx, "b", []string{"x"}))
	got := overlapSummary(t, s, "a") + " | " + overlapSummary(t, s, "b")
	if want := "a|x@1>y@1=0.90/m1/2026-11-17T12:00:00Z a|run:x@1/m1/2026-11-17T12:00:00Z | "; got != want {
		t.Errorf("tenant-scoped records = %q, want %q", got, want)
	}
}
