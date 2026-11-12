package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Spec: §13.10 Standalone Deployment — the SQLite backend persists
// across opens of the same file path.
func TestSQLite_PersistsAcrossOpens(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "podium.db")

	s1, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	ctx := context.Background()
	if err := s1.CreateTenant(ctx, Tenant{ID: "a", Name: "acme"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	rec := ManifestRecord{
		TenantID: "a", ArtifactID: "x", Version: "1.0.0",
		ContentHash: "sha:1", Type: "skill",
		Tags: []string{"finance", "ap"},
	}
	if err := s1.PutManifest(ctx, rec); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite again: %v", err)
	}
	defer func() { _ = s2.Close() }()

	got, err := s2.GetManifest(ctx, "a", "x", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest after reopen: %v", err)
	}
	if got.ContentHash != "sha:1" {
		t.Errorf("ContentHash = %q, want sha:1", got.ContentHash)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "finance" || got.Tags[1] != "ap" {
		t.Errorf("Tags = %v, want [finance ap]", got.Tags)
	}

	tenant, err := s2.GetTenant(ctx, "a")
	if err != nil {
		t.Fatalf("GetTenant: %v", err)
	}
	if tenant.Name != "acme" {
		t.Errorf("Name = %q, want acme", tenant.Name)
	}
}

// Spec: §13.10 — schema apply is idempotent (re-opening an existing
// database does not error and preserves data).
func TestSQLite_SchemaIsIdempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "podium.db")

	for i := 0; i < 3; i++ {
		s, err := OpenSQLite(path)
		if err != nil {
			t.Fatalf("OpenSQLite #%d: %v", i, err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i, err)
		}
	}
}

// Spec: §4.7 Version immutability invariant — the standalone
// backend is a file-backed SQLite database (WAL plus a busy timeout)
// served through a pooled *sql.DB, so writers run on separate
// connections rather than the serialized single connection the in-memory
// store uses. Concurrent ingest of different content for the same
// (tenant, id, version) must accept exactly one writer and reject every
// other with ErrImmutableViolation, never leaking a raw SQLite error
// (a primary-key unique violation or a "database is locked" contention
// error). This exercises true multi-connection concurrency that the
// in-memory conformance run cannot.
func TestSQLite_ConcurrentConflictMapsToImmutableViolation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "concurrent.db")
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	if err := s.CreateTenant(ctx, Tenant{ID: "a", Name: "a"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	const writers = 32
	var accepted, violations atomic.Int64
	var mu sync.Mutex
	var leaks []error

	ready := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		rec := ManifestRecord{
			TenantID:    "a",
			ArtifactID:  "x",
			Version:     "1.0.0",
			ContentHash: fmt.Sprintf("sha:%d", i),
			Type:        "skill",
			Sensitivity: "low",
			Layer:       "L",
		}
		go func() {
			defer wg.Done()
			<-ready
			switch err := s.PutManifest(ctx, rec); {
			case err == nil:
				accepted.Add(1)
			case errors.Is(err, ErrImmutableViolation):
				violations.Add(1)
			default:
				mu.Lock()
				leaks = append(leaks, err)
				mu.Unlock()
			}
		}()
	}
	close(ready)
	wg.Wait()

	if len(leaks) > 0 {
		t.Fatalf("%d/%d writers leaked a raw SQLite error instead of ErrImmutableViolation; first: %v",
			len(leaks), writers, leaks[0])
	}
	if accepted.Load() != 1 {
		t.Errorf("accepted = %d, want exactly 1", accepted.Load())
	}
	if got := accepted.Load() + violations.Load(); got != int64(writers) {
		t.Errorf("accepted (%d) + violations (%d) = %d, want %d", accepted.Load(), violations.Load(), got, writers)
	}
}

const sqliteSelectLayerRepo = `SELECT repo, repo_userinfo FROM layer_configs WHERE tenant_id = ? AND id = ?`

