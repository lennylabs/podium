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

// Spec: §13.4 — the start-time rewrite gives a stored row the form its next
// write would take.
// Spec: §7.3.1 (Repository credentials) — the target reads as the stored row
// reads, and a row already in the target form is not rewritten.
//
// The listed rows pin the outcome for each class, and the properties over the
// corpus pin that the rewrite never changes what a read returns and that the
// target of a target is unchanged, so a second start writes nothing.
func TestLayerRepoFormTarget(t *testing.T) {
	t.Parallel()
	col := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
	type pair struct {
		Repo     string
		Userinfo sql.NullString
	}
	cases := []struct {
		name    string
		stored  pair
		want    pair
		changed bool
	}{
		{"whole credentialed https is split",
			pair{Repo: "https://alice-user:s3cr3tpw@git.acme.com/x.git"},
			pair{"https://git.acme.com/x.git", col("alice-user:s3cr3tpw")}, true},
		{"whole ssh login name is split",
			pair{Repo: "ssh://git@git.acme.com/x.git"},
			pair{"ssh://git.acme.com/x.git", col("git")}, true},
		{"two-column pair the read honors is unchanged",
			pair{"https://git.acme.com/x.git", col("ghp_tok3n")},
			pair{"https://git.acme.com/x.git", col("ghp_tok3n")}, false},
		{"whole scp-like is unchanged", pair{Repo: "git@github.com:acme/x.git"}, pair{Repo: "git@github.com:acme/x.git"}, false},
		{"whole path is unchanged", pair{Repo: "/srv/git/acme.git"}, pair{Repo: "/srv/git/acme.git"}, false},
		{"whole userinfo-free URL is unchanged", pair{Repo: "https://git.acme.com/x.git"}, pair{Repo: "https://git.acme.com/x.git"}, false},
		{"whole fail-closed value is unchanged", pair{Repo: "https://ghp_tok/3n@host/x.git"}, pair{Repo: "https://ghp_tok/3n@host/x.git"}, false},
		{"whole network-path value is unchanged", pair{Repo: "//tok@host/x://y"}, pair{Repo: "//tok@host/x://y"}, false},
		{"ignored column beside scp-like is set to NULL",
			pair{"git@github.com:acme/x.git", col("ghp_tok3n")},
			pair{Repo: "git@github.com:acme/x.git"}, true},
		{"ignored fail-closed column is set to NULL",
			pair{"https://git.acme.com/x.git", col("ghp_tok/3n")},
			pair{Repo: "https://git.acme.com/x.git"}, true},
	}
	type read struct {
		Repo       string
		Registered RegisteredRepo
	}
	readOf := func(p pair) read {
		var r read
		r.Repo, r.Registered = SplitLayerRepo(p.Repo, p.Userinfo)
		return r
	}
	// checkProperties asserts that the target reads as the stored pair reads
	// and is its own target.
	checkProperties := func(stored pair) {
		t.Helper()
		var target, again pair
		var changedAgain bool
		target.Repo, target.Userinfo, _ = layerRepoFormTarget(stored.Repo, stored.Userinfo)
		again.Repo, again.Userinfo, changedAgain = layerRepoFormTarget(target.Repo, target.Userinfo)
		if readOf(target) != readOf(stored) || again != target || changedAgain {
			t.Errorf("stored %+v: target %+v reads %+v, stored reads %+v; target of target %+v, changed %t",
				stored, target, readOf(target), readOf(stored), again, changedAgain)
		}
	}
	for _, tc := range cases {
		var got pair
		var changed bool
		got.Repo, got.Userinfo, changed = layerRepoFormTarget(tc.stored.Repo, tc.stored.Userinfo)
		if got != tc.want || changed != tc.changed {
			t.Errorf("%s: layerRepoFormTarget(%+v) = %+v, changed %t; want %+v, changed %t",
				tc.name, tc.stored, got, changed, tc.want, tc.changed)
		}
		checkProperties(tc.stored)
	}
	columns := []sql.NullString{{}, col(""), col("ghp_tok3n"), col("ghp_tok/3n"), col("tok@evil.com/y")}
	for _, x := range layerRepoCorpus() {
		for _, c := range columns {
			checkProperties(pair{x, c})
		}
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
// the tenant and the layer ID, toEarlier takes the repo, the tenant, and the
// layer ID, and setUserinfo takes the repo_userinfo value, the tenant, and the
// layer ID. migrate is the store's MigrateLayerRepoForm, and migrateRow is its
// conditional per-row statement.
type layerRepoBackend struct {
	st          Store
	db          layerRepoDB
	tenant      string
	sel         string
	toEarlier   string
	setUserinfo string
	migrate     func(ctx context.Context) (int, error)
	migrateRow  layerRepoRowWriter
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
	pgSelectLayerRepo      = `SELECT repo, repo_userinfo FROM layer_configs WHERE tenant_id = $1 AND id = $2`
	pgLayerRepoToEarlier   = `UPDATE layer_configs SET repo = $1, repo_userinfo = NULL WHERE tenant_id = $2 AND id = $3`
	pgLayerRepoSetUserinfo = `UPDATE layer_configs SET repo_userinfo = $1 WHERE tenant_id = $2 AND id = $3`
	layerRepoCredentialed  = "https://alice-user:s3cr3tpw@git.acme.com/acme/a b.git"
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
	return layerRepoBackend{
		st: p, db: conn, tenant: tenant,
		sel: pgSelectLayerRepo, toEarlier: pgLayerRepoToEarlier, setUserinfo: pgLayerRepoSetUserinfo,
		migrate: p.MigrateLayerRepoForm,
		migrateRow: func(ctx context.Context, row layerRepoRow, repo string, userinfo sql.NullString) (bool, error) {
			return rewriteLayerRepoRowPostgres(ctx, conn, row, repo, userinfo)
		},
	}
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

// layerRepoTwoColumn is the two-column form of a split-class value.
func layerRepoTwoColumn(bare, userinfo string) layerRepoColumns {
	return layerRepoColumns{Repo: bare, Userinfo: sql.NullString{String: userinfo, Valid: true}}
}

// layerRepoListReads returns what the list and the deleted list report for the
// backend's tenant, with the get of each listed layer.
func layerRepoListReads(t *testing.T, b layerRepoBackend) []layerRepoRead {
	t.Helper()
	ctx := context.Background()
	list, err := b.st.ListLayerConfigs(ctx, b.tenant)
	mustDo(t, "ListLayerConfigs", err)
	deleted, err := b.st.ListDeletedLayerConfigs(ctx, b.tenant)
	mustDo(t, "ListDeletedLayerConfigs", err)
	cfgs := append(append([]LayerConfig{}, list...), deleted...)
	for _, l := range list {
		cfg, err := b.st.GetLayerConfig(ctx, b.tenant, l.ID)
		mustDo(t, "GetLayerConfig "+l.ID, err)
		cfgs = append(cfgs, cfg)
	}
	return layerRepoReads(cfgs...)
}

// checkLayerRepoFormMigration seeds rows in every class the start-time rewrite
// distinguishes, the earlier whole form and a soft-deleted row included, runs
// the store's MigrateLayerRepoForm twice, and returns the first call's count.
// After the first call the whole credentialed rows, the local one included,
// hold the two-column form, the ignored column beside the scp-like repo is
// NULL, every other row is unchanged, and the reads return what they returned
// before. The second call returns 0 and changes no column.
func checkLayerRepoFormMigration(t *testing.T, b layerRepoBackend) int {
	ctx := context.Background()
	rows := []struct {
		id, sourceType, repo string
		earlier              bool
		want                 layerRepoColumns
	}{
		{"live", "git", "https://alice-user:s3cr3tpw@git.acme.com/acme/live.git", true,
			layerRepoTwoColumn("https://git.acme.com/acme/live.git", "alice-user:s3cr3tpw")},
		{"retired", "git", "https://alice-user:s3cr3tpw@git.acme.com/acme/retired.git", true,
			layerRepoTwoColumn("https://git.acme.com/acme/retired.git", "alice-user:s3cr3tpw")},
		{"ssh", "git", "ssh://git@git.acme.com/acme/ssh.git", true,
			layerRepoTwoColumn("ssh://git.acme.com/acme/ssh.git", "git")},
		{"scp-like", "git", "git@github.com:acme/scp.git", false, layerRepoColumns{Repo: "git@github.com:acme/scp.git"}},
		{"local", "local", "https://alice-user:s3cr3tpw@git.acme.com/acme/local.git", true,
			layerRepoTwoColumn("https://git.acme.com/acme/local.git", "alice-user:s3cr3tpw")},
		{"two-column", "git", "https://ghp_tok3n@git.acme.com/acme/two.git", false,
			layerRepoTwoColumn("https://git.acme.com/acme/two.git", "ghp_tok3n")},
		{"ignored", "git", "git@github.com:acme/x.git", false, layerRepoColumns{Repo: "git@github.com:acme/x.git"}},
	}
	for i, r := range rows {
		mustDo(t, "PutLayerConfig "+r.id, b.st.PutLayerConfig(ctx, LayerConfig{TenantID: b.tenant, ID: r.id, SourceType: r.sourceType, Repo: r.repo, Order: i}))
		if r.earlier {
			_, err := b.db.ExecContext(ctx, b.toEarlier, r.repo, b.tenant, r.id)
			mustDo(t, "set the earlier form of "+r.id, err)
		}
	}
	_, err := b.db.ExecContext(ctx, b.setUserinfo, "ghp_tok3n", b.tenant, "ignored")
	mustDo(t, "set the ignored column", err)
	mustDo(t, "DeleteLayerConfig retired", b.st.DeleteLayerConfig(ctx, b.tenant, "retired"))

	readsBefore := layerRepoListReads(t, b)
	n, err := b.migrate(ctx)
	mustDo(t, "MigrateLayerRepoForm", err)
	readsAfter := layerRepoListReads(t, b)
	var got, want []layerRepoColumns
	for _, r := range rows {
		got = append(got, b.raw(t, r.id))
		want = append(want, r.want)
	}
	if !slices.Equal(got, want) || !slices.Equal(readsAfter, readsBefore) {
		t.Errorf("after the rewrite: columns %+v, want %+v; reads %+v, want %+v", got, want, readsAfter, readsBefore)
	}

	mustDo(t, "RestoreLayerConfig retired", b.st.RestoreLayerConfig(ctx, b.tenant, "retired"))
	restored, err := b.st.GetLayerConfig(ctx, b.tenant, "retired")
	mustDo(t, "GetLayerConfig retired", err)
	again, err := b.migrate(ctx)
	mustDo(t, "MigrateLayerRepoForm (second call)", err)
	var gotAgain []layerRepoColumns
	for _, r := range rows {
		gotAgain = append(gotAgain, b.raw(t, r.id))
	}
	if restored.CloneRepo() != rows[1].repo || again != 0 || !slices.Equal(gotAgain, want) {
		t.Errorf("restored clone %q, want %q; second call rewrote %d, want 0; columns %+v, want %+v",
			restored.CloneRepo(), rows[1].repo, again, gotAgain, want)
	}
	return n
}

// checkLayerRepoFormMigrationLeavesARowAnotherWriterChanged scans two rows in
// an earlier form, lets a writer change each after the scan, and runs the
// store's per-row statement with the stale scan. The first writer moves the
// layer to another remote, which only the repo conjunct detects. The second
// gives a row whose column the read ignores a credential through the members,
// which only the repo_userinfo conjunct detects: without it, the statement
// sets the new credential to NULL.
func checkLayerRepoFormMigrationLeavesARowAnotherWriterChanged(t *testing.T, b layerRepoBackend) {
	ctx := context.Background()
	cases := []struct {
		id, repo  string
		userinfo  string
		writerTo  string
		wantAfter layerRepoColumns
	}{
		{"moved", "https://alice-user:s3cr3tpw@git.acme.com/acme/moved.git", "",
			"https://git.acme.com/acme/other.git", layerRepoColumns{Repo: "https://git.acme.com/acme/other.git"}},
		{"rotated", "https://git.acme.com/x.git", "ghp_tok/3n",
			"https://alice-user:n3wpw@git.acme.com/x.git", layerRepoTwoColumn("https://git.acme.com/x.git", "alice-user:n3wpw")},
	}
	type outcome struct {
		Changed  bool
		Affected bool
		Err      string
		Row      layerRepoColumns
	}
	for _, tc := range cases {
		mustDo(t, "PutLayerConfig "+tc.id, b.st.PutLayerConfig(ctx, LayerConfig{TenantID: b.tenant, ID: tc.id, SourceType: "git", Repo: tc.repo}))
		var err error
		if tc.userinfo == "" {
			_, err = b.db.ExecContext(ctx, b.toEarlier, tc.repo, b.tenant, tc.id)
		} else {
			_, err = b.db.ExecContext(ctx, b.setUserinfo, tc.userinfo, b.tenant, tc.id)
		}
		mustDo(t, "set the earlier form of "+tc.id, err)
		scanned := b.raw(t, tc.id)
		row := layerRepoRow{tenantID: b.tenant, id: tc.id, repo: scanned.Repo, userinfo: scanned.Userinfo}
		repo, userinfo, changed := layerRepoFormTarget(row.repo, row.userinfo)

		mustDo(t, "writer's PutLayerConfig "+tc.id, b.st.PutLayerConfig(ctx, LayerConfig{TenantID: b.tenant, ID: tc.id, SourceType: "git", Repo: tc.writerTo}))
		affected, err := b.migrateRow(ctx, row, repo, userinfo)
		got := outcome{Changed: changed, Affected: affected, Row: b.raw(t, tc.id)}
		if err != nil {
			got.Err = err.Error()
		}
		if want := (outcome{Changed: true, Row: tc.wantAfter}); got != want {
			t.Errorf("%s: stale-scan rewrite = %+v, want %+v", tc.id, got, want)
		}
	}
}

// Spec: §13.4 — at start every Postgres layer row whose repo columns differ
// from the form its next write would take is rewritten into that form,
// soft-deleted rows included, and a second pass writes nothing.
// Spec: §7.3.1 (Repository credentials)
//
// The pass scans every tenant of the test database, so this test does not run
// in parallel: TestPostgres_LayerRepoEarlierFormRow holds an earlier-form row
// between its steps. The count is not asserted for the same reason.
func TestPostgres_LayerRepoFormMigration(t *testing.T) {
	checkLayerRepoFormMigration(t, postgresLayerRepoBackend(t, "layer-repo-migrate"))
}

// Spec: §13.4 — the Postgres per-row statement leaves a row another writer
// changed after the scan as that writer wrote it.
// Spec: §7.3.1 (Repository credentials)
//
// The test does not close its store early, because its cleanup drops the
// test's org schema through that store.
func TestPostgres_LayerRepoFormMigration_LeavesARowAnotherWriterChanged(t *testing.T) {
	checkLayerRepoFormMigrationLeavesARowAnotherWriterChanged(t, postgresLayerRepoBackend(t, "layer-repo-stale"))
}

// Spec: §13.4 — the Postgres pass reaches every tenant, and an org schema that
// lacks repo_userinfo, as one only 0.5.x used does, gains the column before
// the scan reads it. The pass runs on a freshly opened store, whose
// provisioning cache is empty, as a starting process's store is.
// Spec: §7.3.1 (Repository credentials)
func TestPostgres_LayerRepoFormMigration_EveryTenant(t *testing.T) {
	ctx := context.Background()
	const repo = "https://alice-user:s3cr3tpw@git.acme.com/acme/every.git"
	tenants := []string{"layer-repo-every-a", "layer-repo-every-b"}
	conns := make([]*sql.Conn, len(tenants))
	for i, tenant := range tenants {
		p, conn := openPostgresOrg(t, tenant)
		conns[i] = conn
		mustDo(t, "PutLayerConfig "+tenant, p.PutLayerConfig(ctx, LayerConfig{TenantID: tenant, ID: "team", SourceType: "git", Repo: repo}))
		_, err := conn.ExecContext(ctx, pgLayerRepoToEarlier, repo, tenant, "team")
		mustDo(t, "set the earlier form under "+tenant, err)
	}
	_, err := conns[1].ExecContext(ctx, `ALTER TABLE layer_configs DROP COLUMN repo_userinfo`)
	mustDo(t, "drop repo_userinfo", err)

	fresh, err := OpenPostgres(os.Getenv("PODIUM_POSTGRES_DSN"))
	mustDo(t, "OpenPostgres", err)
	t.Cleanup(func() { _ = fresh.Close() })
	_, err = fresh.MigrateLayerRepoForm(ctx)
	mustDo(t, "MigrateLayerRepoForm", err)

	want := layerRepoTwoColumn("https://git.acme.com/acme/every.git", "alice-user:s3cr3tpw")
	for i, tenant := range tenants {
		if got := rawLayerRepo(t, conns[i], pgSelectLayerRepo, tenant, "team"); got != want {
			t.Errorf("row under %s after the pass = %+v, want %+v", tenant, got, want)
		}
	}
}
