package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// Spec: §6.6 step 1 / §13.11 — the large-resource fetch sends the same session
// token and tenant header load_artifact used, so the filesystem backend's
// authenticated /objects/{content_hash} route accepts it (F-6.6.3). The token
// comes from a file to avoid racing process-wide env vars.
func TestGetLargeResource_SendsSessionTokenAndTenant(t *testing.T) {
	t.Parallel()
	var gotAuth, gotTenant string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotTenant = r.Header.Get("X-Podium-Tenant")
		_, _ = w.Write([]byte("data"))
	}))
	t.Cleanup(ts.Close)

	tokFile := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(tokFile, []byte("tok123"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	s := &mcpServer{cfg: &config{
		sessionTokenFile: tokFile,
		tenantID:         "acme",
		identityProvider: "injected-session-token",
	}, http: &http.Client{}}

	body, status, err := s.getLargeResource(ts.URL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("getLargeResource: status=%d err=%v", status, err)
	}
	if string(body) != "data" {
		t.Errorf("body = %q, want data", body)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("Authorization = %q, want Bearer tok123", gotAuth)
	}
	if gotTenant != "acme" {
		t.Errorf("X-Podium-Tenant = %q, want acme", gotTenant)
	}
}

// Spec: §6.6 step 1 — an anonymous deployment (no token, no tenant) sends no
// credential header, so an S3-backend self-signed URL is fetched cleanly.
func TestGetLargeResource_NoCredentialWhenUnset(t *testing.T) {
	t.Parallel()
	var hadAuth, hadTenant bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		_, hadTenant = r.Header["X-Podium-Tenant"]
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(ts.Close)
	s := &mcpServer{cfg: &config{identityProvider: "injected-session-token"}, http: &http.Client{}}
	if _, status, err := s.getLargeResource(ts.URL); err != nil || status != http.StatusOK {
		t.Fatalf("getLargeResource: status=%d err=%v", status, err)
	}
	if hadAuth || hadTenant {
		t.Errorf("unexpected credential headers (auth=%v tenant=%v)", hadAuth, hadTenant)
	}
}

// Spec: §13.11 — an S3 presigned URL is self-validating; "consumers do not send
// credentials when following the URL." Sending an Authorization header
// alongside the Signature V4 query makes S3 reject the request as "multiple
// authentication types" (HTTP 400). So even with a session token and tenant
// configured (the managed-runtime case), a URL carrying X-Amz-Signature is
// fetched credential-free.
func TestGetLargeResource_NoCredentialOnSigV4PresignedURL(t *testing.T) {
	t.Parallel()
	var hadAuth, hadTenant bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		_, hadTenant = r.Header["X-Podium-Tenant"]
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(ts.Close)

	tokFile := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(tokFile, []byte("session-jwt"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	s := &mcpServer{cfg: &config{
		sessionTokenFile: tokFile,
		tenantID:         "acme",
		identityProvider: "injected-session-token",
	}, http: &http.Client{}}

	// The presigned-URL marker S3 appends; the host is the httptest recorder so
	// the request lands and the absence of credentials can be observed.
	sigV4URL := ts.URL + "/blob?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AK%2F20260604%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Signature=deadbeef"
	if _, status, err := s.getLargeResource(sigV4URL); err != nil || status != http.StatusOK {
		t.Fatalf("getLargeResource: status=%d err=%v", status, err)
	}
	if hadAuth {
		t.Error("Authorization header was sent to a SigV4 presigned URL; S3 rejects multiple auth types")
	}
	if hadTenant {
		t.Error("X-Podium-Tenant header was sent to a SigV4 presigned URL")
	}
}

// Spec: §6.6 step 1 — on a 403 (expired presigned URL) the fetch requests a
// fresh URL set and retries against the new URL rather than reusing the stale
// one (F-6.6.6).
func TestFetchOneLargeResource_RefreshesURLOn403(t *testing.T) {
	t.Parallel()
	body := []byte("fresh bytes")
	freshTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(freshTS.Close)
	staleHits := atomic.Int64{}
	staleTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		staleHits.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(staleTS.Close)

	refreshed := atomic.Bool{}
	refresh := func() (map[string]largeResourceLink, error) {
		refreshed.Store(true)
		return map[string]largeResourceLink{"p": {URL: freshTS.URL}}, nil
	}
	s := &mcpServer{cfg: &config{}, http: &http.Client{}}
	got, err := s.fetchOneLargeResource("p", largeResourceLink{URL: staleTS.URL}, refresh)
	if err != nil {
		t.Fatalf("fetchOneLargeResource: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("got %q, want %q", got, body)
	}
	if !refreshed.Load() {
		t.Error("refresh was not called on 403")
	}
	if staleHits.Load() != 1 {
		t.Errorf("stale URL hit %d times, want 1 (it must not be retried after refresh)", staleHits.Load())
	}
}

// Spec: §6.6 step 1 — fetchLargeResources merges the refreshed bytes and still
// verifies the per-resource content hash.
func TestFetchLargeResources_RefreshSwapsURL(t *testing.T) {
	t.Parallel()
	body := []byte("BIG REFRESHED BLOB")
	sum := sha256.Sum256(body)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	freshTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(freshTS.Close)
	staleTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(staleTS.Close)

	refresh := func() (map[string]largeResourceLink, error) {
		return map[string]largeResourceLink{
			"data/big.bin": {URL: freshTS.URL, ContentHash: hash},
		}, nil
	}
	s := &mcpServer{cfg: &config{}, http: &http.Client{}}
	resp := loadArtifactResponse{
		Resources: map[string]string{},
		LargeResources: map[string]largeResourceLink{
			"data/big.bin": {URL: staleTS.URL, ContentHash: hash},
		},
	}
	if err := s.fetchLargeResources(&resp, refresh); err != nil {
		t.Fatalf("fetchLargeResources: %v", err)
	}
	if got := resp.Resources["data/big.bin"]; got != string(body) {
		t.Errorf("merged resource = %q, want %q", got, body)
	}
}
