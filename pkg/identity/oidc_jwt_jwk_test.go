package identity

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TestOIDCVerifier_UnknownKidRejected covers keyForKID's "kid absent from the
// JWKS even after a refresh" path: a token whose kid the issuer never publishes
// is rejected rather than verified against an unrelated key.
func TestOIDCVerifier_UnknownKidRejected(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)
	v := NewOIDCVerifier(idp.issuer(), testAudience, time.Hour)
	// Prime the cache with key-1.
	if _, err := v.Verify(idp.sign(t, "key-1", validClaims(idp.issuer(), testAudience))); err != nil {
		t.Fatalf("prime: %v", err)
	}
	// Sign with the real key but claim a kid the JWKS never publishes; the
	// cache-miss refresh still cannot find it, so the token is rejected.
	idp.mu.Lock()
	priv := idp.keys["key-1"]
	idp.mu.Unlock()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, validClaims(idp.issuer(), testAudience))
	tok.Header["kid"] = "nonexistent-kid"
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(raw); !errors.Is(err, ErrUntrustedToken) {
		t.Fatalf("err = %v, want ErrUntrustedToken (unknown kid)", err)
	}
}

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// validRSAJWK returns a usable RSA jwk for building error-case variants.
func validRSAJWK(t *testing.T) jwk {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return jwk{Kty: "RSA", N: b64u(priv.N.Bytes()), E: b64u(big.NewInt(int64(priv.E)).Bytes())}
}

func TestJWK_PublicKey_RSA(t *testing.T) {
	t.Parallel()
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	k := jwk{Kty: "RSA", N: b64u(priv.N.Bytes()), E: b64u(big.NewInt(int64(priv.E)).Bytes())}
	got, err := k.publicKey()
	if err != nil {
		t.Fatalf("publicKey: %v", err)
	}
	rk, ok := got.(*rsa.PublicKey)
	if !ok || rk.N.Cmp(priv.N) != 0 || rk.E != priv.E {
		t.Errorf("got %T %+v, want the RSA public key", got, got)
	}
}

func TestJWK_PublicKey_EC(t *testing.T) {
	t.Parallel()
	for crv, curve := range map[string]elliptic.Curve{"P-256": elliptic.P256(), "P-384": elliptic.P384(), "P-521": elliptic.P521()} {
		priv, err := ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		k := jwk{Kty: "EC", Crv: crv, X: b64u(priv.X.Bytes()), Y: b64u(priv.Y.Bytes())}
		got, err := k.publicKey()
		if err != nil {
			t.Fatalf("%s publicKey: %v", crv, err)
		}
		ek, ok := got.(*ecdsa.PublicKey)
		if !ok || ek.X.Cmp(priv.X) != 0 || ek.Y.Cmp(priv.Y) != 0 {
			t.Errorf("%s: got %T, want the matching EC public key", crv, got)
		}
	}
}

func TestJWK_PublicKey_Ed25519(t *testing.T) {
	t.Parallel()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k := jwk{Kty: "OKP", Crv: "Ed25519", X: b64u(pub)}
	got, err := k.publicKey()
	if err != nil {
		t.Fatalf("publicKey: %v", err)
	}
	if ek, ok := got.(ed25519.PublicKey); !ok || !ek.Equal(pub) {
		t.Errorf("got %T, want the Ed25519 public key", got)
	}
}

func TestJWK_PublicKey_Errors(t *testing.T) {
	t.Parallel()
	rsaK := validRSAJWK(t)
	ecPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	validX := b64u(ecPriv.X.Bytes())
	validY := b64u(ecPriv.Y.Bytes())

	cases := []struct {
		name string
		k    jwk
	}{
		{"unsupported kty", jwk{Kty: "oct"}},
		{"rsa bad n", jwk{Kty: "RSA", N: "!!!not-base64", E: rsaK.E}},
		{"rsa bad e", jwk{Kty: "RSA", N: rsaK.N, E: "!!!not-base64"}},
		{"rsa e too small", jwk{Kty: "RSA", N: rsaK.N, E: b64u([]byte{1})}},
		{"ec unsupported crv", jwk{Kty: "EC", Crv: "P-192", X: validX, Y: validY}},
		{"ec bad x", jwk{Kty: "EC", Crv: "P-256", X: "!!!", Y: validY}},
		{"ec bad y", jwk{Kty: "EC", Crv: "P-256", X: validX, Y: "!!!"}},
		{"ec coordinate out of range", jwk{Kty: "EC", Crv: "P-256", X: b64u(make([]byte, 33)), Y: validY}},
		{"ec point not on curve", jwk{Kty: "EC", Crv: "P-256", X: b64u(big.NewInt(2).Bytes()), Y: b64u(big.NewInt(2).Bytes())}},
		{"okp unsupported crv", jwk{Kty: "OKP", Crv: "X25519", X: b64u(make([]byte, 32))}},
		{"okp bad x", jwk{Kty: "OKP", Crv: "Ed25519", X: "!!!"}},
		{"okp wrong length", jwk{Kty: "OKP", Crv: "Ed25519", X: b64u([]byte{1, 2, 3})}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.k.publicKey(); err == nil {
				t.Errorf("publicKey() = nil error, want an error for %s", tc.name)
			}
		})
	}
}

