package identity

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// adfsTokenIssuer is the federation-service identifier an AD FS farm stamps on
// its access tokens while it serves discovery under a different, https base.
const adfsTokenIssuer = "http://adfs.acme.example/adfs/services/trust"

// Spec: §6.3.3 — the accepted issuers are the configured issuer and the
// access_token_issuer the same discovery document publishes. A token stamped
// with the second value verifies against the configured issuer's JWKS.
func TestOIDCVerifier_AcceptsDiscoveredAccessTokenIssuer(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)
	idp.setAccessTokenIssuer(adfsTokenIssuer)
	v := NewOIDCVerifier(idp.issuer(), testAudience, 300*time.Second)
	if err := v.Prime(); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	id, err := v.Verify(idp.sign(t, "key-1", validClaims(adfsTokenIssuer, testAudience)))
	if err != nil {
		t.Fatalf("Verify with the access_token_issuer value: %v", err)
	}
	if !id.IsAuthenticated || id.Sub != "alice@acme.com" {
		t.Errorf("Identity = %+v, want the authenticated alice@acme.com caller", id)
	}
	// The configured issuer stays accepted.
	if _, err := v.Verify(idp.sign(t, "key-1", validClaims(idp.issuer(), testAudience))); err != nil {
		t.Fatalf("Verify with the configured issuer: %v", err)
	}
}

// Spec: §6.3.3 — a discovery document that publishes no access_token_issuer
// leaves the configured issuer as the sole accepted value, and any other iss is
// rejected with auth.untrusted_token whatever the document publishes.
func TestOIDCVerifier_RejectsUnacceptedIssuer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name              string
		accessTokenIssuer string // published by the discovery document
		tokenIssuer       string
	}{
		{
			name:        "access_token_issuer value the document does not publish",
			tokenIssuer: adfsTokenIssuer,
		},
		{
			name:              "issuer unrelated to both accepted values",
			accessTokenIssuer: adfsTokenIssuer,
			tokenIssuer:       "https://evil.example",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			idp := newTestIdP(t)
			idp.setAccessTokenIssuer(tc.accessTokenIssuer)
			v := NewOIDCVerifier(idp.issuer(), testAudience, 300*time.Second)
			if err := v.Prime(); err != nil {
				t.Fatalf("Prime: %v", err)
			}

			_, err := v.Verify(idp.sign(t, "key-1", validClaims(tc.tokenIssuer, testAudience)))
			if !errors.Is(err, ErrUntrustedToken) {
				t.Fatalf("err = %v, want ErrUntrustedToken", err)
			}
			var ute *UntrustedTokenError
			if !errors.As(err, &ute) {
				t.Fatalf("err = %T, want *UntrustedTokenError", err)
			}
			if ute.Issuer != tc.tokenIssuer {
				t.Errorf("UntrustedTokenError.Issuer = %q, want %q", ute.Issuer, tc.tokenIssuer)
			}
			// The rejection names the accepted set rather than the configured
			// issuer alone.
			for _, accepted := range v.AcceptedIssuers() {
				if !strings.Contains(ute.Reason, accepted) {
					t.Errorf("reason %q does not name the accepted issuer %q", ute.Reason, accepted)
				}
			}
		})
	}
}

// Spec: §6.3.3 — the second accepted issuer is compared as a string under the
// same trailing-slash rule as the configured issuer.
func TestOIDCVerifier_AccessTokenIssuerTrailingSlash(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name              string
		accessTokenIssuer string
		tokenIssuer       string
	}{
		{"published with a trailing slash", adfsTokenIssuer + "/", adfsTokenIssuer},
		{"token iss with a trailing slash", adfsTokenIssuer, adfsTokenIssuer + "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			idp := newTestIdP(t)
			idp.setAccessTokenIssuer(tc.accessTokenIssuer)
			v := NewOIDCVerifier(idp.issuer(), testAudience, 300*time.Second)
			if err := v.Prime(); err != nil {
				t.Fatalf("Prime: %v", err)
			}
			if _, err := v.Verify(idp.sign(t, "key-1", validClaims(tc.tokenIssuer, testAudience))); err != nil {
				t.Fatalf("Verify: %v", err)
			}
		})
	}
}

// Spec: §6.3.3 — the audience check is unaffected by the second accepted
// issuer, so a token carrying it and another relying party's aud is rejected.
func TestOIDCVerifier_AccessTokenIssuerStillRequiresAudience(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)
	idp.setAccessTokenIssuer(adfsTokenIssuer)
	v := NewOIDCVerifier(idp.issuer(), testAudience, 300*time.Second)
	if err := v.Prime(); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	_, err := v.Verify(idp.sign(t, "key-1", validClaims(adfsTokenIssuer, "https://other.example")))
	if !errors.Is(err, ErrUntrustedToken) {
		t.Fatalf("err = %v, want ErrUntrustedToken", err)
	}
}

