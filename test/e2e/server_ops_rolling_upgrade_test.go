package e2e

// Expand-contract rolling upgrade across two server binaries over one Postgres
// metadata store, driving the upgrade procedure in
// docs/deployment/operator-guide.md.
//
// The operator guide states that schema migrations are bundled in the registry
// binary and applied additively on startup: a new version creates tables and
// columns when absent and never drops or rewrites existing ones, so an upgrade
// migrates the database forward in place. Older and newer replicas coexist
// during the roll because the older replica ignores the new tables and columns,
// and a rollback reverts the binary because the additive schema stays
// forward-compatible with the previous version's binary. The spec backs the
// mechanism in §13.4: "tables are created when absent (CREATE TABLE IF NOT
// EXISTS) and new columns are added when absent (Postgres uses ADD COLUMN IF
// NOT EXISTS) ... so a binary upgrade migrates an existing database forward in
// place, without a separate migration step and without downtime."
//
// Realizing two binary versions: the suite builds one current binary, so the
// earlier binary's database state is staged directly. A prior-version Postgres
// org schema is created with reduced manifests and layer_configs tables (their
// long-standing columns, none of the genuinely-recent additive columns the
// current binary adds) plus a seeded pre-existing artifact under a registered
// public layer, which is the database a recent prior binary version would have
// left behind. The current binary then boots against that database: ensureOrg
// runs CREATE TABLE IF NOT EXISTS (a no-op on the existing tables) and
// applyAdditivePostgres (ADD COLUMN IF NOT EXISTS) on first org access, so the
// upgrade adds the missing recent columns to the existing tables in place and
// the seeded row survives. The coexistence overlap runs two current binaries
// against the one Postgres; the rollback step issues reads and writes against
// the migrated tables using only the prior binary's column set and asserts they
// still succeed. The product has no contract phase that drops columns (§13.4 is
// purely additive), so a clean finalize reduces to the migration's idempotent
// re-application being a clean no-op.
//
// Backends: Postgres metadata (PODIUM_REGISTRY_STORE=postgres) plus a
// filesystem object store and BM25-only search (PODIUM_NO_EMBEDDINGS=true, the
// §13.12 no-embeddings fallback), so the test gates only on a reachable
// Postgres DSN, which is the metadata store the migration operates on. It skips
// cleanly when PODIUM_POSTGRES_DSN is unset.
//
// Isolation: standard mode keys metadata by the fixed "default" org schema,
// which is shared and cannot be isolated per test. These tests reset that one
// org schema to the legacy state at the start of each run, do not run in
// parallel, and the live lane is responsible for not pointing the metadata
// store at a DSN a concurrent pkg/store conformance run is wiping (the same
// constraint standard_stack_parity_test.go documents).
//
// Spec: §13.4 (additive, idempotent forward migration in place; a binary
// upgrade migrates an existing database forward without a separate migration
// step and without downtime), §4.7.1 (schema-per-org isolation; org IDs are
// UUIDs), §13.10 / §13.12 (standard deployment backend configuration), §7.2
// (control plane metadata API).

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

// ruSkipIfNoPostgres skips unless a live Postgres DSN is configured and the
// database is reachable. It returns the resolved DSN. The migration under test
// is the Postgres metadata-store forward migration, so Postgres is the only
// backend the test requires; the object store is filesystem and the vector
// backend is in-process.
func ruSkipIfNoPostgres(t *testing.T) string {
	t.Helper()
	dsn := firstEnv("PODIUM_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN unset; skipping expand-contract rolling-upgrade e2e")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("open postgres: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("ping postgres: %v (database unreachable)", err)
	}
	return dsn
}

// ruDefaultOrgSchema returns the quoted Postgres schema identifier the
// standard-mode default org resolves to. Standard mode keys every org table by
// the deterministic UUIDv5 of "podium:org:default" (serverboot.orgIDForName),
// and pkg/store maps org id X to the schema org_X (§4.7.1). The legacy tables
// the upgrade migrates have to live in that exact schema so the booted server
// finds and migrates them.
func ruDefaultOrgSchema(t *testing.T) string {
	t.Helper()
	return `"org_` + lcDefaultOrgID() + `"`
}

// ruExec runs one statement against db and fails the test on error.
func ruExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// ruLegacyLayer is the layer id the seeded legacy artifact belongs to. A
// matching public layer_configs row registers it so the booted server resolves
// the layer and the §4.6 visibility filter admits the artifact.
const ruLegacyLayer = "ops-legacy"

