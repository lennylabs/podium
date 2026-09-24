package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// Spec: §6.5 — the resolution cache persists (id, version) →
// content_hash to a BoltDB index so offline-first reads can serve
// future requests without contacting the registry.
func TestResolutionCache_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Now()
	r1 := newResolutionCache(dir)
	r1.PutVersion("team/finance", "1.0.0", "sha256:abc", now)
	// A latest request resolving to 1.0.0 maps (id,"latest")→semver
	// and (id,1.0.0)→content_hash (§6.5).
	r1.PutLatest("team/finance", "1.0.0", "sha256:abc", now)
	// Close releases the BoltDB lock so a second handle can open the
	// same on-disk index.
	if err := r1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r2 := newResolutionCache(dir)
	defer r2.Close()
	if got, ok := r2.Resolve("team/finance", "1.0.0", now, 30*time.Second, false); !ok || got != "sha256:abc" {
		t.Errorf("Resolve(1.0.0) = %q ok=%v, want sha256:abc, true", got, ok)
	}
	if got, ok := r2.Resolve("team/finance", "", now, 30*time.Second, false); !ok || got != "sha256:abc" {
		t.Errorf("Resolve(latest) = %q ok=%v, want sha256:abc, true", got, ok)
	}
}

// Spec: §6.5 — disabled cache (empty cache dir) is a no-op.
func TestResolutionCache_DisabledCache(t *testing.T) {
	t.Parallel()
	r := newResolutionCache("")
	r.PutVersion("x", "1.0.0", "sha256:abc", time.Now())
	if _, ok := r.Resolve("x", "1.0.0", time.Now(), 30*time.Second, false); ok {
		t.Errorf("disabled cache returned a hit")
	}
}

// Spec: §6.5 — loadArtifactFromCache reads the bytes the content
// cache holds at contentHash, plus every bundled resource.
func TestLoadArtifactFromCache_RecoversBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cache, err := newContentCache(dir)
	if err != nil {
		t.Fatalf("newContentCache: %v", err)
	}
	const hash = "sha256:abc"
	if err := cache.put(hash, "frontmatter-body", "manifest-body", map[string]string{
		"scripts/run.py": "print('x')\n",
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	srv := &mcpServer{cfg: &config{cacheDir: dir}}
	got, err := srv.loadArtifactFromCache(hash, "team/x")
	if err != nil {
		t.Fatalf("loadArtifactFromCache: %v", err)
	}
	if got.ID != "team/x" {
		t.Errorf("ID = %q, want team/x", got.ID)
	}
	if got.Frontmatter != "frontmatter-body" {
		t.Errorf("Frontmatter = %q", got.Frontmatter)
	}
	if got.ManifestBody != "manifest-body" {
		t.Errorf("ManifestBody = %q", got.ManifestBody)
	}
	if got.Resources["scripts/run.py"] != "print('x')\n" {
		t.Errorf("Resources = %+v", got.Resources)
	}
	// Bucket exists.
	if _, err := filepath.Abs(filepath.Join(dir, sanitizeHash(hash))); err != nil {
		t.Errorf("Abs: %v", err)
	}
}

// Spec: §4.3.4 / §6.5 / §6.6 — a skill's content hash covers ARTIFACT.md plus
// the verbatim SKILL.md (skill_raw). The cache must persist skill_raw so a
// cache-served skill recomputes the same §6.6 step 2 hash a live fetch does and
// materializes the authored SKILL.md byte-for-byte. Regression for a bug where
// the cache stored only frontmatter+body: every cache hit on a skill failed
// content_hash_mismatch (the recompute hashed ARTIFACT.md with an empty slot 1)
// and synthesized a non-authored SKILL.md.
func TestLoadArtifactFromCache_SkillRawRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cache, err := newContentCache(dir)
	if err != nil {
		t.Fatalf("newContentCache: %v", err)
	}

	frontmatter := "---\ntype: skill\nversion: 0.1.0\nsensitivity: high\n---\n\n<!-- body in SKILL.md -->\n"
	body := "# runbook\n\nbody\n"
	skillRaw := "---\nname: runbook\ndescription: A runbook\n---\n\n# runbook\n\nbody\n"
	// The registry keys ingest by ContentHash(ARTIFACT.md, SKILL.md, …) with
	// SKILL.md in slot 1 (verifyContentHash mirrors this).
	hash := "sha256:" + version.CanonicalContentHash([]byte(frontmatter), []byte(skillRaw), nil)

	if err := cache.put(hash, frontmatter, body, nil); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := cache.putExtras(hash, cacheExtras{SkillRaw: skillRaw}); err != nil {
		t.Fatalf("putExtras: %v", err)
	}

	srv := &mcpServer{cfg: &config{cacheDir: dir}}
	got, err := srv.loadArtifactFromCache(hash, "team/runbook")
	if err != nil {
		t.Fatalf("loadArtifactFromCache: %v", err)
	}
	if got.SkillRaw != skillRaw {
		t.Errorf("SkillRaw not restored from cache:\n got %q\nwant %q", got.SkillRaw, skillRaw)
	}
	// The §6.6 step 2 content-hash check passes for the cache-served skill.
	if err := srv.verifyContentHash(*got); err != nil {
		t.Errorf("verifyContentHash on cache-served skill: %v", err)
	}
	// The materialized SKILL.md is the authored bytes, not a synthesis.
	if md := synthesizeSkillMD(*got); md != skillRaw {
		t.Errorf("synthesizeSkillMD = %q, want authored skill_raw %q", md, skillRaw)
	}
}

