package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// spec: §12 — "ETag caching of immutable artifact versions." The MCP client
// sends the cached content-hash ETag as If-None-Match on load_artifact; a 304
// is served from the content-addressed cache without re-downloading the
// manifest body. When HEAD revalidation (§6.5) is unavailable, the
// conditional GET is the revalidation round-trip.
func TestLoadArtifact_SendsIfNoneMatchAndServes304FromCache(t *testing.T) {
	t.Parallel()
	const fm = "---\ntype: context\nversion: 1.0.0\n---\n"
	hash := "sha256:" + version.CanonicalContentHash([]byte(fm), nil, nil)
	cached := sealDelivery(loadArtifactResponse{
		ID: "team/x", Type: "context", Version: "1.0.0", ContentHash: hash,
		Frontmatter: fm, ManifestBody: "cached-body",
	})

	var sawIfNoneMatch atomic.Value
	sawIfNoneMatch.Store("")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			// HEAD revalidation unavailable, so the bridge falls through to
			// the conditional GET below.
			w.WriteHeader(http.StatusMethodNotAllowed)
		case http.MethodGet:
			sawIfNoneMatch.Store(r.Header.Get("If-None-Match"))
			w.Header().Set("ETag", `"`+hash+`"`)
			w.WriteHeader(http.StatusNotModified)
		}
	}))
	t.Cleanup(ts.Close)

	dir := t.TempDir()
	cache, _ := newContentCache(dir)
	resolutions := newResolutionCache(dir)
	t.Cleanup(func() { _ = resolutions.Close() })
	resolutions.PutVersion("team/x", "1.0.0", hash, time.Now())

	srv := &mcpServer{
		cfg:         &config{cacheDir: dir, cacheMode: "always-revalidate", registry: ts.URL, harness: "none", verifyPolicy: sign.PolicyNever},
		cache:       cache,
		resolutions: resolutions,
		adapters:    adapter.DefaultRegistry(),
		http:        &http.Client{},
	}
	// The 304 serves the cached body from the per-ID delivery files.
	if err := srv.cacheVerifiedRecord(cached); err != nil {
		t.Fatalf("cacheVerifiedRecord: %v", err)
	}
	out := srv.loadArtifact(map[string]any{"id": "team/x", "version": "1.0.0"})
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("loadArtifact returned %T (%v), want map", out, out)
	}
	if _, isErr := m["error"]; isErr {
		t.Fatalf("unexpected error result: %v", m)
	}
	if got := sawIfNoneMatch.Load().(string); got != `"`+hash+`"` {
		t.Errorf("If-None-Match = %q, want %q", got, `"`+hash+`"`)
	}
	if m["manifest_body"] != "cached-body" {
		t.Errorf("manifest_body = %v, want cached-body (served from cache): %v", m["manifest_body"], m)
	}
}

// On a 200 (changed artifact), the bridge fetches the fresh body rather than
// serving the stale cache entry.
func TestLoadArtifact_ConditionalGET200ServesFreshBody(t *testing.T) {
	t.Parallel()
	const cachedFM = "---\ntype: context\nversion: 1.0.0\n---\n"
	cachedHash := "sha256:" + version.CanonicalContentHash([]byte(cachedFM), nil, nil)
	const freshFM = "---\ntype: context\nversion: 2.0.0\n---\n"
	freshHash := "sha256:" + version.CanonicalContentHash([]byte(freshFM), nil, nil)
	respBody := loadArtifactJSON(t, map[string]any{
		"id": "team/x", "type": "context", "version": "2.0.0",
		"content_hash": freshHash, "manifest_body": "fresh-body", "frontmatter": freshFM,
	})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			// HEAD reports a changed hash so the §6.5 path does not serve the
			// stale cache and falls through to the conditional GET.
			w.Header().Set("X-Podium-Content-Hash", freshHash)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			w.Header().Set("ETag", `"`+freshHash+`"`)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(respBody))
		}
	}))
	t.Cleanup(ts.Close)

	dir := t.TempDir()
	cache, _ := newContentCache(dir)
	if err := cache.put(cachedHash, cachedFM, "stale-body", nil); err != nil {
		t.Fatalf("put: %v", err)
	}
	resolutions := newResolutionCache(dir)
	t.Cleanup(func() { _ = resolutions.Close() })
	resolutions.PutVersion("team/x", "1.0.0", cachedHash, time.Now())

	srv := &mcpServer{
		cfg:         &config{cacheDir: dir, cacheMode: "always-revalidate", registry: ts.URL, harness: "none", verifyPolicy: sign.PolicyNever},
		cache:       cache,
		resolutions: resolutions,
		adapters:    adapter.DefaultRegistry(),
		http:        &http.Client{},
	}
	out := srv.loadArtifact(map[string]any{"id": "team/x", "version": "1.0.0"})
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("loadArtifact returned %T (%v), want map", out, out)
	}
	if _, isErr := m["error"]; isErr {
		t.Fatalf("unexpected error result: %v", m)
	}
	if m["manifest_body"] != "fresh-body" {
		t.Errorf("manifest_body = %v, want fresh-body: %v", m["manifest_body"], m)
	}
}

