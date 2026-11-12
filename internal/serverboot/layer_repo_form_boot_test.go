package serverboot

import (
	"context"
	"database/sql"
	"net"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

const (
	bootLayerRepoUser     = "alice-user"
	bootLayerRepoPassword = "s3cr3tpw"
)

// earlierFormLayer is a git layer the boot tests seed in the form an earlier
// release writes: the registered value whole in repo beside a NULL
// repo_userinfo.
type earlierFormLayer struct {
	tenant, id string
	deleted    bool
}

// repo is the credentialed remote the layer is registered with.
func (l earlierFormLayer) repo() string {
	return "https://" + bootLayerRepoUser + ":" + bootLayerRepoPassword + "@git.acme.com/acme/" + l.id + ".git"
}

// twoColumn is the raw row the start-time rewrite gives the layer.
func (l earlierFormLayer) twoColumn() bootLayerRepoRow {
	return bootLayerRepoRow{
		Repo:     "https://git.acme.com/acme/" + l.id + ".git",
		Userinfo: sql.NullString{String: bootLayerRepoUser + ":" + bootLayerRepoPassword, Valid: true},
	}
}

// whole is the raw row an earlier release writes for the layer.
func (l earlierFormLayer) whole() bootLayerRepoRow { return bootLayerRepoRow{Repo: l.repo()} }

// bootLayerRepoRow is the raw repo and repo_userinfo columns of one layer row.
type bootLayerRepoRow struct {
	Repo     string
	Userinfo sql.NullString
}

// openRawSQLite opens the fixture's SQLite file for raw SQL, outside the
// store's read and write rules.
func (f *bootFixture) openRawSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", f.sqlitePath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seedEarlierFormLayers puts each layer through the store, soft-deletes the
// ones marked deleted, and then sets each row to the whole form with raw SQL.
// It runs while no server process holds the store, and closes what it opens.
func (f *bootFixture) seedEarlierFormLayers(t *testing.T, layers ...earlierFormLayer) {
	t.Helper()
	ctx := context.Background()
	st := f.openStoreDirect(t)
	for _, l := range layers {
		if err := st.PutLayerConfig(ctx, store.LayerConfig{TenantID: l.tenant, ID: l.id, SourceType: "git", Repo: l.repo(), Ref: "main"}); err != nil {
			t.Fatalf("PutLayerConfig %s/%s: %v", l.tenant, l.id, err)
		}
		if l.deleted {
			if err := st.DeleteLayerConfig(ctx, l.tenant, l.id); err != nil {
				t.Fatalf("DeleteLayerConfig %s/%s: %v", l.tenant, l.id, err)
			}
		}
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	db := f.openRawSQLite(t)
	for _, l := range layers {
		if _, err := db.ExecContext(ctx,
			`UPDATE layer_configs SET repo = ?, repo_userinfo = NULL WHERE tenant_id = ? AND id = ?`,
			l.repo(), l.tenant, l.id); err != nil {
			t.Fatalf("set the earlier form of %s/%s: %v", l.tenant, l.id, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close raw sqlite: %v", err)
	}
}

// rawLayerRepoRows reads the raw repo columns of each layer.
func (f *bootFixture) rawLayerRepoRows(t *testing.T, layers ...earlierFormLayer) []bootLayerRepoRow {
	t.Helper()
	db := f.openRawSQLite(t)
	out := make([]bootLayerRepoRow, 0, len(layers))
	for _, l := range layers {
		var row bootLayerRepoRow
		if err := db.QueryRow(`SELECT repo, repo_userinfo FROM layer_configs WHERE tenant_id = ? AND id = ?`,
			l.tenant, l.id).Scan(&row.Repo, &row.Userinfo); err != nil {
			t.Fatalf("select repo columns of %s/%s: %v", l.tenant, l.id, err)
		}
		out = append(out, row)
	}
	return out
}

// carriesBootCredentialPart reports whether text names either part of the
// seeded credential.
func carriesBootCredentialPart(text string) bool {
	return strings.Contains(text, bootLayerRepoUser) || strings.Contains(text, bootLayerRepoPassword)
}

// Spec: §13.4 — a start whose listener binds rewrites every stored layer row
// into the form its next write would take, for every tenant and soft-deleted
// rows included, before it serves, and logs the count alone. A later start
// finds every row in that form and logs nothing.
// Spec: §7.3.1 (Repository credentials)
func TestRun_RewritesEarlierFormLayerRowsBeforeServing(t *testing.T) {
	f := newBootFixture(t)
	if _, err := f.boot(t); err != nil {
		t.Fatalf("first start: %v", err)
	}
	def := orgIDForName(defaultOrgName)
	layers := []earlierFormLayer{
		{tenant: def, id: "private"},
		{tenant: def, id: "retired", deleted: true},
		{tenant: orgIDForName("globex"), id: "team"},
	}
	f.seedEarlierFormLayers(t, layers...)

	second, err := f.boot(t)
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	got := f.rawLayerRepoRows(t, layers...)
	third, err := f.boot(t)
	if err != nil {
		t.Fatalf("third start: %v", err)
	}
	for i, l := range layers {
		if got[i] != l.twoColumn() {
			t.Errorf("%s/%s after the second start = %+v, want %+v", l.tenant, l.id, got[i], l.twoColumn())
		}
	}
	if !strings.Contains(second, "layer repo migration: rewrote 3 stored layer(s)") || carriesBootCredentialPart(second) {
		t.Errorf("second start's log does not report 3 rewritten layers, or names a credential part:\n%s", second)
	}
	if strings.Contains(third, "layer repo migration") {
		t.Errorf("third start's log reports a layer repo migration:\n%s", third)
	}
}

// Spec: §13.4 — a start whose listener could not bind does not run the
// rewrite, so a process that exits on the bind error never writes the store
// another process is serving.
// Spec: §7.3.1 (Repository credentials)
func TestRun_BindFailureDoesNotRewriteLayerRows(t *testing.T) {
	f := newBootFixture(t)
	if _, err := f.boot(t); err != nil {
		t.Fatalf("first start: %v", err)
	}
	legacy := earlierFormLayer{tenant: orgIDForName(defaultOrgName), id: "legacy"}
	f.seedEarlierFormLayers(t, legacy)

	held, err := net.Listen("tcp", f.addr)
	if err != nil {
		t.Fatalf("hold the bind address: %v", err)
	}
	defer func() { _ = held.Close() }()

	_, runErr := f.boot(t)
	if runErr == nil || !strings.Contains(runErr.Error(), "serve: bind") {
		t.Fatalf("run = %v, want the bind error", runErr)
	}
	if got := f.rawLayerRepoRows(t, legacy)[0]; got != legacy.whole() {
		t.Errorf("legacy row after a start that never bound = %+v, want %+v", got, legacy.whole())
	}
}

// Spec: §13.4 — a start that cannot read or rewrite a layer's stored repo
// fails before it serves. A trigger refuses the rewrite of one row, and the
// start returns the wrapped error, which names no credential part, and leaves
// that row in its earlier form.
// Spec: §7.3.1 (Repository credentials)
func TestRun_LayerRepoMigrationFailureRefusesTheStart(t *testing.T) {
	f := newBootFixture(t)
	if _, err := f.boot(t); err != nil {
		t.Fatalf("first start: %v", err)
	}
	legacy := earlierFormLayer{tenant: orgIDForName(defaultOrgName), id: "legacy"}
	f.seedEarlierFormLayers(t, legacy)
	db := f.openRawSQLite(t)
	if _, err := db.Exec(`CREATE TRIGGER layer_repo_rewrite_refused BEFORE UPDATE ON layer_configs
		WHEN OLD.id = 'legacy' BEGIN SELECT RAISE(ABORT, 'rewrite refused'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close raw sqlite: %v", err)
	}

	_, runErr := f.bootWith(t, func() {
		t.Error("the registry served a start whose layer repo migration failed")
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "layer repo migration:") || carriesBootCredentialPart(runErr.Error()) {
		t.Errorf("run = %v, want a layer repo migration error that names no credential part", runErr)
	}
	if got := f.rawLayerRepoRows(t, legacy)[0]; got != legacy.whole() {
		t.Errorf("legacy row after the refused start = %+v, want %+v", got, legacy.whole())
	}
}