// Spec: §6.6 step 2 — without the persisted skill_raw the recompute hashes
// ARTIFACT.md with an empty slot 1 and the cache-served skill is rejected. This
// pins the failure the fix removes: a skill cached as frontmatter+body only
// (no putExtras) must NOT verify, so the side file is load-bearing.
func TestVerifyContentHash_CacheServedSkillWithoutSkillRawFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cache, err := newContentCache(dir)
	if err != nil {
		t.Fatalf("newContentCache: %v", err)
	}
	frontmatter := "---\ntype: skill\nversion: 0.1.0\n---\n\nbody\n"
	skillRaw := "---\nname: x\ndescription: x\n---\n\n# x\n"
	hash := "sha256:" + version.CanonicalContentHash([]byte(frontmatter), []byte(skillRaw), nil)

	// Store WITHOUT putExtras to model the pre-fix cache layout.
	if err := cache.put(hash, frontmatter, "body", nil); err != nil {
		t.Fatalf("put: %v", err)
	}
	srv := &mcpServer{cfg: &config{cacheDir: dir}}
	got, err := srv.loadArtifactFromCache(hash, "x")
	if err != nil {
		t.Fatalf("loadArtifactFromCache: %v", err)
	}
	if err := srv.verifyContentHash(*got); err == nil {
		t.Fatal("verifyContentHash unexpectedly passed for a skill cached without skill_raw")
	} else if !strings.Contains(err.Error(), "content_hash_mismatch") {
		t.Errorf("err = %v, want content_hash_mismatch", err)
	}
}

