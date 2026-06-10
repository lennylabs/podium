package serverboot

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/lennylabs/podium/pkg/identity"
)

// Spec: §6.3.3 / §13.12 — enabling a gateway-delegated provider flips the unset
// default layer visibility to private, so admin layers are not accidentally
// public to every caller once the registry begins filtering by identity. A
// no-identity standalone stays public.
func TestDefaultBootstrapVisibility_GatewayProvidersArePrivate(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"oidc-jwt", "trusted-headers", "injected-session-token"} {
		if v := defaultBootstrapVisibility(&Config{identityProvider: p}); !visibilityIsEmpty(v) {
			t.Errorf("%s: unset default visibility = %+v, want private (empty)", p, v)
		}
	}
	if v := defaultBootstrapVisibility(&Config{}); !v.Public {
		t.Errorf("no-identity unset default = %+v, want public", v)
	}
	// An explicit =public is honored regardless of the provider.
	if v := defaultBootstrapVisibility(&Config{identityProvider: "trusted-headers", defaultLayerVisibility: "public"}); !v.Public {
		t.Errorf("explicit public under trusted-headers = %+v, want public", v)
	}
}

func TestOIDCJWTConfigGuard(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		provider string
		issuer   string
		audience string
		wantCode string // "" means no error
	}{
		{"not oidc-jwt is exempt", "injected-session-token", "", "", ""},
		{"valid https issuer and audience", "oidc-jwt", "https://acme.okta.com/oauth2/default", "https://podium.acme", ""},
		{"loopback https permitted", "oidc-jwt", "https://localhost:8443", "https://podium.acme", ""},
		{"http issuer refused", "oidc-jwt", "http://acme.okta.com", "https://podium.acme", "config.invalid_issuer_scheme"},
		{"empty issuer refused", "oidc-jwt", "", "https://podium.acme", "config.invalid_issuer_scheme"},
		{"issuer without host refused", "oidc-jwt", "https://", "https://podium.acme", "config.invalid_issuer_scheme"},
		{"missing audience refused", "oidc-jwt", "https://acme.okta.com", "", "config.oidc_jwt_audience_unset"},
		{"blank audience refused", "oidc-jwt", "https://acme.okta.com", "   ", "config.oidc_jwt_audience_unset"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := oidcJWTConfigGuard(tc.provider, tc.issuer, tc.audience)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantCode) {
				t.Fatalf("err = %v, want one containing %q", err, tc.wantCode)
			}
		})
	}
}

func TestTrustedHeadersVerifier(t *testing.T) {
	t.Parallel()

	t.Run("reads identity headers, no secret", func(t *testing.T) {
		verify := trustedHeadersVerifier("")
		r := httptest.NewRequest("GET", "/v1/load_domain", nil)
		r.Header.Set(identity.HeaderUserSub, "alice@acme.com")
		r.Header.Set(identity.HeaderUserEmail, "alice@acme.com")
		r.Header.Set(identity.HeaderUserGroups, "engineering, finance")
		r.Header.Set(identity.HeaderUserOrg, "acme")
		id, err := verify(r)
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if !id.IsAuthenticated || id.Sub != "alice@acme.com" || id.OrgID != "acme" {
			t.Errorf("identity = %+v", id)
		}
		if len(id.Groups) != 2 {
			t.Errorf("groups = %v", id.Groups)
		}
	})

	t.Run("missing headers is anonymous, never an error", func(t *testing.T) {
		verify := trustedHeadersVerifier("")
		r := httptest.NewRequest("GET", "/v1/load_domain", nil)
		id, err := verify(r)
		if err != nil {
			t.Fatalf("verify must not error on anonymous: %v", err)
		}
		if id.IsAuthenticated || id.IsPublic {
			t.Errorf("anonymous identity = %+v, want zero (public-only)", id)
		}
	})

	t.Run("proxy secret gates header trust", func(t *testing.T) {
		verify := trustedHeadersVerifier("s3cr3t")

		// No presented secret: anonymous.
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set(identity.HeaderUserSub, "alice@acme.com")
		if id, _ := verify(r); id.IsAuthenticated {
			t.Error("identity honored without the matching proxy secret")
		}

		// Matching secret: honored.
		r2 := httptest.NewRequest("GET", "/", nil)
		r2.Header.Set(identity.HeaderUserSub, "alice@acme.com")
		r2.Header.Set(identity.HeaderProxySecret, "s3cr3t")
		if id, _ := verify(r2); !id.IsAuthenticated {
			t.Error("identity not honored with the matching proxy secret")
		}
	})
}

func TestOIDCJWTVerifier_NoTokenIsAnonymous(t *testing.T) {
	t.Parallel()
	v := identity.NewOIDCVerifier("https://issuer.example", "aud", 0)
	verify := oidcJWTVerifier(v, "", nil)

	// No Authorization header: anonymous, not a rejection, and no network call.
	r := httptest.NewRequest("GET", "/v1/load_domain", nil)
	id, err := verify(r)
	if err != nil {
		t.Fatalf("no token must be anonymous, got err %v", err)
	}
	if id.IsAuthenticated || id.IsPublic {
		t.Errorf("identity = %+v, want zero (public-only)", id)
	}
}

func TestOIDCJWTVerifier_KeySetUnavailableIsAnonymous(t *testing.T) {
	t.Parallel()
	// Unreachable issuer, no cache: a present, parseable token cannot be
	// verified, and §6.3.3 makes that anonymous rather than a 401.
	const iss = "http://127.0.0.1:1"
	v := identity.NewOIDCVerifier(iss, "aud", 0)
	verify := oidcJWTVerifier(v, "X-Forwarded-Access-Token", nil)

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": iss, "aud": "aud", "sub": "alice@acme.com",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tok.Header["kid"] = "k1"
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/v1/load_domain", nil)
	r.Header.Set("X-Forwarded-Access-Token", "Bearer "+raw)
	id, err := verify(r)
	if err != nil {
		t.Fatalf("key-set-unavailable must be anonymous, got err %v", err)
	}
	if id.IsAuthenticated {
		t.Errorf("identity = %+v, want anonymous", id)
	}
}
