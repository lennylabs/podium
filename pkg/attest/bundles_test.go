package attest_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/version"
)

// bundleServer serves GET /v1/attestations/{digest} from a map, and the
// status in override when it is set.
func bundleServer(t *testing.T, bundles map[string]string, override int) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if override != 0 {
			w.WriteHeader(override)
			return
		}
		body, ok := bundles[strings.TrimPrefix(r.URL.Path, "/v1/attestations/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// Spec: §4.7.11 (Attestation check, step 8), §7.3.5 — the HTTP source
// decodes a canonical base64 bundle, reads a not-found as ErrBundleNotFound,
// refuses a body past the base64 length of the size limit plus 1 KiB as
// limits, a malformed body or another status as fetch, and a registry it
// cannot reach as network.registry_unreachable.
func TestHTTPBundleSource(t *testing.T) {
	t.Parallel()
	bundle := []byte(`{"mediaType":"x"}`)
	d := attest.AttestationDigest(bundle)
	big := strings.Repeat("A", 4*(64/3+1)+1100)
	ts := bundleServer(t, map[string]string{
		d:                                   `{"bundle":"` + base64.StdEncoding.EncodeToString(bundle) + `"}`,
		"sha256:" + strings.Repeat("1", 64): `{"bundle":"eA"}`,
		"sha256:" + strings.Repeat("2", 64): `{"bundle":1}`,
		"sha256:" + strings.Repeat("3", 64): `[]`,
		"sha256:" + strings.Repeat("4", 64): `{"bundle":"` + big + `"}`,
	}, 0)
	src := attest.HTTPBundleSource{BaseURL: ts.URL + "/", Client: ts.Client(), MaxBytes: 64,
		Prepare: func(r *http.Request) { r.Header.Set("Authorization", "Bearer tok") }}
	ctx := context.Background()
	got, err := src.Bundle(ctx, d)
	if err != nil || string(got) != string(bundle) {
		t.Fatalf("Bundle = %q, %v", got, err)
	}
	if _, err := src.Bundle(ctx, "sha256:"+strings.Repeat("9", 64)); !errors.Is(err, attest.ErrBundleNotFound) {
		t.Fatalf("absent: %v", err)
	}
	for _, n := range []string{"1", "2", "3"} {
		_, err := src.Bundle(ctx, "sha256:"+strings.Repeat(n, 64))
		wantFailure(t, err, attest.CodeAttestationInvalid, attest.ConstraintFetch)
	}
	_, err = src.Bundle(ctx, "sha256:"+strings.Repeat("4", 64))
	wantFailure(t, err, attest.CodeAttestationInvalid, attest.ConstraintLimits)

	unauth := attest.HTTPBundleSource{BaseURL: ts.URL, Client: ts.Client()}
	_, err = unauth.Bundle(ctx, d)
	wantFailure(t, err, attest.CodeAttestationInvalid, attest.ConstraintFetch)

	broken := bundleServer(t, nil, http.StatusInternalServerError)
	if _, err = (attest.HTTPBundleSource{BaseURL: broken.URL, Prepare: src.Prepare}).Bundle(ctx, d); !errors.Is(err, attest.ErrBundleUnavailable) {
		t.Fatalf("5xx: %v", err)
	}
	forbidden := bundleServer(t, nil, http.StatusForbidden)
	_, err = attest.HTTPBundleSource{BaseURL: forbidden.URL, Prepare: src.Prepare}.Bundle(ctx, d)
	wantFailure(t, err, attest.CodeAttestationInvalid, attest.ConstraintFetch)

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	_, err = attest.HTTPBundleSource{BaseURL: closed.URL}.Bundle(ctx, d)
	wantFailure(t, err, attest.CodeRegistryUnreachable, "")

	_, err = attest.HTTPBundleSource{BaseURL: "http://\x7f"}.Bundle(ctx, d)
	wantFailure(t, err, attest.CodeAttestationInvalid, attest.ConstraintFetch)
}

// Spec: §7.3.5 — a response whose base64 is not canonical is refused.
func TestDecodeBundleResponse(t *testing.T) {
	t.Parallel()
	if b, err := attest.DecodeBundleResponse([]byte(`{"bundle":"eA=="}`)); err != nil || string(b) != "x" {
		t.Fatalf("decode = %q, %v", b, err)
	}
	for _, body := range []string{`{"bundle":"eA==\n"}`, `{"bundle":"eB=="}`, `{}`, `{"bundle":null}`, `nope`} {
		_, err := attest.DecodeBundleResponse([]byte(body))
		wantFailure(t, err, attest.CodeAttestationInvalid, attest.ConstraintFetch)
	}
}

// Spec: §4.7.11 (Attestation check, step 8), §7.4 — a cached bundle is
// read without a fetch, a fetched bundle is kept, an entry with other bytes
// is ignored, and offline-only refuses a bundle that is not cached.
func TestCachedBundles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	bundle := []byte("bundle-bytes")
	d := attest.AttestationDigest(bundle)
	src := &mapSource{bundles: map[string][]byte{d: bundle}}
	dir := filepath.Join(t.TempDir(), "attestations")
	cache := attest.DirCache{Dir: dir}
	c := attest.CachedBundles{Cache: cache, Source: src}
	for range 2 {
		if b, err := c.Bundle(ctx, d); err != nil || string(b) != string(bundle) {
			t.Fatalf("Bundle = %q, %v", b, err)
		}
	}
	if src.calls.Load() != 1 {
		t.Fatalf("fetched %d times, want once", src.calls.Load())
	}
	if _, err := os.Stat(filepath.Join(dir, strings.TrimPrefix(d, "sha256:"))); err != nil {
		t.Fatalf("cache file: %v", err)
	}
	off := attest.CachedBundles{Cache: cache, OfflineOnly: true}
	if _, err := off.Bundle(ctx, d); err != nil {
		t.Fatalf("offline cached: %v", err)
	}
	other := "sha256:" + strings.Repeat("e", 64)
	_, err := off.Bundle(ctx, other)
	wantFailure(t, err, attest.CodeAttestationMissing, "")

	// A cache file whose bytes have another digest is no entry.
	if err := os.WriteFile(filepath.Join(dir, strings.Repeat("e", 64)), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Get(other); ok {
		t.Fatal("a tampered cache file was read")
	}
	if _, ok := cache.Get("sha256:../x"); ok {
		t.Fatal("a malformed digest was read")
	}
	if err := cache.Put("sha256:x", bundle); err == nil {
		t.Fatal("Put of a malformed digest")
	}
	if _, ok := (attest.DirCache{Dir: dir, MaxBytes: 2}).Get(d); ok {
		t.Fatal("a file over the bound was read")
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (attest.DirCache{Dir: filepath.Join(blocked, "sub")}).Put(d, bundle); err == nil {
		t.Fatal("Put under a file")
	}
	failing := attest.CachedBundles{Source: &mapSource{err: attest.ErrBundleNotFound}}
	if _, err := failing.Bundle(ctx, d); !errors.Is(err, attest.ErrBundleNotFound) {
		t.Fatalf("source error: %v", err)
	}
}

// Spec: §4.7.11 (Attestation check, step 8) — the memory cache keeps
// entries up to its bound and is safe for concurrent use.
func TestMemoryCache(t *testing.T) {
	t.Parallel()
	c := &attest.MemoryCache{Max: 2}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Put(string(rune('a'+i)), []byte{byte(i)})
			c.Get("a")
		}()
	}
	wg.Wait()
	_ = c.Put("z", []byte("z"))
	if b, ok := c.Get("z"); !ok || string(b) != "z" {
		t.Fatal("the last entry is missing")
	}
	def := &attest.MemoryCache{}
	_ = def.Put("k", []byte("v"))
	if _, ok := def.Get("k"); !ok {
		t.Fatal("default bound")
	}
}

// Spec: §4.7.11 (Attestation check, step 7) — a reference is read by its
// digest alone, malformed references are skipped, and repeats are dropped.
func TestReadReferences(t *testing.T) {
	t.Parallel()
	d := "sha256:" + strings.Repeat("a", 64)
	m, err := version.DecodeJSONObject([]byte(`{"attestations":[{"digest":"` + d + `","verdict":"failed"},{"digest":"` + d + `"},"x",{"digest":null}]}`))
	if err != nil {
		t.Fatal(err)
	}
	refs, err := attest.ReadReferences(m)
	if err != nil || len(refs) != 1 || refs[0] != d {
		t.Fatalf("refs = %v, %v", refs, err)
	}
	m, _ = version.DecodeJSONObject([]byte(`{"attestations":null}`))
	if refs, err := attest.ReadReferences(m); err != nil || refs != nil {
		t.Fatalf("null: %v, %v", refs, err)
	}
}

// Spec: §4.7.11 (Attestation check, step 8) — a source with no client of
// its own bounds each fetch, so a registry that does not answer does not
// hold the check.
func TestHTTPBundleSource_DefaultTimeout(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(func() { close(release); ts.Close() })
	start := time.Now()
	_, err := attest.HTTPBundleSource{BaseURL: ts.URL, Timeout: 50 * time.Millisecond}.Bundle(context.Background(), "sha256:"+strings.Repeat("a", 64))
	wantFailure(t, err, attest.CodeRegistryUnreachable, "")
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the fetch took %v", time.Since(start))
	}
	if attest.DefaultBundleFetchTimeout <= 0 {
		t.Fatal("no default timeout")
	}
}
