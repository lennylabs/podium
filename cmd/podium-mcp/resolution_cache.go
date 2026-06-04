package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

// resolutionCache is the §6.5 (id, version) resolution index. It is backed by
// an embedded BoltDB file, satisfying the §6.5 "Index DB: BoltDB or SQLite"
// requirement; the content cache remains a content-addressed directory tree.
//
// Two key kinds live in the bucket:
//
//   - id@latest    → {ResolvedVersion: semver, FetchedAt}   (§6.5 "(id, "latest") → semver")
//   - id@<semver>  → {ContentHash, FetchedAt}               (immutable content pin)
//
// A `latest` lookup chains id@latest → semver → id@semver → content_hash. The
// latest entry carries the fetch timestamp so the §6.5 30-second TTL can treat
// a stale `latest` resolution as a miss and fall through to the registry.
type resolutionCache struct {
	mu  sync.Mutex
	dir string
	db  *bolt.DB
	// observe, when set, receives one call per Resolve reporting whether the
	// lookup hit (true) or missed (false). It feeds the §13.8
	// podium_cache_hits_total / podium_cache_misses_total counters. Calls
	// against a disabled cache (no backing db) are not reported. nil disables
	// the callback.
	observe func(hit bool)
}

// resolutionBucket is the single BoltDB bucket holding every resolution entry.
var resolutionBucket = []byte("resolutions")

// resolutionEntry is the value stored under a resolution key. A latest entry
// records the resolved semver (§6.5); a version entry records the content hash.
// FetchedAt stamps when the resolution was last confirmed, backing the TTL.
type resolutionEntry struct {
	ResolvedVersion string    `json:"resolved_version,omitempty"`
	ContentHash     string    `json:"content_hash,omitempty"`
	FetchedAt       time.Time `json:"fetched_at"`
}

func newResolutionCache(cacheDir string) *resolutionCache {
	if cacheDir == "" {
		return &resolutionCache{}
	}
	dir := filepath.Join(cacheDir, ".resolutions")
	r := &resolutionCache{dir: dir}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return r
	}
	// A short open timeout keeps a stale lock from another process from
	// blocking startup indefinitely; a failed open leaves the cache disabled
	// (db nil) so the bridge still runs against the registry.
	db, err := bolt.Open(filepath.Join(dir, "index.db"), 0o644, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return r
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, e := tx.CreateBucketIfNotExists(resolutionBucket)
		return e
	}); err != nil {
		_ = db.Close()
		return r
	}
	r.db = db
	return r
}

// Close releases the BoltDB file lock. The bridge holds the cache for its
// whole lifetime; tests close one handle before reopening the same directory.
func (r *resolutionCache) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	err := r.db.Close()
	r.db = nil
	return err
}

// resolutionKey is the lookup key. version="" stands for "latest" per the §6.5
// resolution cache.
func resolutionKey(id, version string) string {
	if version == "" {
		version = "latest"
	}
	return id + "@" + version
}

func (r *resolutionCache) putEntry(key string, e resolutionEntry) {
	if r == nil || r.db == nil {
		return
	}
	body, err := json.Marshal(e)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(resolutionBucket)
		if b == nil {
			return nil
		}
		return b.Put([]byte(key), body)
	})
}

func (r *resolutionCache) getEntry(key string) (resolutionEntry, bool) {
	if r == nil || r.db == nil {
		return resolutionEntry{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var e resolutionEntry
	found := false
	_ = r.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(resolutionBucket)
		if b == nil {
			return nil
		}
		v := b.Get([]byte(key))
		if v == nil {
			return nil
		}
		found = json.Unmarshal(v, &e) == nil
		return nil
	})
	return e, found
}

