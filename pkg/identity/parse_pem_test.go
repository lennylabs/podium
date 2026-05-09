package identity_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
)

// Spec: §6.3.2 — `podium admin runtime register` accepts a PKIX
// public key PEM block and an algorithm; the parser hands back the
// strict type needed by the JWT verifier (rsa.PublicKey for RS*,
// ecdsa.PublicKey for ES*, ed25519.PublicKey for EdDSA).
func TestParsePublicKeyPEM_RSA(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pemBytes := mustPEM(t, &priv.PublicKey)
	got, err := identity.ParsePublicKeyPEM(string(pemBytes), "RS256")
	if err != nil {
		t.Fatalf("ParsePublicKeyPEM: %v", err)
	}
	if _, ok := got.(*rsa.PublicKey); !ok {
		t.Errorf("got %T, want *rsa.PublicKey", got)
	}
}

func TestParsePublicKeyPEM_ECDSA(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pemBytes := mustPEM(t, &priv.PublicKey)
	got, err := identity.ParsePublicKeyPEM(string(pemBytes), "ES256")
	if err != nil {
		t.Fatalf("ParsePublicKeyPEM: %v", err)
	}
	if _, ok := got.(*ecdsa.PublicKey); !ok {
		t.Errorf("got %T, want *ecdsa.PublicKey", got)
	}
}

func TestParsePublicKeyPEM_Ed25519(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pemBytes := mustPEM(t, pub)
	got, err := identity.ParsePublicKeyPEM(string(pemBytes), "EdDSA")
	if err != nil {
		t.Fatalf("ParsePublicKeyPEM: %v", err)
	}
	if _, ok := got.(ed25519.PublicKey); !ok {
		t.Errorf("got %T, want ed25519.PublicKey", got)
	}
}

func TestParsePublicKeyPEM_AlgorithmMismatch(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	pemBytes := mustPEM(t, &priv.PublicKey)
	_, err := identity.ParsePublicKeyPEM(string(pemBytes), "ES256")
	if err == nil || !strings.Contains(err.Error(), "want ECDSA") {
		t.Errorf("err = %v, want algorithm-mismatch", err)
	}
}

func mustPEM(t *testing.T, key any) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}
