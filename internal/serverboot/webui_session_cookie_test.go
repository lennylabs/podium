package serverboot

// Unit coverage for the §6.3.3 second accepted credential location: the
// oidcJWTVerifier cookie branch, the header-wins precedence rule, and the
// anonymity rule's browser-flow conjunct. This file owns the resolution
// contract, because the one function it drives is what the meta-tool identity
// middleware and the §7.3.1 layer endpoint both use. What each consumer does
// with a returned error is pinned by the expired-session cases.

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/registry/server"
)

func cookieRequest(cookies ...*http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/v1/load_domain", nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	return r
}

func sessionCookie(value string) *http.Cookie {
	return &http.Cookie{Name: server.CookieSession, Value: value}
}

// Spec: §6.3.3 — the same token resolves an identical identity in either
// accepted location, because both verify through the same OIDCVerifier
// against the issuer JWKS for the same aud.
func TestOIDCJWTVerifier_CookieResolvesLikeTheHeader(t *testing.T) {
	t.Parallel()
	idp := newJWKSIdP(t)
	verifier := identity.NewOIDCVerifier(idp.issuer(), gwAudiences, 0)
	mapping, err := identity.ParseIdpGroupMapping("idp-eng=engineering")
	if err != nil {
		t.Fatalf("ParseIdpGroupMapping: %v", err)
	}
	verify := oidcJWTVerifier(verifier, "", mapping, true)

	claims := gwClaims(idp.issuer(), "alice@acme.com", []string{"idp-eng"})
	claims["org_id"] = "acme"
	token := idp.sign(t, claims)

	header := httptest.NewRequest(http.MethodGet, "/v1/load_domain", nil)
	header.Header.Set("Authorization", "Bearer "+token)
	fromHeader, err := verify(header)
	if err != nil {
		t.Fatalf("header token: %v", err)
	}
	fromCookie, err := verify(cookieRequest(sessionCookie(token)))
	if err != nil {
		t.Fatalf("cookie token: %v", err)
	}
	if fromHeader.Sub != fromCookie.Sub || fromHeader.OrgID != fromCookie.OrgID {
		t.Errorf("cookie identity %+v differs from header identity %+v", fromCookie, fromHeader)
	}
	if len(fromCookie.Groups) != 1 || fromCookie.Groups[0] != "engineering" {
		t.Errorf("cookie groups = %v, want the mapped group", fromCookie.Groups)
	}
	if !fromCookie.IsAuthenticated {
		t.Error("the cookie caller resolved unauthenticated")
	}
}

// Spec: §6.3.3 — the registry reads the configured token header first, and a
// bearer credential found there decides the request's identity. The two
// locations are never merged.
func TestOIDCJWTVerifier_HeaderWinsOverCookie(t *testing.T) {
	t.Parallel()
	idp := newJWKSIdP(t)
	verify := oidcJWTVerifier(identity.NewOIDCVerifier(idp.issuer(), gwAudiences, 0), "", nil, true)

	r := cookieRequest(sessionCookie(idp.sign(t, gwClaims(idp.issuer(), "bob@acme.com", nil))))
	r.Header.Set("Authorization", "Bearer "+idp.sign(t, gwClaims(idp.issuer(), "alice@acme.com", nil)))
	id, err := verify(r)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if id.Sub != "alice@acme.com" {
		t.Errorf("subject = %q, want the header's subject", id.Sub)
	}
}

// Spec: §6.3.3 — a cookie past the token's exp returns
// identity.ErrTokenExpired, which the meta-tool identity middleware maps to
// 401 auth.token_expired.
func TestOIDCJWTVerifier_ExpiredCookieReportsExpiry(t *testing.T) {
	t.Parallel()
	idp := newJWKSIdP(t)
	verify := oidcJWTVerifier(identity.NewOIDCVerifier(idp.issuer(), gwAudiences, 0), "", nil, true)

	claims := gwClaims(idp.issuer(), "alice@acme.com", nil)
	claims["exp"] = time.Now().Add(-time.Hour).Unix()
	_, err := verify(cookieRequest(sessionCookie(idp.sign(t, claims))))
	if err == nil {
		t.Fatal("an expired cookie resolved without an error")
	}
	if !errors.Is(err, identity.ErrTokenExpired) {
		t.Errorf("err = %v, want identity.ErrTokenExpired", err)
	}
}

// Spec: §6.3.3 — the cookie branch is gated on the browser-flow enablement
// field alone, so a registry that boots with the flow disabled reads no
// cookie and a stale __Host-podium_session sent to it resolves anonymous.
func TestOIDCJWTVerifier_CookieIgnoredWhenFlowDisabled(t *testing.T) {
	t.Parallel()
	idp := newJWKSIdP(t)
	verify := oidcJWTVerifier(identity.NewOIDCVerifier(idp.issuer(), gwAudiences, 0), "", nil, false)

	id, err := verify(cookieRequest(sessionCookie(idp.sign(t, gwClaims(idp.issuer(), "alice@acme.com", nil)))))
	if err != nil {
		t.Fatalf("a stale cookie must resolve anonymous, got err %v", err)
	}
	if id.IsAuthenticated {
		t.Errorf("identity = %+v, want anonymous", id)
	}
}

// Spec: §6.3.3 — the fail-closed rule applies in either accepted location, so
// while the issuer JWKS is unreachable a cookie request is anonymous rather
// than rejected.
func TestOIDCJWTVerifier_CookieAnonymousWhileJWKSUnavailable(t *testing.T) {
	t.Parallel()
	const iss = "http://127.0.0.1:1"
	verify := oidcJWTVerifier(identity.NewOIDCVerifier(iss, []string{"aud"}, 0), "", nil, true)

	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": iss, "aud": "aud", "sub": "alice@acme.com",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tok.Header["kid"] = "k1"
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	id, err := verify(cookieRequest(sessionCookie(raw)))
	if err != nil {
		t.Fatalf("key-set-unavailable must be anonymous, got err %v", err)
	}
	if id.IsAuthenticated {
		t.Errorf("identity = %+v, want anonymous", id)
	}
}

// Spec: §6.3.3 — a request presenting neither location resolves anonymous
// with the flow enabled, which is the anonymity rule's conjunct.
func TestOIDCJWTVerifier_NoCredentialIsAnonymousWithFlowEnabled(t *testing.T) {
	t.Parallel()
	idp := newJWKSIdP(t)
	verify := oidcJWTVerifier(identity.NewOIDCVerifier(idp.issuer(), gwAudiences, 0), "", nil, true)
	id, err := verify(cookieRequest())
	if err != nil {
		t.Fatalf("no credential must be anonymous, got err %v", err)
	}
	if id.IsAuthenticated {
		t.Errorf("identity = %+v, want anonymous", id)
	}
}
