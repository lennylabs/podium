package e2e

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/version"
)

const editedRowID = "ops/acme/edited"

// editStoredRow rewrites one stored row's manifest and content hash in the
// SQLite file, the way a party with store write access and without the
// signing key would: the hash is recomputed over the altered bytes, and the
// stored envelope is left in place. It runs while no server holds the store.
func editStoredRow(t *testing.T, sqlitePath, id string, frontmatter []byte) {
	t.Helper()
	st, err := store.OpenSQLite(sqlitePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	tenants, err := st.ListTenants(context.Background())
	if err != nil || len(tenants) == 0 {
		t.Fatalf("list tenants: %v", err)
	}
	var rec store.ManifestRecord
	for _, tenant := range tenants {
		if r, gerr := st.GetManifest(context.Background(), tenant.ID, id, "1.0.0"); gerr == nil {
			rec = r
		}
	}
	_ = st.Close()
	if rec.Signature == "" {
		t.Fatalf("the default start stored %s without a signature", id)
	}
	bodies := map[string][]byte{}
	for _, ref := range rec.Resources {
		bodies[ref.Path] = ref.Inline
	}
	hash := "sha256:" + version.CanonicalContentHash(frontmatter, rec.SkillRaw, bodies)
	db, err := sql.Open("sqlite3", sqlitePath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE manifests SET frontmatter = ?, content_hash = ? WHERE tenant_id = ? AND artifact_id = ?`,
		frontmatter, hash, rec.TenantID, id); err != nil {
		t.Fatalf("edit row: %v", err)
	}
}

// Spec: §13.4 — a registry that signs at ingest by default admits each stored
// row before it serves it, so a row edited in the store with its hash
// recomputed and its envelope left in place is refused with
// materialize.signature_invalid on every serve path, including through a
// consumer that verifies nothing itself and through the podium CLI.
func TestE2E_StoreEditedRowIsRefused(t *testing.T) {
	home := t.TempDir()
	// The store sits beside the default signing key, so the default start
	// signs without PODIUM_SIGN_KEY_PATH (§13.12 Signing).
	sqlitePath := filepath.Join(home, ".podium", "standalone", "podium.db")
	env := []string{"HOME=" + home, "PODIUM_REGISTRY_STORE=sqlite", "PODIUM_SQLITE_PATH=" + sqlitePath}
	reg := writeRegistry(t, map[string]string{
		editedRowID + "/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\nsensitivity: high\ndescription: A runbook the store edit targets in this test.\n---\n\nOriginal body.\n",
	})

	first := startServerArgs(t, env, "serve", "--standalone", "--layer-path", reg)
	if st, body := getRaw(t, first.BaseURL+"/v1/load_artifact?id="+editedRowID); st != 200 {
		t.Fatalf("load before the edit = %d: %s", st, body)
	}
	stopProc(first.cmd)

	editStoredRow(t, sqlitePath, editedRowID, []byte("---\ntype: context\nversion: 1.0.0\nsensitivity: low\ndescription: A runbook the store edit targets in this test.\n---\n\nInjected body.\n"))

	second := startServerArgs(t, env, "serve", "--standalone", "--layer-path", reg)
	st, body := getRaw(t, second.BaseURL+"/v1/load_artifact?id="+editedRowID)
	if st != 500 || !strings.Contains(string(body), `"materialize.signature_invalid"`) {
		t.Fatalf("registry load after the edit = %d %s, want 500 materialize.signature_invalid", st, body)
	}

	// The bridge checks no signature under never, so the refusal it relays
	// is the registry's.
	res := mcpExec(t, []string{"PODIUM_REGISTRY=" + second.BaseURL, "PODIUM_VERIFY_SIGNATURES=never", "PODIUM_CACHE_DIR=" + t.TempDir(), "PODIUM_MATERIALIZE_ROOT=" + t.TempDir()},
		toolCall(1, "load_artifact", map[string]any{"id": editedRowID}))
	if !strings.Contains(res.Stdout, "materialize.signature_invalid") || strings.Contains(res.Stdout, "Injected body") {
		t.Errorf("load_artifact through the bridge was not refused with materialize.signature_invalid:\n%s", res.Stdout)
	}

	show := runPodium(t, "", []string{"PODIUM_REGISTRY=" + second.BaseURL}, "artifact", "show", editedRowID)
	if show.Exit == 0 || !strings.Contains(show.Stdout+show.Stderr, "materialize.signature_invalid") {
		t.Errorf("podium artifact show exit %d, want a materialize.signature_invalid refusal:\nstdout:\n%s\nstderr:\n%s", show.Exit, show.Stdout, show.Stderr)
	}
}
