package identity

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// testIdP is a stub OIDC issuer that serves an OIDC discovery document and a
// JWKS built from one or more RSA keys, supporting key rotation.
type testIdP struct {
	srv  *httptest.Server
	mu   sync.Mutex
	keys map[string]*rsa.PrivateKey // kid -> signing key
}

func newTestIdP(t *testing.T) *testIdP {
	t.Helper()
	idp := &testIdP{keys: map[string]*rsa.PrivateKey{}}
	idp.addKey(t, "key-1")
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":   base,
			"jwks_uri": base + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(idp.jwksJSON()))
	})
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

func (i *testIdP) addKey(t *testing.T, kid string) *rsa.PrivateKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	i.mu.Lock()
	i.keys[kid] = priv
	i.mu.Unlock()
	return priv
}

// rotate replaces the entire key set with a single new key under kid.
func (i *testIdP) rotate(t *testing.T, kid string) *rsa.PrivateKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	i.mu.Lock()
	i.keys = map[string]*rsa.PrivateKey{kid: priv}
	i.mu.Unlock()
	return priv
}

func (i *testIdP) jwksJSON() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	keys := make([]map[string]any, 0, len(i.keys))
	for kid, priv := range i.keys {
		pub := &priv.PublicKey
		keys = append(keys, map[string]any{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": kid,
			"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		})
	}
	b, _ := json.Marshal(map[string]any{"keys": keys})
	return string(b)
}

func (i *testIdP) issuer() string { return i.srv.URL }

func (i *testIdP) sign(t *testing.T, kid string, claims jwt.MapClaims) string {
	t.Helper()
	i.mu.Lock()
	priv := i.keys[kid]
	i.mu.Unlock()
	if priv == nil {
		t.Fatalf("no key for kid %q", kid)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

// validClaims returns a well-formed claim set for the given issuer/audience.
func validClaims(iss, aud string) jwt.MapClaims {
	return jwt.MapClaims{
		"iss":    iss,
		"aud":    aud,
		"sub":    "alice@acme.com",
		"email":  "alice@acme.com",
		"org_id": "acme",
		"groups": []any{"engineering", "finance"},
		"scope":  "podium:read:finance/*",
		"exp":    time.Now().Add(time.Hour).Unix(),
		"iat":    time.Now().Unix(),
	}
}

const testAudience = "https://podium.acme.example"

func TestOIDCVerifier_ValidTokenMapsClaims(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)
	v := NewOIDCVerifier(idp.issuer(), testAudience, 300*time.Second)

	id, err := v.Verify(idp.sign(t, "key-1", validClaims(idp.issuer(), testAudience)))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !id.IsAuthenticated {
		t.Error("IsAuthenticated = false, want true")
	}
	if id.Sub != "alice@acme.com" {
		t.Errorf("Sub = %q", id.Sub)
	}
	if id.Email != "alice@acme.com" {
		t.Errorf("Email = %q", id.Email)
	}
	if id.OrgID != "acme" {
		t.Errorf("OrgID = %q", id.OrgID)
	}
	if len(id.Groups) != 2 || id.Groups[0] != "engineering" || id.Groups[1] != "finance" {
		t.Errorf("Groups = %v", id.Groups)
	}
	if len(id.Scopes) != 1 || id.Scopes[0] != "podium:read:finance/*" {
		t.Errorf("Scopes = %v", id.Scopes)
	}
}

func TestOIDCVerifier_Rejections(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)
	v := NewOIDCVerifier(idp.issuer(), testAudience, 300*time.Second)

	expired := validClaims(idp.issuer(), testAudience)
	expired["exp"] = time.Now().Add(-time.Hour).Unix()

	wrongIss := validClaims("https://evil.example", testAudience)
	wrongAud := validClaims(idp.issuer(), "https://other.example")
	noSub := validClaims(idp.issuer(), testAudience)
	delete(noSub, "sub")
	noExp := validClaims(idp.issuer(), testAudience)
	delete(noExp, "exp")

	tests := []struct {
		name    string
		token   string
		wantErr error // sentinel the error must wrap
	}{
		{"empty", "", ErrUntrustedToken},
		{"garbage", "not.a.jwt", ErrUntrustedToken},
		{"expired", idp.sign(t, "key-1", expired), ErrTokenExpired},
		{"wrong issuer", idp.sign(t, "key-1", wrongIss), ErrUntrustedToken},
		{"wrong audience", idp.sign(t, "key-1", wrongAud), ErrUntrustedToken},
		{"missing sub", idp.sign(t, "key-1", noSub), ErrUntrustedToken},
		{"missing exp", idp.sign(t, "key-1", noExp), ErrUntrustedToken},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := v.Verify(tc.token)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want errors.Is(_, %v)", err, tc.wantErr)
			}
		})
	}
}

func TestOIDCVerifier_BadSignatureRejected(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)
	v := NewOIDCVerifier(idp.issuer(), testAudience, 300*time.Second)

	// Sign with a key whose public half is not in the JWKS.
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, validClaims(idp.issuer(), testAudience))
	tok.Header["kid"] = "key-1" // claims a known kid but signs with a foreign key
	raw, err := tok.SignedString(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(raw); !errors.Is(err, ErrUntrustedToken) {
		t.Fatalf("err = %v, want ErrUntrustedToken", err)
	}
}

