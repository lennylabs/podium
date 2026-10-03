package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/lennylabs/podium/internal/revmark"
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
//
// A second bucket, revmark.Bucket(), holds the §6.5 revision marks: per
// registry and artifact ID, the highest artifact_revision accepted for a
// `latest` load, stored as decimal Unix microseconds. The marks are
// consumer-side state, so the §7.2.1 timestamp encoding does not govern them.
type resolutionCache struct {
	// mu serializes every bucket access and both maps below. A mark is read
	// and advanced in one critical section, so two concurrent loads cannot
	// interleave a read of the mark with the other's write.
	mu  sync.Mutex
	dir string
	db  *bolt.DB
	// mem holds the revision marks, keyed by revmark.Key.Encode, when db is
	// nil: the cache directory is unset, or the index DB could not be opened
	// (another podium-mcp holds its lock). The marks then last for this
	// process only.
	mem map[string]uint64
	// sessions holds the session references: per mark key and effective
	// session_id, the revision of the first `latest` answer this process
	// accepted in that session. They are always in memory, because a session
	// does not outlive the process that recorded its registry pin.
	sessions map[sessionMarkKey]uint64
	// observe, when set, receives one call per Resolve reporting whether the
	// lookup hit (true) or missed (false). It feeds the §13.8
	// podium_cache_hits_total / podium_cache_misses_total counters. Calls
	// against a disabled cache (no backing db) are not reported. nil disables
	// the callback.
	observe func(hit bool)
}

// resolutionBucket is the BoltDB bucket holding every resolution entry.
var resolutionBucket = []byte("resolutions")

// sessionMarkKey identifies one session reference. The encoded mark key and
// the session are separate fields, so two sessions, two registries, or two
// artifact IDs never share an entry.
type sessionMarkKey struct {
	mark    string
	session string
}

// resolutionEntry is the value stored under a resolution key. A latest entry
// records the resolved semver (§6.5); a version entry records the content hash.
// FetchedAt stamps when the resolution was last confirmed, backing the TTL.
type resolutionEntry struct {
	ResolvedVersion string    `json:"resolved_version,omitempty"`
	ContentHash     string    `json:"content_hash,omitempty"`
	FetchedAt       time.Time `json:"fetched_at"`
}

func newResolutionCache(cacheDir string) *resolutionCache {
	r := &resolutionCache{mem: map[string]uint64{}, sessions: map[sessionMarkKey]uint64{}}
	if cacheDir == "" {
		return r
	}
	r.dir = filepath.Join(cacheDir, revmark.DirName)
	db, err := openIndexDB(revmark.IndexPath(cacheDir))
	if err != nil {
		// A failed open leaves the index disabled (db nil) so the bridge still
		// runs against the registry, with its revision marks in mem.
		log.Printf("warning: podium-mcp: resolution index under %s unavailable (%v); revision marks are held in memory for this process", cacheDir, err)
		return r
	}
	r.db = db
	return r
}

// openIndexDB opens the index DB at path and creates the resolution and
// revision-mark buckets in one transaction. A short open timeout keeps a lock
// held by another process from blocking startup indefinitely.
func openIndexDB(path string) (*bolt.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0o644, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, err
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		if _, e := tx.CreateBucketIfNotExists(resolutionBucket); e != nil {
			return e
		}
		_, e := tx.CreateBucketIfNotExists(revmark.Bucket())
		return e
	}); err != nil {
		// Bucket creation fails only on an I/O error against a file bbolt
		// has just opened read-write, which no unit test can provoke.
		_ = db.Close()
		return nil, err
	}
	return db, nil
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

