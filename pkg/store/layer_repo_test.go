package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// Spec: §7.3.1 (Repository credentials) — the read rule recombines a repo
// column with a non-NULL repo_userinfo column only when the result describes
// the row, and it ignores the column otherwise.
//
// The ssh row pins the round-trip comparison, Bare == repo and RawUserinfo ==
// userinfo.String, which no other row reaches. Join returns
// ssh://tok@evil.com/y@git.acme.com/x.git. The '@'-after-authority check
// applies to http and https alone, so that value is in the split class, and
// its parts are the userinfo tok and the bare URL
// ssh://evil.com/y@git.acme.com/x.git. Neither equals its input. A rule that
// omits the comparison returns a clone URL whose host is evil.com.
func TestSplitLayerRepo(t *testing.T) {
	t.Parallel()
	col := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
	cases := []struct {
		name      string
		repo      string
		userinfo  sql.NullString
		wantRepo  string
		wantClone string
		wantSplit bool
	}{
		{"column recombined", "https://git.acme.com/x.git", col("ghp_tok3n"),
			"https://git.acme.com/x.git", "https://ghp_tok3n@git.acme.com/x.git", true},
		{"empty non-NULL column", "https://git.acme.com/x.git", col(""),
			"https://git.acme.com/x.git", "https://@git.acme.com/x.git", true},
		{"repo already split, column ignored", "https://alice-user@git.acme.com/x.git", col("ghp_tok3n"),
			"https://git.acme.com/x.git", "https://alice-user@git.acme.com/x.git", true},
		{"scp-like repo, column ignored", "git@github.com:acme/x.git", col("ghp_tok3n"),
			"git@github.com:acme/x.git", "git@github.com:acme/x.git", false},
		{"recombined value fail-closed, column ignored", "https://git.acme.com/x.git", col("ghp_tok/3n"),
			"https://git.acme.com/x.git", "https://git.acme.com/x.git", false},
		{"recombined value splits into other parts, column ignored", "ssh://git.acme.com/x.git", col("tok@evil.com/y"),
			"ssh://git.acme.com/x.git", "ssh://git.acme.com/x.git", false},
		{"NULL column", "https://git.acme.com/x.git", sql.NullString{},
			"https://git.acme.com/x.git", "https://git.acme.com/x.git", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo, registered := SplitLayerRepo(tc.repo, tc.userinfo)
			cfg := LayerConfig{Repo: repo, RegisteredRepo: registered}
			if repo != tc.wantRepo || cfg.CloneRepo() != tc.wantClone || registered.Present() != tc.wantSplit {
				t.Errorf("SplitLayerRepo(%q, %+v) = Repo %q, CloneRepo %q, split %t; want %q, %q, %t",
					tc.repo, tc.userinfo, repo, cfg.CloneRepo(), registered.Present(),
					tc.wantRepo, tc.wantClone, tc.wantSplit)
			}
		})
	}
}