// Spec: §4.7.9 / §6.6 — end-to-end through enforceSignaturePolicy: a signed
// high-sensitivity skill served from cache verifies under always when
// skill_raw round-trips, because the content hash the signature covers is the
// one the recompute reproduces. Guards the S19 cache-hit path.
func TestEnforceSignaturePolicy_CacheServedSignedSkill(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cache, err := newContentCache(dir)
	if err != nil {
		t.Fatalf("newContentCache: %v", err)
	}
	frontmatter := "---\ntype: skill\nversion: 0.1.0\nsensitivity: high\n---\n\nbody\n"
	skillRaw := "---\nname: signed\ndescription: signed\n---\n\n# signed\n"
	hash := "sha256:" + version.CanonicalContentHash([]byte(frontmatter), []byte(skillRaw), nil)

	// Registry-managed signer over the canonical hash, mirroring ingest.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer := sign.RegistryManagedKey{PrivateKey: priv, PublicKey: pub}
	envelope, err := signer.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if err := cache.put(hash, frontmatter, "body", nil); err != nil {
		t.Fatalf("put: %v", err)
	}
	// Persist sensitivity + signature the way deliverLoadArtifact does, so the
	// cache-served response carries the fields the §4.7.9 policy gate reads.
	if err := cache.putExtras(hash, cacheExtras{SkillRaw: skillRaw, Sensitivity: "high", Signature: envelope}); err != nil {
		t.Fatalf("putExtras: %v", err)
	}

	srv := &mcpServer{cfg: &config{
		cacheDir:          dir,
		verifyPolicy:      sign.PolicyAlways,
		signatureProvider: "registry-managed",
		verifier:          sign.RegistryManagedKey{PublicKey: pub},
	}}

	got, err := srv.loadArtifactFromCache(hash, "signed")
	if err != nil {
		t.Fatalf("loadArtifactFromCache: %v", err)
	}
	// The cache round-trips sensitivity and the signature envelope, so the
	// policy gate runs on exactly the data a live fetch would supply.
	if got.Sensitivity != "high" {
		t.Errorf("Sensitivity = %q, want high (restored from cache)", got.Sensitivity)
	}
	if got.Signature != envelope {
		t.Errorf("Signature not restored from cache:\n got %q\nwant %q", got.Signature, envelope)
	}
	if err := srv.enforceSignaturePolicy(*got); err != nil {
		t.Errorf("enforceSignaturePolicy on cache-served signed skill: %v", err)
	}
}

// Spec: §4.7.9 / §6.6 — an artifact served from cache without a stored
// signature is refused under always, not waved through. Sensitivity is still
// recovered from the cached frontmatter when no sensitivity side file was
// written (a prefetch-warmed entry), so the cache-served response reports what
// a live fetch does. Regression for a cache hit silently bypassing §4.7.9.
func TestEnforceSignaturePolicy_CacheServedHighSensitivityUnsignedRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cache, err := newContentCache(dir)
	if err != nil {
		t.Fatalf("newContentCache: %v", err)
	}
	// Frontmatter declares high sensitivity; the bucket stores no signature.
	frontmatter := "---\ntype: context\nversion: 1.0.0\nsensitivity: high\n---\n\nbody\n"
	hash := "sha256:" + version.CanonicalContentHash([]byte(frontmatter), nil, nil)
	if err := cache.put(hash, frontmatter, "", nil); err != nil {
		t.Fatalf("put: %v", err)
	}

	srv := &mcpServer{cfg: &config{
		cacheDir:          dir,
		verifyPolicy:      sign.PolicyAlways,
		signatureProvider: "noop",
	}}
	got, err := srv.loadArtifactFromCache(hash, "x")
	if err != nil {
		t.Fatalf("loadArtifactFromCache: %v", err)
	}
	if got.Sensitivity != "high" {
		t.Fatalf("Sensitivity = %q, want high recovered from frontmatter", got.Sensitivity)
	}
	if err := srv.enforceSignaturePolicy(*got); err == nil {
		t.Fatal("enforceSignaturePolicy waved through an unsigned high-sensitivity cache hit")
	} else if !strings.Contains(err.Error(), "signature_missing") {
		t.Errorf("err = %v, want signature_missing", err)
	}
}

// Spec: §6.5 — loadArtifactFromCache returns an error on missing
// bucket so offline-only mode can surface the network.offline_cache_miss
// envelope (§7.4 / §6.10).
func TestLoadArtifactFromCache_Missing(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{cacheDir: t.TempDir()}}
	_, err := srv.loadArtifactFromCache("sha256:absent", "x")
	if err == nil || !strings.Contains(err.Error(), "cache miss") {
		t.Errorf("err = %v, want cache miss", err)
	}
}

