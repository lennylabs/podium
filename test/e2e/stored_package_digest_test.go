package e2e

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

const digestRowID = "ops/acme/runbook"

// loadPackageDigest returns the package_digest member of a load of id, and
// whether the response carries it.
func loadPackageDigest(t *testing.T, baseURL, id string) (string, bool) {
	t.Helper()
	status, body := getRaw(t, baseURL+"/v1/load_artifact?id="+id)
	if status != 200 {
		t.Fatalf("load %s = %d: %s", id, status, body)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode load: %v", err)
	}
	d, ok := m["package_digest"].(string)
	return d, ok
}

// forgetPackageDigests empties every stored package digest and removes the
// fill's completion record, which is the state of a store an earlier release
// wrote before the column existed. It runs while no server holds the store.
func forgetPackageDigests(t *testing.T, sqlitePath string) {
	t.Helper()
	db, err := sql.Open("sqlite3", sqlitePath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE manifests SET package_digest = ''`); err != nil {
		t.Fatalf("empty digests: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM data_migrations WHERE name = ?`, store.DataMigrationPackageDigest); err != nil {
		t.Fatalf("remove the completion record: %v", err)
	}
}

// Spec: §13.4, §7.2, §7.6.1, §4.7.6 — through the binary: ingest stores the
// package digest of the walk, a load serves it as package_digest, a start
// over a store whose rows carry no digest and no completion record fills
// each row with the digest ingest stored, and podium artifact show prints
// the member on standard error and in its --json envelope, leaving the
// manifest document alone on standard output.
func TestE2E_StoredPackageDigestIsFilledAndServed(t *testing.T) {
	home := t.TempDir()
	sqlitePath := filepath.Join(home, ".podium", "standalone", "podium.db")
	env := []string{"HOME=" + home, "PODIUM_REGISTRY_STORE=sqlite", "PODIUM_SQLITE_PATH=" + sqlitePath}
	reg := writeRegistry(t, map[string]string{
		digestRowID + "/ARTIFACT.md":        "---\ntype: agent\nversion: 1.0.0\nsensitivity: medium\ndescription: A runbook whose package digest this test reads.\n---\n\nRunbook body.\n",
		digestRowID + "/scripts/restart.sh": "#!/bin/sh\necho restart\n",
	})
	walked, err := ingest.LayerContent(os.DirFS(reg))
	if err != nil || len(walked.Subjects) != 1 {
		t.Fatalf("ingest.LayerContent = %+v, %v", walked, err)
	}
	want := walked.Subjects[0].Digest

	first := startServerArgs(t, env, "serve", "--standalone", "--layer-path", reg)
	ingested, ok := loadPackageDigest(t, first.BaseURL, digestRowID)
	stopProc(first.cmd)
	if !ok || ingested != want {
		t.Fatalf("load after ingest serves package_digest %q (present %v), want the walk digest %q", ingested, ok, want)
	}

	forgetPackageDigests(t, sqlitePath)
	second := startServerArgs(t, env, "serve", "--standalone", "--layer-path", reg)
	if got, ok := loadPackageDigest(t, second.BaseURL, digestRowID); !ok || got != want {
		t.Errorf("load after the fill serves package_digest %q (present %v), want %q", got, ok, want)
	}

	cli := []string{"PODIUM_REGISTRY=" + second.BaseURL}
	human := runPodium(t, "", cli, "artifact", "show", digestRowID)
	if human.Exit != 0 || !strings.Contains(human.Stderr, "package_digest: "+want) || strings.Contains(human.Stdout, want) || !strings.Contains(human.Stdout, "Runbook body.") {
		t.Errorf("artifact show: exit %d\nstdout:\n%s\nstderr:\n%s\nwant the digest on stderr alone", human.Exit, human.Stdout, human.Stderr)
	}
	js := runPodium(t, "", cli, "artifact", "show", digestRowID, "--json")
	var got map[string]any
	if err := json.Unmarshal([]byte(js.Stdout), &got); err != nil || got["package_digest"] != want {
		t.Errorf("artifact show --json = %v (%v), want package_digest %q", got, err, want)
	}
}
