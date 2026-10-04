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
	"time"

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

// loadArtifactStub serves /v1/load_artifact with the given content hash and
// the §4.7.10 delivery pair, mirroring the registry's LoadArtifactResponse
// fields. An empty value is left out of the response.
func loadArtifactStub(t *testing.T, contentHash, deliveryHash, deliverySignature string) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/load_artifact" {
			t.Errorf("path = %q, want /v1/load_artifact", r.URL.Path)
		}
		hits++
		body := map[string]any{"id": r.URL.Query().Get("id"), "content_hash": contentHash}
		if deliveryHash != "" {
			body["delivery_hash"] = deliveryHash
		}
		if deliverySignature != "" {
			body["delivery_signature"] = deliverySignature
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// spec: §4.7.9 — `podium sign <artifact>` resolves the artifact's
// canonical content hash and signs it. The doc example
// `podium sign finance/ap/pay-invoice` must not be a usage error.
func TestSignCmd_PositionalArtifactResolvesAndSigns(t *testing.T) {
	hash := "sha256:" + strings.Repeat("a", 64)
	srv, hits := loadArtifactStub(t, hash, "sha256:"+strings.Repeat("9", 64), "")
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

// spec: §4.7.9 — `podium verify <artifact>` resolves the delivery pair and
// verifies the delivery signature over the delivery hash under the
// registry-managed default, whose public half resolves from the registry key
// file with no flags. The stub's content hash differs from its delivery hash,
// so a form that verifies over the content hash fails.
func TestVerifyCmd_PositionalArtifactVerifiesDeliverySignature(t *testing.T) {
	priv, _ := writeRegistryKeyFile(t)
	hash := "sha256:" + strings.Repeat("b", 64)
	delivery := "sha256:" + strings.Repeat("8", 64)
	srv, hits := loadArtifactStub(t, hash, delivery, signWith(t, priv, delivery))
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

// spec: §4.7.9 — a delivery signature the resolved key made over a different
// value fails verification (exit 1, not a usage error), with the provider
// resolved so only the mismatch fails.
func TestVerifyCmd_PositionalArtifactRejectsTamperedSignature(t *testing.T) {
	priv, _ := writeRegistryKeyFile(t)
	hash := "sha256:" + strings.Repeat("c", 64)
	delivery := "sha256:" + strings.Repeat("7", 64)
	srv, _ := loadArtifactStub(t, hash, delivery, signWith(t, priv, "sha256:"+strings.Repeat("d", 64)))
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

// spec: §4.7.9, §4.7.10 — verifying an artifact a registry without a signing
// key served reports the missing delivery signature (exit 1) rather than
// passing.
func TestVerifyCmd_PositionalArtifactNoDeliverySignature(t *testing.T) {
	hash := "sha256:" + strings.Repeat("e", 64)
	srv, _ := loadArtifactStub(t, hash, "sha256:"+strings.Repeat("6", 64), "")
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	var code int
	stderr := captureStderr(t, func() {
		code = verifyCmd([]string{"unsigned-artifact"})
	})
	if code != 1 || !strings.Contains(stderr, "has no delivery signature") {
		t.Errorf("verifyCmd = %d, stderr %q; want 1 naming the missing delivery signature", code, stderr)
	}
}

// spec: §4.7.9 — `podium verify <artifact> --signature "$(podium sign
// <artifact>)"` round-trips: the explicit envelope is verified over the
// content hash `podium sign` signed, never over the delivery hash, and the
// explicit form reads neither delivery field.
func TestSignVerifyCmd_PositionalArtifactExplicitSignatureRoundTrips(t *testing.T) {
	priv, _ := writeRegistryKeyFile(t)
	hash := "sha256:" + strings.Repeat("a", 64)
	delivery := "sha256:" + strings.Repeat("5", 64)
	srv, _ := loadArtifactStub(t, hash, delivery, signWith(t, priv, delivery))
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	sigOut := captureStdout(t, func() {
		withStderr(t, func() {
			if code := signCmd([]string{"finance/ap/pay-invoice"}); code != 0 {
				t.Fatalf("signCmd = %d", code)
			}
		})
	})
	signed := strings.TrimSpace(sigOut)
	verify := func(sig string) (int, string) {
		var code int
		stderr := captureStderr(t, func() {
			code = verifyCmd([]string{"finance/ap/pay-invoice", "--signature", sig})
		})
		return code, stderr
	}
	if code, stderr := verify(signed); code != 0 {
		t.Errorf("explicit envelope over the content hash = %d, want 0; stderr %q", code, stderr)
	}
	if code, _ := verify(signWith(t, priv, delivery)); code != 1 {
		t.Errorf("explicit envelope over the delivery hash = %d, want 1", code)
	}
	bare, _ := loadArtifactStub(t, hash, "", "")
	t.Setenv("PODIUM_REGISTRY", bare.URL)
	if code, stderr := verify(signed); code != 0 {
		t.Errorf("explicit envelope against a stub with no delivery fields = %d, want 0; stderr %q", code, stderr)
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

// spec: §4.7.9 — a malformed PODIUM_SIGNATURE_VERIFY_KEY, including a list
// with a bad or empty entry, refuses verify naming the variable, even though
// the key file resolves, and is not read by sign, which takes the key file's
// private half.
func TestSignVerifyCmd_MalformedVerifyKey(t *testing.T) {
	_, pub := writeRegistryKeyFile(t)
	hash := "sha256:" + strings.Repeat("a", 64)
	for _, bad := range []string{"!!!not base64", base64.StdEncoding.EncodeToString(pub) + ",!!!", base64.StdEncoding.EncodeToString(pub) + ","} {
		t.Setenv("PODIUM_SIGNATURE_VERIFY_KEY", bad)
		code, stderr := verifyContentHash(t, hash, "envelope")
		if code != 1 || !strings.Contains(stderr, "config.signature_provider_unavailable") || !strings.Contains(stderr, "PODIUM_SIGNATURE_VERIFY_KEY") {
			t.Errorf("verifyCmd with %q = %d, stderr %q; want 1 naming config.signature_provider_unavailable and the variable", bad, code, stderr)
		}
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

// spec: §4.7.9 — a registry that refuses the load, answers a body that does
// not decode, or serves no content hash stops the <artifact> form of verify
// with exit 1 before any delivery field is read.
func TestVerifyCmd_PositionalArtifactResolutionFailures(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"refused":         func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
		"undecodable":     func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) },
		"no content hash": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"delivery_hash":"sha256:d"}`)) },
	} {
		srv := httptest.NewServer(handler)
		t.Setenv("PODIUM_REGISTRY", srv.URL)
		var code int
		withStderr(t, func() { code = verifyCmd([]string{"team/x"}) })
		srv.Close()
		if code != 1 {
			t.Errorf("%s: verifyCmd = %d, want 1", name, code)
		}
	}
}

// spec: §4.7.9 — PODIUM_SIGNATURE_VERIFY_KEY is a comma-separated list, and
// verify accepts a signature under the second listed key.
func TestVerifyCmd_SecondListedKey(t *testing.T) {
	hermeticNoKeyHome(t)
	pubA, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB, privB, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PODIUM_SIGNATURE_VERIFY_KEY", base64.StdEncoding.EncodeToString(pubA)+", "+base64.StdEncoding.EncodeToString(pubB))
	hash := "sha256:" + strings.Repeat("b", 64)
	if code, stderr := verifyContentHash(t, hash, signWith(t, privB, hash)); code != 0 {
		t.Errorf("verify under the second listed key = %d, want 0; stderr %q", code, stderr)
	}
}

// spec: §4.7.9 — with a public-only key file, a consumer's copy, podium sign
// exits 1 with config.signature_provider_unavailable and prints no envelope,
// while podium verify with PODIUM_SIGNATURE_VERIFY_KEY unset verifies through
// the key-file fallback under the public: key and under a verify: key.
func TestSignVerifyCmd_PublicOnlyKeyFile(t *testing.T) {
	hermeticNoKeyHome(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	retiredPub, retiredPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "registry-signing.key")
	if err := sign.WriteKeyFile(path, sign.KeyFile{Public: pub, Verify: []ed25519.PublicKey{retiredPub}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PODIUM_SIGN_KEY_PATH", path)
	hash := "sha256:" + strings.Repeat("c", 64)
	var code int
	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() { code = signCmd([]string{"--content-hash", hash}) })
	})
	if code != 1 || !strings.Contains(stderr, "config.signature_provider_unavailable") || strings.TrimSpace(stdout) != "" {
		t.Errorf("signCmd = %d, stdout %q, stderr %q; want 1, no envelope, and config.signature_provider_unavailable", code, stdout, stderr)
	}
	for name, k := range map[string]ed25519.PrivateKey{"public": priv, "verify": retiredPriv} {
		if code, stderr := verifyContentHash(t, hash, signWith(t, k, hash)); code != 0 {
			t.Errorf("verify under the %s: key = %d, want 0; stderr %q", name, code, stderr)
		}
	}
}

// spec: §4.7.9 — an envelope podium sign mints carries the key_id of the key
// file's signing key.
func TestSignCmd_EnvelopeCarriesKeyID(t *testing.T) {
	_, pub := writeRegistryKeyFile(t)
	sig := signContentHash(t, "sha256:"+strings.Repeat("d", 64))
	var env struct {
		KeyID string `json:"key_id"`
	}
	if err := json.Unmarshal([]byte(sig), &env); err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if want := sign.KeyIDFor(pub); env.KeyID != want {
		t.Errorf("envelope key_id = %q, want %q", env.KeyID, want)
	}
}

// Spec: §6.2
// Matrix: §6.10 (config.invalid)
// TestSigstoreRequestTimeout pins how podium sign reads
// PODIUM_SIGSTORE_REQUEST_TIMEOUT: an unset or blank value takes
// sign.DefaultRequestTimeout, a positive duration is used after trimming, and
// any other value is refused with config.invalid naming the variable. The
// cases use t.Setenv, so they cannot run in parallel.
func TestSigstoreRequestTimeout(t *testing.T) {
	const unset = "<unset>"
	cases := []struct {
		value   string
		want    time.Duration
		wantErr bool
	}{
		{value: unset, want: sign.DefaultRequestTimeout},
		{value: "", want: sign.DefaultRequestTimeout},
		{value: "  ", want: sign.DefaultRequestTimeout},
		{value: "5s", want: 5 * time.Second},
		{value: " 5s ", want: 5 * time.Second},
		{value: "abc", wantErr: true},
		{value: "0", wantErr: true},
		{value: "0s", wantErr: true},
		{value: "-1s", wantErr: true},
		{value: "60", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("PODIUM_SIGSTORE_REQUEST_TIMEOUT", tc.value)
			if tc.value == unset {
				if err := os.Unsetenv("PODIUM_SIGSTORE_REQUEST_TIMEOUT"); err != nil {
					t.Fatal(err)
				}
			}
			got, err := sigstoreRequestTimeout()
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "config.invalid") || !strings.Contains(err.Error(), "PODIUM_SIGSTORE_REQUEST_TIMEOUT") {
					t.Fatalf("sigstoreRequestTimeout() = %v, %v; want a config.invalid error naming PODIUM_SIGSTORE_REQUEST_TIMEOUT", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("sigstoreRequestTimeout() = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

// Spec: §6.2
// Matrix: §6.10 (config.invalid)
// TestLoadSignatureProvider_SigstoreVerifyIgnoresRequestTimeout pins that
// only the sign use reads PODIUM_SIGSTORE_REQUEST_TIMEOUT. Verification is
// offline, so an invalid value must not break podium verify, while podium sign
// refuses it and carries a valid value into SigstoreKeyless.RequestTimeout.
func TestLoadSignatureProvider_SigstoreVerifyIgnoresRequestTimeout(t *testing.T) {
	t.Setenv("PODIUM_SIGSTORE_REQUEST_TIMEOUT", "abc")
	if _, err := loadSignatureProvider("sigstore-keyless", keyForVerify); err != nil {
		t.Errorf("verify use with an invalid timeout: err = %v, want nil", err)
	}
	if _, err := loadSignatureProvider("sigstore-keyless", keyForSign); err == nil || !strings.Contains(err.Error(), "config.invalid") {
		t.Errorf("sign use with an invalid timeout: err = %v, want config.invalid", err)
	}

	t.Setenv("PODIUM_SIGSTORE_REQUEST_TIMEOUT", "5s")
	p, err := loadSignatureProvider("sigstore-keyless", keyForSign)
	if err != nil {
		t.Fatalf("sign use with 5s: %v", err)
	}
	ks, ok := p.(sign.SigstoreKeyless)
	if !ok {
		t.Fatalf("provider = %T, want sign.SigstoreKeyless", p)
	}
	if ks.RequestTimeout != 5*time.Second {
		t.Errorf("RequestTimeout = %v, want 5s", ks.RequestTimeout)
	}
}