// Spec: §6.3.3 — AcceptedIssuers reports the values the registry logs at
// startup: the configured issuer alone until the discovery document names an
// access_token_issuer, and both values once it does.
func TestOIDCVerifier_AcceptedIssuers(t *testing.T) {
	t.Parallel()

	t.Run("before the discovery document is resolved", func(t *testing.T) {
		t.Parallel()
		idp := newTestIdP(t)
		idp.setAccessTokenIssuer(adfsTokenIssuer)
		v := NewOIDCVerifier(idp.issuer()+"/", testAudience, 300*time.Second)
		if got, want := v.AcceptedIssuers(), []string{idp.issuer()}; !slices.Equal(got, want) {
			t.Errorf("AcceptedIssuers() = %v, want %v", got, want)
		}
	})

	t.Run("document publishes access_token_issuer", func(t *testing.T) {
		t.Parallel()
		idp := newTestIdP(t)
		idp.setAccessTokenIssuer(adfsTokenIssuer + "/")
		v := NewOIDCVerifier(idp.issuer(), testAudience, 300*time.Second)
		if err := v.Prime(); err != nil {
			t.Fatalf("Prime: %v", err)
		}
		want := []string{idp.issuer(), adfsTokenIssuer}
		if got := v.AcceptedIssuers(); !slices.Equal(got, want) {
			t.Errorf("AcceptedIssuers() = %v, want %v", got, want)
		}
	})

	t.Run("document publishes no access_token_issuer", func(t *testing.T) {
		t.Parallel()
		idp := newTestIdP(t)
		v := NewOIDCVerifier(idp.issuer(), testAudience, 300*time.Second)
		if err := v.Prime(); err != nil {
			t.Fatalf("Prime: %v", err)
		}
		if got, want := v.AcceptedIssuers(), []string{idp.issuer()}; !slices.Equal(got, want) {
			t.Errorf("AcceptedIssuers() = %v, want %v", got, want)
		}
	})
}

// Spec: §6.3.3 — both accepted values are named only when the published
// access_token_issuer differs from the configured issuer. A document that
// publishes the configured issuer itself leaves one accepted value, so the
// startup log and the rejection message name it once rather than reporting a
// widened accepted set.
func TestOIDCVerifier_AccessTokenIssuerEqualsConfiguredIssuer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		// configured is the issuer passed to NewOIDCVerifier, and published is
		// the access_token_issuer the discovery document names. Both are built
		// from the stub IdP's base URL.
		configured func(base string) string
		published  func(base string) string
	}{
		{
			name:       "identical to the configured issuer",
			configured: func(base string) string { return base },
			published:  func(base string) string { return base },
		},
		{
			name:       "published with a trailing slash",
			configured: func(base string) string { return base },
			published:  func(base string) string { return base + "/" },
		},
		{
			name:       "configured with a trailing slash",
			configured: func(base string) string { return base + "/" },
			published:  func(base string) string { return base },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			idp := newTestIdP(t)
			idp.setAccessTokenIssuer(tc.published(idp.issuer()))
			v := NewOIDCVerifier(tc.configured(idp.issuer()), testAudience, 300*time.Second)
			if err := v.Prime(); err != nil {
				t.Fatalf("Prime: %v", err)
			}

			if got, want := v.AcceptedIssuers(), []string{idp.issuer()}; !slices.Equal(got, want) {
				t.Errorf("AcceptedIssuers() = %v, want %v", got, want)
			}
			if _, err := v.Verify(idp.sign(t, "key-1", validClaims(idp.issuer(), testAudience))); err != nil {
				t.Fatalf("Verify with the configured issuer: %v", err)
			}

			// The rejection names the sole accepted value once.
			_, err := v.Verify(idp.sign(t, "key-1", validClaims("https://evil.example", testAudience)))
			var ute *UntrustedTokenError
			if !errors.As(err, &ute) {
				t.Fatalf("err = %v, want *UntrustedTokenError", err)
			}
			if got := strings.Count(ute.Reason, idp.issuer()); got != 1 {
				t.Errorf("reason %q names the accepted issuer %d times, want 1", ute.Reason, got)
			}
		})
	}
}

// Spec: §6.3.3 — the discovery document is read once, so the JWKS refresh a kid
// miss forces leaves the second accepted issuer in place for the process
// lifetime.
func TestOIDCVerifier_JWKSRefreshKeepsAccessTokenIssuer(t *testing.T) {
	t.Parallel()
	idp := newTestIdP(t)
	idp.setAccessTokenIssuer(adfsTokenIssuer)
	// Long TTL: the refresh below is driven by the kid cache miss.
	v := NewOIDCVerifier(idp.issuer(), testAudience, time.Hour)
	if err := v.Prime(); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	if _, err := v.Verify(idp.sign(t, "key-1", validClaims(adfsTokenIssuer, testAudience))); err != nil {
		t.Fatalf("initial verify: %v", err)
	}

	// Rotate the key and stop publishing access_token_issuer. A token signed by
	// the new key presents an unknown kid, which forces a JWKS refetch; that
	// refetch must not re-resolve the discovery document and drop the stored
	// second issuer.
	idp.rotate(t, "key-2")
	idp.setAccessTokenIssuer("")
	if _, err := v.Verify(idp.sign(t, "key-2", validClaims(adfsTokenIssuer, testAudience))); err != nil {
		t.Fatalf("verify after rotation: %v", err)
	}
	want := []string{idp.issuer(), adfsTokenIssuer}
	if got := v.AcceptedIssuers(); !slices.Equal(got, want) {
		t.Errorf("AcceptedIssuers() after refresh = %v, want %v", got, want)
	}
}