// ruLegacyManifestsTable is the manifests table as an earlier (intermediate)
// binary version left it: the long-standing columns that version shipped with
// (including layer, description, deprecated, frontmatter, and body), but none of
// the genuinely-recent additive columns the current binary adds (the §8.4
// soft-delete deleted_at, the retention deprecated_at, the externalized
// resources, the ingest signature, search_visibility, skill_raw, extends_pin,
// tags, and sensitivity). This is an upgrade from a recent prior version rather
// than from the first release, which is the realistic expand-contract case. A
// boot against this table must add the missing recent columns in place before
// the soft-delete-filtered read (WHERE deleted_at IS NULL) can run at all.
const ruLegacyManifestsTable = `CREATE TABLE manifests (
	tenant_id TEXT NOT NULL,
	artifact_id TEXT NOT NULL,
	version TEXT NOT NULL,
	content_hash TEXT NOT NULL,
	type TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	layer TEXT NOT NULL DEFAULT '',
	deprecated BOOLEAN NOT NULL DEFAULT FALSE,
	ingested_at TIMESTAMPTZ NOT NULL,
	frontmatter BYTEA,
	body BYTEA,
	PRIMARY KEY (tenant_id, artifact_id, version)
)`

// ruLegacyLayerConfigsTable is the layer_configs table as the same prior binary
// version left it: the long-standing columns including the public visibility
// flag, but none of the recent additive columns (webhook_secret,
// force_push_policy, git_provider, the soft-delete deleted_at, and the staleness
// last_ingested_at). The booted server adds those on the in-place upgrade.
const ruLegacyLayerConfigsTable = `CREATE TABLE layer_configs (
	tenant_id TEXT NOT NULL,
	id TEXT NOT NULL,
	source_type TEXT NOT NULL,
	repo TEXT NOT NULL DEFAULT '',
	ref TEXT NOT NULL DEFAULT '',
	root TEXT NOT NULL DEFAULT '',
	local_path TEXT NOT NULL DEFAULT '',
	ord BIGINT NOT NULL DEFAULT 0,
	user_defined BOOLEAN NOT NULL DEFAULT FALSE,
	owner TEXT NOT NULL DEFAULT '',
	public BOOLEAN NOT NULL DEFAULT FALSE,
	organization BOOLEAN NOT NULL DEFAULT FALSE,
	groups TEXT NOT NULL DEFAULT '',
	users TEXT NOT NULL DEFAULT '',
	last_ingested_ref TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL,
	PRIMARY KEY (tenant_id, id)
)`

// ruStageLegacyDatabase resets the default org schema to the state a prior
// binary version would have left: it drops the schema, recreates the legacy
// manifests and layer_configs tables (missing the recent additive columns), and
// seeds one pre-existing artifact under a registered public layer, keyed by the
// default org's UUID. The remaining org tables are intentionally absent so the
// booted server creates them fresh (CREATE TABLE IF NOT EXISTS), reproducing a
// realistic mixed upgrade where some tables predate the new binary and others
// are new. It returns the seeded artifact id, version, and content hash.
//
// Dropping the schema also clears any tenants/admin_grants the standard server
// bootstrap wrote on a prior run, so the boot below re-seeds the admin and the
// org from a clean legacy baseline rather than racing leftover state.
func ruStageLegacyDatabase(t *testing.T, dsn string) (id, version, contentHash string) {
	t.Helper()
	id = "ops/runbooks/restart-gateway"
	version = "1.4.2"
	contentHash = "sha256:legacyhash1234"

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open postgres for staging: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // keep search_path on one session across the staging statements

	schema := ruDefaultOrgSchema(t)
	ruExec(t, db, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
	ruExec(t, db, "CREATE SCHEMA "+schema)
	ruExec(t, db, "SET search_path TO "+schema)
	ruExec(t, db, ruLegacyManifestsTable)
	ruExec(t, db, ruLegacyLayerConfigsTable)

	tenantID := lcDefaultOrgID()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO manifests (tenant_id, artifact_id, version, content_hash, type, description, layer, ingested_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, now())`,
		tenantID, id, version, contentHash, "context", "restart the gateway", ruLegacyLayer); err != nil {
		t.Fatalf("seed legacy manifest row: %v", err)
	}
	// Register the seeded artifact's layer as a public admin layer so the booted
	// server resolves it and the §4.6 visibility filter admits the artifact.
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO layer_configs (tenant_id, id, source_type, public, created_at)
		 VALUES ($1, $2, $3, TRUE, now())`,
		tenantID, ruLegacyLayer, "local"); err != nil {
		t.Fatalf("seed legacy layer_config row: %v", err)
	}
	// Precondition: the legacy tables genuinely lack a recent additive column, so
	// the assertion that the boot adds it is meaningful.
	if ruColumnExists(t, dsn, "manifests", "deleted_at") {
		t.Fatal("precondition failed: legacy manifests already has the recent deleted_at column")
	}
	if ruColumnExists(t, dsn, "layer_configs", "webhook_secret") {
		t.Fatal("precondition failed: legacy layer_configs already has the recent webhook_secret column")
	}
	return id, version, contentHash
}