// Spec: §7.3.1 (Repository credentials) — the write rule stores the
// registered bytes only when they are in the split class and redact to Repo.
//
// The conformance error path reaches the Clean != Repo half with a registered
// value inside the split class. The first rows here reach the split-class
// half: each pairs a registered value outside the class with the Repo its
// redaction reports, so a rule that compares the redaction alone accepts
// them. The last row fails a rule that redacts Repo before comparing, because
// redacting that Repo a second time gives [redacted].
func TestLayerRepoColumn(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		registered string
		repo       string
		want       string
		refused    bool
	}{
		{"fail-closed registered value", "https://ghp_tok/3n@host/x.git", "[redacted]", "", true},
		{"scp-like registered value", "git@github.com:acme/x.git", "git@github.com:acme/x.git", "", true},
		{"network-path registered value", "//tok@host/x://y", "//host/x://y", "", true},
		{"registered value without userinfo", "https://git.acme.com/x.git", "https://git.acme.com/x.git", "", true},
		{"split class, Repo differs", "https://ghp_tok3n@git.acme.com/x.git", "https://git.acme.com/other.git", "", true},
		{"split class, Repo matches", "https://ghp_tok3n@git.acme.com/x.git", "https://git.acme.com/x.git",
			"https://ghp_tok3n@git.acme.com/x.git", false},
		{"reported form carries @ after the host", "https://ghp_tok3n@git.acme.com/acme/a%40b c.git",
			"https://git.acme.com/acme/a@b%20c.git", "https://ghp_tok3n@git.acme.com/acme/a%40b c.git", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := LayerRepoColumn(LayerConfig{Repo: tc.repo, RegisteredRepo: RegisteredRepo(tc.registered)})
			if got != tc.want || errors.Is(err, ErrRepoCredentialMismatch) != tc.refused || (err != nil) != tc.refused {
				t.Errorf("LayerRepoColumn(registered %q, repo %q) = %q, %v; want %q, refused %t",
					tc.registered, tc.repo, got, err, tc.want, tc.refused)
			}
		})
	}
}

// layerRepoColumns is the raw content of one layer row's repo columns.
type layerRepoColumns struct {
	Repo     string
	Userinfo sql.NullString
}

// rowQuerier is the read both *sql.DB and *sql.Conn offer.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// rawLayerRepo reads the repo and repo_userinfo columns of one layer row with
// no store read rule in between. query carries the backend's placeholders.
func rawLayerRepo(t *testing.T, q rowQuerier, query, tenantID, id string) layerRepoColumns {
	t.Helper()
	var got layerRepoColumns
	if err := q.QueryRowContext(context.Background(), query, tenantID, id).Scan(&got.Repo, &got.Userinfo); err != nil {
		t.Fatalf("select repo, repo_userinfo for %s/%s: %v", tenantID, id, err)
	}
	return got
}

const pgSelectLayerRepo = `SELECT repo, repo_userinfo FROM layer_configs WHERE tenant_id = $1 AND id = $2`

// legacyPostgresPutLayerConfig is a frozen copy of the upsert that the 0.5.2
// Postgres PutLayerConfig executes (git show v0.5.2:pkg/store/postgres.go).
// It names no repo_userinfo column. Delete it, with the rollback test that
// executes it, when step 2 of the credential split raises the rollback floor
// above 0.5.2.
const legacyPostgresPutLayerConfig = `
		INSERT INTO layer_configs
			(tenant_id, id, source_type, repo, ref, root, local_path, ord,
			 user_defined, owner, public, organization, groups, users,
			 webhook_secret, last_ingested_ref, force_push_policy, git_provider, created_at, deleted_at, last_ingested_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
		ON CONFLICT (tenant_id, id) DO UPDATE SET
			source_type = EXCLUDED.source_type,
			repo = EXCLUDED.repo,
			ref = EXCLUDED.ref,
			root = EXCLUDED.root,
			local_path = EXCLUDED.local_path,
			ord = EXCLUDED.ord,
			user_defined = EXCLUDED.user_defined,
			owner = EXCLUDED.owner,
			public = EXCLUDED.public,
			organization = EXCLUDED.organization,
			groups = EXCLUDED.groups,
			users = EXCLUDED.users,
			webhook_secret = EXCLUDED.webhook_secret,
			last_ingested_ref = EXCLUDED.last_ingested_ref,
			force_push_policy = EXCLUDED.force_push_policy,
			git_provider = EXCLUDED.git_provider,
			created_at = EXCLUDED.created_at,
			deleted_at = EXCLUDED.deleted_at,
			last_ingested_at = EXCLUDED.last_ingested_at`

