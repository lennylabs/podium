package revmark

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/lennylabs/podium/pkg/version"
)

// Spec: §6.5 — the index DB lives at <cacheDir>/.resolutions/index.db and the
// marks in the revisions bucket.
func TestIndexPathAndBucket(t *testing.T) {
	t.Parallel()
	if got, want := IndexPath("/c"), filepath.Join("/c", ".resolutions", "index.db"); got != want {
		t.Errorf("IndexPath = %q, want %q", got, want)
	}
	b := Bucket()
	b[0] = 'X'
	if string(Bucket()) != "revisions" {
		t.Errorf("Bucket() = %q after mutating a returned slice, want revisions", Bucket())
	}
}

// Spec: §6.5 — the mark key holds the normalized PODIUM_REGISTRY URL.
func TestNormalizeRegistry(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"HTTPS://Registry.ACME.com:443/":  "https://registry.acme.com",
		"http://registry.acme.com:80":     "http://registry.acme.com",
		"http://registry.acme.com:443":    "http://registry.acme.com:443",
		"https://registry.acme.com:80/x/": "https://registry.acme.com:80/x",
		"  http://[::1]:80//  ":           "http://[::1]",
		"http://127.0.0.1:8080":           "http://127.0.0.1:8080",
		"  not a url  ":                   "not a url",
		"":                                "",
		"http://%zz":                      "http://%zz",
	}
	for in, want := range cases {
		if got := NormalizeRegistry(in); got != want {
			t.Errorf("NormalizeRegistry(%q) = %q, want %q", in, got, want)
		}
	}
}

// Spec: §6.5 — the registry and artifact ID are joined by NUL.
func TestKeyEncode(t *testing.T) {
	t.Parallel()
	k := Key{Registry: "https://r", ArtifactID: "team/x"}
	if got := string(k.Encode()); got != "https://r\x00team/x" {
		t.Errorf("Encode = %q", got)
	}
	if id, ok := artifactIDOf(k.Encode()); !ok || id != "team/x" {
		t.Errorf("artifactIDOf = %q, %v", id, ok)
	}
	if _, ok := artifactIDOf([]byte("no-separator")); ok {
		t.Error("artifactIDOf accepted a key without a separator")
	}
}

// Spec: §6.5, §7.2.1 — only the canonical artifact_revision form parses.
func TestParseRevision(t *testing.T) {
	t.Parallel()
	want := time.Date(2026, 5, 1, 12, 30, 45, 123456000, time.UTC)
	got, err := ParseRevision("2026-05-01T12:30:45.123456Z")
	if err != nil || got != uint64(want.UnixMicro()) {
		t.Fatalf("ParseRevision = %d, %v; want %d", got, err, want.UnixMicro())
	}
	if s := version.FormatArtifactRevision(time.UnixMicro(int64(got))); s != "2026-05-01T12:30:45.123456Z" {
		t.Errorf("FormatArtifactRevision is not the inverse: %q", s)
	}
	if got, err := ParseRevision("1970-01-01T00:00:00.000000Z"); err != nil || got != 0 {
		t.Errorf("epoch = %d, %v; want 0, nil", got, err)
	}
	for _, bad := range []string{
		"",
		"1714566645123456",
		"2026-05-01T12:30:45.12345Z",
		"2026-05-01T12:30:45.1234567Z",
		"2026-05-01T12:30:45Z",
		"2026-05-01T12:30:45.123456+00:00",
		"1969-12-31T23:59:59.999999Z",
		"2026-05-01t12:30:45.123456z",
	} {
		if _, err := ParseRevision(bad); !errors.Is(err, ErrMalformedRevision) {
			t.Errorf("ParseRevision(%q) err = %v, want ErrMalformedRevision", bad, err)
		}
	}
}

