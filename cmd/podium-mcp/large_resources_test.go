package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// Spec: §6.6 step 1 — large_resources are fetched via presigned
// URL and merged into the inline Resources map before
// materialization runs.
func TestFetchLargeResources_MergesIntoResources(t *testing.T) {
	t.Parallel()
	body := []byte("BIG BLOB BYTES")
	sum := sha256.Sum256(body)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(ts.Close)

	srv := &mcpServer{cfg: &config{}, http: &http.Client{}}
	resp := loadArtifactResponse{
		ID:        "team/x",
		Resources: map[string]string{},
		LargeResources: map[string]largeResourceLink{
			"data/big.bin": {URL: ts.URL, ContentHash: hash},
		},
	}
	if err := srv.fetchLargeResources(&resp, nil); err != nil {
		t.Fatalf("fetchLargeResources: %v", err)
	}
	if got := resp.Resources["data/big.bin"]; got != string(body) {
		t.Errorf("Resources[data/big.bin] = %q, want %q", got, body)
	}
}

// Spec: §6.6 step 1, §4.7.10 — a content_hash mismatch aborts the fetch with
// version.ErrLinkedHashMismatch, which verifyServedArtifact reports as
// materialize.content_hash_mismatch, so a tampered or stale presigned URL
// cannot place altered bytes on the host.
func TestFetchLargeResources_HashMismatchAborts(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("tampered"))
	}))
	t.Cleanup(ts.Close)

	srv := &mcpServer{cfg: &config{}, http: &http.Client{}}
	resp := loadArtifactResponse{
		LargeResources: map[string]largeResourceLink{
			"x.bin": {URL: ts.URL, ContentHash: "sha256:" + strings.Repeat("a", 64)},
		},
	}
	err := srv.fetchLargeResources(&resp, nil)
	if !errors.Is(err, version.ErrLinkedHashMismatch) {
		t.Errorf("err = %v, want version.ErrLinkedHashMismatch", err)
	}
}

// Spec: §6.6 step 1 — 403 / 5xx / network errors retry up to
// three times. Two 403s followed by a 200 succeeds; three 403s
// fail.
func TestFetchOneLargeResource_RetriesOnTransientFailure(t *testing.T) {
	t.Parallel()
	hits := atomic.Int64{}
	body := []byte("ok")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := hits.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(ts.Close)
	srv := &mcpServer{cfg: &config{}, http: &http.Client{}}
	got, err := srv.fetchOneLargeResource("data/big.bin", largeResourceLink{URL: ts.URL}, nil)
	if err != nil {
		t.Fatalf("fetchOneLargeResource: %v", err)
	}
	if string(got) != "ok" {
		t.Errorf("got %q, want ok", got)
	}
	if hits.Load() != 3 {
		t.Errorf("hits = %d, want 3", hits.Load())
	}
}

func TestFetchOneLargeResource_ExhaustsRetriesOn403(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(ts.Close)
	srv := &mcpServer{cfg: &config{}, http: &http.Client{}}
	_, err := srv.fetchOneLargeResource("x.bin", largeResourceLink{URL: ts.URL}, nil)
	if err == nil || !strings.Contains(err.Error(), "after 3 attempts") {
		t.Errorf("err = %v, want exhaust-retry message", err)
	}
}

// Spec: §6.6 step 1 — non-403 4xx (e.g. 404) is permanent and
// short-circuits without retrying.
func TestFetchOneLargeResource_404FailsFast(t *testing.T) {
	t.Parallel()
	hits := atomic.Int64{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.NotFound(w, nil)
	}))
	t.Cleanup(ts.Close)
	srv := &mcpServer{cfg: &config{}, http: &http.Client{}}
	_, err := srv.fetchOneLargeResource("y.bin", largeResourceLink{URL: ts.URL}, nil)
	if err == nil {
		t.Errorf("err = nil, want refusal")
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1 (no retry on 404)", hits.Load())
	}
}

// Spec: §4.7.10 — a large-resource link with an empty content_hash frames the
// empty value. An authentic body served under a delivery_hash computed over
// the body's true digest, as the registry computes it, therefore fails the
// delivery comparison with materialize.content_hash_mismatch, because the
// consumer never substitutes the fetched body's digest for the link's hash,
// and nothing is cached.
func TestDeliverLoadArtifact_EmptyLinkHashRefused(t *testing.T) {
	t.Parallel()
	body := []byte("authentic large bytes")
	obj := newObjectStub(t, map[string][]byte{"/big.bin": body})
	s := newTestServer(t, &config{harness: "none", verifyPolicy: sign.PolicyNever})
	rec := liveRecord("---\ntype: context\nversion: 1.0.0\n---\nbody\n")
	rec.LargeResources = map[string]largeResourceLink{"data/big.bin": {URL: obj.ts.URL + "/big.bin", ContentHash: sha256Hex(body)}}
	rec = sealDelivery(rec)
	rec.LargeResources["data/big.bin"] = largeResourceLink{URL: obj.ts.URL + "/big.bin"}
	wantRefused(t, s.deliverLoadArtifact(rec), "materialize.content_hash_mismatch")
	if obj.requests("/big.bin") == 0 {
		t.Errorf("the authentic body was never fetched")
	}
	if s.cache.has(rec.ContentHash) {
		t.Errorf("a refused record was cached under %s", rec.ContentHash)
	}
}
