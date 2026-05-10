package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// resolutionCache is the §6.5 (id, version) → content_hash index.
// Maintained alongside the content cache so offline-first /
// offline-only modes can serve `load_artifact` without contacting
// the registry. Entries are written on every successful registry
// fetch; the file is human-readable JSON for easy debugging.
type resolutionCache struct {
	mu      sync.Mutex
	dir     string
	entries map[string]string
}

func newResolutionCache(cacheDir string) *resolutionCache {
	if cacheDir == "" {
		return &resolutionCache{}
	}
	r := &resolutionCache{
		dir:     filepath.Join(cacheDir, ".resolutions"),
		entries: map[string]string{},
	}
	r.load()
	return r
}

func (r *resolutionCache) load() {
	if r.dir == "" {
		return
	}
	path := filepath.Join(r.dir, "index.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &r.entries)
}

func (r *resolutionCache) save() error {
	if r.dir == "" {
		return nil
	}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(r.entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(r.dir, "index.json.tmp")
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(r.dir, "index.json"))
}

// resolutionKey is the lookup key. version="" stands for "latest"
// per the §6.5 resolution cache.
func resolutionKey(id, version string) string {
	if version == "" {
		version = "latest"
	}
	return id + "@" + version
}

// Put records a resolution. Errors that prevent persistence are
// non-fatal; the in-memory map still serves the current process.
// A no-op when the cache is disabled (empty dir).
func (r *resolutionCache) Put(id, version, contentHash string) {
	if r == nil || r.dir == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = map[string]string{}
	}
	r.entries[resolutionKey(id, version)] = contentHash
	_ = r.save()
}

// Get returns the cached content hash for (id, version) or
// ("", false) when the resolution isn't cached.
func (r *resolutionCache) Get(id, version string) (string, bool) {
	if r == nil || r.dir == "" {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.entries[resolutionKey(id, version)]
	return h, ok
}

// loadArtifactFromCache reconstructs a loadArtifactResponse from
// the bytes the content cache holds at contentHash. Used by the
// offline-first / offline-only cache modes.
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
	return resp, nil
}

// errOfflineCacheMiss is returned in offline-only mode when the
// requested artifact isn't in the cache. Maps to
// cache.offline_miss in the §6.10 namespace.
var errOfflineCacheMiss = errors.New("cache.offline_miss: artifact not in offline cache")

// asArgs converts a generic argument map's `id` and `version`
// fields to canonical strings.
func argsIDAndVersion(args map[string]any) (string, string) {
	id, _ := args["id"].(string)
	version, _ := args["version"].(string)
	return strings.TrimSpace(id), strings.TrimSpace(version)
}
