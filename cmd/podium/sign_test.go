package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
)

// writeRegistryKeyFile points HOME at a fresh directory, clears the signing
// variables, and writes a registry key file at the sign.KeyFilePath default
// in the format the registry writes, which is what a standalone registry
// generates on its first start. It returns both halves.
func writeRegistryKeyFile(t *testing.T) (ed25519.PrivateKey, ed25519.PublicKey) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PODIUM_SIGN_KEY_PATH", "")
	t.Setenv("PODIUM_SIGNATURE_VERIFY_KEY", "")
	t.Setenv("PODIUM_SIGNATURE_PROVIDER", "")
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path, err := sign.KeyFilePath("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "private: " + base64.StdEncoding.EncodeToString(priv) + "\n" +
		"public: " + base64.StdEncoding.EncodeToString(pub) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return priv, pub
}

// signWith returns a registry-managed envelope over hash under priv.
func signWith(t *testing.T, priv ed25519.PrivateKey, hash string) string {
	t.Helper()
	env, err := sign.RegistryManagedKey{PrivateKey: priv}.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return env
}

// hermeticNoKeyHome points HOME at an empty directory and clears the signing
// variables, so no registry-managed material resolves.
func hermeticNoKeyHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PODIUM_SIGN_KEY_PATH", "")
	t.Setenv("PODIUM_SIGNATURE_VERIFY_KEY", "")
	t.Setenv("PODIUM_SIGNATURE_PROVIDER", "")
}

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
// canonical content hash and signs it. The doc example
// `podium sign finance/ap/pay-invoice` must not be a usage error.
func TestSignCmd_PositionalArtifactResolvesAndSigns(t *testing.T) {
	hash := "sha256:" + strings.Repeat("a", 64)
	srv, hits := loadArtifactStub(t, hash, "")
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	var code int
	withStderr(t, func() {
		code = signCmd([]string{"--provider", "noop", "finance/ap/pay-invoice"})
	})
	if code != 0 {
		t.Fatalf("signCmd = %d, want 0", code)
	}
	if *hits != 1 {
		t.Errorf("load_artifact hits = %d, want 1", *hits)
	}
}

// noopRefusal is the text Noop.Verify returns for every signature. A CLI
// verify through the noop provider exits 1 and prints it.
const noopRefusal = "the noop provider does not verify"

// spec: §4.7.9 — `podium verify <artifact>` resolves the stored
// signature and verifies it under the registry-managed default, whose public
// half resolves from the registry key file with no flags.
func TestVerifyCmd_PositionalArtifactVerifiesStoredSignature(t *testing.T) {
	priv, _ := writeRegistryKeyFile(t)
	hash := "sha256:" + strings.Repeat("b", 64)
	srv, hits := loadArtifactStub(t, hash, signWith(t, priv, hash))
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	var code int
	stderr := captureStderr(t, func() {
		code = verifyCmd([]string{"finance/ap/pay-invoice"})
	})
	if code != 0 {
		t.Errorf("verifyCmd = %d, want 0; stderr %q", code, stderr)
	}
	if *hits != 1 {
		t.Errorf("load_artifact hits = %d, want 1", *hits)
	}
}