// Reference returns the value a fresh `latest` answer for k in session is
// compared with: the session reference when this process accepted an answer
// for k in session, and otherwise the stored mark. A stored mark that does not
// parse is deleted, logged by its key, and reported as absent. A nil cache
// holds no reference.
//
// Spec: §6.5
func (r *resolutionCache) Reference(k revmark.Key, session string) (uint64, bool) {
	if r == nil {
		return 0, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if ref, ok := r.sessions[sessionMarkKey{mark: string(k.Encode()), session: session}]; ok {
		return ref, true
	}
	return r.readMarkLocked(k.Encode())
}

// readMarkLocked returns the stored mark for the encoded key. The caller
// holds r.mu.
func (r *resolutionCache) readMarkLocked(key []byte) (uint64, bool) {
	if r.db == nil {
		mark, ok := r.mem[string(key)]
		return mark, ok
	}
	var mark uint64
	found := false
	_ = r.db.Update(func(tx *bolt.Tx) error {
		mark, found = readMark(tx.Bucket(revmark.Bucket()), key)
		return nil
	})
	return mark, found
}

// readMark reads the mark under key from b. A value that does not parse as a
// decimal within the int64 range is deleted and reported as absent; the
// warning names the key only, because the value is untrusted bytes. The bound
// is 63 bits because a mark is a count of Unix microseconds that the freshness
// check converts back to a time. A nil bucket holds no mark.
func readMark(b *bolt.Bucket, key []byte) (uint64, bool) {
	if b == nil {
		return 0, false
	}
	v := b.Get(key)
	if v == nil {
		return 0, false
	}
	mark, err := strconv.ParseUint(string(v), 10, 63)
	if err != nil {
		log.Printf("warning: podium-mcp: deleting unparseable revision mark %q", key)
		_ = b.Delete(key)
		return 0, false
	}
	return mark, true
}

// NoteSession records revision as the session reference for (k, session) when
// session is non-empty and holds none. It never changes an existing reference
// and never reads or writes a mark, so a revalidated cache delivery and a
// resources-mirror answer pin the session without advancing the mark.
//
// Spec: §6.5
func (r *resolutionCache) NoteSession(k revmark.Key, session string, revision uint64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.noteSessionLocked(k, session, revision)
}

// noteSessionLocked is NoteSession for a caller that holds r.mu.
func (r *resolutionCache) noteSessionLocked(k revmark.Key, session string, revision uint64) {
	if session == "" {
		return
	}
	sk := sessionMarkKey{mark: string(k.Encode()), session: session}
	if _, ok := r.sessions[sk]; ok {
		return
	}
	if r.sessions == nil {
		r.sessions = map[sessionMarkKey]uint64{}
	}
	r.sessions[sk] = revision
}

// PutLatestAdvancing records a resolved `latest` request whose served
// artifact_revision is revision. When revision is at or above the stored mark
// for k, one transaction advances the mark to revision and writes the
// (id, "latest") → semver and (id, semver) → content_hash entries (§6.5); when
// the registry returned no version the hash is stored on the latest key
// directly so offline reads still resolve. When revision is below the mark
// (the session reference admitted the answer, or a concurrent load advanced
// the mark), the transaction writes nothing, so the index never resolves
// `latest` to an older record than the mark. Either way the session reference
// is recorded when the session holds none. With no index DB the same rule
// applies to the in-memory marks.
//
// Spec: §6.5
func (r *resolutionCache) PutLatestAdvancing(k revmark.Key, session, id, version, contentHash string, revision uint64, now time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.noteSessionLocked(k, session, revision)
	key := k.Encode()
	if r.db == nil {
		if mark, ok := r.mem[string(key)]; !ok || revision >= mark {
			if r.mem == nil {
				r.mem = map[string]uint64{}
			}
			r.mem[string(key)] = revision
		}
		return
	}
	entries, err := latestEntries(id, version, contentHash, now)
	if err != nil {
		return
	}
	_ = r.db.Update(func(tx *bolt.Tx) error {
		marks := tx.Bucket(revmark.Bucket())
		res := tx.Bucket(resolutionBucket)
		if marks == nil || res == nil {
			return nil
		}
		if mark, ok := readMark(marks, key); ok && revision < mark {
			return nil
		}
		if err := marks.Put(key, []byte(strconv.FormatUint(revision, 10))); err != nil {
			return err
		}
		for k, body := range entries {
			if err := res.Put([]byte(k), body); err != nil {
				return err
			}
		}
		return nil
	})
}

// latestEntries returns the encoded resolution entries a `latest` resolution
// writes, keyed by resolution key.
func latestEntries(id, version, contentHash string, now time.Time) (map[string][]byte, error) {
	want := map[string]resolutionEntry{}
	if version == "" {
		want[resolutionKey(id, "")] = resolutionEntry{ContentHash: contentHash, FetchedAt: now}
	} else {
		want[resolutionKey(id, "")] = resolutionEntry{ResolvedVersion: version, FetchedAt: now}
		want[resolutionKey(id, version)] = resolutionEntry{ContentHash: contentHash, FetchedAt: now}
	}
	out := make(map[string][]byte, len(want))
	for k, e := range want {
		body, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		out[k] = body
	}
	return out, nil
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

// loadArtifactFromCache reconstructs a loadArtifactResponse from the content
// cache at contentHash for the artifact idHint. Used by the offline-first /
// offline-only cache modes, the always-revalidate HEAD-revalidated hit, the 304
// path, and the degraded-network fallback. Each caller passes the result
// through deliverLoadArtifact, which re-runs the §6.6 delivery check and the
// §4.7.9 policy, so no cache mode serves an unverified record.
//
// The served document, body, delivery pair, and sensitivity come from the
// per-ID delivery files putDelivery wrote. A bucket without them for idHint,
// including one written before the delivery record existed, is a miss, and so
// is a per-ID directory whose id file names another artifact.
//
// Spec: §6.5, §6.6 step 2, §4.7.10
func (s *mcpServer) loadArtifactFromCache(contentHash, idHint string) (*loadArtifactResponse, error) {
	bucket := filepath.Join(s.cfg.cacheDir, sanitizeHash(contentHash))
	d, err := readDeliveryFiles(filepath.Join(bucket, "delivery", deliverySegment(idHint)), idHint)
	if err != nil {
		return nil, fmt.Errorf("cache miss for %s: %w", contentHash, err)
	}
	resp := &loadArtifactResponse{
		ID:                idHint,
		ContentHash:       contentHash,
		Frontmatter:       d.Frontmatter,
		ManifestBody:      d.Body,
		Sensitivity:       d.Sensitivity,
		DeliveryHash:      d.DeliveryHash,
		DeliverySignature: d.DeliverySignature,
		ArtifactRevision:  d.ArtifactRevision,
		Resources:         map[string]string{},
	}
	// A skill's verbatim SKILL.md is bucket-level, because the authored
	// package the content hash names carries it.
	if sr, err := os.ReadFile(filepath.Join(bucket, "skill_raw")); err == nil {
		resp.SkillRaw = string(sr)
	}
	// Recover the resolved version and type from the cached served document so
	// a cache-served load reports the version and type a live fetch does. The
	// delivery record frames both, so a recovered value that differs from the
	// served one fails the delivery check.
	if ctx := manifestContext(d.Frontmatter); ctx != nil {
		if v, ok := ctx["version"].(string); ok {
			resp.Version = v
		}
		if tp, ok := ctx["type"].(string); ok {
			resp.Type = tp
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
	// §6.5 last-access accounting: a content bucket is written once
	// and only read afterward, so `podium cache prune` (mtime-based) would
	// evict a frequently-read but never-rewritten bucket. Touch the bucket on
	// every cache hit so a read counts as access.
	touchBucket(bucket)
	return resp, nil
}

// readDeliveryFiles reads the per-ID delivery files putDelivery wrote in dir.
// The id file, the served document, the body, the delivery hash, and the
// artifact revision are required, and the id file must hold id verbatim; the
// signature and sensitivity files read as empty when absent. A record cached
// before the artifact_revision file existed is therefore a miss, because its
// delivery check would frame an empty revision.
func readDeliveryFiles(dir, id string) (deliveryFiles, error) {
	required := map[string]string{}
	for _, name := range []string{"id", "frontmatter", "body", "delivery_hash", "artifact_revision"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return deliveryFiles{}, fmt.Errorf("delivery %s: %w", name, err)
		}
		required[name] = string(b)
	}
	if required["id"] != id {
		return deliveryFiles{}, fmt.Errorf("delivery record belongs to %q, not %q", required["id"], id)
	}
	if required["delivery_hash"] == "" {
		return deliveryFiles{}, errors.New("delivery record carries no delivery hash")
	}
	optional := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return string(b)
	}
	return deliveryFiles{
		Frontmatter:       required["frontmatter"],
		Body:              required["body"],
		DeliveryHash:      required["delivery_hash"],
		DeliverySignature: optional("delivery_signature"),
		Sensitivity:       optional("sensitivity"),
		ArtifactRevision:  required["artifact_revision"],
	}, nil
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
// is the other degraded-network code.
var errOfflineCacheMiss = errors.New("network.offline_cache_miss: requested content not in offline cache")

// argsIDAndVersion converts a generic argument map's `id` and `version` fields
// to canonical strings.
func argsIDAndVersion(args map[string]any) (string, string) {
	id, _ := args["id"].(string)
	version, _ := args["version"].(string)
	return strings.TrimSpace(id), strings.TrimSpace(version)
}
