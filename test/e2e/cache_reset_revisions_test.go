package e2e

// End-to-end tests for `podium cache reset-revisions`, which deletes the §6.5
// revision marks podium-mcp persists in the resolution index. The tests drive
// the compiled binary and assert its exit code and output for the found,
// not-found, and locked branches. The freshness refusal the marks feed is
// covered by the delivery-freshness tests.

import (
	"os"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"

	"github.com/lennylabs/podium/internal/revmark"
)

// seedIndexMarks writes a resolution index under dir whose revisions bucket
// holds one mark per artifact ID for the given registry.
func seedIndexMarks(t *testing.T, dir, registry string, ids ...string) {
	t.Helper()
	path := revmark.IndexPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := bolt.Open(path, 0o644, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(revmark.Bucket())
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := b.Put(revmark.Key{Registry: registry, ArtifactID: id}.Encode(), []byte("100")); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Spec: §6.5 — the reset deletes the marks for the named artifact IDs across
// every registry and prints the count.
func TestCacheResetRevisions_DeletesMarks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	seedIndexMarks(t, dir, "https://r1.example", "team/a", "team/b")
	seedIndexMarks(t, dir, "https://r2.example", "team/a")

	res := runPodium(t, "", nil, "cache", "reset-revisions", "--dir", dir, "team/a")
	cliWantExit(t, res, 0, "cache reset-revisions team/a")
	cliContains(t, res.Stdout, "cache: reset 2 revision mark(s)", "reset count by ID")

	all := runPodium(t, "", []string{"PODIUM_CACHE_DIR=" + dir}, "cache", "reset-revisions")
	cliWantExit(t, all, 0, "cache reset-revisions (all)")
	cliContains(t, all.Stdout, "cache: reset 1 revision mark(s)", "reset count for every mark")
}

// Spec: §6.5 — against a directory with no index the reset prints the no-marks
// line, exits 0, and creates no index DB.
func TestCacheResetRevisions_NoMarksCreatesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	res := runPodium(t, "", nil, "cache", "reset-revisions", "--dir", dir)
	cliWantExit(t, res, 0, "cache reset-revisions on an empty directory")
	cliContains(t, res.Stdout, "cache: no revision marks under "+dir, "no-marks line")
	if _, err := os.Stat(revmark.IndexPath(dir)); !os.IsNotExist(err) {
		t.Errorf("reset-revisions created %s: %v", revmark.IndexPath(dir), err)
	}
}

// Spec: §6.5 — while another process holds the index lock, as a running
// podium-mcp does, the reset exits 1 with the stop-and-retry message and
// deletes nothing.
func TestCacheResetRevisions_LockedIndexExits1(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	seedIndexMarks(t, dir, "https://r1.example", "team/a")
	db, err := bolt.Open(revmark.IndexPath(dir), 0o644, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := runPodium(t, "", nil, "cache", "reset-revisions", "--dir", dir)
	_ = db.Close()
	cliWantExit(t, res, 1, "cache reset-revisions on a locked index")
	cliContains(t, res.Stderr, "error: "+revmark.IndexPath(dir)+" is in use by a running podium-mcp; stop every MCP server that uses this cache directory and retry.", "lock message")

	after := runPodium(t, "", nil, "cache", "reset-revisions", "--dir", dir)
	cliWantExit(t, after, 0, "cache reset-revisions after the lock is released")
	cliContains(t, after.Stdout, "cache: reset 1 revision mark(s)", "the locked run deleted nothing")
}
