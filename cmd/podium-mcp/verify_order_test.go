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
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/sign"
)

// sandboxedFM declares a sandbox profile a zero-config host does not support,
// so the §4.4.1 sandbox gate refuses it.
const sandboxedFM = "---\ntype: context\nversion: 1.0.0\nsandbox_profile: seccomp-strict\n---\nbody\n"

// runtimeFM declares a python requirement a host advertising 3.9.0 cannot
// satisfy, the fixture runtime_policy_test.go uses against the gate directly.
const runtimeFM = "---\ntype: context\nversion: 1.0.0\nruntime_requirements:\n  python: \">=3.11\"\n---\nbody\n"

// orderServer returns a test server whose local audit sink writes to a file,
// and the path of that file, so a case can observe whether the §8.2 read
// event was emitted.
func orderServer(t *testing.T, cfg *config) (*mcpServer, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	s := newTestServer(t, cfg)
	s.audit = sink
	return s, path
}

// loadedEventCount returns how many artifact.loaded events the local sink at
// path holds.
func loadedEventCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read audit log: %v", err)
	}
	return strings.Count(string(data), string(audit.EventArtifactLoaded))
}

// tamperedHash returns resp with a content_hash the served bytes do not
// reproduce, so the §6.6 step-2 recomputation refuses it.
func tamperedHash(resp loadArtifactResponse) loadArtifactResponse {
	resp.ContentHash = "sha256:" + strings.Repeat("0", 64)
	return resp
}

// Spec: §6.6 step 2, §4.7.9 — the verification runs before the §4.4.1 sandbox
// gate reads the manifest, so a record that fails the verification reports
// the verification's code rather than the sandbox refusal. The signed arm
// pins the same order for a signature that does not validate.
func TestDeliverLoadArtifact_ManifestGatesRunAfterVerification(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &config{harness: "none", verifyPolicy: sign.PolicyNever})
	out := s.deliverLoadArtifact(tamperedHash(fixtureResp("team/x", sandboxedFM)))
	if got := errorMessageText(out); !strings.HasPrefix(got, "materialize.content_hash_mismatch") {
		t.Errorf("hash arm: error = %q, want materialize.content_hash_mismatch ahead of the sandbox gate", got)
	}

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	_, otherPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	resp := fixtureResp("team/x", sandboxedFM)
	resp.Signature, err = sign.RegistryManagedKey{PrivateKey: otherPriv}.Sign(context.Background(), resp.ContentHash)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	s = newTestServer(t, &config{harness: "none", verifyPolicy: sign.PolicyAlways, signatureProvider: "registry-managed", verifier: sign.RegistryManagedKey{PublicKey: pub}})
	if got := errorMessageText(s.deliverLoadArtifact(resp)); !strings.HasPrefix(got, "materialize.signature_invalid") {
		t.Errorf("signature arm: error = %q, want materialize.signature_invalid ahead of the sandbox gate", got)
	}
}

// Spec: §6.6 step 2, §8.2 — a load the verification refuses emits no local
// artifact.loaded event, while a verified load the §4.4.1 sandbox gate
// refuses still emits it, because the read event stays above the gates.
func TestDeliverLoadArtifact_NoAuditEventOnVerificationRefusal(t *testing.T) {
	t.Parallel()
	t.Run("verification refusal", func(t *testing.T) {
		t.Parallel()
		s, path := orderServer(t, &config{harness: "none", verifyPolicy: sign.PolicyNever})
		out := s.deliverLoadArtifact(tamperedHash(fixtureResp("team/x", "---\ntype: context\n---\nbody\n")))
		if got := errorMessageText(out); !strings.HasPrefix(got, "materialize.content_hash_mismatch") {
			t.Fatalf("error = %q, want materialize.content_hash_mismatch", got)
		}
		if n := loadedEventCount(t, path); n != 0 {
			t.Errorf("artifact.loaded events = %d, want 0 on a verification refusal", n)
		}
	})
	t.Run("sandbox refusal", func(t *testing.T) {
		t.Parallel()
		s, path := orderServer(t, &config{harness: "none", verifyPolicy: sign.PolicyNever})
		out := s.deliverLoadArtifact(fixtureResp("team/x", sandboxedFM))
		if got := errorMessageText(out); !strings.HasPrefix(got, "materialize.sandbox_unsupported") {
			t.Fatalf("error = %q, want materialize.sandbox_unsupported", got)
		}
		if n := loadedEventCount(t, path); n != 1 {
			t.Errorf("artifact.loaded events = %d, want 1 above the sandbox gate", n)
		}
	})
}

