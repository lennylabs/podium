package store

import (
	"context"
	"database/sql"
	"errors"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/repourl"
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

// Spec: §7.3.1 (Repository credentials) — the write rule returns a
// registered value in the split class as its bare form and its userinfo, the
// empty userinfo included, and every other value whole with a NULL userinfo.
// It splits RegisteredRepo when present and Repo otherwise.
//
// The conformance error path reaches the Clean != Repo half with a registered
// value inside the split class. The first refusing rows reach the split-class
// half: each pairs a registered value outside the class with the Repo its
// redaction reports, so a rule that compares the redaction alone accepts
// them. The reported-form row fails a rule that redacts Repo before
// comparing, because redacting that Repo a second time gives [redacted].
func TestLayerRepoColumns(t *testing.T) {
	t.Parallel()
	registered := func(reg, repo string) LayerConfig {
		return LayerConfig{Repo: repo, RegisteredRepo: RegisteredRepo(reg)}
	}
	split := func(bare, userinfo string) layerRepoColumns {
		return layerRepoColumns{Repo: bare, Userinfo: sql.NullString{String: userinfo, Valid: true}}
	}
	whole := func(repo string) layerRepoColumns { return layerRepoColumns{Repo: repo} }
	cases := []struct {
		name    string
		cfg     LayerConfig
		want    layerRepoColumns
		refused bool
	}{
		{"fail-closed registered value", registered("https://ghp_tok/3n@host/x.git", "[redacted]"), layerRepoColumns{}, true},
		{"scp-like registered value", registered("git@github.com:acme/x.git", "git@github.com:acme/x.git"), layerRepoColumns{}, true},
		{"network-path registered value", registered("//tok@host/x://y", "//host/x://y"), layerRepoColumns{}, true},
		{"registered value without userinfo", registered("https://git.acme.com/x.git", "https://git.acme.com/x.git"), layerRepoColumns{}, true},
		{"split class, Repo differs", registered("https://ghp_tok3n@git.acme.com/x.git", "https://git.acme.com/other.git"), layerRepoColumns{}, true},
		{"split class, Repo matches", registered("https://ghp_tok3n@git.acme.com/x.git", "https://git.acme.com/x.git"),
			split("https://git.acme.com/x.git", "ghp_tok3n"), false},
		{"reported form carries @ after the host",
			registered("https://ghp_tok3n@git.acme.com/acme/a%40b c.git", "https://git.acme.com/acme/a@b%20c.git"),
			split("https://git.acme.com/acme/a%40b c.git", "ghp_tok3n"), false},
		{"credential in Repo alone", LayerConfig{Repo: "https://ghp_tok3n@git.acme.com/x.git"},
			split("https://git.acme.com/x.git", "ghp_tok3n"), false},
		{"empty userinfo", LayerConfig{Repo: "https://@git.acme.com/x.git"}, split("https://git.acme.com/x.git", ""), false},
		{"colon userinfo", LayerConfig{Repo: "https://:@host/x"}, split("https://host/x", ":"), false},
		{"userinfo with @", LayerConfig{Repo: "https://a@b@host/x"}, split("https://host/x", "a@b"), false},
		{"uppercase scheme", LayerConfig{Repo: "HTTPS://t@host/a.git"}, split("HTTPS://host/a.git", "t"), false},
		{"ssh with port, query, and fragment", LayerConfig{Repo: "ssh://u:p@h:22/x?y#z@w"}, split("ssh://h:22/x?y#z@w", "u:p"), false},
		{"scp-like remote", LayerConfig{Repo: "git@github.com:acme/x.git"}, whole("git@github.com:acme/x.git"), false},
		{"filesystem path", LayerConfig{Repo: "/srv/git/acme.git"}, whole("/srv/git/acme.git"), false},
		{"network-path value", LayerConfig{Repo: "//tok@host/x://y"}, whole("//tok@host/x://y"), false},
		{"fail-closed value", LayerConfig{Repo: "https://ghp_tok/3n@host/x.git"}, whole("https://ghp_tok/3n@host/x.git"), false},
		{"userinfo-free URL", LayerConfig{Repo: "https://git.acme.com/x.git"}, whole("https://git.acme.com/x.git"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo, userinfo, err := LayerRepoColumns(tc.cfg)
			got := layerRepoColumns{Repo: repo, Userinfo: userinfo}
			if got != tc.want || errors.Is(err, ErrRepoCredentialMismatch) != tc.refused || (err != nil) != tc.refused {
				t.Errorf("LayerRepoColumns(registered %q, repo %q) = %+v, %v; want %+v, refused %t",
					string(tc.cfg.RegisteredRepo), tc.cfg.Repo, got, err, tc.want, tc.refused)
			}
		})
	}
}

// layerRepoCorpus returns the round-trip test's inputs: every combination of
// a scheme prefix, a userinfo, a host, and a path, followed by values drawn
// from an adversarial alphabet under a fixed seed. The prefixes cover an
// uppercase scheme, the http class with its fail-closed '@' check, a scheme
// outside that class, a network-path value, and no scheme, and the explicit
// entries add an scp-like remote and a filesystem path.
func layerRepoCorpus() []string {
	schemes := []string{"https://", "HTTPS://", "http://", "ssh://", "git+ssh://", "file://", "//", ""}
	userinfos := []string{"-", "", "tok", "u:p", ":", "a@b", "t%40k", "a b", "t/k", "t?k", "t#k", "t:p%2F"}
	hosts := []string{"git.acme.com", "h:22", ""}
	paths := []string{"", "/x.git", "/a b.git", "/a%40b", "/x?y#z@w", "/x@y", "/x://y", "?q@r", "#f@g"}
	corpus := []string{"git@github.com:acme/x.git", "/srv/git/acme.git", "./rel/x.git", "", "https://"}
	for _, scheme := range schemes {
		for _, userinfo := range userinfos {
			for _, host := range hosts {
				for _, path := range paths {
					authority := host
					if userinfo != "-" {
						authority = userinfo + "@" + host
					}
					corpus = append(corpus, scheme+authority+path)
				}
			}
		}
	}
	const alphabet = "@:/?#% aZ.%40"
	rng := rand.New(rand.NewPCG(54, 2))
	for range 4000 {
		b := []byte(schemes[rng.IntN(len(schemes))])
		for range rng.IntN(16) {
			b = append(b, alphabet[rng.IntN(len(alphabet))])
		}
		corpus = append(corpus, string(b))
	}
	return corpus
}

// Spec: §7.3.1 (Repository credentials) — the read of what the write rule
// stores returns what the read of the registered value held whole returns, for
// every value a caller registers. A value the write splits must rejoin into
// the registered bytes, and a value it keeps whole must read as held. This is
// the invariant the write rule's unreachable fallback guards.
func TestLayerRepoColumns_RoundTrip(t *testing.T) {
	t.Parallel()
	type read struct {
		Repo       string
		Registered RegisteredRepo
	}
	var classes struct{ split, emptyUserinfo, failClosed, noScheme, wholeURL int }
	for _, x := range layerRepoCorpus() {
		repo, userinfo, err := LayerRepoColumns(LayerConfig{Repo: x})
		var got, want read
		got.Repo, got.Registered = SplitLayerRepo(repo, userinfo)
		want.Repo, want.Registered = SplitLayerRepo(x, sql.NullString{})
		if err != nil || got != want {
			t.Errorf("value %q: stored as %q, %+v (err %v) reads %+v; held whole it reads %+v", x, repo, userinfo, err, got, want)
		}
		switch parts, ok := repourl.Split(x); {
		case ok && parts.RawUserinfo == "":
			classes.emptyUserinfo++
		case ok:
			classes.split++
		case repourl.Unsafe(x):
			classes.failClosed++
		case !strings.Contains(x, "://"):
			classes.noScheme++
		default:
			classes.wholeURL++
		}
	}
	if classes.split == 0 || classes.emptyUserinfo == 0 || classes.failClosed == 0 || classes.noScheme == 0 || classes.wholeURL == 0 {
		t.Errorf("corpus misses a class: %+v", classes)
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

// layerRepoDB is the raw access both *sql.DB and *sql.Conn offer.
type layerRepoDB interface {
	rowQuerier
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// layerRepoBackend is a SQL store under test together with raw access to its
// layer_configs table. The queries carry the backend's placeholders: sel takes
// the tenant and the layer ID, and toEarlier takes the repo, the tenant, and
// the layer ID.
type layerRepoBackend struct {
	st        Store
	db        layerRepoDB
	tenant    string
	sel       string
	toEarlier string
}

func (b layerRepoBackend) raw(t *testing.T, id string) layerRepoColumns {
	t.Helper()
	return rawLayerRepo(t, b.db, b.sel, b.tenant, id)
}

// layerRepoRead is what a store read returns for a layer's repo.
type layerRepoRead struct {
	Repo  string
	Clone string
}

func layerRepoReads(cfgs ...LayerConfig) []layerRepoRead {
	out := make([]layerRepoRead, 0, len(cfgs))
	for _, c := range cfgs {
		out = append(out, layerRepoRead{Repo: c.Repo, Clone: c.CloneRepo()})
	}
	return out
}

// mustDo fails the test on err. It keeps the store calls below free of their
// own branches, so each test step ends in one combined comparison.
func mustDo(t *testing.T, step string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", step, err)
	}
}

const (
	pgSelectLayerRepo     = `SELECT repo, repo_userinfo FROM layer_configs WHERE tenant_id = $1 AND id = $2`
	pgLayerRepoToEarlier  = `UPDATE layer_configs SET repo = $1, repo_userinfo = NULL WHERE tenant_id = $2 AND id = $3`
	layerRepoCredentialed = "https://alice-user:s3cr3tpw@git.acme.com/acme/a b.git"
)

// checkLayerRepoColumnsHoldBareAndUserinfo puts one layer per value, then gets
// it and puts it back, and asserts the raw columns after each put.
func checkLayerRepoColumnsHoldBareAndUserinfo(t *testing.T, b layerRepoBackend) {
	ctx := context.Background()
	split := func(bare, userinfo string) layerRepoColumns {
		return layerRepoColumns{Repo: bare, Userinfo: sql.NullString{String: userinfo, Valid: true}}
	}
	cases := []struct {
		id   string
		repo string
		want layerRepoColumns
	}{
		{"credentialed", layerRepoCredentialed, split("https://git.acme.com/acme/a b.git", "alice-user:s3cr3tpw")},
		{"empty-userinfo", "https://@git.acme.com/x.git", split("https://git.acme.com/x.git", "")},
		{"scp-like", "git@github.com:acme/x.git", layerRepoColumns{Repo: "git@github.com:acme/x.git"}},
		{"path", "/srv/git/acme.git", layerRepoColumns{Repo: "/srv/git/acme.git"}},
		{"network-path", "//tok@host/x://y", layerRepoColumns{Repo: "//tok@host/x://y"}},
		{"fail-closed", "https://ghp_tok/3n@host/x.git", layerRepoColumns{Repo: "https://ghp_tok/3n@host/x.git"}},
	}
	for _, tc := range cases {
		mustDo(t, "PutLayerConfig "+tc.id, b.st.PutLayerConfig(ctx, LayerConfig{TenantID: b.tenant, ID: tc.id, SourceType: "git", Repo: tc.repo}))
		afterPut := b.raw(t, tc.id)
		cfg, err := b.st.GetLayerConfig(ctx, b.tenant, tc.id)
		mustDo(t, "GetLayerConfig "+tc.id, err)
		cfg.LastIngestedRef = "abc123"
		mustDo(t, "PutLayerConfig (read-modify-write) "+tc.id, b.st.PutLayerConfig(ctx, cfg))
		if got := [2]layerRepoColumns{afterPut, b.raw(t, tc.id)}; got != [2]layerRepoColumns{tc.want, tc.want} {
			t.Errorf("layer %q columns after put and after read-modify-write = %+v, want %+v twice", tc.id, got, tc.want)
		}
	}
}

// checkLayerRepoEarlierFormRow sets a row this release wrote back to the form
// an earlier version writes, and asserts that the reads return what they
// returned before, that a soft delete and a restore leave the row in that
// form, and that the next put writes the two-column form again.
func checkLayerRepoEarlierFormRow(t *testing.T, b layerRepoBackend) {
	ctx := context.Background()
	const id = "team"
	twoColumn := layerRepoColumns{
		Repo:     "https://git.acme.com/acme/a b.git",
		Userinfo: sql.NullString{String: "alice-user:s3cr3tpw", Valid: true},
	}
	earlier := layerRepoColumns{Repo: layerRepoCredentialed}

	mustDo(t, "PutLayerConfig", b.st.PutLayerConfig(ctx, LayerConfig{TenantID: b.tenant, ID: id, SourceType: "git", Repo: layerRepoCredentialed}))
	if got := b.raw(t, id); got != twoColumn {
		t.Fatalf("columns after put = %+v, want %+v", got, twoColumn)
	}
	before, err := b.st.GetLayerConfig(ctx, b.tenant, id)
	mustDo(t, "GetLayerConfig before", err)
	_, err = b.db.ExecContext(ctx, b.toEarlier, layerRepoCredentialed, b.tenant, id)
	mustDo(t, "set the earlier form", err)

	got, err := b.st.GetLayerConfig(ctx, b.tenant, id)
	mustDo(t, "GetLayerConfig", err)
	list, err := b.st.ListLayerConfigs(ctx, b.tenant)
	mustDo(t, "ListLayerConfigs", err)
	mustDo(t, "DeleteLayerConfig", b.st.DeleteLayerConfig(ctx, b.tenant, id))
	afterDelete := b.raw(t, id)
	deleted, err := b.st.ListDeletedLayerConfigs(ctx, b.tenant)
	mustDo(t, "ListDeletedLayerConfigs", err)
	mustDo(t, "RestoreLayerConfig", b.st.RestoreLayerConfig(ctx, b.tenant, id))
	afterRestore := b.raw(t, id)
	cfg, err := b.st.GetLayerConfig(ctx, b.tenant, id)
	mustDo(t, "GetLayerConfig after restore", err)
	mustDo(t, "PutLayerConfig (put back)", b.st.PutLayerConfig(ctx, cfg))
	afterPut := b.raw(t, id)

	reads := layerRepoReads(append(append([]LayerConfig{before, got}, list...), deleted...)...)
	read := layerRepoRead{Repo: "https://git.acme.com/acme/a%20b.git", Clone: layerRepoCredentialed}
	want := []layerRepoRead{read, read, read, read}
	gotCols := [3]layerRepoColumns{afterDelete, afterRestore, afterPut}
	wantCols := [3]layerRepoColumns{earlier, earlier, twoColumn}
	if !slices.Equal(reads, want) || gotCols != wantCols {
		t.Errorf("earlier-form row: reads %+v, want %+v; columns after delete, restore, and put back %+v, want %+v",
			reads, want, gotCols, wantCols)
	}
}

// postgresLayerRepoBackend opens a Postgres org for tenant as a layerRepoBackend.
func postgresLayerRepoBackend(t *testing.T, tenant string) layerRepoBackend {
	t.Helper()
	p, conn := openPostgresOrg(t, tenant)
	return layerRepoBackend{st: p, db: conn, tenant: tenant, sel: pgSelectLayerRepo, toEarlier: pgLayerRepoToEarlier}
}

// Spec: §7.3.1 (Repository credentials) — on Postgres, a put and a
// read-modify-write of a split-class value store its bare form in repo and its
// userinfo in repo_userinfo, the empty userinfo as a non-NULL empty string.
// Every other value is stored whole beside a NULL repo_userinfo.
func TestPostgres_LayerRepoColumnsHoldBareAndUserinfo(t *testing.T) {
	checkLayerRepoColumnsHoldBareAndUserinfo(t, postgresLayerRepoBackend(t, "layer-repo-raw"))
}

// Spec: §7.3.1 (Repository credentials) — a Postgres row that holds the
// registered value whole in repo, as an earlier version writes it, reads as
// held, keeps that form through a soft delete and a restore, and takes the
// two-column form at the next put.
// Spec: §13.4
func TestPostgres_LayerRepoEarlierFormRow(t *testing.T) {
	checkLayerRepoEarlierFormRow(t, postgresLayerRepoBackend(t, "layer-repo-earlier"))
}

// Spec: §7.3.1 (Repository credentials) — the Postgres read honors a non-NULL
// repo_userinfo column on the get, the list, and the deleted list. The put
// back writes the same two columns, and a later put of the layer with a
// userinfo-free repo replaces repo_userinfo with NULL.
//
// The last assertion fails when repo_userinfo = EXCLUDED.repo_userinfo is
// absent from the DO UPDATE SET list, because the upsert then leaves the
// column holding ghp_tok3n, and the read rejoins that credential to the new
// repo. The put back cannot detect the omission, because it writes the value
// the column already holds.
func TestPostgres_LayerRepoUserinfoColumnReadAndReplaced(t *testing.T) {
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

	// Step 1: the row exists, so the puts in steps 4 and 5 take the ON
	// CONFLICT arm.
	if err := p.PutLayerConfig(ctx, LayerConfig{TenantID: tenant, ID: "team", SourceType: "git", Repo: bare}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	// Step 2: the column holds the userinfo of the registered value.
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
	afterPutBack := rawLayerRepo(t, conn, pgSelectLayerRepo, tenant, "team")
	// Step 5: put the layer with a userinfo-free repo and no RegisteredRepo.
	if err := p.PutLayerConfig(ctx, LayerConfig{TenantID: tenant, ID: "team", SourceType: "git", Repo: bare}); err != nil {
		t.Fatalf("PutLayerConfig (userinfo-free): %v", err)
	}
	got := [2]layerRepoColumns{afterPutBack, rawLayerRepo(t, conn, pgSelectLayerRepo, tenant, "team")}
	want := [2]layerRepoColumns{{Repo: bare, Userinfo: sql.NullString{String: "ghp_tok3n", Valid: true}}, {Repo: bare}}
	if got != want {
		t.Errorf("columns after the put back and after the userinfo-free put = %+v, want %+v", got, want)
	}
}