// PutLatest records a resolved `latest` request: the (id, "latest") key maps to
// the resolved semver (§6.5) and the (id, semver) key maps to the content hash
// so the hash stays recoverable. When the registry returned no version the hash
// is stored on the latest key directly so offline reads still resolve.
func (r *resolutionCache) PutLatest(id, resolvedVersion, contentHash string, now time.Time) {
	if resolvedVersion == "" {
		r.putEntry(resolutionKey(id, ""), resolutionEntry{ContentHash: contentHash, FetchedAt: now})
		return
	}
	r.putEntry(resolutionKey(id, ""), resolutionEntry{ResolvedVersion: resolvedVersion, FetchedAt: now})
	r.putEntry(resolutionKey(id, resolvedVersion), resolutionEntry{ContentHash: contentHash, FetchedAt: now})
}

// PutVersion records a pinned (id, version) → content_hash resolution. Pinned
// versions are immutable, so the entry never expires.
func (r *resolutionCache) PutVersion(id, version, contentHash string, now time.Time) {
	r.putEntry(resolutionKey(id, version), resolutionEntry{ContentHash: contentHash, FetchedAt: now})
}

// RefreshLatest bumps the (id, "latest") entry's fetch timestamp after a
// successful HEAD revalidation (§6.5 always-revalidate), restarting the TTL
// window without rewriting the resolved-version chain. A no-op when no latest
// entry exists.
func (r *resolutionCache) RefreshLatest(id string, now time.Time) {
	e, ok := r.getEntry(resolutionKey(id, ""))
	if !ok {
		return
	}
	e.FetchedAt = now
	r.putEntry(resolutionKey(id, ""), e)
}

// Resolve returns the cached content hash for (id, version). For a `latest`
// request (version=""), a resolution older than ttl is treated as a miss unless
// allowStale is set: offline-only mode and the degraded-network fallback serve
// a stale `latest` because they cannot refresh it. Pinned versions are
// immutable and never expire.
func (r *resolutionCache) Resolve(id, version string, now time.Time, ttl time.Duration, allowStale bool) (hash string, hit bool) {
	// §13.8: report the lookup outcome once, on the way out, but only when the
	// cache is operational so a disabled cache does not inflate the miss count.
	if r != nil && r.db != nil && r.observe != nil {
		defer func() { r.observe(hit) }()
	}
	e, ok := r.getEntry(resolutionKey(id, version))
	if !ok {
		return "", false
	}
	if version == "" {
		if !allowStale && ttl > 0 && now.Sub(e.FetchedAt) > ttl {
			return "", false
		}
		if e.ContentHash != "" {
			return e.ContentHash, true
		}
		if e.ResolvedVersion != "" {
			if ve, ok := r.getEntry(resolutionKey(id, e.ResolvedVersion)); ok && ve.ContentHash != "" {
				return ve.ContentHash, true
			}
		}
		return "", false
	}
	if e.ContentHash != "" {
		return e.ContentHash, true
	}
	return "", false
}

// Len returns the number of cached resolution keys, surfaced as the §13.9
// health tool's cache size. A disabled cache reports 0.
func (r *resolutionCache) Len() int {
	if r == nil || r.db == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	_ = r.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(resolutionBucket)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			n++
		}
		return nil
	})
	return n
}

