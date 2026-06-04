package sign_test

import (
	"context"
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
	envelope, err := signer.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Verifier has only the private key; should derive public.
	verifier := sign.RegistryManagedKey{PrivateKey: priv}
	if err := verifier.Verify(context.Background(), hash, envelope); err != nil {
		t.Errorf("Verify with derived public: %v", err)
	}
	// Force-set the public key from generated pub for explicit form.
	verifier2 := sign.RegistryManagedKey{PublicKey: pub}
	if err := verifier2.Verify(context.Background(), hash, envelope); err != nil {
		t.Errorf("Verify with explicit public: %v", err)
	}
}

// Verify rejects an envelope that doesn't parse as JSON.
func TestRegistryManagedKey_VerifyMalformedEnvelope(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	v := sign.RegistryManagedKey{PrivateKey: priv}
	if err := v.Verify(context.Background(), "sha256:"+hashHex("x"), "not json"); !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Errorf("err = %v", err)
	}
}

// Verify rejects a non-base64 signature field.
func TestRegistryManagedKey_VerifyBadSignatureBase64(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	v := sign.RegistryManagedKey{PrivateKey: priv}
	bad, _ := json.Marshal(map[string]string{"signature": "!!!not base64"})
	if err := v.Verify(context.Background(), "sha256:"+hashHex("x"), string(bad)); !errors.Is(err, sign.ErrSignatureInvalid) {
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
	if err := v.Verify(context.Background(), "not-a-valid-hash", string(env)); !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Errorf("err = %v", err)
	}
}

// Verify rejects when the envelope's signature does not check out.
func TestRegistryManagedKey_VerifyTamperedSignature(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer := sign.RegistryManagedKey{PrivateKey: priv}
	hash := "sha256:" + hashHex("body")
	env, _ := signer.Sign(context.Background(), hash)
	// Modify the signature byte string.
	var parsed map[string]string
	_ = json.Unmarshal([]byte(env), &parsed)
	parsed["signature"] = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	tampered, _ := json.Marshal(parsed)
	if err := signer.Verify(context.Background(), hash, string(tampered)); !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Errorf("err = %v", err)
	}
}

// PublicKeyFromBase64 round-trips a generated public key and the decoded key
// verifies an envelope the matching private key produced. This is the
// consumer-side path: the registry publishes its base64 public key, the
// consumer decodes it and constructs a RegistryManagedKey for Verify.
func TestPublicKeyFromBase64_RoundTrip(t *testing.T) {
	t.Parallel()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(pub)
	decoded, err := sign.PublicKeyFromBase64(encoded)
	if err != nil {
		t.Fatalf("PublicKeyFromBase64: %v", err)
	}
	if !decoded.Equal(pub) {
		t.Fatalf("decoded key != original")
	}
	hash := "sha256:" + hashHex("body")
	env, err := sign.RegistryManagedKey{PrivateKey: priv}.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := (sign.RegistryManagedKey{PublicKey: decoded}).Verify(context.Background(), hash, env); err != nil {
		t.Errorf("Verify with decoded public key: %v", err)
	}
}

// PublicKeyFromBase64 rejects malformed base64 and wrong-length keys so a
// misconfigured PODIUM_SIGNATURE_VERIFY_KEY fails loudly at startup rather than
// silently producing a verifier that rejects every signature.
func TestPublicKeyFromBase64_Rejects(t *testing.T) {
	t.Parallel()
	if _, err := sign.PublicKeyFromBase64("!!!not base64"); err == nil {
		t.Error("malformed base64 accepted")
	}
	// 16 bytes is valid base64 but the wrong length for an Ed25519 public key.
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	if _, err := sign.PublicKeyFromBase64(short); err == nil {
		t.Error("wrong-length key accepted")
	}
}

func hashHex(s string) string {
	return "" + // placeholder; concrete hash bytes don't matter for envelope tests
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
}
