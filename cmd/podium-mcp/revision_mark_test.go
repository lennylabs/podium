package main

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/lennylabs/podium/internal/revmark"
)

// putLatest records a `latest` resolution at revision 0 under an empty
// session, for tests that exercise the resolution index rather than the
// revision marks.
func putLatest(r *resolutionCache, id, version, contentHash string, now time.Time) {
	r.PutLatestAdvancing(testMarkKey(id), "", id, version, contentHash, 0, now)
}

func testMarkKey(id string) revmark.Key {
	return revmark.Key{Registry: "https://registry.acme.com", ArtifactID: id}
}

// latestVersion returns the semver the (id, "latest") entry resolves to.
func latestVersion(t *testing.T, r *resolutionCache, id string) string {
	t.Helper()
	e, ok := r.getEntry(resolutionKey(id, ""))
	if !ok {
		return ""
	}
	return e.ResolvedVersion
}

// Spec: §6.5 — the mark advances only inside the transaction that writes the
// id@latest entry, to max(mark, served), and a lower answer writes nothing.
func TestResolutionCache_PutLatestAdvancing(t *testing.T) {
	t.Parallel()
	r := newResolutionCache(t.TempDir())
	defer func() { _ = r.Close() }()
	k := testMarkKey("team/x")
	now := time.Unix(1_700_000_000, 0)

	if _, ok := r.Reference(k, ""); ok {
		t.Fatal("fresh cache reported a mark")
	}
	r.PutLatestAdvancing(k, "", "team/x", "2.0.0", "sha256:two", 200, now)
	if ref, ok := r.Reference(k, ""); !ok || ref != 200 {
		t.Fatalf("mark after 200 = %d, %v", ref, ok)
	}
	// A lower revision leaves the mark and the latest entry unchanged.
	r.PutLatestAdvancing(k, "", "team/x", "1.0.0", "sha256:one", 100, now)
	if ref, _ := r.Reference(k, ""); ref != 200 {
		t.Errorf("mark after 100 = %d, want 200", ref)
	}
	if v := latestVersion(t, r, "team/x"); v != "2.0.0" {
		t.Errorf("latest after a lower answer = %q, want 2.0.0", v)
	}
	if _, ok := r.getEntry(resolutionKey("team/x", "1.0.0")); ok {
		t.Error("a lower answer wrote its id@semver entry")
	}
	// Equality is accepted and rewrites the entries.
	r.PutLatestAdvancing(k, "", "team/x", "2.0.1", "sha256:two1", 200, now)
	if v := latestVersion(t, r, "team/x"); v != "2.0.1" {
		t.Errorf("latest after an equal answer = %q, want 2.0.1", v)
	}
	// A higher revision advances the mark.
	r.PutLatestAdvancing(k, "", "team/x", "3.0.0", "sha256:three", 300, now)
	if ref, _ := r.Reference(k, ""); ref != 300 {
		t.Errorf("mark after 300 = %d, want 300", ref)
	}
	// A record with no version stores the hash on the latest key.
	r.PutLatestAdvancing(k, "", "team/x", "", "sha256:bare", 300, now)
	if got, ok := r.Resolve("team/x", "", now, 0, false); !ok || got != "sha256:bare" {
		t.Errorf("Resolve after a versionless answer = %q, %v", got, ok)
	}
}

