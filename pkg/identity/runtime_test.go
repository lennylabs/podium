package identity_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lennylabs/podium/pkg/identity"
)

func newRSAKeyPair(t *testing.T) (*rsa.PrivateKey, *rsa.PublicKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	return priv, &priv.PublicKey
}

func signJWT(t *testing.T, priv *rsa.PrivateKey, alg jwt.SigningMethod, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(alg, claims)
	signed, err := token.SignedString(priv)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return signed
}

// Spec: §6.3.2 Runtime Trust Model — a JWT signed by a registered
// runtime key verifies, and claims (sub, org_id, groups, email) flow
// into the resulting Identity.
func TestJWTVerifier_AcceptsRegisteredKey(t *testing.T) {
	t.Parallel()
	priv, pub := newRSAKeyPair(t)
	reg := identity.NewRuntimeKeyRegistry()
	if err := reg.Register(identity.RuntimeKey{
		Issuer: "managed-runtime-1", Algorithm: "RS256", Key: pub,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	verify := reg.JWTVerifier("https://podium.acme.com", nil)
	signed := signJWT(t, priv, jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":    "managed-runtime-1",
		"aud":    "https://podium.acme.com",
		"sub":    "joan",
		"org_id": "acme",
		"email":  "joan@acme.com",
		"groups": []string{"finance", "engineering"},
		"act":    "managed-runtime-1",
		"exp":    time.Now().Add(5 * time.Minute).Unix(),
	})

	id, err := verify(signed)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if id.Sub != "joan" || id.OrgID != "acme" || id.Email != "joan@acme.com" {
		t.Errorf("claims wrong: %+v", id)
	}
	if len(id.Groups) != 2 || id.Groups[0] != "finance" {
		t.Errorf("Groups = %v", id.Groups)
	}
	if !id.IsAuthenticated {
		t.Errorf("IsAuthenticated = false")
	}
}

// Spec: §6.3.2 / §6.10 — a token signed by an unregistered issuer
// fails with auth.untrusted_runtime.
// Matrix: §6.10 (auth.untrusted_runtime)
func TestJWTVerifier_RejectsUnregisteredIssuer(t *testing.T) {
	t.Parallel()
	priv, _ := newRSAKeyPair(t)
	reg := identity.NewRuntimeKeyRegistry()
	verify := reg.JWTVerifier("https://podium.acme.com", nil)
	signed := signJWT(t, priv, jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "unknown-runtime",
		"aud": "https://podium.acme.com",
		"sub": "joan",
		"act": "unknown-runtime",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	_, err := verify(signed)
	if !errors.Is(err, identity.ErrUntrustedRuntime) {
		t.Fatalf("got %v, want ErrUntrustedRuntime", err)
	}
}

// Spec: §6.3.2 — a token signed by a key that does not match the
// registered key fails verification.
func TestJWTVerifier_RejectsWrongSignature(t *testing.T) {
	t.Parallel()
	_, pub := newRSAKeyPair(t)
	otherPriv, _ := newRSAKeyPair(t)
	reg := identity.NewRuntimeKeyRegistry()
	if err := reg.Register(identity.RuntimeKey{
		Issuer: "rt", Algorithm: "RS256", Key: pub,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	verify := reg.JWTVerifier("https://podium.acme.com", nil)
	signed := signJWT(t, otherPriv, jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "rt", "aud": "https://podium.acme.com",
		"sub": "joan", "act": "rt",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	_, err := verify(signed)
	if !errors.Is(err, identity.ErrUntrustedRuntime) {
		t.Fatalf("got %v, want ErrUntrustedRuntime", err)
	}
}

// Spec: §6.3.2 / §6.10 — an expired token fails with auth.token_expired.
// Matrix: §6.10 (auth.token_expired)
func TestJWTVerifier_RejectsExpired(t *testing.T) {
	t.Parallel()
	priv, pub := newRSAKeyPair(t)
	reg := identity.NewRuntimeKeyRegistry()
	if err := reg.Register(identity.RuntimeKey{
		Issuer: "rt", Algorithm: "RS256", Key: pub,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	verify := reg.JWTVerifier("https://podium.acme.com", nil)
	signed := signJWT(t, priv, jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "rt", "aud": "https://podium.acme.com",
		"sub": "joan", "act": "rt",
		"exp": time.Now().Add(-1 * time.Minute).Unix(),
	})
	_, err := verify(signed)
	if !errors.Is(err, identity.ErrTokenExpired) {
		t.Fatalf("got %v, want ErrTokenExpired", err)
	}
}

// Spec: §6.3.2 — wrong audience is rejected.
func TestJWTVerifier_RejectsWrongAudience(t *testing.T) {
	t.Parallel()
	priv, pub := newRSAKeyPair(t)
	reg := identity.NewRuntimeKeyRegistry()
	if err := reg.Register(identity.RuntimeKey{
		Issuer: "rt", Algorithm: "RS256", Key: pub,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	verify := reg.JWTVerifier("https://podium.acme.com", nil)
	signed := signJWT(t, priv, jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "rt", "aud": "https://something-else",
		"sub": "joan", "act": "rt",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	_, err := verify(signed)
	if !errors.Is(err, identity.ErrUntrustedRuntime) {
		t.Fatalf("got %v, want ErrUntrustedRuntime", err)
	}
}

// Spec: §6.3.2 — sub claim is required.
func TestJWTVerifier_RejectsMissingSub(t *testing.T) {
	t.Parallel()
	priv, pub := newRSAKeyPair(t)
	reg := identity.NewRuntimeKeyRegistry()
	if err := reg.Register(identity.RuntimeKey{
		Issuer: "rt", Algorithm: "RS256", Key: pub,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	verify := reg.JWTVerifier("https://podium.acme.com", nil)
	signed := signJWT(t, priv, jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "rt", "aud": "https://podium.acme.com",
		"act": "rt",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	_, err := verify(signed)
	if !errors.Is(err, identity.ErrUntrustedRuntime) {
		t.Fatalf("got %v, want ErrUntrustedRuntime", err)
	}
	if !strings.Contains(err.Error(), "sub") {
		t.Errorf("expected error message to mention sub, got %v", err)
	}
}

// Spec: §6.3.2 — act claim is required.
func TestJWTVerifier_RejectsMissingAct(t *testing.T) {
	t.Parallel()
	priv, pub := newRSAKeyPair(t)
	reg := identity.NewRuntimeKeyRegistry()
	if err := reg.Register(identity.RuntimeKey{
		Issuer: "rt", Algorithm: "RS256", Key: pub,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	verify := reg.JWTVerifier("https://podium.acme.com", nil)
	signed := signJWT(t, priv, jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "rt", "aud": "https://podium.acme.com",
		"sub": "joan",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	_, err := verify(signed)
	if !errors.Is(err, identity.ErrUntrustedRuntime) {
		t.Fatalf("got %v, want ErrUntrustedRuntime", err)
	}
}

// Spec: §6.3.2 — exp claim is required.
func TestJWTVerifier_RejectsMissingExp(t *testing.T) {
	t.Parallel()
	priv, pub := newRSAKeyPair(t)
	reg := identity.NewRuntimeKeyRegistry()
	if err := reg.Register(identity.RuntimeKey{
		Issuer: "rt", Algorithm: "RS256", Key: pub,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	verify := reg.JWTVerifier("https://podium.acme.com", nil)
	signed := signJWT(t, priv, jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "rt", "aud": "https://podium.acme.com",
		"sub": "joan", "act": "rt",
	})
	_, err := verify(signed)
	if err == nil {
		t.Errorf("expected error for missing exp")
	}
}

// Spec: §9.1 — the registry rejects unsupported key types at register
// time so misconfiguration is caught early rather than at request time.
func TestRuntimeKeyRegistry_RejectsUnsupportedKey(t *testing.T) {
	t.Parallel()
	reg := identity.NewRuntimeKeyRegistry()
	err := reg.Register(identity.RuntimeKey{
		Issuer: "rt", Algorithm: "HS256", Key: "not-a-key-type",
	})
	if err == nil {
		t.Errorf("expected error for unsupported key type")
	}
}

// Spec: §6.3.2 — an Ed25519 key registered is also accepted.
func TestRuntimeKeyRegistry_AcceptsEd25519(t *testing.T) {
	t.Parallel()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	reg := identity.NewRuntimeKeyRegistry()
	if err := reg.Register(identity.RuntimeKey{
		Issuer: "rt", Algorithm: "EdDSA", Key: pub,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	verify := reg.JWTVerifier("https://podium.acme.com", nil)
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": "rt", "aud": "https://podium.acme.com",
		"sub": "joan", "act": "rt",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	signed, err := token.SignedString(priv)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	id, err := verify(signed)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if id.Sub != "joan" {
		t.Errorf("Sub = %q", id.Sub)
	}
}