// Spec: §6.6 step 2, §4.4.1 — the runtime gate runs after the verification:
// a record that fails the verification reports the verification's code, and
// a verified record emits the read event and then the runtime refusal.
func TestDeliverLoadArtifact_RuntimeGateRunsAfterVerification(t *testing.T) {
	t.Parallel()
	t.Run("record fails verification", func(t *testing.T) {
		t.Parallel()
		s, path := orderServer(t, &config{harness: "none", verifyPolicy: sign.PolicyNever, hostPython: "3.9.0"})
		out := s.deliverLoadArtifact(tamperedHash(fixtureResp("team/x", runtimeFM)))
		if got := errorMessageText(out); !strings.HasPrefix(got, "materialize.content_hash_mismatch") {
			t.Errorf("error = %q, want materialize.content_hash_mismatch ahead of the runtime gate", got)
		}
		if n := loadedEventCount(t, path); n != 0 {
			t.Errorf("artifact.loaded events = %d, want 0", n)
		}
	})
	t.Run("record verifies", func(t *testing.T) {
		t.Parallel()
		s, path := orderServer(t, &config{harness: "none", verifyPolicy: sign.PolicyNever, hostPython: "3.9.0"})
		out := s.deliverLoadArtifact(fixtureResp("team/x", runtimeFM))
		if got := errorMessageText(out); !strings.Contains(got, "materialize.runtime_unavailable") {
			t.Errorf("error = %q, want materialize.runtime_unavailable", got)
		}
		if n := loadedEventCount(t, path); n != 1 {
			t.Errorf("artifact.loaded events = %d, want 1 above the runtime gate", n)
		}
	})
}

// Spec: §6.6 step 1, §8.2 — a large-resource fetch runs inside the
// verification, so a failed fetch returns materialize.fetch_failed and emits
// no local artifact.loaded event.
func TestDeliverLoadArtifact_NoAuditEventOnResourceFetchFailure(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("tampered"))
	}))
	t.Cleanup(ts.Close)
	s, path := orderServer(t, &config{harness: "none", verifyPolicy: sign.PolicyNever})
	resp := fixtureResp("team/x", "---\ntype: context\n---\nbody\n")
	resp.LargeResources = map[string]largeResourceLink{
		"data/big.bin": {URL: ts.URL, ContentHash: "sha256:" + strings.Repeat("a", 64)},
	}
	out := s.deliverLoadArtifact(resp)
	if got := errorMessageText(out); !strings.HasPrefix(got, "materialize.fetch_failed") {
		t.Fatalf("error = %q, want materialize.fetch_failed", got)
	}
	if n := loadedEventCount(t, path); n != 0 {
		t.Errorf("artifact.loaded events = %d, want 0 on a failed resource fetch", n)
	}
}

// Spec: §6.10 — an enforcing policy over a response that carries no signature
// returns materialize.signature_missing, the code with its own operator
// remedy, rather than materialize.signature_invalid.
// Matrix: §6.10 (materialize.signature_missing)
func TestDeliverLoadArtifact_MissingSignatureCarriesItsOwnCode(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &config{harness: "none", verifyPolicy: sign.PolicyAlways, signatureProvider: "registry-managed"})
	out := s.deliverLoadArtifact(fixtureResp("team/x", "---\ntype: context\n---\nbody\n"))
	got := errorMessageText(out)
	if !strings.HasPrefix(got, "materialize.signature_missing: ") {
		t.Errorf("error = %q, want a leading materialize.signature_missing code", got)
	}
}

// Spec: §6.6 step 1 — a presigned manifest body that cannot be fetched fails
// the verification with materialize.fetch_failed.
func TestVerifyServedArtifact_ManifestBodyFetchFailure(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)
	s := newTestServer(t, &config{verifyPolicy: sign.PolicyNever})
	resp := loadArtifactResponse{ID: "team/x", Type: "context", ManifestBodyURL: &largeResourceLink{URL: ts.URL}}
	err := s.verifyServedArtifact(&resp, deliverOpts{})
	if err == nil || !strings.HasPrefix(err.Error(), "materialize.fetch_failed") {
		t.Errorf("err = %v, want materialize.fetch_failed", err)
	}
}

// Spec: §6.6 step 1 — an inline resource flagged base64 that does not decode
// fails the verification with its own code.
func TestVerifyServedArtifact_InvalidBase64(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &config{verifyPolicy: sign.PolicyNever})
	resp := fixtureResp("team/x", "---\ntype: context\n---\n")
	resp.Resources = map[string]string{"a.txt": "!!not base64!!"}
	resp.ResourcesB64 = true
	err := s.verifyServedArtifact(&resp, deliverOpts{})
	if err == nil || !strings.HasPrefix(err.Error(), "materialize.invalid_base64") {
		t.Errorf("err = %v, want materialize.invalid_base64", err)
	}
}

// sha256Hex returns the "sha256:<hex>" digest of b.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
