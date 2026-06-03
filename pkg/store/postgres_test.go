package store_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// Spec: §9.3 — every backend that satisfies Store passes the
// conformance suite. The Postgres backend runs the full suite
// when PODIUM_POSTGRES_DSN is configured; CI and developer
// machines without Postgres skip it.
//
// The DSN follows lib/pq form, e.g.:
//
//	postgres://podium:podium@localhost:5432/podium?sslmode=disable
//
// Each sub-test starts from an empty schema by truncating all
// Podium tables. Tests do not run in parallel because they share
// the same backing database.
func TestPostgres_ConformanceSuite(t *testing.T) {
	dsn := os.Getenv("PODIUM_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN unset; skipping Postgres conformance suite")
	}
	// One open per Suite invocation; the factory truncates between
	// sub-tests so each starts clean while reusing the connection
	// pool.
	s, err := store.OpenPostgres(dsn)
	if err != nil {
		t.Skipf("OpenPostgres %q: %v (database unreachable)", dsn, err)
	}
	t.Cleanup(func() { _ = s.Close() })

	storetest.Suite(t, func(t *testing.T) store.Store {
		t.Helper()
		// §4.7.1 schema-per-org: org tables live in per-org schemas, so a
		// clean slate drops every org schema and truncates the shared tables
		// (and clears the provisioning cache) rather than truncating a fixed
		// set of public tables.
		if err := s.ResetForTest(context.Background()); err != nil {
			t.Fatalf("reset before sub-test: %v", err)
		}
		return s
	})
}

// Spec: §13.2.1 / §13.9 (F-13.9.4) — the Postgres backend computes
// observed replication lag from pg_last_xact_replay_timestamp(). On a
// primary (the usual DSN target) the function is NULL and the query
// reports 0; on a replica it reports the trailing lag. The query path
// runs when PODIUM_POSTGRES_DSN is configured; otherwise the test skips.
func TestPostgres_ReplicationLagSeconds(t *testing.T) {
	dsn := os.Getenv("PODIUM_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN unset; skipping Postgres replication-lag check")
	}
	s, err := store.OpenPostgres(dsn)
	if err != nil {
		t.Skipf("OpenPostgres %q: %v (database unreachable)", dsn, err)
	}
	t.Cleanup(func() { _ = s.Close() })

	n, err := s.ReplicationLagSeconds(context.Background())
	if err != nil {
		t.Fatalf("ReplicationLagSeconds: %v", err)
	}
	if n < 0 {
		t.Errorf("lag = %d, want >= 0", n)
	}
}