func TestOIDCVerifier_RejectsHS256(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)
	v := NewOIDCVerifier(idp.issuer(), testAudience, 300*time.Second)

	// An HS256 token signed with the RSA public modulus as the HMAC secret is
	// the classic algorithm-confusion attack; the verifier must reject HS*.
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims(idp.issuer(), testAudience))
	tok.Header["kid"] = "key-1"
	idp.mu.Lock()
	pub := idp.keys["key-1"].PublicKey.N.Bytes()
	idp.mu.Unlock()
	raw, err := tok.SignedString(pub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(raw); !errors.Is(err, ErrUntrustedToken) {
		t.Fatalf("err = %v, want ErrUntrustedToken (HS256 must be rejected)", err)
	}
}

func TestOIDCVerifier_UntrustedTokenCarriesIssuer(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)
	v := NewOIDCVerifier(idp.issuer(), testAudience, 300*time.Second)

	wrongAud := validClaims(idp.issuer(), "https://other.example")
	_, err := v.Verify(idp.sign(t, "key-1", wrongAud))
	var ute *UntrustedTokenError
	if !errors.As(err, &ute) {
		t.Fatalf("err = %T, want *UntrustedTokenError", err)
	}
	if ute.Issuer != idp.issuer() {
		t.Errorf("UntrustedTokenError.Issuer = %q, want %q", ute.Issuer, idp.issuer())
	}
}

func TestOIDCVerifier_KeyRotationRefreshesJWKS(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)
	// Long TTL: the refresh must be driven by the kid cache-miss, not expiry.
	v := NewOIDCVerifier(idp.issuer(), testAudience, time.Hour)

	// Prime the cache with key-1.
	if _, err := v.Verify(idp.sign(t, "key-1", validClaims(idp.issuer(), testAudience))); err != nil {
		t.Fatalf("initial verify: %v", err)
	}

	// Rotate to key-2. A token signed by key-2 presents an unknown kid, which
	// must force a JWKS refetch before rejection.
	idp.rotate(t, "key-2")
	if _, err := v.Verify(idp.sign(t, "key-2", validClaims(idp.issuer(), testAudience))); err != nil {
		t.Fatalf("verify after rotation: %v (kid cache-miss should have refreshed the JWKS)", err)
	}
}

func TestOIDCVerifier_CacheTTLRefetch(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)
	v := NewOIDCVerifier(idp.issuer(), testAudience, time.Minute)
	now := time.Now()
	v.clock = func() time.Time { return now }

	if _, err := v.Verify(idp.sign(t, "key-1", validClaims(idp.issuer(), testAudience))); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// Advance the clock past the TTL and rotate, keeping the same kid so the
	// only way to verify the new signature is a TTL-driven refetch.
	priv := idp.rotate(t, "key-1")
	now = now.Add(2 * time.Minute)
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, validClaims(idp.issuer(), testAudience))
	tok.Header["kid"] = "key-1"
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(raw); err != nil {
		t.Fatalf("verify after TTL: %v (stale cache should have refetched)", err)
	}
}

func TestOIDCVerifier_UnreachableIssuerFailsPrime(t *testing.T) {
	t.Parallel()
	// Port 1 on loopback is effectively always refused.
	v := NewOIDCVerifier("http://127.0.0.1:1", testAudience, 300*time.Second)
	if err := v.Prime(); err == nil {
		t.Fatal("Prime() = nil, want error for unreachable issuer")
	}
}

func TestOIDCVerifier_KeySetUnavailableIsDistinctSignal(t *testing.T) {
	t.Parallel()
	// Issuer unreachable and no cache primed: a present, well-formed token
	// cannot be verified, so Verify reports ErrKeySetUnavailable. The serverboot
	// wrapper maps this to an anonymous (public-only) caller rather than a 401,
	// per §6.3.3 "while the key set is unavailable at runtime, verification fails
	// closed and the request is anonymous".
	const iss = "http://127.0.0.1:1"
	v := NewOIDCVerifier(iss, testAudience, 300*time.Second)
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, validClaims(iss, testAudience))
	tok.Header["kid"] = "k"
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	_, err = v.Verify(raw)
	if !errors.Is(err, ErrKeySetUnavailable) {
		t.Fatalf("err = %v, want ErrKeySetUnavailable", err)
	}
	if errors.Is(err, ErrUntrustedToken) {
		t.Error("an unavailable key set must not be reported as ErrUntrustedToken")
	}
}

func TestOIDCVerifier_NoKidSingleKey(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)
	v := NewOIDCVerifier(idp.issuer(), testAudience, 300*time.Second)

	// A token with no kid header still verifies when the JWKS has a single key.
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, validClaims(idp.issuer(), testAudience))
	idp.mu.Lock()
	priv := idp.keys["key-1"]
	idp.mu.Unlock()
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(raw); err != nil {
		t.Fatalf("verify no-kid single-key: %v", err)
	}
}