// ----- cached-signature recovery (§6.5 cache-miss rule) ---------------------

// rotationStub is a registry that answers HEAD with a configured status and
// content hash, a conditional GET with 304, and an unconditional GET with
// fresh. abortGET makes every GET fail at the transport, the way a registry
// that went away after the HEAD does, and rejectGET answers every GET with a
// structured 403. It counts each request kind.
type rotationStub struct {
	hash       string
	headStatus int
	fresh      loadArtifactResponse
	abortGET   bool
	rejectGET  bool

	heads, condGets, uncondGets atomic.Int32
}

func (r *rotationStub) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch {
	case req.Method == http.MethodHead:
		r.heads.Add(1)
		w.Header().Set("X-Podium-Content-Hash", r.hash)
		w.WriteHeader(r.headStatus)
	case r.abortGET:
		panic(http.ErrAbortHandler)
	case r.rejectGET:
		r.uncondGets.Add(1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"auth.untrusted_runtime","message":"runtime not trusted"}`))
	case req.Header.Get("If-None-Match") != "":
		r.condGets.Add(1)
		w.WriteHeader(http.StatusNotModified)
	default:
		r.uncondGets.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(r.fresh)
	}
}

// requests returns the number of requests the stub answered.
func (r *rotationStub) requests() int32 {
	return r.heads.Load() + r.condGets.Load() + r.uncondGets.Load()
}

// rotationFixture holds a bridge that trusts key B alone, a cache seeded with
// a delivery pair signed under key A, and the record signed under B and under
// C, a key outside the bridge's set.
type rotationFixture struct {
	srv              *mcpServer
	cached           loadArtifactResponse
	underB           loadArtifactResponse
	underC           loadArtifactResponse
	keyA, keyB, keyC sign.RegistryManagedKey
}

// args is the load_artifact call every rotation case makes.
var rotationArgs = map[string]any{"id": "team/x", "version": "1.0.0"}

// newRotationFixture builds the fixture against registry in mode.
func newRotationFixture(t *testing.T, registry, mode string) *rotationFixture {
	t.Helper()
	gen := func() sign.RegistryManagedKey {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}
		return sign.RegistryManagedKey{PrivateKey: priv, PublicKey: pub}
	}
	f := &rotationFixture{keyA: gen(), keyB: gen(), keyC: gen()}
	rec := cachedRecord("team/x", "---\ntype: context\nversion: 1.0.0\n---\nbody\n", "cached-body", nil)
	f.cached = signDelivery(t, f.keyA, rec)
	f.underB = signDelivery(t, f.keyB, rec)
	f.underC = signDelivery(t, f.keyC, rec)

	f.srv = cacheServer(t, t.TempDir(), registry, mode)
	f.srv.cfg.verifyPolicy = sign.PolicyAlways
	f.srv.cfg.signatureProvider = "registry-managed"
	f.srv.cfg.verifier = sign.RegistryManagedKey{Trusted: []ed25519.PublicKey{f.keyB.PublicKey}}
	// The pair a bridge that still trusted A cached before the rotation.
	if err := f.srv.cacheVerifiedRecord(f.cached); err != nil {
		t.Fatalf("cacheVerifiedRecord: %v", err)
	}
	f.srv.resolutions.PutVersion("team/x", "1.0.0", rec.ContentHash, time.Now())
	return f
}

// rotationSetup starts a stub registry whose HEAD answers headStatus with the
// cached content hash and whose unconditional GET serves the record signed
// under B, and builds the fixture against it in mode.
func rotationSetup(t *testing.T, mode string, headStatus int) (*rotationFixture, *rotationStub, *httptest.Server) {
	t.Helper()
	stub := &rotationStub{headStatus: headStatus}
	ts := httptest.NewServer(stub)
	t.Cleanup(ts.Close)
	f := newRotationFixture(t, ts.URL, mode)
	stub.hash = f.cached.ContentHash
	stub.fresh = f.underB
	return f, stub, ts
}

// wantNotDelivered fails when out answers loaded or carries the cached body.
func wantNotDelivered(t *testing.T, out any) {
	t.Helper()
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("load = %T (%v), want map", out, out)
	}
	if _, has := m["manifest_body"]; has {
		t.Errorf("the failing cached record was delivered: %v", m)
	}
	if m["status"] == "loaded" {
		t.Errorf("status = loaded, want no delivery: %v", m)
	}
}

// Spec: §4.7.9, §6.5 — on the conditional-request path, a 304 over a cached
// delivery pair whose signature fails under the current key set is a cache
// miss: one unconditional GET replaces the record, and the load succeeds.
func TestLoadArtifact_304WithRetiredKeySignatureRefetches(t *testing.T) {
	t.Parallel()
	f, stub, _ := rotationSetup(t, "always-revalidate", http.StatusMethodNotAllowed)

	wantServed(t, f.srv.loadArtifact(rotationArgs), "cached-body")
	if got := stub.condGets.Load(); got != 1 {
		t.Errorf("conditional GETs = %d, want 1", got)
	}
	if got := stub.uncondGets.Load(); got != 1 {
		t.Errorf("unconditional GETs = %d, want 1", got)
	}
}
