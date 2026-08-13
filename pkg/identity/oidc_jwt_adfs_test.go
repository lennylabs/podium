package identity

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AD FS compatibility: access tokens carry the federation-service identifier
// as iss (the discovery document's access_token_issuer), no sub claim (a
// stable idsub instead), group membership under the full claim-type URI, and
// a plain string when the user is in exactly one group.

const adfsGroupsClaim = "http://schemas.microsoft.com/ws/2008/06/identity/claims/groups"

func adfsClaims(iss, aud string) jwt.MapClaims {
	return jwt.MapClaims{
		"iss":   iss,
		"aud":   aud,
		"idsub": "dpytRNwI-stable-pairwise-id",
		"email": "alice@acme.com",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	}
}

func TestOIDCVerifier_AcceptsAccessTokenIssuer(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)
	const fsIdentifier = "http://adfs.acme.example/adfs/services/trust"
	idp.setAccessTokenIssuer(fsIdentifier)

	v := NewOIDCVerifier(idp.issuer(), testAudience, 300*time.Second)
	// Prime fetches the discovery document, which carries the
	// access_token_issuer; the boot path always primes before serving.
	if err := v.Prime(); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	claims := validClaims(fsIdentifier, testAudience)
	id, err := v.Verify(idp.sign(t, "key-1", claims))
	if err != nil {
		t.Fatalf("Verify with iss=access_token_issuer: %v", err)
	}
	if id.Sub != "alice@acme.com" {
		t.Errorf("Sub = %q", id.Sub)
	}

	// The configured issuer stays accepted.
	if _, err := v.Verify(idp.sign(t, "key-1", validClaims(idp.issuer(), testAudience))); err != nil {
		t.Errorf("Verify with iss=issuer: %v", err)
	}

	// An iss matching neither value stays rejected.
	_, err = v.Verify(idp.sign(t, "key-1", validClaims("https://other.example", testAudience)))
	if err == nil || !strings.Contains(err.Error(), "access_token_issuer") {
		t.Errorf("Verify with foreign iss = %v, want a rejection naming access_token_issuer", err)
	}
}

func TestOIDCVerifier_SubjectClaimOverride(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)

	// Without the override, a token with no sub is rejected.
	v := NewOIDCVerifier(idp.issuer(), testAudience, 300*time.Second)
	_, err := v.Verify(idp.sign(t, "key-1", adfsClaims(idp.issuer(), testAudience)))
	if err == nil || !strings.Contains(err.Error(), "sub claim missing") {
		t.Fatalf("Verify without override = %v, want sub-claim-missing", err)
	}

	// With the override, the configured claim becomes the subject.
	v2 := NewOIDCVerifier(idp.issuer(), testAudience, 300*time.Second)
	v2.SetSubjectClaim("idsub")
	id, err := v2.Verify(idp.sign(t, "key-1", adfsClaims(idp.issuer(), testAudience)))
	if err != nil {
		t.Fatalf("Verify with subject claim: %v", err)
	}
	if id.Sub != "dpytRNwI-stable-pairwise-id" {
		t.Errorf("Sub = %q, want the idsub value", id.Sub)
	}

	// The configured claim takes precedence when both are present.
	both := adfsClaims(idp.issuer(), testAudience)
	both["sub"] = "oidc-standard-sub"
	id, err = v2.Verify(idp.sign(t, "key-1", both))
	if err != nil || id.Sub != "dpytRNwI-stable-pairwise-id" {
		t.Errorf("Sub with both claims = %q, %v; want the configured claim's value", id.Sub, err)
	}

	// sub is the fallback when the configured claim is absent.
	fallback := adfsClaims(idp.issuer(), testAudience)
	delete(fallback, "idsub")
	fallback["sub"] = "oidc-standard-sub"
	id, err = v2.Verify(idp.sign(t, "key-1", fallback))
	if err != nil || id.Sub != "oidc-standard-sub" {
		t.Errorf("Sub with only sub = %q, %v; want the sub fallback", id.Sub, err)
	}

	// Both absent is an error naming the configured claim.
	neither := adfsClaims(idp.issuer(), testAudience)
	delete(neither, "idsub")
	_, err = v2.Verify(idp.sign(t, "key-1", neither))
	if err == nil || !strings.Contains(err.Error(), `"idsub"`) {
		t.Errorf("Verify with neither claim = %v, want an error naming the configured claim", err)
	}
}

func TestOIDCVerifier_GroupsClaimOverride(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)
	v := NewOIDCVerifier(idp.issuer(), testAudience, 300*time.Second)
	v.SetGroupsClaim(adfsGroupsClaim)

	// Single membership serializes as a plain string in AD FS tokens.
	one := validClaims(idp.issuer(), testAudience)
	delete(one, "groups")
	one[adfsGroupsClaim] = "oidc.podium.common"
	id, err := v.Verify(idp.sign(t, "key-1", one))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(id.Groups) != 1 || id.Groups[0] != "oidc.podium.common" {
		t.Errorf("Groups from string claim = %v", id.Groups)
	}

	// Multiple memberships serialize as an array.
	many := validClaims(idp.issuer(), testAudience)
	delete(many, "groups")
	many[adfsGroupsClaim] = []any{"oidc.podium.common", "oidc.podium.advanced"}
	id, err = v.Verify(idp.sign(t, "key-1", many))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(id.Groups) != 2 || id.Groups[1] != "oidc.podium.advanced" {
		t.Errorf("Groups from array claim = %v", id.Groups)
	}

	// With the override set, the standard groups claim is not consulted.
	standard := validClaims(idp.issuer(), testAudience)
	id, err = v.Verify(idp.sign(t, "key-1", standard))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(id.Groups) != 0 {
		t.Errorf("Groups without the override claim = %v, want none", id.Groups)
	}
}

func TestOIDCVerifier_StringGroupsUnderDefaultClaim(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)
	v := NewOIDCVerifier(idp.issuer(), testAudience, 300*time.Second)

	claims := validClaims(idp.issuer(), testAudience)
	claims["groups"] = "finance"
	id, err := v.Verify(idp.sign(t, "key-1", claims))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(id.Groups) != 1 || id.Groups[0] != "finance" {
		t.Errorf("Groups from string claim = %v, want [finance]", id.Groups)
	}
}
