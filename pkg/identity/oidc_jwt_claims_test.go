package identity

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// adfsSubjectClaim and adfsGroupsClaim are the claim names an AD FS farm emits
// in place of "sub" and "groups": a pairwise subject under its own key, and
// group membership under the full claim-type URI an issuance rule authored
// without a short name produces (§6.3.3).
const (
	adfsSubjectClaim = "idsub"
	adfsGroupsClaim  = "http://schemas.microsoft.com/ws/2008/06/identity/claims/groups"
)

// Spec: §6.3.3 — WithSubjectClaim and WithGroupsClaim name the claims the
// oidc-jwt verifier reads for the caller's subject and group membership, and a
// verifier constructed without them reads "sub" and "groups".
func TestOIDCVerifier_ConfiguredClaimNames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		opts       []OIDCOption
		mutate     func(jwt.MapClaims)
		wantSub    string
		wantGroups []string
	}{
		{
			name:       "no options reproduces the default derivation",
			wantSub:    "alice@acme.com",
			wantGroups: []string{"engineering", "finance"},
		},
		{
			name: "configured subject and group claims",
			opts: []OIDCOption{WithSubjectClaim(adfsSubjectClaim), WithGroupsClaim(adfsGroupsClaim)},
			mutate: func(c jwt.MapClaims) {
				delete(c, "sub")
				delete(c, "groups")
				c[adfsSubjectClaim] = "alice-pairwise"
				c[adfsGroupsClaim] = []any{"acme-finance", "acme-eng"}
			},
			wantSub:    "alice-pairwise",
			wantGroups: []string{"acme-finance", "acme-eng"},
		},
		{
			// A caller in exactly one group receives the claim as a plain
			// string. The whole value is one group name.
			name: "configured group claim in the single-string form",
			opts: []OIDCOption{WithGroupsClaim(adfsGroupsClaim)},
			mutate: func(c jwt.MapClaims) {
				delete(c, "groups")
				c[adfsGroupsClaim] = "acme-finance"
			},
			wantSub:    "alice@acme.com",
			wantGroups: []string{"acme-finance"},
		},
		{
			name:       "default group claim in the single-string form",
			mutate:     func(c jwt.MapClaims) { c["groups"] = "finance" },
			wantSub:    "alice@acme.com",
			wantGroups: []string{"finance"},
		},
		{
			// The named claim is read alone, so the token's own "groups" claim
			// contributes nothing.
			name:    "configured group claim absent from the token",
			opts:    []OIDCOption{WithGroupsClaim(adfsGroupsClaim)},
			wantSub: "alice@acme.com",
		},
		{
			name:       "empty option names leave the defaults in place",
			opts:       []OIDCOption{WithSubjectClaim(""), WithGroupsClaim("")},
			wantSub:    "alice@acme.com",
			wantGroups: []string{"engineering", "finance"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			idp := newTestIdP(t)
			v := NewOIDCVerifier(idp.issuer(), testAudience, 300*time.Second, tc.opts...)

			claims := validClaims(idp.issuer(), testAudience)
			if tc.mutate != nil {
				tc.mutate(claims)
			}
			id, err := v.Verify(idp.sign(t, "key-1", claims))
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if id.Sub != tc.wantSub || !id.IsAuthenticated {
				t.Errorf("Sub = %q, IsAuthenticated = %v, want %q and true", id.Sub, id.IsAuthenticated, tc.wantSub)
			}
			if !slices.Equal(id.Groups, tc.wantGroups) {
				t.Errorf("Groups = %v, want %v", id.Groups, tc.wantGroups)
			}
			// The claim names reach the subject and the groups alone.
			if id.Email != "alice@acme.com" || id.OrgID != "acme" {
				t.Errorf("Identity = %+v, want the alice@acme.com / acme email and organization", id)
			}
		})
	}
}

// Spec: §6.3.3 — under a configured subject claim the registry reads that claim
// alone and rejects a token that does not carry it with auth.untrusted_token.
func TestOIDCVerifier_ConfiguredSubjectClaimHasNoFallback(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mutate func(jwt.MapClaims)
	}{
		{
			name:   "token carries sub and not the configured claim",
			mutate: func(jwt.MapClaims) {},
		},
		{
			name:   "token carries neither claim",
			mutate: func(c jwt.MapClaims) { delete(c, "sub") },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			idp := newTestIdP(t)
			v := NewOIDCVerifier(idp.issuer(), testAudience, 300*time.Second, WithSubjectClaim(adfsSubjectClaim))

			claims := validClaims(idp.issuer(), testAudience)
			tc.mutate(claims)
			_, err := v.Verify(idp.sign(t, "key-1", claims))
			if !errors.Is(err, ErrUntrustedToken) {
				t.Fatalf("err = %v, want ErrUntrustedToken", err)
			}
			var ute *UntrustedTokenError
			if !errors.As(err, &ute) {
				t.Fatalf("err = %T, want *UntrustedTokenError", err)
			}
			if ute.Issuer != idp.issuer() {
				t.Errorf("UntrustedTokenError.Issuer = %q, want %q", ute.Issuer, idp.issuer())
			}
			if want := adfsSubjectClaim + " claim missing"; ute.Reason != want {
				t.Errorf("reason = %q, want %q", ute.Reason, want)
			}
		})
	}
}