// openPostgresOrg opens the Postgres store named by PODIUM_POSTGRES_DSN and
// returns it with a connection pinned to a fresh org schema for tenantID, so
// a test can read and write the org's layer_configs table with raw SQL. The
// test skips when the DSN is unset or the database is unreachable. Each test
// uses its own tenant ID and drops the org on cleanup, which keeps it apart
// from the conformance run on the same database.
func openPostgresOrg(t *testing.T, tenantID string) (*Postgres, *sql.Conn) {
	t.Helper()
	dsn := os.Getenv("PODIUM_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN unset; skipping Postgres layer repo check")
	}
	p, err := OpenPostgres(dsn)
	if err != nil {
		t.Skipf("OpenPostgres: %v (database unreachable)", err)
	}
	ctx := context.Background()
	if err := p.DropOrg(ctx, tenantID); err != nil {
		t.Fatalf("DropOrg before test: %v", err)
	}
	t.Cleanup(func() {
		_ = p.DropOrg(context.Background(), tenantID)
		_ = p.Close()
	})
	if err := p.CreateTenant(ctx, Tenant{ID: tenantID, Name: tenantID}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		t.Fatalf("org connection: %v", err)
	}
	// Registered after the DropOrg cleanup, so the connection is released
	// before the schema it is pinned to is dropped.
	t.Cleanup(release)
	return p, conn
}