// spec: §4.7.9 — a stored signature the resolved key made over a different
// hash fails verification (exit 1, not a usage error), with the provider
// resolved so only the mismatch fails.
func TestVerifyCmd_PositionalArtifactRejectsTamperedSignature(t *testing.T) {
	priv, _ := writeRegistryKeyFile(t)
	hash := "sha256:" + strings.Repeat("c", 64)
	srv, _ := loadArtifactStub(t, hash, signWith(t, priv, "sha256:"+strings.Repeat("d", 64)))
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	var code int
	stderr := captureStderr(t, func() {
		code = verifyCmd([]string{"some-artifact"})
	})
	if code != 1 {
		t.Errorf("verifyCmd = %d, want 1 for a mismatched signature", code)
	}
	if !strings.Contains(stderr, "verify failed") || !strings.Contains(stderr, "signature does not verify") {
		t.Errorf("stderr = %q, want the failed verification of the envelope", stderr)
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

// spec: §4.7.9 — the lower-level `--content-hash` form signs with the
// registry key file's private half and verifies with its public half, both
// with no flags and no variables, which is the standalone machine's setup.
func TestSignVerifyCmd_ContentHashFormRoundTrips(t *testing.T) {
	writeRegistryKeyFile(t)
	hash := "sha256:" + strings.Repeat("f", 64)
	sig := signContentHash(t, hash)
	if code, stderr := verifyContentHash(t, hash, sig); code != 0 {
		t.Errorf("verifyCmd --content-hash = %d, want 0; stderr %q", code, stderr)
	}
}

// signContentHash runs `podium sign --content-hash` with the ambient
// configuration and returns the envelope it prints.
func signContentHash(t *testing.T, hash string, extra ...string) string {
	t.Helper()
	sigOut := captureStdout(t, func() {
		withStderr(t, func() {
			if code := signCmd(append(extra, "--content-hash", hash)); code != 0 {
				t.Fatalf("signCmd --content-hash = %d", code)
			}
		})
	})
	sig := strings.TrimSpace(sigOut)
	if sig == "" {
		t.Fatalf("sign produced no envelope")
	}
	return sig
}

// verifyContentHash runs `podium verify --content-hash --signature` with the
// ambient configuration and returns its exit code and stderr.
func verifyContentHash(t *testing.T, hash, sig string, extra ...string) (int, string) {
	t.Helper()
	var code int
	stderr := captureStderr(t, func() {
		code = verifyCmd(append(extra, "--content-hash", hash, "--signature", sig))
	})
	return code, stderr
}

// spec: §4.7.9 — the noop provider refuses the envelope its own Sign just
// produced, because that value is derived from a content hash served in the
// clear.
func TestVerifyCmd_NoopRefusesItsOwnEnvelope(t *testing.T) {
	hash := "sha256:" + strings.Repeat("a", 64)
	sig := signContentHash(t, hash, "--provider", "noop")
	code, stderr := verifyContentHash(t, hash, sig, "--provider", "noop")
	if code != 1 || !strings.Contains(stderr, noopRefusal) {
		t.Errorf("verifyCmd --provider noop = %d, stderr %q; want 1 naming %q", code, stderr, noopRefusal)
	}
}

// spec: §4.7.9, §6.2 — with no provider flag and no resolvable key, both
// commands exit 1 naming config.signature_provider_unavailable before any
// Sign or Verify call.
func TestSignVerifyCmd_NoResolvableKeyRefuses(t *testing.T) {
	hermeticNoKeyHome(t)
	hash := "sha256:" + strings.Repeat("a", 64)
	var code int
	stderr := captureStderr(t, func() { code = signCmd([]string{"--content-hash", hash}) })
	if code != 1 || !strings.Contains(stderr, "config.signature_provider_unavailable") {
		t.Errorf("signCmd = %d, stderr %q; want 1 naming config.signature_provider_unavailable", code, stderr)
	}
	code, stderr = verifyContentHash(t, hash, "envelope")
	if code != 1 || !strings.Contains(stderr, "config.signature_provider_unavailable") {
		t.Errorf("verifyCmd = %d, stderr %q; want 1 naming config.signature_provider_unavailable", code, stderr)
	}
}

// spec: §4.7.9 — a set PODIUM_SIGNATURE_VERIFY_KEY is authoritative for
// verification: with the key file holding key A and the variable holding key
// B, an envelope B signed verifies and one A signed does not.
func TestVerifyCmd_VerifyKeyOutranksTheKeyFile(t *testing.T) {
	privA, _ := writeRegistryKeyFile(t)
	pubB, privB, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PODIUM_SIGNATURE_VERIFY_KEY", base64.StdEncoding.EncodeToString(pubB))
	hash := "sha256:" + strings.Repeat("a", 64)
	if code, stderr := verifyContentHash(t, hash, signWith(t, privB, hash)); code != 0 {
		t.Errorf("verify of B's envelope = %d, want 0; stderr %q", code, stderr)
	}
	if code, _ := verifyContentHash(t, hash, signWith(t, privA, hash)); code != 1 {
		t.Errorf("verify of A's envelope = %d, want 1", code)
	}
}

// spec: §4.7.9 — a malformed PODIUM_SIGNATURE_VERIFY_KEY refuses verify
// naming the variable, even though the key file resolves, and is not read by
// sign, which takes the key file's private half.
func TestSignVerifyCmd_MalformedVerifyKey(t *testing.T) {
	_, pub := writeRegistryKeyFile(t)
	t.Setenv("PODIUM_SIGNATURE_VERIFY_KEY", "!!!not base64")
	hash := "sha256:" + strings.Repeat("a", 64)
	code, stderr := verifyContentHash(t, hash, "envelope")
	if code != 1 || !strings.Contains(stderr, "config.signature_provider_unavailable") || !strings.Contains(stderr, "PODIUM_SIGNATURE_VERIFY_KEY") {
		t.Errorf("verifyCmd = %d, stderr %q; want 1 naming config.signature_provider_unavailable and the variable", code, stderr)
	}
	sig := signContentHash(t, hash)
	if err := (sign.RegistryManagedKey{PublicKey: pub}).Verify(context.Background(), hash, sig); err != nil {
		t.Errorf("the key file's public half does not verify the envelope sign produced: %v", err)
	}
}

// spec: §4.7.9 — a remote consumer with PODIUM_SIGNATURE_VERIFY_KEY and no
// key file verifies, and cannot sign: sign refuses naming the key path.
func TestSignVerifyCmd_VerifyKeyWithoutKeyFile(t *testing.T) {
	hermeticNoKeyHome(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PODIUM_SIGNATURE_VERIFY_KEY", base64.StdEncoding.EncodeToString(pub))
	hash := "sha256:" + strings.Repeat("a", 64)
	if code, stderr := verifyContentHash(t, hash, signWith(t, priv, hash)); code != 0 {
		t.Errorf("verifyCmd = %d, want 0; stderr %q", code, stderr)
	}
	var code int
	stderr := captureStderr(t, func() { code = signCmd([]string{"--content-hash", hash}) })
	if code != 1 || !strings.Contains(stderr, "config.signature_provider_unavailable") || !strings.Contains(stderr, "registry-signing.key") {
		t.Errorf("signCmd = %d, stderr %q; want 1 naming config.signature_provider_unavailable and the key path", code, stderr)
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
