package sign_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
)

// Verify falls back to deriving the public key from the private key
// when PublicKey is not explicitly set.
func TestRegistryManagedKey_DerivesPublicFromPrivate(t *testing.T) {
	t.Parallel()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	signer := sign.RegistryManagedKey{PrivateKey: priv}
	hash := "sha256:" + hashHex("body")
	envelope, err := signer.Sign(hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Verifier has only the private key; should derive public.
	verifier := sign.RegistryManagedKey{PrivateKey: priv}
	if err := verifier.Verify(hash, envelope); err != nil {
		t.Errorf("Verify with derived public: %v", err)
	}
	// Force-set the public key from generated pub for explicit form.
	verifier2 := sign.RegistryManagedKey{PublicKey: pub}
	if err := verifier2.Verify(hash, envelope); err != nil {
		t.Errorf("Verify with explicit public: %v", err)
	}
}

// Verify rejects an envelope that doesn't parse as JSON.
func TestRegistryManagedKey_VerifyMalformedEnvelope(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	v := sign.RegistryManagedKey{PrivateKey: priv}
	if err := v.Verify("sha256:"+hashHex("x"), "not json"); !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Errorf("err = %v", err)
	}
}

// Verify rejects a non-base64 signature field.
func TestRegistryManagedKey_VerifyBadSignatureBase64(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	v := sign.RegistryManagedKey{PrivateKey: priv}
	bad, _ := json.Marshal(map[string]string{"signature": "!!!not base64"})
	if err := v.Verify("sha256:"+hashHex("x"), string(bad)); !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Errorf("err = %v", err)
	}
}

// Verify rejects a bogus content hash.
func TestRegistryManagedKey_VerifyBadContentHash(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	v := sign.RegistryManagedKey{PrivateKey: priv}
	// Build a well-formed envelope.
	sig := ed25519.Sign(priv, []byte("ignored"))
	env, _ := json.Marshal(map[string]string{"signature": base64.StdEncoding.EncodeToString(sig)})
	if err := v.Verify("not-a-valid-hash", string(env)); !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Errorf("err = %v", err)
	}
}

// Verify rejects when the envelope's signature does not check out.
func TestRegistryManagedKey_VerifyTamperedSignature(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer := sign.RegistryManagedKey{PrivateKey: priv}
	hash := "sha256:" + hashHex("body")
	env, _ := signer.Sign(hash)
	// Modify the signature byte string.
	var parsed map[string]string
	_ = json.Unmarshal([]byte(env), &parsed)
	parsed["signature"] = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	tampered, _ := json.Marshal(parsed)
	if err := signer.Verify(hash, string(tampered)); !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Errorf("err = %v", err)
	}
}

func hashHex(s string) string {
	return "" + // placeholder; concrete hash bytes don't matter for envelope tests
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
}
