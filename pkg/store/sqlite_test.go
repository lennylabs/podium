package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

// Spec: §7.3.1 (Repository credentials) — "stores it unchanged". After a put
// and a read-modify-write of a credential-bearing layer on SQLite, the repo
// column holds the registered bytes and repo_userinfo is NULL.
func TestSQLite_LayerRepoColumnHoldsRegisteredBytes(t *testing.T) {
	t.Parallel()
	const registered = "https://alice-user:s3cr3tpw@git.acme.com/acme/a b.git"
	s := openLayerRepoSQLite(t)
	ctx := context.Background()
	want := layerRepoColumns{Repo: registered}

	if err := s.PutLayerConfig(ctx, LayerConfig{TenantID: "t", ID: "team", SourceType: "git", Repo: registered}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	if got := rawLayerRepo(t, s.db, sqliteSelectLayerRepo, "t", "team"); got != want {
		t.Errorf("columns after put = %+v, want %+v", got, want)
	}

	cfg, err := s.GetLayerConfig(ctx, "t", "team")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	cfg.LastIngestedRef = "abc123"
	if err := s.PutLayerConfig(ctx, cfg); err != nil {
		t.Fatalf("PutLayerConfig (read-modify-write): %v", err)
	}
	if got := rawLayerRepo(t, s.db, sqliteSelectLayerRepo, "t", "team"); got != want {
		t.Errorf("columns after read-modify-write = %+v, want %+v", got, want)
	}
}

// legacySQLitePutLayerConfig is a frozen copy of the statement that the 0.5.2
// SQLite PutLayerConfig executes (git show v0.5.2:pkg/store/sqlite.go). It
// names no repo_userinfo column. Delete it, with the rollback test that
// executes it, when step 2 of the credential split raises the rollback floor
// above 0.5.2.
const legacySQLitePutLayerConfig = `
		INSERT OR REPLACE INTO layer_configs
			(tenant_id, id, source_type, repo, ref, root, local_path, ord,
			 user_defined, owner, public, organization, groups, users,
			 webhook_secret, last_ingested_ref, force_push_policy, git_provider, created_at, deleted_at, last_ingested_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// Spec: §7.3.1 (Repository credentials) — a 0.5.2 binary runs against a
// SQLite database this release created and wrote. The statement is the frozen
// 0.5.2 writer, which names no repo_userinfo column, so the test fails if the
// column is ever declared NOT NULL without a default. Delete this test when
// step 2 of the credential split raises the rollback floor.
func TestSQLite_LayerRepoRollbackWriter(t *testing.T) {
	t.Parallel()
	const registered = "https://alice-user:s3cr3tpw@git.acme.com/acme/x.git"
	s := openLayerRepoSQLite(t)
	ctx := context.Background()

	// This release writes the row first, so the 0.5.2 statement replaces an
	// existing row and inserts a new one.
	if err := s.PutLayerConfig(ctx, LayerConfig{TenantID: "t", ID: "existing", SourceType: "git", Repo: registered}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	for _, id := range []string{"existing", "added"} {
		if _, err := s.db.ExecContext(ctx, legacySQLitePutLayerConfig,
			"t", id, "git", registered, "main", "", "", 0,
			0, "", 0, 0, "", "",
			"", "", "", "", time.Now().UTC().Format(time.RFC3339Nano), nil, nil); err != nil {
			t.Fatalf("0.5.2 writer on layer %q: %v", id, err)
		}
		got, err := s.GetLayerConfig(ctx, "t", id)
		if err != nil {
			t.Fatalf("GetLayerConfig(%q): %v", id, err)
		}
		if got.Repo != "https://git.acme.com/acme/x.git" || got.CloneRepo() != registered {
			t.Errorf("layer %q after the 0.5.2 writer: Repo %q, CloneRepo %q; want the split of %q",
				id, got.Repo, got.CloneRepo(), registered)
		}
	}
}

// Spec: §7.3.1 (Repository credentials) — the SQLite read honors a non-NULL
// repo_userinfo column on the get, the list, and the deleted list, and the
// next put stores the recomposed registered bytes in repo.
//
// The NULL asserted at the end does not show that the put clears the column:
// INSERT OR REPLACE resets repo_userinfo whether or not the statement binds
// it. The Postgres test pins the clearing.
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

	// The row a step-2 writer would store: a bare repo and its userinfo.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO layer_configs (tenant_id, id, source_type, repo, created_at, repo_userinfo)
		 VALUES ('t', 'team', 'git', ?, '2026-01-01T00:00:00Z', 'ghp_tok3n')`, bare); err != nil {
		t.Fatalf("insert the step-2 row: %v", err)
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
	want := layerRepoColumns{Repo: joined, Userinfo: sql.NullString{}}
	if got := rawLayerRepo(t, s.db, sqliteSelectLayerRepo, "t", "team"); got != want {
		t.Errorf("columns after the put back = %+v, want %+v", got, want)
	}
}