// Spec: §7.3.1 (Repository credentials) — "stores it unchanged". After a put
// and a read-modify-write of a credential-bearing layer on Postgres, the repo
// column holds the registered bytes and repo_userinfo is NULL.
func TestPostgres_LayerRepoColumnHoldsRegisteredBytes(t *testing.T) {
	const tenant, registered = "layer-repo-raw", "https://alice-user:s3cr3tpw@git.acme.com/acme/a b.git"
	p, conn := openPostgresOrg(t, tenant)
	ctx := context.Background()
	want := layerRepoColumns{Repo: registered}

	if err := p.PutLayerConfig(ctx, LayerConfig{TenantID: tenant, ID: "team", SourceType: "git", Repo: registered}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	if got := rawLayerRepo(t, conn, pgSelectLayerRepo, tenant, "team"); got != want {
		t.Errorf("columns after put = %+v, want %+v", got, want)
	}

	cfg, err := p.GetLayerConfig(ctx, tenant, "team")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	cfg.LastIngestedRef = "abc123"
	if err := p.PutLayerConfig(ctx, cfg); err != nil {
		t.Fatalf("PutLayerConfig (read-modify-write): %v", err)
	}
	if got := rawLayerRepo(t, conn, pgSelectLayerRepo, tenant, "team"); got != want {
		t.Errorf("columns after read-modify-write = %+v, want %+v", got, want)
	}
}

// Spec: §7.3.1 (Repository credentials) — a 0.5.2 binary runs against a
// Postgres database this release created and wrote. The statement is the
// frozen 0.5.2 upsert, which names no repo_userinfo column, so the test fails
// if the column is ever declared NOT NULL without a default. Delete this test
// when step 2 of the credential split raises the rollback floor.
func TestPostgres_LayerRepoRollbackWriter(t *testing.T) {
	const tenant, registered = "layer-repo-rollback", "https://alice-user:s3cr3tpw@git.acme.com/acme/x.git"
	p, conn := openPostgresOrg(t, tenant)
	ctx := context.Background()

	// This release writes the row first, so the 0.5.2 statement takes both
	// the insert arm (a new layer) and the conflict arm (the existing one).
	if err := p.PutLayerConfig(ctx, LayerConfig{TenantID: tenant, ID: "existing", SourceType: "git", Repo: registered}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	for _, id := range []string{"existing", "added"} {
		if _, err := conn.ExecContext(ctx, legacyPostgresPutLayerConfig,
			tenant, id, "git", registered, "main", "", "", 0,
			false, "", false, false, "", "",
			"", "", "", "", time.Now().UTC(), nil, nil); err != nil {
			t.Fatalf("0.5.2 writer on layer %q: %v", id, err)
		}
		got, err := p.GetLayerConfig(ctx, tenant, id)
		if err != nil {
			t.Fatalf("GetLayerConfig(%q): %v", id, err)
		}
		if got.Repo != "https://git.acme.com/acme/x.git" || got.CloneRepo() != registered {
			t.Errorf("layer %q after the 0.5.2 writer: Repo %q, CloneRepo %q; want the split of %q",
				id, got.Repo, got.CloneRepo(), registered)
		}
	}
}

// Spec: §7.3.1 (Repository credentials) — the Postgres read honors a non-NULL
// repo_userinfo column on the get, the list, and the deleted list, and the
// next put clears the column.
//
// The last assertion fails when repo_userinfo = EXCLUDED.repo_userinfo is
// absent from the DO UPDATE SET list, because the upsert then leaves the
// column holding ghp_tok3n beside a repo that already carries it. A store
// read cannot stand in for the raw SELECT: once repo is in the split class
// the read ignores the column, so a stale value is invisible to
// GetLayerConfig.
func TestPostgres_LayerRepoUserinfoColumnReadAndCleared(t *testing.T) {
	const (
		tenant = "layer-repo-forward"
		bare   = "https://git.acme.com/x.git"
		joined = "https://ghp_tok3n@git.acme.com/x.git"
	)
	p, conn := openPostgresOrg(t, tenant)
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

	// Step 1: the row exists, so the put in step 4 takes the ON CONFLICT arm.
	if err := p.PutLayerConfig(ctx, LayerConfig{TenantID: tenant, ID: "team", SourceType: "git", Repo: bare}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	// Step 2: the column holds what a step-2 writer would store.
	if _, err := conn.ExecContext(ctx,
		`UPDATE layer_configs SET repo_userinfo = 'ghp_tok3n' WHERE tenant_id = $1 AND id = $2`, tenant, "team"); err != nil {
		t.Fatalf("set repo_userinfo: %v", err)
	}

	// Step 3: scanLayerConfigPG recombines on each of the three reads.
	cfg, err := p.GetLayerConfig(ctx, tenant, "team")
	check("GetLayerConfig", []LayerConfig{cfg}, err)
	list, err := p.ListLayerConfigs(ctx, tenant)
	check("ListLayerConfigs", list, err)
	if err := p.DeleteLayerConfig(ctx, tenant, "team"); err != nil {
		t.Fatalf("DeleteLayerConfig: %v", err)
	}
	deleted, err := p.ListDeletedLayerConfigs(ctx, tenant)
	check("ListDeletedLayerConfigs", deleted, err)
	if err := p.RestoreLayerConfig(ctx, tenant, "team"); err != nil {
		t.Fatalf("RestoreLayerConfig: %v", err)
	}

	// Step 4: put back the config the get returned.
	if err := p.PutLayerConfig(ctx, cfg); err != nil {
		t.Fatalf("PutLayerConfig (put back): %v", err)
	}
	// Step 5: the raw row holds the registered bytes and a cleared column.
	if got, want := rawLayerRepo(t, conn, pgSelectLayerRepo, tenant, "team"), (layerRepoColumns{Repo: joined}); got != want {
		t.Errorf("columns after the put back = %+v, want %+v", got, want)
	}
}

// Spec: §7.3.1 (Repository credentials) — the frozen 0.5.2 writers name no
// repo_userinfo column. A copy edited to name it would no longer stand for
// the 0.5.2 binary, and the rollback tests would pass without testing it.
func TestLegacyLayerWriters_NameNoUserinfoColumn(t *testing.T) {
	t.Parallel()
	for name, stmt := range map[string]string{
		"sqlite":   legacySQLitePutLayerConfig,
		"postgres": legacyPostgresPutLayerConfig,
	} {
		if strings.Contains(stmt, "repo_userinfo") {
			t.Errorf("the frozen 0.5.2 %s writer names repo_userinfo", name)
		}
	}
}