// Spec: §6.5 — the session reference is the first accepted answer in the
// session and takes precedence over the mark; it is recorded whatever the
// comparison with the mark, and NoteSession never touches the mark.
func TestResolutionCache_SessionReferences(t *testing.T) {
	t.Parallel()
	r := newResolutionCache(t.TempDir())
	defer func() { _ = r.Close() }()
	a, b := testMarkKey("team/a"), testMarkKey("team/b")
	now := time.Now()

	r.PutLatestAdvancing(a, "s1", "team/a", "3.0.0", "sha256:a3", 300, now)
	r.PutLatestAdvancing(a, "s2", "team/a", "5.0.0", "sha256:a5", 500, now)
	// s1 keeps its first answer; s2 recorded 500; a new session reads the mark.
	for session, want := range map[string]uint64{"s1": 300, "s2": 500, "s3": 500, "": 500} {
		if ref, ok := r.Reference(a, session); !ok || ref != want {
			t.Errorf("Reference(a, %q) = %d, %v; want %d", session, ref, ok, want)
		}
	}
	// A below-mark answer records a reference for a session that holds none.
	r.PutLatestAdvancing(a, "s4", "team/a", "4.0.0", "sha256:a4", 400, now)
	if ref, _ := r.Reference(a, "s4"); ref != 400 {
		t.Errorf("Reference(a, s4) = %d, want 400", ref)
	}
	// A later answer never changes an existing reference.
	r.PutLatestAdvancing(a, "s1", "team/a", "6.0.0", "sha256:a6", 600, now)
	if ref, _ := r.Reference(a, "s1"); ref != 300 {
		t.Errorf("Reference(a, s1) after 600 = %d, want 300", ref)
	}

	// NoteSession records once, ignores an empty session, and never marks.
	r.NoteSession(b, "s1", 100)
	r.NoteSession(b, "s1", 900)
	r.NoteSession(b, "", 700)
	if ref, ok := r.Reference(b, "s1"); !ok || ref != 100 {
		t.Errorf("Reference(b, s1) = %d, %v; want 100", ref, ok)
	}
	if _, ok := r.Reference(b, "s9"); ok {
		t.Error("NoteSession wrote a mark for team/b")
	}
	// Another registry shares no entry with the first.
	other := revmark.Key{Registry: "https://other.acme.com", ArtifactID: "team/a"}
	if _, ok := r.Reference(other, "s1"); ok {
		t.Error("a second registry read the first registry's references")
	}
}

// Spec: §6.5 — the marks persist in the index DB across a reopen, and session
// references do not.
func TestResolutionCache_MarksPersist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	k := testMarkKey("team/x")
	r1 := newResolutionCache(dir)
	r1.PutLatestAdvancing(k, "s1", "team/x", "2.0.0", "sha256:two", 200, time.Now())
	if err := r1.Close(); err != nil {
		t.Fatal(err)
	}
	r2 := newResolutionCache(dir)
	defer func() { _ = r2.Close() }()
	if ref, ok := r2.Reference(k, "s1"); !ok || ref != 200 {
		t.Errorf("Reference after reopen = %d, %v; want the mark 200", ref, ok)
	}
	if _, ok := r2.sessions[sessionMarkKey{mark: string(k.Encode()), session: "s1"}]; ok {
		t.Error("a session reference survived the reopen")
	}
}