// openIsolationPG opens a Postgres store for the §4.7.1 tenancy-isolation
// tests and resets it to an empty state. Gated on PODIUM_POSTGRES_DSN.
func openIsolationPG(t *testing.T) *store.Postgres {
	t.Helper()
	dsn := os.Getenv("PODIUM_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN unset; skipping Postgres tenancy-isolation test")
	}
	s, err := store.OpenPostgres(dsn)
	if err != nil {
		t.Skipf("OpenPostgres %q: %v (database unreachable)", dsn, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.ResetForTest(context.Background()); err != nil {
		t.Fatalf("ResetForTest: %v", err)
	}
	return s
}

func schemaExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM pg_namespace WHERE nspname = $1`, name).Scan(&n); err != nil {
		t.Fatalf("schema lookup %q: %v", name, err)
	}
	return n > 0
}

func isoManifest(tenantID, artifactID, hash string) store.ManifestRecord {
	return store.ManifestRecord{
		TenantID: tenantID, ArtifactID: artifactID, Version: "1.0.0",
		ContentHash: hash, Type: "skill", Description: "iso", Layer: "team",
		IngestedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
}

// Spec: §4.7.1 — "Each org has its own schema ... Schema-per-org ... bounds the
// blast radius of SQL injection." Org data lives in a per-org schema, so a
// connection scoped to one org cannot read another org's rows even with a
// forged tenant_id WHERE clause: the table it reads is physically a different
// table. Against the prior single shared-table layout this forged read returns
// the other org's row, so the test fails without the schema-per-org change.
func TestPostgres_SchemaPerOrgIsolation_ForgedWhere(t *testing.T) {
	s := openIsolationPG(t)
	ctx := context.Background()

	for _, id := range []string{"orga", "orgb"} {
		if err := s.CreateTenant(ctx, store.Tenant{ID: id, Name: id}); err != nil {
			t.Fatalf("CreateTenant(%s): %v", id, err)
		}
	}
	if err := s.PutManifest(ctx, isoManifest("orga", "skill/secret", "sha256:a")); err != nil {
		t.Fatalf("PutManifest(orga): %v", err)
	}

	if !schemaExists(t, s.DB(), "org_orga") {
		t.Error("org A schema org_orga not provisioned (schema-per-org not in effect)")
	}
	if !schemaExists(t, s.DB(), "org_orgb") {
		t.Error("org B schema org_orgb not provisioned")
	}

	conn, err := s.DB().Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()

	// A connection scoped to org B forges A's tenant_id. Under schema-per-org
	// the manifests table in org B's schema is a different table that never
	// held A's row, so the forged read returns nothing.
	if _, err := conn.ExecContext(ctx, `SET search_path TO "org_orgb", public`); err != nil {
		t.Fatalf("set search_path org_orgb: %v", err)
	}
	var leaked int
	if err := conn.QueryRowContext(ctx,
		`SELECT count(*) FROM manifests WHERE tenant_id = $1`, "orga").Scan(&leaked); err != nil {
		t.Fatalf("forged cross-org read: %v", err)
	}
	if leaked != 0 {
		t.Errorf("forged WHERE tenant_id='orga' from org B's schema returned %d rows; org schemas are not isolated", leaked)
	}

	// Sanity: A's own schema holds the row, so the zero above is isolation, not
	// a lost write.
	if _, err := conn.ExecContext(ctx, `SET search_path TO "org_orga", public`); err != nil {
		t.Fatalf("set search_path org_orga: %v", err)
	}
	var own int
	if err := conn.QueryRowContext(ctx,
		`SELECT count(*) FROM manifests WHERE tenant_id = $1`, "orga").Scan(&own); err != nil {
		t.Fatalf("own-org read: %v", err)
	}
	if own != 1 {
		t.Errorf("org A schema holds %d rows for tenant orga, want 1", own)
	}
}

// Spec: §4.7.1 — "cross-org tables (e.g., shared infrastructure metadata) use
// row-level security with org_id checks ... run under a non-owner role with a
// per-request SET LOCAL podium.org_id." The tenants registry carries an
// org_id-keyed RLS policy. Enforcement requires a non-owner, non-superuser role
// (the owner/superuser the suite connects as bypasses RLS), so the test sets
// podium.org_id and assumes that role: the policy then returns only the org's
// own registry row. Without the policy the cross-org SELECT returns the other
// org's row, so the test fails without the change.
func TestPostgres_TenantsRLS_DeniesCrossOrgRead(t *testing.T) {
	s := openIsolationPG(t)
	ctx := context.Background()

	for _, id := range []string{"orga", "orgb"} {
		if err := s.CreateTenant(ctx, store.Tenant{ID: id, Name: id}); err != nil {
			t.Fatalf("CreateTenant(%s): %v", id, err)
		}
	}

	conn, err := s.DB().Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()

	// Provision a non-owner, non-superuser role so the RLS policy is actually
	// enforced rather than bypassed by the privileged suite connection.
	const cleanupRole = `DO $$ BEGIN
		IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'podium_rls_probe') THEN
			EXECUTE 'DROP OWNED BY podium_rls_probe';
			EXECUTE 'DROP ROLE podium_rls_probe';
		END IF;
	END $$`
	if _, err := conn.ExecContext(ctx, cleanupRole); err != nil {
		t.Skipf("cannot manage RLS probe role (need CREATEROLE/superuser): %v", err)
	}
	if _, err := conn.ExecContext(ctx, `CREATE ROLE podium_rls_probe NOSUPERUSER NOLOGIN`); err != nil {
		t.Skipf("cannot create RLS probe role: %v", err)
	}
	defer conn.ExecContext(context.Background(), cleanupRole)
	for _, g := range []string{
		`GRANT USAGE ON SCHEMA public TO podium_rls_probe`,
		`GRANT SELECT ON public.tenants TO podium_rls_probe`,
	} {
		if _, err := conn.ExecContext(ctx, g); err != nil {
			t.Fatalf("grant to probe role: %v", err)
		}
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT set_config('podium.org_id', $1, true)`, "orga"); err != nil {
		t.Fatalf("set_config: %v", err)
	}
	// Assume the non-owner role for the duration of the transaction so the
	// policy applies; rollback restores the suite role.
	if _, err := tx.ExecContext(ctx, `SET LOCAL ROLE podium_rls_probe`); err != nil {
		t.Fatalf("set local role: %v", err)
	}
	var own int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM public.tenants WHERE id = $1`, "orga").Scan(&own); err != nil {
		t.Fatalf("own-org tenants read: %v", err)
	}
	if own != 1 {
		t.Errorf("org A cannot see its own tenants row under RLS: count=%d, want 1", own)
	}
	var cross int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM public.tenants WHERE id = $1`, "orgb").Scan(&cross); err != nil {
		t.Fatalf("cross-org tenants read: %v", err)
	}
	if cross != 0 {
		t.Errorf("RLS did not deny cross-org read: org A saw %d rows for org B, want 0", cross)
	}
}

// Spec: §4.7.1 — "Schema-per-org gives clean drop-org semantics." DropOrg
// removes exactly one org's schema and registry row and leaves every other org
// intact.
func TestPostgres_DropOrg_RemovesExactlyOneOrg(t *testing.T) {
	s := openIsolationPG(t)
	ctx := context.Background()

	for _, id := range []string{"orga", "orgb"} {
		if err := s.CreateTenant(ctx, store.Tenant{ID: id, Name: id}); err != nil {
			t.Fatalf("CreateTenant(%s): %v", id, err)
		}
	}
	if err := s.PutManifest(ctx, isoManifest("orga", "skill/a", "sha256:a")); err != nil {
		t.Fatalf("PutManifest(orga): %v", err)
	}
	if err := s.PutManifest(ctx, isoManifest("orgb", "skill/b", "sha256:b")); err != nil {
		t.Fatalf("PutManifest(orgb): %v", err)
	}

	if err := s.DropOrg(ctx, "orga"); err != nil {
		t.Fatalf("DropOrg(orga): %v", err)
	}

	if schemaExists(t, s.DB(), "org_orga") {
		t.Error("DropOrg left org A's schema in place")
	}
	if !schemaExists(t, s.DB(), "org_orgb") {
		t.Error("DropOrg removed org B's schema as collateral")
	}

	if _, err := s.GetTenant(ctx, "orga"); !errors.Is(err, store.ErrTenantNotFound) {
		t.Errorf("GetTenant(orga) after drop = %v, want ErrTenantNotFound", err)
	}
	if _, err := s.GetTenant(ctx, "orgb"); err != nil {
		t.Errorf("GetTenant(orgb) after dropping org A = %v, want intact", err)
	}
	got, err := s.GetManifest(ctx, "orgb", "skill/b", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest(orgb) after dropping org A: %v", err)
	}
	if got.ContentHash != "sha256:b" {
		t.Errorf("org B manifest = %q, want sha256:b (org B data must survive org A drop)", got.ContentHash)
	}
}
