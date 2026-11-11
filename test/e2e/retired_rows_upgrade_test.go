package e2e

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/version"
)

const (
	upgradeParentID  = "ops/acme/parent"
	upgradeChildID   = "ops/acme/legacy-child"
	upgradeControlID = "ops/acme/legacy-plain"
	// upgradeLegacyLayer is a layer no --layer-path names, so the boot
	// reingest never rewrites the rows stored under it.
	upgradeLegacyLayer = "legacy"
)

// seedUpgradedStore turns the store into the state an earlier release left
// behind: the manifests table gains the extends_pin column, and the legacy
// layer holds a child whose ARTIFACT.md declares extends: with a non-empty pin
// beside a plain row. It runs while no server process holds the store.
func seedUpgradedStore(t *testing.T, sqlitePath string) {
	t.Helper()
	db, err := sql.Open("sqlite3", sqlitePath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`ALTER TABLE manifests ADD COLUMN extends_pin TEXT NOT NULL DEFAULT ''`); err != nil {
		t.Fatalf("add the extends_pin column: %v", err)
	}

	st, err := store.OpenSQLite(sqlitePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	parent, ok := storedRow(t, st, upgradeParentID)
	if !ok {
		t.Fatalf("the first start stored no %s row", upgradeParentID)
	}
	rows := map[string]string{
		upgradeChildID:   "---\ntype: context\nversion: 1.0.0\nextends: " + upgradeParentID + "\ndescription: A child an earlier release merged.\n---\n\nChild body.\n",
		upgradeControlID: "---\ntype: context\nversion: 1.0.0\ndescription: A plain row in the legacy layer.\n---\n\nPlain body.\n",
	}
	for id, doc := range rows {
		rec := store.ManifestRecord{
			TenantID: parent.TenantID, ArtifactID: id, Version: "1.0.0", Type: "context",
			Layer: upgradeLegacyLayer, Frontmatter: []byte(doc), IngestedAt: time.Now().UTC(),
			ContentHash: "sha256:" + version.CanonicalContentHash([]byte(doc), nil, nil),
		}
		if err := st.PutManifest(context.Background(), rec); err != nil {
			t.Fatalf("store %s: %v", id, err)
		}
	}
	_ = st.Close()

	res, err := db.Exec(`UPDATE manifests SET extends_pin = ? WHERE tenant_id = ? AND artifact_id = ?`,
		upgradeParentID+"@1.0.0", parent.TenantID, upgradeChildID)
	if err != nil {
		t.Fatalf("set the pin: %v", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("set the pin on %d row(s): %v", n, err)
	}
}

// storedRow returns the stored row of id at 1.0.0 from whichever tenant holds
// it.
func storedRow(t *testing.T, st *store.SQLite, id string) (store.ManifestRecord, bool) {
	t.Helper()
	tenants, err := st.ListTenants(context.Background())
	if err != nil {
		t.Fatalf("list tenants: %v", err)
	}
	for _, tenant := range tenants {
		if rec, err := st.GetManifest(context.Background(), tenant.ID, id, "1.0.0"); err == nil {
			return rec, true
		}
	}
	return store.ManifestRecord{}, false
}

// Spec: §4.6, §13.4, §11 — a standalone registry upgraded over a SQLite store
// an earlier release filled starts with the unused extends_pin column in
// place, treats the stored child whose ARTIFACT.md declares extends: as
// retired, so a load of it answers registry.not_found, and keeps serving the
// parent and the plain row stored beside the child.
func TestE2E_RetiredRowsAfterUpgrade(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	sqlitePath := filepath.Join(home, "podium.db")
	env := []string{"HOME=" + home, "PODIUM_REGISTRY_STORE=sqlite", "PODIUM_SQLITE_PATH=" + sqlitePath, "PODIUM_SIGN=none"}
	reg := writeRegistry(t, map[string]string{
		upgradeParentID + "/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: The parent an earlier child extended.\n---\n\nParent body.\n",
	})

	first := startServerArgs(t, env, "serve", "--standalone", "--layer-path", reg)
	if st, body := getRaw(t, first.BaseURL+"/v1/load_artifact?id="+upgradeParentID); st != 200 {
		t.Fatalf("parent load before the upgrade = %d: %s", st, body)
	}
	stopProc(first.cmd)

	seedUpgradedStore(t, sqlitePath)

	second := startServerArgs(t, env, "serve", "--standalone", "--layer-path", reg)
	st, body := getRaw(t, second.BaseURL+"/v1/load_artifact?id="+upgradeChildID)
	if st != 404 || !strings.Contains(string(body), "registry.not_found") {
		t.Errorf("retired child load = %d %s, want 404 registry.not_found", st, body)
	}
	for _, id := range []string{upgradeParentID, upgradeControlID} {
		if st, body := getRaw(t, second.BaseURL+"/v1/load_artifact?id="+id); st != 200 {
			t.Errorf("load %s after the upgrade = %d: %s", id, st, body)
		}
	}
}