// ruColumnExists reports whether the default org schema's table has column col,
// queried through information_schema on a fresh connection.
func ruColumnExists(t *testing.T, dsn, table, col string) bool {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open postgres for column check: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_schema = $1 AND table_name = $2 AND column_name = $3`,
		"org_"+lcDefaultOrgID(), table, col).Scan(&n); err != nil {
		t.Fatalf("information_schema lookup %s.%s: %v", table, col, err)
	}
	return n > 0
}

// ruSeededManifestRow reads the seeded manifest row's content hash directly from
// the default org schema, on a fresh connection. It is the store-independent
// witness that the in-place upgrade preserved the pre-existing row rather than
// dropping or rewriting it. Returns ("", false) when the row is absent.
func ruSeededManifestRow(t *testing.T, dsn, id, version string) (hash string, ok bool) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open postgres for row check: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ruExec(t, db, "SET search_path TO "+ruDefaultOrgSchema(t))
	err = db.QueryRowContext(context.Background(),
		`SELECT content_hash FROM manifests WHERE tenant_id = $1 AND artifact_id = $2 AND version = $3`,
		lcDefaultOrgID(), id, version).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", false
	}
	if err != nil {
		t.Fatalf("read seeded manifest row: %v", err)
	}
	return hash, true
}

// ruStartUpgradedServer boots a registry against the Postgres DSN in the
// minimal standard configuration the migration test needs: Postgres metadata,
// a filesystem object store, BM25-only search (no embedding provider required),
// and the injected-session-token identity provider with a bootstrapped admin.
// Seeding the admin writes admin_grants, an org table, so ensureOrg runs the
// §13.4 additive migration on the prior-version schema at boot (the expand
// step). The caller registers the runtime key and mints tokens.
func ruStartUpgradedServer(t *testing.T, dsn string) *serverProc {
	t.Helper()
	return startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_REGISTRY_STORE=postgres",
		"PODIUM_POSTGRES_DSN=" + dsn,
		"PODIUM_OBJECT_STORE=filesystem",
		"PODIUM_FILESYSTEM_ROOT=" + t.TempDir(),
		// BM25-only (§13.12 --no-embeddings fallback): ingest persists manifests
		// to Postgres with no embedding provider, so the test gates on Postgres
		// alone. The migration under test is the metadata-store schema, not the
		// search index, and the assertions go through load_artifact, not search.
		"PODIUM_NO_EMBEDDINGS=true",
		"PODIUM_IDENTITY_PROVIDER=injected-session-token",
		"PODIUM_OAUTH_AUDIENCE=" + injAudience,
		"PODIUM_BOOTSTRAP_ADMINS=alice@acme.com",
		"PODIUM_DEFAULT_LAYER_VISIBILITY=public",
	}, "serve")
}

// ruLoad GETs load_artifact for an explicit version with the bearer token and
// returns the HTTP status and decoded §7.6.1 lifecycle envelope. Standard mode
// gates reads on a verified runtime token, so the load carries the admin's
// session token rather than running anonymously.
func ruLoad(t *testing.T, srv *serverProc, token, id, version string) (int, lcLoadResp, []byte) {
	t.Helper()
	url := srv.BaseURL + "/v1/load_artifact?id=" + id
	if version != "" {
		url += "&version=" + version
	}
	st, body := injGet(t, url, token)
	var r lcLoadResp
	if st == 200 {
		if err := json.Unmarshal(body, &r); err != nil {
			t.Fatalf("decode load_artifact %s@%s: %v\nbody: %s", id, version, err, body)
		}
	}
	return st, r, body
}

// ruIngestLayer publishes a single-artifact layer through the standard server as
// the authenticated admin and waits until the artifact loads, proving a write
// landed in the migrated Postgres metadata store. A remote standard server
// cannot read a client --local path, so the author publishes through a git
// source, the same flow standard_stack_parity_test.go uses. It returns the
// artifact id.
func ruIngestLayer(t *testing.T, srv *serverProc, token, layerID, artifactID, body string) string {
	t.Helper()
	reg := writeRegistry(t, map[string]string{
		artifactID + "/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: " + body + "\n---\n\n" + body + " body.\n",
	})
	msPublishGitLayer(t, srv.BaseURL, token, layerID, reg)

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if st, _, _ := ruLoad(t, srv, token, artifactID, ""); st == 200 {
			return artifactID
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("ingested artifact %q never loaded from the migrated store\nserver log:\n%s", artifactID, srv.log())
	return ""
}

// TestServerOps_RollingUpgradeCoexistence drives the operator guide's upgrade
// procedure over a live Postgres metadata store: a database written by an
// earlier binary's schema is migrated forward in place by the current binary,
// the pre-existing data survives, two current binaries coexist against the one
// Postgres during the overlap, and both serve reads and writes. The re-applied
// migration is a clean idempotent no-op (the additive schema has no contract
// phase that drops columns, so "finalize" is the no-op re-migrate).
//
// Spec: §13.4 (additive forward migration in place, idempotent, without a
// separate migration step and without downtime), §4.7.1 (schema-per-org), §7.2
// (control plane).
func TestServerOps_RollingUpgradeCoexistence(t *testing.T) {
	// Not parallel: the default org schema is shared and is reset to the legacy
	// state at the start of the run (ruStageLegacyDatabase).
	dsn := ruSkipIfNoPostgres(t)

	// ---- Stage the earlier binary's database (legacy schema + seeded row) -----
	id, version, contentHash := ruStageLegacyDatabase(t, dsn)

	// ---- Expand: boot the current binary; the additive migration runs --------
	// Bootstrapping the admin writes admin_grants (an org table), so ensureOrg
	// runs the §13.4 additive migration on the legacy schema at boot.
	priv, pemPath := injKeyPair(t)
	srvNew := ruStartUpgradedServer(t, dsn)
	injRegisterRuntime(t, srvNew, pemPath)
	token := injSignJWT(t, priv, injClaims("alice@acme.com"))

	// The boot added the recent additive columns to the pre-existing manifests
	// table in place: columns the prior binary version never created now exist.
	for _, col := range []string{"deleted_at", "deprecated_at", "resources", "signature", "search_visibility", "skill_raw"} {
		if !ruColumnExists(t, dsn, "manifests", col) {
			t.Errorf("recent additive column manifests.%s not added by the in-place upgrade", col)
		}
	}
	// The layer_configs table gained its recent columns too, so the seeded
	// public layer reads back through the migrated schema.
	if !ruColumnExists(t, dsn, "layer_configs", "webhook_secret") {
		t.Errorf("recent additive column layer_configs.webhook_secret not added by the in-place upgrade")
	}

	// The seeded pre-existing row survived the migration with its content hash:
	// the upgrade added columns, it did not drop or rewrite the table.
	if hash, ok := ruSeededManifestRow(t, dsn, id, version); !ok {
		t.Errorf("seeded legacy manifest row %s@%s was lost across the in-place upgrade", id, version)
	} else if hash != contentHash {
		t.Errorf("seeded row content_hash = %q, want %q (the upgrade must preserve existing data)", hash, contentHash)
	}

	// The new binary serves the migrated legacy artifact (read): load_artifact
	// resolves the seeded row with its original version and hash. The legacy row
	// carried no frontmatter/body columns, so the upgrade backfilled them; the
	// indexed fields (version, type, content hash) survive verbatim.
	stLoad, loaded, raw := ruLoad(t, srvNew, token, id, "")
	if stLoad != 200 {
		t.Fatalf("load migrated artifact from new binary: HTTP %d\nbody: %s\nserver log:\n%s", stLoad, raw, srvNew.log())
	}
	if loaded.Version != version || loaded.ContentHash != contentHash {
		t.Errorf("migrated artifact version=%q hash=%q, want %s/%s", loaded.Version, loaded.ContentHash, version, contentHash)
	}

	// The new binary serves a write: ingesting a fresh layer persists a new
	// manifest to the migrated store, which only succeeds because the additive
	// columns the ingest writes (layer, frontmatter, body, ...) now exist.
	newID := ruIngestLayer(t, srvNew, token, "ops-new", "ops/runbooks/rotate-keys", "rotate keys")

	// ---- Overlap: a second binary coexists against the one Postgres ----------
	// A second current binary boots against the same Postgres and the same
	// already-migrated org schema, standing in for the older replica that
	// continues serving during the roll. The additive schema is forward- and
	// backward-compatible, so the two processes coexist over one database. The
	// same runtime key is registered with the second server so the admin's token
	// verifies against both.
	srvOther := ruStartUpgradedServer(t, dsn)
	injRegisterRuntime(t, srvOther, pemPath)

	// During the overlap both binaries serve reads: each resolves the seeded
	// legacy artifact and the artifact the first binary ingested.
	for name, srv := range map[string]*serverProc{"new": srvNew, "other": srvOther} {
		if st, _, body := ruLoad(t, srv, token, id, ""); st != 200 {
			t.Errorf("%s binary failed to load the seeded artifact during overlap: HTTP %d\nbody: %s", name, st, body)
		}
		if st, _, body := ruLoad(t, srv, token, newID, ""); st != 200 {
			t.Errorf("%s binary failed to load the first binary's ingest during overlap: HTTP %d\nbody: %s", name, st, body)
		}
	}

	// During the overlap both binaries serve writes: the second binary ingests
	// its own layer into the shared Postgres, and the first binary then resolves
	// it, proving writes from one replica are visible to the other.
	otherID := ruIngestLayer(t, srvOther, token, "ops-other", "ops/runbooks/drain-node", "drain node")
	if st, _, body := ruLoad(t, srvNew, token, otherID, ""); st != 200 {
		t.Errorf("write by the second binary not visible to the first during overlap: HTTP %d\nbody: %s", st, body)
	}

	// ---- Finalize: the re-applied migration is a clean idempotent no-op ------
	// §13.4 is purely additive with no contract phase that drops columns, so the
	// finalize step is the migration re-applying cleanly. Opening the migrated
	// database directly re-runs applyAdditivePostgres (ADD COLUMN IF NOT EXISTS)
	// as a no-op and the seeded row is unchanged.
	pg, err := store.OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("re-open migrated Postgres (idempotent re-migrate): %v", err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	manifests, err := pg.ListManifests(context.Background(), lcDefaultOrgID())
	if err != nil {
		t.Fatalf("ListManifests after idempotent re-migrate: %v", err)
	}
	var found bool
	for _, m := range manifests {
		if m.ArtifactID == id && m.Version == version {
			found = true
			if m.ContentHash != contentHash {
				t.Errorf("after idempotent re-migrate seeded row hash = %q, want %q", m.ContentHash, contentHash)
			}
		}
	}
	if !found {
		t.Errorf("seeded row %s@%s absent after idempotent re-migrate (manifests: %d rows)", id, version, len(manifests))
	}
}

// TestServerOps_RollbackBeforeFinalize asserts the operator guide's rollback
// claim over a live Postgres metadata store: after the current binary migrates
// the database forward (the expand step), reverting to the earlier binary is
// safe because the additive schema stays forward-compatible with the previous
// version's binary. The earlier binary referenced only its prior-version column
// set, without the recent additive columns; this test reproduces that binary by
// issuing reads and writes against the migrated (wider) manifests table using
// only that prior column set, and asserts they still succeed. It also boots a
// current binary afterward to confirm the post-rollback writes coexist with the
// migrated schema. There is no contract phase that drops columns (§13.4), so a
// rollback before any finalize never loses the new binary's data.
//
// Spec: §13.4 (the additive schema stays forward-compatible with the previous
// binary, so an older binary continues to run against the migrated database),
// §4.7.1 (schema-per-org).
func TestServerOps_RollbackBeforeFinalize(t *testing.T) {
	// Not parallel: shares and resets the default org schema.
	dsn := ruSkipIfNoPostgres(t)

	// ---- Stage the earlier binary's database, then expand with the new binary -
	id, version, contentHash := ruStageLegacyDatabase(t, dsn)
	priv, pemPath := injKeyPair(t)
	srvNew := ruStartUpgradedServer(t, dsn)
	injRegisterRuntime(t, srvNew, pemPath)
	token := injSignJWT(t, priv, injClaims("alice@acme.com"))

	// The new binary ingests an artifact, populating the recent additive
	// columns. A rollback before any finalize must not lose this row.
	newID := ruIngestLayer(t, srvNew, token, "ops-new", "ops/runbooks/rotate-keys", "rotate keys")
	if !ruColumnExists(t, dsn, "manifests", "deleted_at") {
		t.Fatalf("expand step did not add manifests.deleted_at; cannot test rollback")
	}

	// ---- Roll back: the earlier binary's column set still reads and writes ----
	// The earlier binary's queries never reference the recent additive columns.
	// A rollback reverts to that binary, which must keep reading and writing the
	// migrated database. Reproduce it with a raw connection that touches only the
	// prior-version columns against the now-wider manifests table.
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open postgres as rolled-back binary: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	ruExec(t, db, "SET search_path TO "+ruDefaultOrgSchema(t))

	// Read: the earlier binary selects only its original columns and still finds
	// both the seeded row and the new binary's ingest in the migrated table.
	rollbackRead := func(artifactID string) (gotVersion, gotHash, gotType string, ok bool) {
		row := db.QueryRowContext(context.Background(),
			`SELECT version, content_hash, type FROM manifests WHERE tenant_id = $1 AND artifact_id = $2`,
			lcDefaultOrgID(), artifactID)
		err := row.Scan(&gotVersion, &gotHash, &gotType)
		if err == sql.ErrNoRows {
			return "", "", "", false
		}
		if err != nil {
			t.Fatalf("rolled-back read of %q (legacy column set) failed against migrated table: %v", artifactID, err)
		}
		return gotVersion, gotHash, gotType, true
	}
	if v, h, _, ok := rollbackRead(id); !ok {
		t.Errorf("rolled-back binary cannot read the seeded row from the migrated table")
	} else if v != version || h != contentHash {
		t.Errorf("rolled-back read of seeded row = %s/%s, want %s/%s", v, h, version, contentHash)
	}
	if _, _, _, ok := rollbackRead(newID); !ok {
		t.Errorf("rolled-back binary cannot read the new binary's ingest %q (rollback lost the new data)", newID)
	}

	// Write: the earlier binary inserts a row naming only the columns it knew
	// (its prior-version column set, which did not include the recent additive
	// columns). Those recent columns carry NOT-NULL defaults or are nullable, so
	// the insert against the wider migrated table succeeds without naming them.
	// The row joins the seeded public layer so the re-upgraded server can load it.
	rolledBackID := "ops/runbooks/failover-db"
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO manifests (tenant_id, artifact_id, version, content_hash, type, description, layer, ingested_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, now())`,
		lcDefaultOrgID(), rolledBackID, "2.0.0", "sha256:rolledback", "context", "failover the db", ruLegacyLayer); err != nil {
		t.Fatalf("rolled-back write (legacy column set) failed against migrated table: %v", err)
	}
	if v, h, _, ok := rollbackRead(rolledBackID); !ok || v != "2.0.0" || h != "sha256:rolledback" {
		t.Errorf("rolled-back write not durable: got %s/%s ok=%v, want 2.0.0/sha256:rolledback", v, h, ok)
	}

	// ---- Re-upgrade coexists: a current binary reads every row -----------------
	// Rolling forward again (a current binary against the same database) reads
	// the seeded row, the first upgrade's ingest, and the row the rolled-back
	// binary wrote, so no step in the rollback lost data and the schema stayed
	// forward-compatible throughout.
	srvReupgrade := ruStartUpgradedServer(t, dsn)
	injRegisterRuntime(t, srvReupgrade, pemPath)
	for _, want := range []string{id, newID, rolledBackID} {
		if st, _, body := ruLoad(t, srvReupgrade, token, want, ""); st != 200 {
			t.Errorf("re-upgraded binary cannot load %q after the rollback: HTTP %d\nbody: %s", want, st, body)
		}
	}

	// Scan-safety: the row the rolled-back binary wrote omitted every recent
	// additive column (resources, signature, deleted_at, and the rest), so a
	// current-binary store read must scan it cleanly with those columns at their
	// nullable/default backfill rather than erroring. GetManifest returning no
	// error is the scan-safety witness; the written columns survive verbatim.
	pg, err := store.OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("re-open migrated Postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	m, err := pg.GetManifest(context.Background(), lcDefaultOrgID(), rolledBackID, "2.0.0")
	if err != nil {
		t.Fatalf("store read of the rolled-back binary's row (scan-safety after legacy insert): %v", err)
	}
	if m.ContentHash != "sha256:rolledback" {
		t.Errorf("rolled-back row content_hash = %q, want sha256:rolledback", m.ContentHash)
	}
	if m.Description != "failover the db" {
		t.Errorf("rolled-back row description = %q, want %q (the legacy-column write must survive the store scan)", m.Description, "failover the db")
	}
	if m.Layer != ruLegacyLayer {
		t.Errorf("rolled-back row layer = %q, want %q", m.Layer, ruLegacyLayer)
	}
}