func TestECCurve(t *testing.T) {
	t.Parallel()
	for _, crv := range []string{"P-256", "P-384", "P-521"} {
		if _, _, err := ecCurve(crv); err != nil {
			t.Errorf("ecCurve(%q): %v", crv, err)
		}
	}
	if _, _, err := ecCurve("P-192"); err == nil {
		t.Error("ecCurve(P-192) = nil error, want unsupported")
	}
}

func TestNewOIDCVerifier_DefaultCacheTTL(t *testing.T) {
	t.Parallel()
	// A non-positive TTL falls back to the §13.12 default of 300 seconds.
	if v := NewOIDCVerifier("https://issuer.example", "aud", 0); v.cacheTTL != 300*time.Second {
		t.Errorf("cacheTTL = %v, want 300s", v.cacheTTL)
	}
	if v := NewOIDCVerifier("https://issuer.example", "aud", -5); v.cacheTTL != 300*time.Second {
		t.Errorf("negative TTL: cacheTTL = %v, want 300s", v.cacheTTL)
	}
}

func TestUntrustedTokenError_Message(t *testing.T) {
	t.Parallel()
	withIss := (&UntrustedTokenError{Issuer: "https://iss.example", Reason: "bad sig"}).Error()
	if !strings.Contains(withIss, "https://iss.example") || !strings.Contains(withIss, "bad sig") {
		t.Errorf("error with issuer = %q", withIss)
	}
	noIss := (&UntrustedTokenError{Reason: "malformed"}).Error()
	if strings.Contains(noIss, "https://") || !strings.Contains(noIss, "malformed") {
		t.Errorf("error without issuer = %q", noIss)
	}
}

func TestOIDCVerifier_AudienceUnsetFailsClosed(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)
	// Audience unset: even a well-formed token is rejected, because the required
	// aud claim cannot be verified.
	v := NewOIDCVerifier(idp.issuer(), "", 300*time.Second)
	_, err := v.Verify(idp.sign(t, "key-1", validClaims(idp.issuer(), "anything")))
	if err == nil || !strings.Contains(err.Error(), "audience is not configured") {
		t.Fatalf("err = %v, want an audience-not-configured rejection", err)
	}
}

// TestOIDCVerifier_PrimeFetchErrors covers the discovery-document and JWKS fetch
// failure branches (a non-200, a missing jwks_uri, malformed JSON, and a key set
// with no usable keys) via Prime, which exercises discoverJWKSURI and fetchJWKS.
func TestOIDCVerifier_PrimeFetchErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"discovery 500", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }},
		{"discovery missing jwks_uri", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"issuer":"x"}`))
		}},
		{"discovery malformed json", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{not json`))
		}},
		{"jwks malformed json", func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "openid-configuration") {
				_, _ = w.Write([]byte(`{"jwks_uri":"http://` + r.Host + `/jwks"}`))
				return
			}
			_, _ = w.Write([]byte(`{not json`))
		}},
		{"jwks no usable keys", func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "openid-configuration") {
				_, _ = w.Write([]byte(`{"jwks_uri":"http://` + r.Host + `/jwks"}`))
				return
			}
			_, _ = w.Write([]byte(`{"keys":[{"kty":"oct","kid":"x"}]}`))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			v := NewOIDCVerifier(srv.URL, "aud", 300*time.Second)
			if err := v.Prime(); err == nil {
				t.Errorf("Prime() = nil error, want an error for %s", tc.name)
			}
		})
	}
}