// unframedContentHash reproduces the pre-framing §4.7.6 composition: the
// SHA-256 over the plain concatenation of the manifest bytes, the SKILL.md
// bytes, and each resource's path and body in ascending path order, with no
// length prefix on any part. It exists so a test can write the cache state an
// upgraded consumer inherits from the previous binary.
func unframedContentHash(artifactBytes, skillBytes []byte, resources map[string][]byte) string {
	h := sha256.New()
	h.Write(artifactBytes)
	h.Write(skillBytes)
	keys := make([]string, 0, len(resources))
	for k := range resources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write(resources[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Spec: §6.5, §6.6 — the resolution index is keyed by (id, version) rather
// than by content, so an upgraded consumer whose cache still holds a
// pre-upgrade bucket resolves that hash, hits it, and fails the step 2 check.
// Clearing the cache directory is the documented remedy.
// Matrix: §6.10 (materialize.content_hash_mismatch)
func TestOfflineFirst_PreUpgradeCacheBucketRefusesTheLoad(t *testing.T) {
	t.Parallel()
	const fm = "---\ntype: context\nversion: 1.0.0\n---\n"
	const body = "cached-body"
	resources := map[string]string{"references/notes.md": "notes\n"}
	resourceBytes := map[string][]byte{"references/notes.md": []byte("notes\n")}
	preUpgrade := "sha256:" + unframedContentHash([]byte(fm), nil, resourceBytes)

	respBody := loadArtifactJSON(t, map[string]any{
		"id": "team/x", "type": "context", "version": "1.0.0",
		"manifest_body": body, "frontmatter": fm, "resources": resources,
	})
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respBody))
	}))
	defer registry.Close()

	dir := t.TempDir()
	newServer := func(t *testing.T) (*mcpServer, *resolutionCache) {
		t.Helper()
		cache, err := newContentCache(dir)
		if err != nil {
			t.Fatalf("newContentCache: %v", err)
		}
		resolutions := newResolutionCache(dir)
		return &mcpServer{
			cfg: &config{
				cacheDir: dir, cacheMode: "offline-first", registry: registry.URL,
				harness: "none", verifyPolicy: sign.PolicyNever, resolutionTTL: 30 * time.Second,
			},
			cache:       cache,
			resolutions: resolutions,
			adapters:    adapter.DefaultRegistry(),
			http:        &http.Client{},
		}, resolutions
	}

	// A bucket and a resolution entry the previous binary would have written.
	srv, resolutions := newServer(t)
	if err := srv.cache.put(preUpgrade, fm, body, resources); err != nil {
		t.Fatalf("put: %v", err)
	}
	resolutions.PutVersion("team/x", "1.0.0", preUpgrade, time.Now())

	out := srv.loadArtifact(map[string]any{"id": "team/x", "version": "1.0.0"})
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("loadArtifact = %T (%v), want map", out, out)
	}
	if got, _ := m["error"].(string); !strings.Contains(got, "materialize.content_hash_mismatch") {
		t.Errorf("error = %v, want materialize.content_hash_mismatch", m["error"])
	}
	// The index is left in place, which is why `podium cache prune` is not the
	// remedy: the entry that resolves to the stale bucket survives it.
	if got, hit := resolutions.Resolve("team/x", "1.0.0", time.Now(), 30*time.Second, true); !hit || got != preUpgrade {
		t.Errorf("Resolve = %q hit=%v, want %q, true", got, hit, preUpgrade)
	}
	if err := resolutions.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Clearing the cache directory is the remedy: the next fetch warms a bucket
	// under the canonical hash and the load passes the §6.6 step 2 check.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	srv2, resolutions2 := newServer(t)
	defer resolutions2.Close()
	out2 := srv2.loadArtifact(map[string]any{"id": "team/x", "version": "1.0.0"})
	m2, ok := out2.(map[string]any)
	if !ok {
		t.Fatalf("loadArtifact = %T (%v), want map", out2, out2)
	}
	if _, isErr := m2["error"]; isErr {
		t.Fatalf("load after clearing the cache directory failed: %v", m2["error"])
	}
	if m2["manifest_body"] != body {
		t.Errorf("manifest_body = %v, want %q", m2["manifest_body"], body)
	}
	want := "sha256:" + version.CanonicalContentHash([]byte(fm), nil, resourceBytes)
	if m2["content_hash"] != want {
		t.Errorf("content_hash = %v, want %v", m2["content_hash"], want)
	}
}