// writeIndex creates an index DB under dir holding the given buckets and marks.
func writeIndex(t *testing.T, dir string, buckets []string, marks map[string]string) {
	t.Helper()
	path := IndexPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := bolt.Open(path, 0o644, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, name := range buckets {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		for k, v := range marks {
			if err := tx.Bucket(Bucket()).Put([]byte(k), []byte(v)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// remaining returns the keys left in the revisions bucket, or nil when the
// bucket is absent.
func remaining(t *testing.T, dir string) map[string]bool {
	t.Helper()
	db, err := bolt.Open(IndexPath(dir), 0o644, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var out map[string]bool
	_ = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(Bucket())
		if b == nil {
			return nil
		}
		out = map[string]bool{}
		return b.ForEach(func(k, _ []byte) error {
			out[string(k)] = true
			return nil
		})
	})
	return out
}

// Spec: §6.5 — a reset against an absent index creates nothing.
func TestReset_AbsentIndexCreatesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	n, found, err := Reset(context.Background(), dir, nil)
	if err != nil || n != 0 || found {
		t.Fatalf("Reset = %d, %v, %v; want 0, false, nil", n, found, err)
	}
	if _, err := os.Stat(filepath.Join(dir, DirName)); !os.IsNotExist(err) {
		t.Errorf("Reset created %s: %v", DirName, err)
	}
}

// Spec: §6.5 — an index DB without the revisions bucket (written by a
// podium-mcp that predates the marks) reports no marks and gains no bucket.
func TestReset_MissingBucket(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeIndex(t, dir, []string{"resolutions"}, nil)
	n, found, err := Reset(context.Background(), dir, []string{"team/x"})
	if err != nil || n != 0 || found {
		t.Fatalf("Reset = %d, %v, %v; want 0, false, nil", n, found, err)
	}
	if got := remaining(t, dir); got != nil {
		t.Errorf("Reset created the revisions bucket: %v", got)
	}
}

// Spec: §6.5 — an empty bucket reports no marks.
func TestReset_EmptyBucket(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeIndex(t, dir, []string{"resolutions", "revisions"}, nil)
	n, found, err := Reset(context.Background(), dir, nil)
	if err != nil || n != 0 || found {
		t.Fatalf("Reset = %d, %v, %v; want 0, false, nil", n, found, err)
	}
}

// Spec: §6.5 — a reset by artifact ID matches across every registry prefix,
// and a reset with no IDs deletes every mark.
func TestReset_ByIDAcrossRegistriesAndAll(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a1 := string(Key{Registry: "https://r1", ArtifactID: "team/a"}.Encode())
	a2 := string(Key{Registry: "https://r2", ArtifactID: "team/a"}.Encode())
	b1 := string(Key{Registry: "https://r1", ArtifactID: "team/b"}.Encode())
	writeIndex(t, dir, []string{"revisions"}, map[string]string{a1: "1", a2: "2", b1: "3", "stray": "4"})

	n, found, err := Reset(context.Background(), dir, []string{"team/a"})
	if err != nil || n != 2 || !found {
		t.Fatalf("Reset(team/a) = %d, %v, %v; want 2, true, nil", n, found, err)
	}
	if got := remaining(t, dir); len(got) != 2 || !got[b1] || !got["stray"] {
		t.Errorf("remaining after Reset(team/a) = %v", got)
	}

	n, found, err = Reset(context.Background(), dir, []string{"team/zzz"})
	if err != nil || n != 0 || !found {
		t.Fatalf("Reset(no match) = %d, %v, %v; want 0, true, nil", n, found, err)
	}

	n, found, err = Reset(context.Background(), dir, nil)
	if err != nil || n != 2 || !found {
		t.Fatalf("Reset(all) = %d, %v, %v; want 2, true, nil", n, found, err)
	}
	if got := remaining(t, dir); len(got) != 0 {
		t.Errorf("remaining after Reset(all) = %v", got)
	}
}

// Spec: §6.5 — a running podium-mcp holds the index lock, so a reset reports
// ErrIndexLocked after the open timeout.
func TestReset_LockedIndex(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeIndex(t, dir, []string{"revisions"}, map[string]string{"r\x00team/a": "1"})
	db, err := bolt.Open(IndexPath(dir), 0o644, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, _, err := Reset(context.Background(), dir, nil); !errors.Is(err, ErrIndexLocked) {
		t.Fatalf("Reset on a locked index err = %v, want ErrIndexLocked", err)
	}
}

// Spec: §6.5 — a canceled context stops the reset before it opens anything,
// and an index path that is not a BoltDB file is an open error.
func TestReset_ErrorPaths(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Reset(ctx, t.TempDir(), nil); !errors.Is(err, context.Canceled) {
		t.Errorf("Reset with a canceled context err = %v", err)
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(IndexPath(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(IndexPath(dir), []byte("not a bolt file, padded to exceed one page header"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Reset(context.Background(), dir, nil); err == nil || errors.Is(err, ErrIndexLocked) {
		t.Errorf("Reset on a corrupt index err = %v, want an open error", err)
	}

	blocked := t.TempDir()
	if err := os.WriteFile(filepath.Join(blocked, DirName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Reset(context.Background(), blocked, nil); err == nil {
		t.Error("Reset with a file in place of the index directory returned nil")
	}
}
