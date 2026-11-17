package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// overlapStatementFailures breaks the overlap tables of tenant step by step
// through exec and asserts that every overlap call whose statement fails
// returns the error rather than reporting success or an empty result. The
// computation table is first replaced by one that lacks the model column, so
// a replace reads no stored entry and fails at the entry upsert; dropping it
// makes a replace fail at the entry read and a delete at the entry delete.
// The record table is then replaced by one that lacks the record columns, so
// a replace fails at the record insert and a list at its query; dropping it
// makes a replace fail at the record delete and a delete at its first
// statement.
func overlapStatementFailures(t *testing.T, s Store, tenant string, exec func(stmt string)) {
	t.Helper()
	ctx := context.Background()
	comp := OverlapComputation{ArtifactID: "a", Version: "1", ComputedAt: time.Now()}
	recs := []OverlapRecord{{Owner: "a", OwnerVersion: "1", Peer: "b", PeerVersion: "1", Similarity: 0.9, ComputedAt: time.Now()}}
	listRecs := func() error { _, err := s.ListOverlapRecords(ctx, tenant); return err }
	listRuns := func() error { _, err := s.ListOverlapComputations(ctx, tenant); return err }
	var errs []error
	exec(`DROP TABLE semantic_overlap_computations`)
	exec(`CREATE TABLE semantic_overlap_computations (tenant_id TEXT, artifact_id TEXT, version TEXT, computed_at TEXT)`)
	errs = append(errs, s.ReplaceOverlapRecords(ctx, tenant, comp, recs))
	exec(`DROP TABLE semantic_overlap_computations`)
	errs = append(errs, s.ReplaceOverlapRecords(ctx, tenant, comp, recs), s.DeleteOverlapArtifacts(ctx, tenant, []string{"a"}), listRuns())
	exec(`DROP TABLE semantic_overlap_records`)
	exec(`CREATE TABLE semantic_overlap_records (tenant_id TEXT, owner_id TEXT)`)
	errs = append(errs, s.ReplaceOverlapRecords(ctx, tenant, comp, recs), listRecs())
	exec(`DROP TABLE semantic_overlap_records`)
	errs = append(errs, s.ReplaceOverlapRecords(ctx, tenant, comp, recs), s.DeleteOverlapArtifacts(ctx, tenant, []string{"a"}))
	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 0 {
		t.Errorf("%d of %d overlap calls on broken tables returned no error: %v", succeeded, len(errs), errs)
	}
}

// Spec: §7.3.1 — the SQLite overlap calls return the error of a statement
// that fails inside the transaction, and a stored time that does not parse
// fails the read rather than reading as no overlap.
func TestSQLite_OverlapStatementFailures(t *testing.T) {
	t.Parallel()
	s, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	exec := func(stmt string) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	exec(`INSERT INTO semantic_overlap_records VALUES ('bad', 'a', '1', 'b', '1', 0.9, 'm1', 'not a time')`)
	exec(`INSERT INTO semantic_overlap_computations VALUES ('bad', 'a', '1', 'm1', 'not a time')`)
	_, recErr := s.ListOverlapRecords(ctx, "bad")
	_, runErr := s.ListOverlapComputations(ctx, "bad")
	if recErr == nil || runErr == nil {
		t.Errorf("reads of an unparsable computed_at = %v and %v, want errors", recErr, runErr)
	}
	overlapStatementFailures(t, s, "acme", exec)
}

// Spec: §7.3.1 — the Postgres overlap calls return the error of a statement
// that fails inside the transaction in the org schema. Gated on
// PODIUM_POSTGRES_DSN; the org is unique to the run and dropped afterwards.
func TestPostgres_OverlapStatementFailures(t *testing.T) {
	dsn := os.Getenv("PODIUM_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN unset; skipping the Postgres overlap statement failures")
	}
	p, err := OpenPostgres(dsn)
	if err != nil {
		t.Skipf("OpenPostgres: %v (database unreachable)", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	ctx := context.Background()
	tenant := fmt.Sprintf("ovlfail%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = p.DropOrg(context.Background(), tenant) })
	if err := p.CreateTenant(ctx, Tenant{ID: tenant, Name: "acme"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	conn, release, err := p.org(ctx, tenant)
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	t.Cleanup(release)
	overlapStatementFailures(t, p, tenant, func(stmt string) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	})
}