// loadArtifactFromCache reconstructs a loadArtifactResponse from the bytes the
// content cache holds at contentHash. Used by the offline-first / offline-only
// cache modes and the always-revalidate HEAD-revalidated hit.
func (s *mcpServer) loadArtifactFromCache(contentHash, idHint string) (*loadArtifactResponse, error) {
	bucket := filepath.Join(s.cfg.cacheDir, sanitizeHash(contentHash))
	frontmatter, err := os.ReadFile(filepath.Join(bucket, "frontmatter"))
	if err != nil {
		return nil, fmt.Errorf("cache miss for %s: %w", contentHash, err)
	}
	body, err := os.ReadFile(filepath.Join(bucket, "body"))
	if err != nil {
		return nil, fmt.Errorf("cache body missing for %s: %w", contentHash, err)
	}
	resp := &loadArtifactResponse{
		ID:           idHint,
		ContentHash:  contentHash,
		Frontmatter:  string(frontmatter),
		ManifestBody: string(body),
		Resources:    map[string]string{},
	}
	// Restore the auxiliary content putExtras persisted so the cache-served
	// response drives the §6.6 gates exactly as a live fetch does. Without
	// skill_raw a cache-served skill recomputes its §6.6 step 2 hash over
	// ARTIFACT.md only (slot 1 empty) and fails content_hash_mismatch, and
	// materializes a synthesized SKILL.md rather than the authored bytes. A
	// present raw_frontmatter marks an extends-merged manifest so
	// verifyContentHash hashes the pre-merge frontmatter. sensitivity and
	// signature drive enforceSignaturePolicy: dropping sensitivity would skip
	// §4.7.9 verification on a cache hit (a high-sensitivity artifact would
	// materialize unverified), and dropping the signature would fail a policy it
	// should pass. Each file is absent when its field was empty at ingest, so a
	// plain low-sensitivity context artifact restores none of them.
	if sr, err := os.ReadFile(filepath.Join(bucket, "skill_raw")); err == nil {
		resp.SkillRaw = string(sr)
	}
	if rf, err := os.ReadFile(filepath.Join(bucket, "raw_frontmatter")); err == nil {
		resp.RawFrontmatter = string(rf)
		resp.ManifestMerged = true
	}
	if sv, err := os.ReadFile(filepath.Join(bucket, "sensitivity")); err == nil {
		resp.Sensitivity = string(sv)
	}
	if sig, err := os.ReadFile(filepath.Join(bucket, "signature")); err == nil {
		resp.Signature = string(sig)
	}
	// Recover the resolved version and type from the cached frontmatter so a
	// cache-served load reports the same version/type as a live fetch (the
	// content bucket is keyed by hash, which is 1:1 with a version because the
	// frontmatter carries the version line). Recover sensitivity from the
	// frontmatter too when the side file is absent (a prefetch-warmed entry does
	// not write one): the §4.7.9 policy gate must know the sensitivity so a
	// high-sensitivity cache-served artifact is verified rather than waved
	// through, and the frontmatter is the authoritative source for it.
	if ctx := manifestContext(string(frontmatter)); ctx != nil {
		if v, ok := ctx["version"].(string); ok {
			resp.Version = v
		}
		if tp, ok := ctx["type"].(string); ok {
			resp.Type = tp
		}
		if resp.Sensitivity == "" {
			if sv, ok := ctx["sensitivity"].(string); ok {
				resp.Sensitivity = sv
			}
		}
	}
	resourcesDir := filepath.Join(bucket, "resources")
	_ = filepath.Walk(resourcesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(resourcesDir, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		resp.Resources[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	// §6.5 last-access accounting (F-6.5.6): a content bucket is written once
	// and only read afterward, so `podium cache prune` (mtime-based) would
	// evict a frequently-read but never-rewritten bucket. Touch the bucket on
	// every cache hit so a read counts as access.
	touchBucket(bucket)
	return resp, nil
}

// touchBucket updates the bucket's file mtimes to now so a cache read refreshes
// its "last access" time for prune (§6.5).
func touchBucket(bucket string) {
	now := time.Now()
	for _, name := range []string{"frontmatter", "body"} {
		_ = os.Chtimes(filepath.Join(bucket, name), now, now)
	}
}

// errOfflineCacheMiss is returned in offline-only mode when the requested
// artifact (or discovery result) is not in the local cache. §7.4 mandates a
// "structured error if cache miss" but does not name a code; the §6.10
// namespace list (auth.*, config.*, ingest.*, materialize.*, quota.*, mcp.*,
// network.*, registry.*, domain.*) has no cache.* namespace, so the code lives
// under network.* — the same namespace as network.registry_unreachable, which
// is the other degraded-network code (F-7.4.5).
var errOfflineCacheMiss = errors.New("network.offline_cache_miss: requested content not in offline cache")

// argsIDAndVersion converts a generic argument map's `id` and `version` fields
// to canonical strings.
func argsIDAndVersion(args map[string]any) (string, string) {
	id, _ := args["id"].(string)
	version, _ := args["version"].(string)
	return strings.TrimSpace(id), strings.TrimSpace(version)
}
