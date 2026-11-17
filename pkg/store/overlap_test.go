package store_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// overlapReadsFail asserts every overlap call returns an error on a canceled
// context rather than an empty result, so a caller never reads a failed read
// as "no overlap" and never believes a failed write succeeded.
func overlapReadsFail(t *testing.T, s store.Store, tenant string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	comp := store.OverlapComputation{ArtifactID: "a", Version: "1"}
	rec := store.OverlapRecord{Owner: "a", OwnerVersion: "1", Peer: "b", PeerVersion: "1", Similarity: 0.9}
	if err := s.ReplaceOverlapRecords(ctx, tenant, comp, []store.OverlapRecord{rec}); err == nil {
		t.Error("ReplaceOverlapRecords on a canceled context succeeded")
	}
	if got, err := s.ListOverlapRecords(ctx, tenant); err == nil {
		t.Errorf("ListOverlapRecords on a canceled context = %v, nil; want an error", got)
	}
	if got, err := s.ListOverlapComputations(ctx, tenant); err == nil {
		t.Errorf("ListOverlapComputations on a canceled context = %v, nil; want an error", got)
	}
	if err := s.DeleteOverlapArtifacts(ctx, tenant, []string{"a"}); err == nil {
		t.Error("DeleteOverlapArtifacts on a canceled context succeeded")
	}
}

// Spec: §7.3.1 — the SQLite overlap calls return the store error.
func TestSQLite_OverlapRecordsReturnErrors(t *testing.T) {
	t.Parallel()
	s, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	overlapReadsFail(t, s, "acme")
}

// Spec: §7.3.1 — the Postgres overlap calls return the error of a store call
// that cannot reach the org's schema. Gated on PODIUM_POSTGRES_DSN.
func TestPostgres_OverlapRecordsReturnErrors(t *testing.T) {
	s := openIsolationPG(t)
	if err := s.CreateTenant(context.Background(), store.Tenant{ID: "ovlorg", Name: "acme"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	overlapReadsFail(t, s, "ovlorg")
}