// openLayerRepoSQLite opens a fresh file-backed SQLite store for the layer
// repo tests, which read and write layer_configs with raw SQL beside the
// store methods.
func openLayerRepoSQLite(t *testing.T) *SQLite {
	t.Helper()
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "layers.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// sqliteLayerRepoBackend opens a fresh SQLite store as a layerRepoBackend.
func sqliteLayerRepoBackend(t *testing.T) layerRepoBackend {
	t.Helper()
	s := openLayerRepoSQLite(t)
	return layerRepoBackend{
		st: s, db: s.db, tenant: "t", sel: sqliteSelectLayerRepo,
		toEarlier:   `UPDATE layer_configs SET repo = ?, repo_userinfo = NULL WHERE tenant_id = ? AND id = ?`,
		setUserinfo: `UPDATE layer_configs SET repo_userinfo = ? WHERE tenant_id = ? AND id = ?`,
		migrate:     s.MigrateLayerRepoForm,
		migrateRow: func(ctx context.Context, row layerRepoRow, repo string, userinfo sql.NullString) (bool, error) {
			return rewriteLayerRepoRowSQLite(ctx, s.db, row, repo, userinfo)
		},
	}
}

// Spec: §7.3.1 (Repository credentials) — on SQLite, a put and a
// read-modify-write of a split-class value store its bare form in repo and its
// userinfo in repo_userinfo, the empty userinfo as a non-NULL empty string.
// Every other value is stored whole beside a NULL repo_userinfo.
func TestSQLite_LayerRepoColumnsHoldBareAndUserinfo(t *testing.T) {
	t.Parallel()
	checkLayerRepoColumnsHoldBareAndUserinfo(t, sqliteLayerRepoBackend(t))
}

// Spec: §7.3.1 (Repository credentials) — a SQLite row that holds the
// registered value whole in repo, as an earlier version writes it, reads as
// held, keeps that form through a soft delete and a restore, and takes the
// two-column form at the next put.
// Spec: §13.4
func TestSQLite_LayerRepoEarlierFormRow(t *testing.T) {
	t.Parallel()
	checkLayerRepoEarlierFormRow(t, sqliteLayerRepoBackend(t))
}

// Spec: §7.3.1 (Repository credentials) — the SQLite read honors a non-NULL
// repo_userinfo column on the get, the list, and the deleted list, and the
// put back writes the bare repo and the userinfo to their two columns again.
//
// INSERT OR REPLACE replaces the whole row, so a put that binds no userinfo
// leaves the column NULL on SQLite whatever the statement lists. The Postgres
// test pins that a later userinfo-free put replaces the column, which there
// depends on the DO UPDATE SET list.
func TestSQLite_LayerRepoUserinfoColumnRead(t *testing.T) {
	t.Parallel()
	const bare, joined = "https://git.acme.com/x.git", "https://ghp_tok3n@git.acme.com/x.git"
	s := openLayerRepoSQLite(t)
	ctx := context.Background()
	check := func(step string, cfgs []LayerConfig, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		if len(cfgs) != 1 || cfgs[0].Repo != bare || cfgs[0].CloneRepo() != joined {
			t.Fatalf("%s = %+v, want one layer with Repo %q and CloneRepo %q", step, cfgs, bare, joined)
		}
	}

	// The row this release writes: a bare repo and its userinfo.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO layer_configs (tenant_id, id, source_type, repo, created_at, repo_userinfo)
		 VALUES ('t', 'team', 'git', ?, '2026-01-01T00:00:00Z', 'ghp_tok3n')`, bare); err != nil {
		t.Fatalf("insert the two-column row: %v", err)
	}

	cfg, err := s.GetLayerConfig(ctx, "t", "team")
	check("GetLayerConfig", []LayerConfig{cfg}, err)
	list, err := s.ListLayerConfigs(ctx, "t")
	check("ListLayerConfigs", list, err)
	if err := s.DeleteLayerConfig(ctx, "t", "team"); err != nil {
		t.Fatalf("DeleteLayerConfig: %v", err)
	}
	deleted, err := s.ListDeletedLayerConfigs(ctx, "t")
	check("ListDeletedLayerConfigs", deleted, err)
	if err := s.RestoreLayerConfig(ctx, "t", "team"); err != nil {
		t.Fatalf("RestoreLayerConfig: %v", err)
	}

	if err := s.PutLayerConfig(ctx, cfg); err != nil {
		t.Fatalf("PutLayerConfig (put back): %v", err)
	}
	want := layerRepoColumns{Repo: bare, Userinfo: sql.NullString{String: "ghp_tok3n", Valid: true}}
	if got := rawLayerRepo(t, s.db, sqliteSelectLayerRepo, "t", "team"); got != want {
		t.Errorf("columns after the put back = %+v, want %+v", got, want)
	}
}

// Spec: §13.4 — at start every SQLite layer row whose repo columns differ
// from the form its next write would take is rewritten into that form, for
// every tenant and soft-deleted rows included, and a second pass writes
// nothing.
// Spec: §7.3.1 (Repository credentials)
//
// The count is 6: the four whole credentialed rows of the check, its
// ignored-column row, and the second tenant's row.
func TestSQLite_LayerRepoFormMigration(t *testing.T) {
	t.Parallel()
	b := sqliteLayerRepoBackend(t)
	const globexRepo = "https://bob-user:hunt3r2@git.acme.com/globex/x.git"
	ctx := context.Background()
	mustDo(t, "PutLayerConfig globex", b.st.PutLayerConfig(ctx, LayerConfig{TenantID: "globex", ID: "team", SourceType: "git", Repo: globexRepo}))
	_, err := b.db.ExecContext(ctx, b.toEarlier, globexRepo, "globex", "team")
	mustDo(t, "set the earlier form under globex", err)

	n := checkLayerRepoFormMigration(t, b)
	got := rawLayerRepo(t, b.db, b.sel, "globex", "team")
	want := layerRepoTwoColumn("https://git.acme.com/globex/x.git", "bob-user:hunt3r2")
	if n != 6 || got != want {
		t.Errorf("first call rewrote %d, want 6; globex row %+v, want %+v", n, got, want)
	}
}

// Spec: §13.4 — the SQLite per-row statement leaves a row another writer
// changed after the scan as that writer wrote it, and a pass on a closed store
// returns an error.
// Spec: §7.3.1 (Repository credentials)
func TestSQLite_LayerRepoFormMigration_LeavesARowAnotherWriterChanged(t *testing.T) {
	t.Parallel()
	b := sqliteLayerRepoBackend(t)
	checkLayerRepoFormMigrationLeavesARowAnotherWriterChanged(t, b)
	mustDo(t, "Close", b.st.(*SQLite).Close())
	if n, err := b.migrate(context.Background()); err == nil || !strings.HasPrefix(err.Error(), "store: migrate layer repo form: ") {
		t.Errorf("MigrateLayerRepoForm on a closed store = %d, %v; want a store: migrate layer repo form error", n, err)
	}
}

// Spec: §4.7.3 Reverse Dependency Index — a dependency read the database
// cannot answer returns the error rather than an empty edge list or zero
// in-degrees, so a caller never reads a failed query as "no dependents".
func TestSQLite_DependencyReadsReturnQueryErrors(t *testing.T) {
	t.Parallel()
	s := openLayerRepoSQLite(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := s.DependentsOf(ctx, "a", "parent"); err == nil {
		t.Errorf("DependentsOf on a canceled context = %v, nil; want an error", got)
	}
	if got, err := s.DependencyInDegree(ctx, "a"); err == nil {
		t.Errorf("DependencyInDegree on a canceled context = %v, nil; want an error", got)
	}
}