// Spec: §6.5 — opening an index DB written before the marks existed adds the
// revisions bucket, and podium cache reset-revisions removes what the bridge
// wrote.
func TestResolutionCache_CreatesRevisionsBucketAndResets(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := revmark.IndexPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := bolt.Open(path, 0o644, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, e := tx.CreateBucket(resolutionBucket)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	r := newResolutionCache(dir)
	k := testMarkKey("team/x")
	r.PutLatestAdvancing(k, "", "team/x", "1.0.0", "sha256:one", 100, time.Now())
	if ref, ok := r.Reference(k, ""); !ok || ref != 100 {
		t.Fatalf("mark = %d, %v; want 100", ref, ok)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	n, found, err := revmark.Reset(context.Background(), dir, []string{"team/x"})
	if err != nil || n != 1 || !found {
		t.Fatalf("Reset = %d, %v, %v; want 1, true, nil", n, found, err)
	}
}

// Spec: §6.5 — with no index DB (no cache directory, or a lock held by another
// podium-mcp) the marks live in memory under the same advancing rule.
func TestResolutionCache_InMemoryMarks(t *testing.T) {
	t.Parallel()
	for name, r := range map[string]*resolutionCache{
		"no cache dir":   newResolutionCache(""),
		"zero value":     {},
		"open failed":    newResolutionCache(blockedCacheDir(t)),
		"locked by peer": lockedCache(t),
	} {
		if r.db != nil {
			t.Fatalf("%s: cache has a db handle", name)
		}
		k := testMarkKey("team/x")
		r.PutLatestAdvancing(k, "s1", "team/x", "2.0.0", "sha256:two", 200, time.Now())
		r.PutLatestAdvancing(k, "", "team/x", "1.0.0", "sha256:one", 100, time.Now())
		if ref, ok := r.Reference(k, ""); !ok || ref != 200 {
			t.Errorf("%s: mark = %d, %v; want 200", name, ref, ok)
		}
		r.PutLatestAdvancing(k, "", "team/x", "3.0.0", "sha256:three", 300, time.Now())
		if ref, _ := r.Reference(k, ""); ref != 300 {
			t.Errorf("%s: mark = %d, want 300", name, ref)
		}
		if ref, _ := r.Reference(k, "s1"); ref != 200 {
			t.Errorf("%s: session reference = %d, want 200", name, ref)
		}
		if _, ok := r.Reference(testMarkKey("team/y"), "s1"); ok {
			t.Errorf("%s: team/y shares team/x's entry", name)
		}
	}
}

// blockedCacheDir returns a cache directory whose .resolutions path is a file,
// so the index DB cannot be created.
func blockedCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, revmark.DirName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// lockedCache returns a cache opened on a directory whose index DB another
// handle holds, as a second podium-mcp on one PODIUM_CACHE_DIR sees it.
func lockedCache(t *testing.T) *resolutionCache {
	t.Helper()
	dir := t.TempDir()
	holder := newResolutionCache(dir)
	t.Cleanup(func() { _ = holder.Close() })
	return newResolutionCache(dir)
}

// Spec: §6.5 — a nil cache compares nothing and records nothing.
func TestResolutionCache_NilReceiver(t *testing.T) {
	t.Parallel()
	var r *resolutionCache
	k := testMarkKey("team/x")
	r.PutLatestAdvancing(k, "s1", "team/x", "1.0.0", "sha256:one", 100, time.Now())
	r.NoteSession(k, "s1", 100)
	if _, ok := r.Reference(k, "s1"); ok {
		t.Error("nil cache reported a reference")
	}
}

// Spec: §6.5 — a stored mark that does not parse is deleted with a warning
// naming its key and never its value, and is treated as absent. The test swaps
// the global log output, so it does not run in parallel.
func TestResolutionCache_UnparseableMarkDeleted(t *testing.T) {
	dir := t.TempDir()
	r := newResolutionCache(dir)
	defer func() { _ = r.Close() }()
	k := testMarkKey("team/x")
	if err := r.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(revmark.Bucket()).Put(k.Encode(), []byte("secret-garbage"))
	}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	if _, ok := r.Reference(k, ""); ok {
		t.Fatal("an unparseable mark was reported")
	}
	out := buf.String()
	if !strings.Contains(out, "team/x") || strings.Contains(out, "secret-garbage") {
		t.Errorf("warning = %q; want the key and not the value", out)
	}
	_ = r.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket(revmark.Bucket()).Get(k.Encode()) != nil {
			t.Error("the unparseable mark was not deleted")
		}
		return nil
	})
	// An unparseable mark does not block the next advance.
	r.PutLatestAdvancing(k, "", "team/x", "1.0.0", "sha256:one", 5, time.Now())
	if ref, ok := r.Reference(k, ""); !ok || ref != 5 {
		t.Errorf("mark after advance = %d, %v; want 5", ref, ok)
	}
}

// Spec: §6.5 — a failed index open logs one warning naming the cache
// directory. The test swaps the global log output, so it does not run in
// parallel.
func TestResolutionCache_OpenFailureWarns(t *testing.T) {
	dir := blockedCacheDir(t)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	r := newResolutionCache(dir)
	if r.db != nil {
		t.Fatal("open succeeded on a blocked directory")
	}
	out := buf.String()
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, dir) || !strings.Contains(out, "in memory") {
		t.Errorf("warning = %q; want one line naming %s", out, dir)
	}
}
