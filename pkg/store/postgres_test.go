package store_test

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
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

// Spec: §13.2.1 / §13.9 — the Postgres backend computes
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

// nonUTCPostgresDSN returns dsn carrying a session time zone that is not
// UTC, so lib/pq scans a timestamptz back in that zone. It accepts both DSN
// forms the backend takes: a URL and a keyword string.
func nonUTCPostgresDSN(dsn string) string {
	const zone = "Asia/Tokyo"
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return dsn + " timezone=" + zone
		}
		q := u.Query()
		q.Set("timezone", zone)
		u.RawQuery = q.Encode()
		return u.String()
	}
	return dsn + " timezone=" + zone
}

// Spec: §7.2.1 — the §7.3.1 layer object's timestamps are RFC 3339 in UTC.
// lib/pq hands a timestamptz back in the connection's session time zone and
// nothing pins that zone, so the Postgres read normalizes created_at the way
// it already normalizes the nullable stamps beside it. Without the
// conversion a registry over a non-UTC Postgres session emits created_at
// with an offset while the standalone SQLite deployment emits Z, which §2.2
// does not permit. The arm lives at the store level because a server-level
// arm over a UTC session passes either way.
func TestPostgres_LayerConfigTimestampsAreUTC(t *testing.T) {
	dsn := os.Getenv("PODIUM_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN unset; skipping the Postgres layer-timestamp check")
	}
	s, err := store.OpenPostgres(nonUTCPostgresDSN(dsn))
	if err != nil {
		t.Skipf("OpenPostgres %q: %v (database unreachable)", dsn, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if err := s.ResetForTest(ctx); err != nil {
		t.Fatalf("ResetForTest: %v", err)
	}
	if err := s.CreateTenant(ctx, store.Tenant{ID: "tzorg", Name: "tzorg"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	ingested := time.Date(2026, 6, 2, 9, 30, 0, 0, time.UTC)
	deleted := time.Date(2026, 6, 3, 8, 15, 0, 0, time.UTC)
	live := store.LayerConfig{
		TenantID: "tzorg", ID: "live", SourceType: "local", LocalPath: "/srv/live",
		CreatedAt: created, LastIngestedAt: &ingested,
	}
	if err := s.PutLayerConfig(ctx, live); err != nil {
		t.Fatalf("PutLayerConfig(live): %v", err)
	}
	tombstoned := store.LayerConfig{
		TenantID: "tzorg", ID: "tombstoned", SourceType: "local", LocalPath: "/srv/gone",
		CreatedAt: created, DeletedAt: &deleted,
	}
	if err := s.PutLayerConfig(ctx, tombstoned); err != nil {
		t.Fatalf("PutLayerConfig(tombstoned): %v", err)
	}

	assertUTC := func(where string, got time.Time, want time.Time) {
		t.Helper()
		if got.Location() != time.UTC {
			t.Errorf("%s location = %v, want UTC", where, got.Location())
		}
		if !got.Equal(want) {
			t.Errorf("%s = %v, want the instant %v", where, got, want)
		}
	}

	one, err := s.GetLayerConfig(ctx, "tzorg", "live")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	assertUTC("GetLayerConfig created_at", one.CreatedAt, created)
	if one.LastIngestedAt == nil {
		t.Fatalf("GetLayerConfig dropped last_ingested_at")
	}
	assertUTC("GetLayerConfig last_ingested_at", *one.LastIngestedAt, ingested)

	listed, err := s.ListLayerConfigs(ctx, "tzorg")
	if err != nil {
		t.Fatalf("ListLayerConfigs: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListLayerConfigs returned %d layers, want 1", len(listed))
	}
	assertUTC("ListLayerConfigs created_at", listed[0].CreatedAt, created)

	gone, err := s.ListDeletedLayerConfigs(ctx, "tzorg")
	if err != nil {
		t.Fatalf("ListDeletedLayerConfigs: %v", err)
	}
	if len(gone) != 1 {
		t.Fatalf("ListDeletedLayerConfigs returned %d layers, want 1", len(gone))
	}
	assertUTC("ListDeletedLayerConfigs created_at", gone[0].CreatedAt, created)
	if gone[0].DeletedAt == nil {
		t.Fatalf("ListDeletedLayerConfigs dropped deleted_at")
	}
	assertUTC("ListDeletedLayerConfigs deleted_at", *gone[0].DeletedAt, deleted)
}
