package identity_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
)

func encodePKIX(t *testing.T, key any) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func TestParsePublicKeyPEM_NoPEMBlock(t *testing.T) {
	t.Parallel()
	_, err := identity.ParsePublicKeyPEM("not a PEM", "RS256")
	if err == nil || !strings.Contains(err.Error(), "no PEM block") {
		t.Errorf("err = %v", err)
	}
}

func TestParsePublicKeyPEM_RSAHappyPath(t *testing.T) {
	t.Parallel()
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	pemStr := encodePKIX(t, &priv.PublicKey)
	got, err := identity.ParsePublicKeyPEM(pemStr, "RS256")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, ok := got.(*rsa.PublicKey); !ok {
		t.Errorf("type = %T, want *rsa.PublicKey", got)
	}
}

func TestParsePublicKeyPEM_ECDSAHappyPath(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pemStr := encodePKIX(t, &priv.PublicKey)
	got, err := identity.ParsePublicKeyPEM(pemStr, "ES256")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, ok := got.(*ecdsa.PublicKey); !ok {
		t.Errorf("type = %T, want *ecdsa.PublicKey", got)
	}
}

func TestParsePublicKeyPEM_Ed25519HappyPath(t *testing.T) {
	t.Parallel()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pemStr := encodePKIX(t, pub)
	got, err := identity.ParsePublicKeyPEM(pemStr, "EdDSA")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, ok := got.(ed25519.PublicKey); !ok {
		t.Errorf("type = %T, want ed25519.PublicKey", got)
	}
}

func TestParsePublicKeyPEM_AlgorithmTypeMismatch(t *testing.T) {
	t.Parallel()
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	pemStr := encodePKIX(t, &priv.PublicKey)
	// RSA key with ES256 algorithm should fail.
	_, err := identity.ParsePublicKeyPEM(pemStr, "ES256")
	if !errors.Is(err, identity.ErrUnsupportedKey) {
		t.Errorf("err = %v, want ErrUnsupportedKey", err)
	}
	_, err = identity.ParsePublicKeyPEM(pemStr, "EdDSA")
	if !errors.Is(err, identity.ErrUnsupportedKey) {
		t.Errorf("err = %v, want ErrUnsupportedKey", err)
	}
}

func TestParsePublicKeyPEM_UnknownAlgorithm(t *testing.T) {
	t.Parallel()
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	pemStr := encodePKIX(t, &priv.PublicKey)
	_, err := identity.ParsePublicKeyPEM(pemStr, "BOGUS-ALG")
	if !errors.Is(err, identity.ErrUnsupportedKey) {
		t.Errorf("err = %v", err)
	}
}

func TestParsePublicKeyPEM_PKCS1Fallback(t *testing.T) {
	t.Parallel()
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	der := x509.MarshalPKCS1PublicKey(&priv.PublicKey)
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: der}))
	got, err := identity.ParsePublicKeyPEM(pemStr, "RS256")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, ok := got.(*rsa.PublicKey); !ok {
		t.Errorf("PKCS1 fallback returned %T", got)
	}
}

func TestParsePublicKeyPEM_GarbledBlockErrors(t *testing.T) {
	t.Parallel()
	bogus := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("garbage")}))
	_, err := identity.ParsePublicKeyPEM(bogus, "RS256")
	if err == nil {
		t.Errorf("expected error for garbled DER")
	}
}
