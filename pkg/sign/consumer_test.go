package sign

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// Spec: §4.7.9 — under never ResolveVerifier reads no material, so malformed
// key arguments still resolve to a nil verifier and no error.
func TestResolveVerifier_NeverReadsNoMaterial(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "absent.key")
	p, err := ResolveVerifier(PolicyNever, "!!!not base64", missing)
	if err != nil || p != nil {
		t.Fatalf("ResolveVerifier(never) = %v, %v; want nil, nil", p, err)
	}
}

// Spec: §4.7.9, §6.9 — a non-empty verifyKey is authoritative, so a malformed
// list refuses with config.signature_provider_unavailable naming the variable
// even when a usable key file exists.
func TestResolveVerifier_MalformedVerifyKeyIsAuthoritative(t *testing.T) {
	t.Parallel()
	pub, priv := genKey(t)
	path := writeKeyFile(t, registryKeyFileBody(priv, pub))
	_, err := ResolveVerifier(PolicyAlways, b64(pub)+",!!!bad", path)
	if err == nil || !strings.HasPrefix(err.Error(), "config.signature_provider_unavailable: ") ||
		!strings.Contains(err.Error(), "PODIUM_SIGNATURE_VERIFY_KEY") ||
		!strings.HasSuffix(err.Error(), "; supply the verification material or set PODIUM_VERIFY_SIGNATURES=never") {
		t.Fatalf("err = %v, want config.signature_provider_unavailable naming PODIUM_SIGNATURE_VERIFY_KEY", err)
	}
}

// Spec: §4.7.9 — an empty verifyKey falls through to the key file at keyPath,
// and a file with a public: line and a verify: line yields a registry-managed
// verifier that accepts a signature under each key.
func TestResolveVerifier_KeyFileFallback(t *testing.T) {
	t.Parallel()
	pub, priv := genKey(t)
	retiredPub, retiredPriv := genKey(t)
	path := writeKeyFile(t, "public: "+b64(pub)+"\nverify: "+b64(retiredPub)+"\n")
	p, err := ResolveVerifier(PolicyAlways, "", path)
	if err != nil {
		t.Fatalf("ResolveVerifier: %v", err)
	}
	if _, ok := p.(RegistryManagedKey); !ok {
		t.Fatalf("verifier = %T, want RegistryManagedKey", p)
	}
	hash := "sha256:" + strings.Repeat("ab", 32)
	for i, k := range [][]byte{priv, retiredPriv} {
		env, err := RegistryManagedKey{PrivateKey: k}.Sign(context.Background(), hash)
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}
		if err := p.Verify(context.Background(), hash, env); err != nil {
			t.Errorf("verify under key %d: %v", i, err)
		}
	}
}

// Spec: §4.7.9, §6.9 — with no material the resolution refuses with
// config.signature_provider_unavailable, naming both sources it tried.
func TestResolveVerifier_NoMaterialRefuses(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "absent.key")
	_, err := ResolveVerifier(PolicyAlways, "", missing)
	if err == nil || !strings.HasPrefix(err.Error(), "config.signature_provider_unavailable: ") ||
		!strings.Contains(err.Error(), "PODIUM_SIGNATURE_VERIFY_KEY") ||
		!strings.Contains(err.Error(), "PODIUM_SIGN_KEY_PATH") {
		t.Fatalf("err = %v, want config.signature_provider_unavailable naming both sources", err)
	}
}
