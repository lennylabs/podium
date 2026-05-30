package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// loadArtifactStub serves /v1/load_artifact with the given content hash
// and signature, mirroring the registry's LoadArtifactResponse shape.
func loadArtifactStub(t *testing.T, contentHash, signature string) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/load_artifact" {
			t.Errorf("path = %q, want /v1/load_artifact", r.URL.Path)
		}
		hits++
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":           r.URL.Query().Get("id"),
			"content_hash": contentHash,
			"signature":    signature,
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// spec: §4.7.9 — `podium sign <artifact>` resolves the artifact's
// canonical content hash and signs it (F-4.7.9). The doc example
// `podium sign finance/ap/pay-invoice` must not be a usage error.
func TestSignCmd_PositionalArtifactResolvesAndSigns(t *testing.T) {
	hash := "sha256:" + strings.Repeat("a", 64)
	srv, hits := loadArtifactStub(t, hash, "")
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	var code int
	withStderr(t, func() {
		code = signCmd([]string{"finance/ap/pay-invoice"})
	})
	if code != 0 {
		t.Fatalf("signCmd = %d, want 0", code)
	}
	if *hits != 1 {
		t.Errorf("load_artifact hits = %d, want 1", *hits)
	}
}

// spec: §4.7.9 — `podium verify <artifact>` resolves the stored
// signature and verifies it against the canonical content hash.
func TestVerifyCmd_PositionalArtifactVerifiesStoredSignature(t *testing.T) {
	hash := "sha256:" + strings.Repeat("b", 64)
	srv, _ := loadArtifactStub(t, hash, "noop:"+hash) // noop envelope = "noop:"+hash
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	var code int
	withStderr(t, func() {
		code = verifyCmd([]string{"finance/ap/pay-invoice"})
	})
	if code != 0 {
		t.Errorf("verifyCmd = %d, want 0 (stored signature is valid)", code)
	}
}

// spec: §4.7.9 — a stored signature that does not match the content
// hash fails verification (exit 1, not a usage error).
func TestVerifyCmd_PositionalArtifactRejectsTamperedSignature(t *testing.T) {
	hash := "sha256:" + strings.Repeat("c", 64)
	srv, _ := loadArtifactStub(t, hash, "noop:sha256:"+strings.Repeat("d", 64))
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	var code int
	withStderr(t, func() {
		code = verifyCmd([]string{"some-artifact"})
	})
	if code != 1 {
		t.Errorf("verifyCmd = %d, want 1 for a mismatched signature", code)
	}
}

// spec: §4.7.9 — verifying an artifact the registry stored without a
// signature reports the missing envelope (exit 1) rather than passing.
func TestVerifyCmd_PositionalArtifactNoStoredSignature(t *testing.T) {
	hash := "sha256:" + strings.Repeat("e", 64)
	srv, _ := loadArtifactStub(t, hash, "")
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	var code int
	withStderr(t, func() {
		code = verifyCmd([]string{"unsigned-artifact"})
	})
	if code != 1 {
		t.Errorf("verifyCmd = %d, want 1 when no signature is stored", code)
	}
}

// The lower-level `--content-hash` form keeps working unchanged.
func TestSignVerifyCmd_ContentHashFormRoundTrips(t *testing.T) {
	hash := "sha256:" + strings.Repeat("f", 64)
	sigOut := captureStdout(t, func() {
		if code := signCmd([]string{"--content-hash", hash}); code != 0 {
			t.Fatalf("signCmd --content-hash = %d", code)
		}
	})
	sig := strings.TrimSpace(sigOut)
	if sig == "" {
		t.Fatalf("sign produced no envelope")
	}
	var code int
	withStderr(t, func() {
		code = verifyCmd([]string{"--content-hash", hash, "--signature", sig})
	})
	if code != 0 {
		t.Errorf("verifyCmd --content-hash = %d, want 0", code)
	}
}

// Passing both <artifact> and --content-hash is ambiguous: usage error.
// Flags precede the positional so flag.Parse sees --content-hash.
func TestSignCmd_PositionalAndContentHashConflict(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	var code int
	withStderr(t, func() {
		code = signCmd([]string{"--content-hash", "sha256:x", "finance/ap/pay-invoice"})
	})
	if code != 2 {
		t.Errorf("signCmd = %d, want 2 (conflicting inputs)", code)
	}
}

// The <artifact> form needs a registry to resolve against.
func TestVerifyCmd_PositionalWithoutRegistryExits2(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "")
	var code int
	withStderr(t, func() {
		code = verifyCmd([]string{"finance/ap/pay-invoice"})
	})
	if code != 2 {
		t.Errorf("verifyCmd = %d, want 2 (missing registry)", code)
	}
}
