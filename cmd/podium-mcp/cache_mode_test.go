package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	defer func() { _ = r2.Close() }()
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

// ----- fixtures -------------------------------------------------------------

// cachedRecord returns a sealed context record for id whose content hash is the
// §4.7.6 digest of its frontmatter and resources.
func cachedRecord(id, frontmatter, body string, resources map[string]string) loadArtifactResponse {
	raw := make(map[string][]byte, len(resources))
	for k, v := range resources {
		raw[k] = []byte(v)
	}
	a := manifestFields(frontmatter)
	return sealDelivery(loadArtifactResponse{
		ID:           id,
		Type:         a["type"],
		Version:      a["version"],
		Sensitivity:  a["sensitivity"],
		Frontmatter:  frontmatter,
		ManifestBody: body,
		Resources:    resources,
		ContentHash:  "sha256:" + version.CanonicalContentHash([]byte(frontmatter), nil, raw),
	})
}

// manifestFields reads the string-valued top-level keys of a frontmatter block.
func manifestFields(frontmatter string) map[string]string {
	out := map[string]string{}
	for k, v := range manifestContext(frontmatter) {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// cacheStub serves each record's /v1/load_artifact response by the id query
// parameter and answers HEAD with the record's content hash. A GET carrying
// If-None-Match is answered 304 when notModified is set.
type cacheStub struct {
	records     map[string]loadArtifactResponse
	notModified bool
}

func (c *cacheStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec, ok := c.records[r.URL.Query().Get("id")]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"registry.not_found","message":"not found"}`))
		return
	}
	switch {
	case r.Method == http.MethodHead:
		w.Header().Set("X-Podium-Content-Hash", rec.ContentHash)
		w.WriteHeader(http.StatusOK)
	case c.notModified && r.Header.Get("If-None-Match") != "":
		w.WriteHeader(http.StatusNotModified)
	default:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rec)
	}
}

// newCacheStub starts a stub registry serving records.
func newCacheStub(t *testing.T, records ...loadArtifactResponse) (*httptest.Server, *cacheStub) {
	t.Helper()
	stub := &cacheStub{records: map[string]loadArtifactResponse{}}
	for _, r := range records {
		stub.records[r.ID] = r
	}
	ts := httptest.NewServer(stub)
	t.Cleanup(ts.Close)
	return ts, stub
}

// cacheServer returns a bridge whose content cache and resolution index both
// live under dir, so a test can inspect and tamper the files the bridge wrote.
func cacheServer(t *testing.T, dir, registry, mode string) *mcpServer {
	t.Helper()
	cache, err := newContentCache(dir)
	if err != nil {
		t.Fatalf("newContentCache: %v", err)
	}
	resolutions := newResolutionCache(dir)
	t.Cleanup(func() { _ = resolutions.Close() })
	return &mcpServer{
		cfg: &config{
			cacheDir: dir, cacheMode: mode, registry: registry,
			harness: "none", verifyPolicy: sign.PolicyNever, resolutionTTL: 30 * time.Second,
		},
		cache:       cache,
		resolutions: resolutions,
		adapters:    adapter.DefaultRegistry(),
		http:        &http.Client{},
	}
}

// deliveryDir returns the per-ID delivery directory of rec in the cache at dir.
func deliveryDir(dir string, rec loadArtifactResponse) string {
	return filepath.Join(dir, sanitizeHash(rec.ContentHash), "delivery", deliverySegment(rec.ID))
}

// wantServed fails unless out is a successful load result carrying body.
func wantServed(t *testing.T, out any, body string) {
	t.Helper()
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("load = %T (%v), want map", out, out)
	}
	if _, isErr := m["error"]; isErr {
		t.Fatalf("load failed: %v", m["error"])
	}
	if m["manifest_body"] != body {
		t.Errorf("manifest_body = %v, want %q", m["manifest_body"], body)
	}
}

// wantRefused fails unless out is an error result whose message leads with code.
func wantRefused(t *testing.T, out any, code string) {
	t.Helper()
	if got := errorMessageText(out); !strings.HasPrefix(got, code) {
		t.Errorf("error = %q, want a leading %s", got, code)
	}
}

// primeLive loads rec live through a bridge in offline-first mode, which is a
// miss on an empty cache, so the verified record lands in the cache and the
// resolution index the way a production load writes them.
func primeLive(t *testing.T, s *mcpServer, rec loadArtifactResponse, args map[string]any) {
	t.Helper()
	mode := s.cfg.cacheMode
	s.cfg.cacheMode = "offline-first"
	defer func() { s.cfg.cacheMode = mode }()
	wantServed(t, s.loadArtifact(args), rec.ManifestBody)
}

// ----- the cache-served record ---------------------------------------------

// Spec: §6.5 — loadArtifactFromCache reads the per-ID delivery files and the
// bucket-level resources putDelivery and put wrote.
func TestLoadArtifactFromCache_RecoversBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := cacheServer(t, dir, "", "")
	rec := cachedRecord("team/x", "---\ntype: context\nversion: 1.0.0\n---\nbody\n", "body\n",
		map[string]string{"scripts/run.py": "print('x')\n"})
	if err := s.cacheVerifiedRecord(rec); err != nil {
		t.Fatalf("cacheVerifiedRecord: %v", err)
	}
	got, err := s.loadArtifactFromCache(rec.ContentHash, "team/x")
	if err != nil {
		t.Fatalf("loadArtifactFromCache: %v", err)
	}
	if got.ID != "team/x" || got.Version != "1.0.0" || got.Type != "context" {
		t.Errorf("identity = %q %q %q, want team/x 1.0.0 context", got.ID, got.Version, got.Type)
	}
	if got.Frontmatter != rec.Frontmatter || got.ManifestBody != rec.ManifestBody {
		t.Errorf("document = %q / %q, want the served values", got.Frontmatter, got.ManifestBody)
	}
	if got.Resources["scripts/run.py"] != "print('x')\n" {
		t.Errorf("Resources = %+v", got.Resources)
	}
	if err := verifyDeliveryHash(*got); err != nil {
		t.Errorf("verifyDeliveryHash on the cache-served record: %v", err)
	}
}

// Spec: §4.3.4, §6.5, §4.7.10 — the delivery record frames the verbatim
// SKILL.md, so the cache persists skill_raw and a cache-served skill
// recomputes the delivery hash a live fetch does and materializes the authored
// SKILL.md byte for byte.
func TestLoadArtifactFromCache_SkillRawRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := cacheServer(t, dir, "", "")
	frontmatter := "---\ntype: skill\nversion: 0.1.0\nsensitivity: high\n---\n\n<!-- body in SKILL.md -->\n"
	skillRaw := "---\nname: runbook\ndescription: A runbook\n---\n\n# runbook\n\nbody\n"
	rec := sealDelivery(loadArtifactResponse{
		ID: "team/runbook", Type: "skill", Version: "0.1.0", Sensitivity: "high",
		Frontmatter: frontmatter, ManifestBody: "\n# runbook\n\nbody\n", SkillRaw: skillRaw,
		ContentHash: "sha256:" + version.CanonicalContentHash([]byte(frontmatter), []byte(skillRaw), nil),
	})
	if err := s.cacheVerifiedRecord(rec); err != nil {
		t.Fatalf("cacheVerifiedRecord: %v", err)
	}
	got, err := s.loadArtifactFromCache(rec.ContentHash, "team/runbook")
	if err != nil {
		t.Fatalf("loadArtifactFromCache: %v", err)
	}
	if got.SkillRaw != skillRaw {
		t.Errorf("SkillRaw not restored from cache:\n got %q\nwant %q", got.SkillRaw, skillRaw)
	}
	if err := verifyDeliveryHash(*got); err != nil {
		t.Errorf("verifyDeliveryHash on cache-served skill: %v", err)
	}
	if md := synthesizeSkillMD(*got); md != skillRaw {
		t.Errorf("synthesizeSkillMD = %q, want authored skill_raw %q", md, skillRaw)
	}
}

// Spec: §6.6 step 2, §4.7.10 — without the persisted skill_raw the record
// frames an empty SKILL.md and the cache-served skill is rejected, so the
// side file is required for a skill to load from cache.
func TestVerifyDeliveryHash_CacheServedSkillWithoutSkillRawFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := cacheServer(t, dir, "", "")
	frontmatter := "---\ntype: skill\nversion: 0.1.0\n---\n\nbody\n"
	skillRaw := "---\nname: x\ndescription: x\n---\n\n# x\n"
	rec := sealDelivery(loadArtifactResponse{
		ID: "x", Type: "skill", Version: "0.1.0", Frontmatter: frontmatter,
		ManifestBody: "\n# x\n", SkillRaw: skillRaw,
		ContentHash: "sha256:" + version.CanonicalContentHash([]byte(frontmatter), []byte(skillRaw), nil),
	})
	if err := s.cache.put(rec.ContentHash, frontmatter, rec.ManifestBody, nil); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := s.cache.putDelivery(rec.ContentHash, rec.ID, deliveryFiles{
		Frontmatter: rec.Frontmatter, Body: rec.ManifestBody, DeliveryHash: rec.DeliveryHash,
	}); err != nil {
		t.Fatalf("putDelivery: %v", err)
	}
	got, err := s.loadArtifactFromCache(rec.ContentHash, "x")
	if err != nil {
		t.Fatalf("loadArtifactFromCache: %v", err)
	}
	if err := verifyDeliveryHash(*got); err == nil || !strings.Contains(err.Error(), "content_hash_mismatch") {
		t.Errorf("err = %v, want content_hash_mismatch for a skill cached without skill_raw", err)
	}
}

// Spec: §4.7.9, §6.6 — a signed high-sensitivity skill served from cache
// verifies under always, because the cache persists the delivery pair the
// live response carried and the recomputation reproduces the signed hash.
func TestEnforceSignaturePolicy_CacheServedSignedSkill(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	s := cacheServer(t, dir, "", "")
	s.cfg.verifyPolicy = sign.PolicyAlways
	s.cfg.signatureProvider = "registry-managed"
	s.cfg.verifier = sign.RegistryManagedKey{PublicKey: pub}

	frontmatter := "---\ntype: skill\nversion: 0.1.0\nsensitivity: high\n---\n\nbody\n"
	skillRaw := "---\nname: signed\ndescription: signed\n---\n\n# signed\n"
	rec := sealDelivery(loadArtifactResponse{
		ID: "signed", Type: "skill", Version: "0.1.0", Sensitivity: "high",
		Frontmatter: frontmatter, ManifestBody: "\n# signed\n", SkillRaw: skillRaw,
		ContentHash: "sha256:" + version.CanonicalContentHash([]byte(frontmatter), []byte(skillRaw), nil),
	})
	rec.DeliverySignature, err = sign.RegistryManagedKey{PrivateKey: priv, PublicKey: pub}.Sign(context.Background(), rec.DeliveryHash)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := s.cacheVerifiedRecord(rec); err != nil {
		t.Fatalf("cacheVerifiedRecord: %v", err)
	}
	got, err := s.loadArtifactFromCache(rec.ContentHash, "signed")
	if err != nil {
		t.Fatalf("loadArtifactFromCache: %v", err)
	}
	if got.Sensitivity != "high" || got.DeliverySignature != rec.DeliverySignature {
		t.Errorf("cache-served sensitivity %q signature %q, want the served values", got.Sensitivity, got.DeliverySignature)
	}
	if err := s.verifyServedArtifact(got, deliverOpts{}); err != nil {
		t.Errorf("verifyServedArtifact on cache-served signed skill: %v", err)
	}
}

// Spec: §4.7.9, §6.6 — an artifact served from cache without a delivery
// signature is refused under always, not waved through, and the cache-served
// response reports the sensitivity the live response carried.
func TestEnforceSignaturePolicy_CacheServedHighSensitivityUnsignedRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := cacheServer(t, dir, "", "")
	s.cfg.verifyPolicy = sign.PolicyAlways
	s.cfg.signatureProvider = "noop"
	rec := cachedRecord("x", "---\ntype: context\nversion: 1.0.0\nsensitivity: high\n---\n\nbody\n", "\nbody\n", nil)
	if err := s.cacheVerifiedRecord(rec); err != nil {
		t.Fatalf("cacheVerifiedRecord: %v", err)
	}
	got, err := s.loadArtifactFromCache(rec.ContentHash, "x")
	if err != nil {
		t.Fatalf("loadArtifactFromCache: %v", err)
	}
	if got.Sensitivity != "high" {
		t.Fatalf("Sensitivity = %q, want high from the per-ID file", got.Sensitivity)
	}
	if err := s.verifyServedArtifact(got, deliverOpts{}); err == nil || !strings.HasPrefix(err.Error(), "materialize.signature_missing") {
		t.Errorf("err = %v, want materialize.signature_missing", err)
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

// ----- TEST-11: cache-served loads re-verify the per-ID delivery record -----

// Spec: §4.7.10, §6.5, §6.6, §7.4 — a bucket without per-ID delivery files, the
// layout a consumer written before the delivery record inherits, is a miss:
// offline-only returns the offline cache-miss error and offline-first fetches
// live and serves the verified record.
func TestOfflineFirst_PreUpgradeCacheBucketIsAMiss(t *testing.T) {
	t.Parallel()
	rec := cachedRecord("team/x", "---\ntype: context\nversion: 1.0.0\n---\n", "cached-body",
		map[string]string{"references/notes.md": "notes\n"})
	registry, _ := newCacheStub(t, rec)
	dir := t.TempDir()
	s := cacheServer(t, dir, registry.URL, "offline-only")
	if err := s.cache.put(rec.ContentHash, rec.Frontmatter, rec.ManifestBody, rec.Resources); err != nil {
		t.Fatalf("put: %v", err)
	}
	s.resolutions.PutVersion("team/x", "1.0.0", rec.ContentHash, time.Now())
	args := map[string]any{"id": "team/x", "version": "1.0.0"}

	wantRefused(t, s.loadArtifact(args), "network.offline_cache_miss")

	s.cfg.cacheMode = "offline-first"
	out := s.loadArtifact(args)
	wantServed(t, out, "cached-body")
	if m := out.(map[string]any); m["content_hash"] != rec.ContentHash {
		t.Errorf("content_hash = %v, want %v", m["content_hash"], rec.ContentHash)
	}
	if _, err := os.Stat(filepath.Join(deliveryDir(dir, rec), "delivery_hash")); err != nil {
		t.Errorf("the live load did not write the per-ID delivery files: %v", err)
	}
}

// Spec: §4.7.10, §6.5, §6.6, §7.4 — an offline-only load re-runs the delivery
// check and the policy against the per-ID files a live load wrote and serves
// the record.
func TestOfflineOnly_CacheHitReverifiesThePerIDRecord(t *testing.T) {
	t.Parallel()
	rec := cachedRecord("team/x", "---\ntype: context\nversion: 1.0.0\n---\nbody\n", "body\n", nil)
	registry, _ := newCacheStub(t, rec)
	dir := t.TempDir()
	s := cacheServer(t, dir, registry.URL, "offline-only")
	loads := []map[string]any{{"id": "team/x", "version": "1.0.0"}, {"id": "team/x"}}
	for _, args := range loads {
		primeLive(t, s, rec, args)
	}
	registry.Close()
	for _, args := range loads {
		wantServed(t, s.loadArtifact(args), "body\n")
	}
}

// Spec: §4.7.10, §6.5 — two IDs over byte-identical packages share one bucket,
// and each loads cache-served without reading the other's delivery record.
// a/b_c and a_b/c collide under a separator-replacing segment mapping.
func TestOfflineOnly_SharedBucketKeepsEachIDsRecord(t *testing.T) {
	t.Parallel()
	const fm = "---\ntype: context\nversion: 1.0.0\n---\nshared\n"
	first := cachedRecord("a/b_c", fm, "shared\n", nil)
	second := cachedRecord("a_b/c", fm, "shared\n", nil)
	if first.ContentHash != second.ContentHash {
		t.Fatal("fixture: the two packages must share a content hash")
	}
	registry, _ := newCacheStub(t, first, second)
	s := cacheServer(t, t.TempDir(), registry.URL, "offline-only")
	for _, rec := range []loadArtifactResponse{first, second} {
		primeLive(t, s, rec, map[string]any{"id": rec.ID, "version": "1.0.0"})
	}
	for _, rec := range []loadArtifactResponse{first, second} {
		out := s.loadArtifact(map[string]any{"id": rec.ID, "version": "1.0.0"})
		wantServed(t, out, "shared\n")
		if id := out.(map[string]any)["id"]; id != rec.ID {
			t.Errorf("served id = %v, want %s", id, rec.ID)
		}
	}
}

// Spec: §4.7.10, §6.5 — two children with byte-identical authored packages
// that pinned different parent versions share a bucket and are served
// different merged documents. Each loads cache-served and materializes its own
// merged document, which a reader of the bucket-level frontmatter would not.
func TestOfflineOnly_DifferingPinsServeEachChildsMergedDocument(t *testing.T) {
	t.Parallel()
	authored := "---\ntype: context\nversion: 1.0.0\nextends: shared/base@1.x\n---\nchild\n"
	hash := "sha256:" + version.CanonicalContentHash([]byte(authored), nil, nil)
	child := func(id, description string) loadArtifactResponse {
		return sealDelivery(loadArtifactResponse{
			ID: id, Type: "context", Version: "1.0.0", ContentHash: hash,
			Frontmatter:  "---\ntype: context\nversion: 1.0.0\ndescription: " + description + "\n---\nchild\n",
			ManifestBody: "child\n",
		})
	}
	first, second := child("team/one", "from base 1.0.0"), child("team/two", "from base 1.1.0")
	registry, _ := newCacheStub(t, first, second)
	s := cacheServer(t, t.TempDir(), registry.URL, "offline-only")
	primeLive(t, s, first, map[string]any{"id": first.ID, "version": "1.0.0"})
	primeLive(t, s, second, map[string]any{"id": second.ID, "version": "1.0.0"})

	dest := t.TempDir()
	out := s.loadArtifact(map[string]any{"id": first.ID, "version": "1.0.0", "destination": dest})
	wantServed(t, out, "child\n")
	got, err := os.ReadFile(filepath.Join(dest, "team/one", "ARTIFACT.md"))
	if err != nil {
		t.Fatalf("read materialized: %v", err)
	}
	if string(got) != first.Frontmatter {
		t.Errorf("materialized %q, want team/one's own merged document %q", got, first.Frontmatter)
	}
}

// Spec: §4.7.10, §6.5 — the delivery segment is one fixed-length path
// component of lowercase hex for every canonical ID, and separator-colliding
// IDs map to distinct segments.
func TestDeliverySegment_FixedLengthSinglePathComponent(t *testing.T) {
	t.Parallel()
	for _, pair := range [][2]string{{"a/b_c", "a_b/c"}, {"x/..", "x/_"}, {"x/y", "x_y"}} {
		if deliverySegment(pair[0]) == deliverySegment(pair[1]) {
			t.Errorf("deliverySegment(%q) == deliverySegment(%q)", pair[0], pair[1])
		}
	}
	for _, id := range []string{".", "..", `a\b`, "a/b_c", strings.Repeat("x", 1000)} {
		seg := deliverySegment(id)
		if len(seg) != 64 || strings.Trim(seg, "0123456789abcdef") != "" {
			t.Errorf("deliverySegment(%q) = %q, want 64 bytes of [0-9a-f]", id, seg)
		}
	}
}

// Spec: §4.7.10, §6.5 — an artifact whose canonical ID exceeds the 255-byte
// path-component limit loads live and then cache-served.
func TestOfflineOnly_LongIDLoadsLiveAndCacheServed(t *testing.T) {
	t.Parallel()
	id := "team/" + strings.Repeat("segment-", 40)
	if len(id) <= 255 {
		t.Fatal("fixture: the ID must exceed 255 bytes")
	}
	rec := cachedRecord(id, "---\ntype: context\nversion: 1.0.0\n---\nlong\n", "long\n", nil)
	registry, _ := newCacheStub(t, rec)
	s := cacheServer(t, t.TempDir(), registry.URL, "offline-only")
	args := map[string]any{"id": id, "version": "1.0.0"}
	primeLive(t, s, rec, args)
	wantServed(t, s.loadArtifact(args), "long\n")
}

// Spec: §4.7.10, §6.5 — a per-ID directory whose id file names a different
// artifact is a miss, so a reader never serves another ID's document.
func TestOfflineOnly_ForeignIDFileIsAMiss(t *testing.T) {
	t.Parallel()
	rec := cachedRecord("team/x", "---\ntype: context\nversion: 1.0.0\n---\nbody\n", "body\n", nil)
	registry, _ := newCacheStub(t, rec)
	dir := t.TempDir()
	s := cacheServer(t, dir, registry.URL, "offline-only")
	args := map[string]any{"id": "team/x", "version": "1.0.0"}
	primeLive(t, s, rec, args)
	if err := os.WriteFile(filepath.Join(deliveryDir(dir, rec), "id"), []byte("team/other"), 0o644); err != nil {
		t.Fatalf("rewrite id file: %v", err)
	}
	wantRefused(t, s.loadArtifact(args), "network.offline_cache_miss")
}

// Spec: §4.7.10, §6.6 — a per-ID sensitivity file rewritten from high to low
// fails the delivery check on the cache-served load, because the record frames
// the served sensitivity rather than a value parsed from the frontmatter.
func TestOfflineOnly_RewrittenSensitivityFails(t *testing.T) {
	t.Parallel()
	rec := cachedRecord("team/x", "---\ntype: context\nversion: 1.0.0\nsensitivity: high\n---\nbody\n", "body\n", nil)
	registry, _ := newCacheStub(t, rec)
	dir := t.TempDir()
	s := cacheServer(t, dir, registry.URL, "offline-only")
	args := map[string]any{"id": "team/x", "version": "1.0.0"}
	primeLive(t, s, rec, args)
	if err := os.WriteFile(filepath.Join(deliveryDir(dir, rec), "sensitivity"), []byte("low"), 0o644); err != nil {
		t.Fatalf("rewrite sensitivity: %v", err)
	}
	wantRefused(t, s.loadArtifact(args), "materialize.content_hash_mismatch")
}

// Spec: §4.7.10, §6.6 — a per-ID body file rewritten while the per-ID document
// is unchanged fails the delivery check, because the record frames the served
// body rather than one re-derived from the cached document.
func TestOfflineOnly_RewrittenBodyFails(t *testing.T) {
	t.Parallel()
	rec := cachedRecord("team/x", "---\ntype: context\nversion: 1.0.0\n---\nbody\n", "body\n", nil)
	registry, _ := newCacheStub(t, rec)
	dir := t.TempDir()
	s := cacheServer(t, dir, registry.URL, "offline-only")
	args := map[string]any{"id": "team/x", "version": "1.0.0"}
	primeLive(t, s, rec, args)
	if err := os.WriteFile(filepath.Join(deliveryDir(dir, rec), "body"), []byte("forged\n"), 0o644); err != nil {
		t.Fatalf("rewrite body: %v", err)
	}
	wantRefused(t, s.loadArtifact(args), "materialize.content_hash_mismatch")
}

// latestFetchedAt returns the (id, "latest") entry's fetch time.
func latestFetchedAt(t *testing.T, s *mcpServer, id string) time.Time {
	t.Helper()
	e, ok := s.resolutions.getEntry(resolutionKey(id, ""))
	if !ok {
		t.Fatalf("no latest entry for %s", id)
	}
	return e.FetchedAt
}

// Spec: §6.5, §6.6 — a revalidated `latest` load restarts the TTL window only
// after the cached record passes verification: a HEAD match or a 304 whose
// cached record fails leaves the fetch time unchanged, and a verified one
// advances it.
func TestRevalidatedLatest_RefreshesOnlyAfterVerification(t *testing.T) {
	t.Parallel()
	for _, path := range []struct {
		name        string
		mode        string
		notModified bool
	}{
		{name: "HEAD match", mode: "always-revalidate"},
		{name: "304", mode: "", notModified: true},
	} {
		for _, tamper := range []bool{true, false} {
			t.Run(path.name+map[bool]string{true: " refused", false: " verified"}[tamper], func(t *testing.T) {
				t.Parallel()
				rec := cachedRecord("team/x", "---\ntype: context\nversion: 1.0.0\n---\nbody\n", "body\n", nil)
				registry, stub := newCacheStub(t, rec)
				dir := t.TempDir()
				s := cacheServer(t, dir, registry.URL, path.mode)
				args := map[string]any{"id": "team/x"}
				primeLive(t, s, rec, args)
				stub.notModified = path.notModified
				past := time.Now().Add(-time.Hour).Truncate(time.Second)
				s.resolutions.PutLatest("team/x", "1.0.0", rec.ContentHash, past)
				if tamper {
					fm := filepath.Join(deliveryDir(dir, rec), "frontmatter")
					if err := os.WriteFile(fm, []byte(rec.Frontmatter+"tampered\n"), 0o644); err != nil {
						t.Fatalf("tamper: %v", err)
					}
				}
				out := s.loadArtifact(args)
				got := latestFetchedAt(t, s, "team/x")
				if tamper {
					wantRefused(t, out, "materialize.content_hash_mismatch")
					if !got.Equal(past) {
						t.Errorf("fetch time = %s, want unchanged %s after a refused load", got, past)
					}
					return
				}
				wantServed(t, out, "body\n")
				if !got.After(past) {
					t.Errorf("fetch time = %s, want later than %s after a verified load", got, past)
				}
			})
		}
	}
}

// Spec: §6.5 — a verified record the cache cannot store fails the load with
// the cache error and records no resolution entry, whether the bucket, the
// SKILL.md side file, or the per-ID delivery directory cannot be written.
func TestDeliverLoadArtifact_CacheWriteFailureRecordsNothing(t *testing.T) {
	t.Parallel()
	fm := "---\ntype: skill\nversion: 1.0.0\n---\n"
	skillRaw := "---\nname: s\ndescription: s\n---\n\nprose\n"
	rec := sealDelivery(loadArtifactResponse{
		ID: "team/s", Type: "skill", Version: "1.0.0", Frontmatter: fm, ManifestBody: "\nprose\n", SkillRaw: skillRaw,
		ContentHash: "sha256:" + version.CanonicalContentHash([]byte(fm), []byte(skillRaw), nil),
	})
	for name, block := range map[string]string{
		"bucket":    "",
		"skill_raw": "skill_raw",
		"delivery":  "delivery",
		"id file":   "id file",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			bucket := filepath.Join(dir, sanitizeHash(rec.ContentHash))
			switch block {
			case "":
				if err := os.WriteFile(bucket, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			case "id file":
				// A directory where the per-ID id file belongs blocks it.
				if err := os.MkdirAll(filepath.Join(bucket, "delivery", deliverySegment(rec.ID), "id", "occupied"), 0o755); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.MkdirAll(filepath.Join(bucket, block, "occupied"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if block == "delivery" {
				// A file where the per-ID directory belongs blocks it.
				if err := os.WriteFile(filepath.Join(bucket, "delivery", deliverySegment(rec.ID)), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			s := cacheServer(t, dir, "", "")
			out := s.deliverLoadArtifact(rec, deliverOpts{resolution: &resolutionWrite{ID: rec.ID, Version: "1.0.0", Now: time.Now()}})
			wantRefused(t, out, "cache: ")
			if _, ok := s.resolutions.getEntry(resolutionKey(rec.ID, "1.0.0")); ok {
				t.Error("a failed cache write recorded a resolution entry")
			}
		})
	}
}

// Spec: §6.5 — a disabled content cache stores nothing and fails nothing.
func TestCacheVerifiedRecord_DisabledCacheIsANoOp(t *testing.T) {
	t.Parallel()
	cache, err := newContentCache("")
	if err != nil {
		t.Fatal(err)
	}
	rec := cachedRecord("team/x", "---\ntype: context\nversion: 1.0.0\n---\nbody\n", "body\n", nil)
	if err := (&mcpServer{cache: cache}).cacheVerifiedRecord(rec); err != nil {
		t.Errorf("cacheVerifiedRecord on a disabled cache: %v", err)
	}
}
