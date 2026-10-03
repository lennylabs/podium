// Package revmark holds the key, path, and encoding helpers for the §6.5
// revision marks that podium-mcp keeps for `latest` loads, and the reset that
// `podium cache reset-revisions` runs against them. The marks live in the
// MCP server's resolution index DB; podium-mcp owns the bucket, and this
// package exposes no bbolt type so the storage stays an implementation detail.
//
// Spec: §6.5
package revmark

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"

	"github.com/lennylabs/podium/pkg/version"
)

// DirName is the dot-prefixed directory under the cache directory that holds
// the resolution index DB. `podium cache prune` skips it.
const DirName = ".resolutions"

// indexFile is the BoltDB file inside DirName.
const indexFile = "index.db"

// bucketName is the BoltDB bucket that holds the revision marks.
const bucketName = "revisions"

// openTimeout bounds the wait for the BoltDB file lock. podium-mcp holds the
// lock for its whole lifetime, so a reset against a directory a running
// server uses fails after this interval instead of blocking.
const openTimeout = 2 * time.Second

// revisionLayout is the §7.2.1 fixed layout of artifact_revision.
const revisionLayout = "2006-01-02T15:04:05.000000Z"

// ErrIndexLocked reports that another process holds the index DB lock.
var ErrIndexLocked = errors.New("revmark: index DB is locked")

// ErrMalformedRevision reports an artifact_revision value that is not in the
// canonical §7.2.1 form.
var ErrMalformedRevision = errors.New("revmark: malformed artifact_revision")

// IndexPath returns the resolution index DB path under cacheDir.
func IndexPath(cacheDir string) string {
	return filepath.Join(cacheDir, DirName, indexFile)
}

// Bucket returns the name of the BoltDB bucket that holds the revision marks.
// It returns a fresh slice on each call so a caller cannot alter the name.
func Bucket() []byte {
	return []byte(bucketName)
}

// Key identifies one revision mark: a normalized registry URL and an
// artifact ID. The key carries no tenant, because the registry selects the
// tenant from the authenticated identity and podium-mcp holds no value that
// names it.
type Key struct {
	Registry   string
	ArtifactID string
}

// Encode returns the stored form of k: the registry and the artifact ID
// joined by NUL. Neither component contains NUL, so the split is unambiguous.
func (k Key) Encode() []byte {
	return []byte(k.Registry + "\x00" + k.ArtifactID)
}

// artifactIDOf returns the artifact-ID component of an encoded key, and false
// when the key holds no separator.
func artifactIDOf(encoded []byte) (string, bool) {
	i := bytes.LastIndexByte(encoded, 0)
	if i < 0 {
		return "", false
	}
	return string(encoded[i+1:]), true
}

// NormalizeRegistry returns the canonical form of a registry URL for a mark
// key, so equivalent spellings of one PODIUM_REGISTRY share their marks. It
// lowercases the scheme and host, drops the default port (:80 on http, :443
// on https), and removes trailing slashes. A value that does not parse as an
// absolute URL is returned trimmed.
func NormalizeRegistry(raw string) string {
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return trimmed
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if p := u.Port(); (u.Scheme == "http" && p == "80") || (u.Scheme == "https" && p == "443") {
		u.Host = strings.TrimSuffix(u.Host, ":"+p)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return strings.TrimRight(u.String(), "/")
}

// ParseRevision parses a served artifact_revision into Unix microseconds. It
// accepts only the canonical form version.FormatArtifactRevision writes, so
// the empty string, a decimal integer, a value with other than six fractional
// digits, a numeric zone offset, and a time before 1970 are all rejected with
// an error wrapping ErrMalformedRevision. FormatArtifactRevision of
// time.UnixMicro(int64(n)) is its inverse.
func ParseRevision(s string) (uint64, error) {
	t, err := time.Parse(revisionLayout, s)
	if err != nil {
		return 0, fmt.Errorf("%w: %q: %w", ErrMalformedRevision, s, err)
	}
	if t.UnixMicro() < 0 {
		return 0, fmt.Errorf("%w: %q is before the epoch", ErrMalformedRevision, s)
	}
	if version.FormatArtifactRevision(t) != s {
		return 0, fmt.Errorf("%w: %q is not canonical", ErrMalformedRevision, s)
	}
	return uint64(t.UnixMicro()), nil
}

// Reset deletes the revision marks under cacheDir whose artifact ID is in
// ids, or every mark when ids is empty. found reports whether the bucket held
// at least one mark before the call, and deleted counts the marks removed.
//
// Reset creates nothing: an absent index file, an absent bucket (an index DB
// written by a podium-mcp that predates the marks holds only the resolution
// bucket), and an empty bucket all return (0, false, nil). A lock held by a
// running podium-mcp returns ErrIndexLocked.
//
// Spec: §6.5
func Reset(ctx context.Context, cacheDir string, ids []string) (deleted int, found bool, err error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	path := IndexPath(cacheDir)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("revmark: stat %s: %w", path, err)
	}
	db, err := bolt.Open(path, 0o644, &bolt.Options{Timeout: openTimeout})
	if err != nil {
		if errors.Is(err, bolterrors.ErrTimeout) {
			return 0, false, fmt.Errorf("%w: %s", ErrIndexLocked, path)
		}
		return 0, false, fmt.Errorf("revmark: open %s: %w", path, err)
	}
	defer func() { _ = db.Close() }()
	err = db.Update(func(tx *bolt.Tx) error {
		deleted, found, err = deleteMarks(tx.Bucket(Bucket()), ids)
		return err
	})
	if err != nil {
		return 0, false, fmt.Errorf("revmark: reset %s: %w", path, err)
	}
	return deleted, found, nil
}

// deleteMarks removes the keys of b that match ids, or every key when ids is
// empty. The keys are collected first because bbolt forbids mutating a bucket
// inside ForEach. A nil bucket holds no mark.
func deleteMarks(b *bolt.Bucket, ids []string) (deleted int, found bool, err error) {
	if b == nil {
		return 0, false, nil
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var victims [][]byte
	if err := b.ForEach(func(k, _ []byte) error {
		found = true
		if len(want) == 0 {
			victims = append(victims, append([]byte(nil), k...))
			return nil
		}
		if id, ok := artifactIDOf(k); ok && want[id] {
			victims = append(victims, append([]byte(nil), k...))
		}
		return nil
	}); err != nil {
		return 0, false, err
	}
	for _, k := range victims {
		if err := b.Delete(k); err != nil {
			return 0, false, err
		}
	}
	return len(victims), found, nil
}
