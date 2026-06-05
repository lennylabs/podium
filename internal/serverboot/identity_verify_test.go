package serverboot

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lennylabs/podium/pkg/identity"
)

func signRuntimeJWT(t *testing.T, priv *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	s, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

// Spec: §6.3.1 / §6.3.2 — the boot-wired verifier maps verified claims to a
// layer.Identity, applies the IdpGroupMapping to the token group claims, and
// carries the OAuth scopes.
func TestInjectedTokenVerifier_MapsClaims(t *testing.T) {
	t.Parallel()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	reg := identity.NewRuntimeKeyRegistry()
	if err := reg.Register(identity.RuntimeKey{Issuer: "rt", Algorithm: "RS256", Key: &priv.PublicKey}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	mapping := identity.NewIdpGroupMapping(map[string]string{"00gFinanceOID": "finance"})
	verify := injectedTokenVerifier(reg, "https://podium.acme.com", mapping)

	raw := signRuntimeJWT(t, priv, jwt.MapClaims{
		"iss": "rt", "aud": "https://podium.acme.com", "sub": "alice", "act": "rt",
		"email":  "alice@acme.com",
		"groups": []string{"00gFinanceOID", "already-named"},
		"scope":  "podium:read:finance/*",
		"exp":    time.Now().Add(5 * time.Minute).Unix(),
	})
	req, _ := http.NewRequest(http.MethodGet, "http://x/v1/search_artifacts", nil)
	req.Header.Set("Authorization", "Bearer "+raw)

	id, err := verify(req)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if id.Sub != "alice" || id.Email != "alice@acme.com" || !id.IsAuthenticated {
		t.Errorf("identity = %+v", id)
	}
	// 00gFinanceOID maps to finance; already-named passes through.
	wantGroups := map[string]bool{"finance": true, "already-named": true}
	if len(id.Groups) != 2 {
		t.Fatalf("Groups = %v, want finance + already-named", id.Groups)
	}
	for _, g := range id.Groups {
		if !wantGroups[g] {
			t.Errorf("unexpected group %q in %v", g, id.Groups)
		}
	}
	if len(id.Scopes) != 1 || id.Scopes[0] != "podium:read:finance/*" {
		t.Errorf("Scopes = %v", id.Scopes)
	}
}

// Spec: §6.3.2 — a token from an unregistered runtime is rejected with a
// typed *UntrustedRuntimeError carrying the issuer.
func TestInjectedTokenVerifier_RejectsUnregistered(t *testing.T) {
	t.Parallel()
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	reg := identity.NewRuntimeKeyRegistry() // no keys registered
	verify := injectedTokenVerifier(reg, "https://podium.acme.com", nil)
	raw := signRuntimeJWT(t, priv, jwt.MapClaims{
		"iss": "ghost-runtime", "aud": "https://podium.acme.com", "sub": "alice", "act": "ghost-runtime",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	req, _ := http.NewRequest(http.MethodGet, "http://x/v1/load_artifact", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	_, err := verify(req)
	var ute *identity.UntrustedRuntimeError
	if !errors.As(err, &ute) || ute.Issuer != "ghost-runtime" {
		t.Fatalf("got %v, want *UntrustedRuntimeError{ghost-runtime}", err)
	}
}

// Spec: §6.3.2 — a request with no bearer token is rejected (no anonymous
// fallback in injected-session-token mode).
func TestInjectedTokenVerifier_RejectsMissingToken(t *testing.T) {
	t.Parallel()
	reg := identity.NewRuntimeKeyRegistry()
	verify := injectedTokenVerifier(reg, "https://podium.acme.com", nil)
	req, _ := http.NewRequest(http.MethodGet, "http://x/v1/search_artifacts", nil)
	if _, err := verify(req); !errors.Is(err, identity.ErrUntrustedRuntime) {
		t.Fatalf("missing token: got %v, want ErrUntrustedRuntime", err)
	}
}

// Spec: §4.6 / §7.3.1 — the layer endpoint resolves the
// caller from the same request-time verifier wired on the meta-tool server. A
// verified token yields the authenticated identity used to attribute a
// user-defined layer and gate admin operations; a missing/invalid token or a
// nil verifier resolves to the anonymous-public caller (fail-closed).
func TestLayerIdentityResolver(t *testing.T) {
	t.Parallel()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	reg := identity.NewRuntimeKeyRegistry()
	if err := reg.Register(identity.RuntimeKey{Issuer: "rt", Algorithm: "RS256", Key: &priv.PublicKey}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	verify := injectedTokenVerifier(reg, "https://podium.acme.com", nil)
	resolve := layerIdentityResolver(verify)

	// Valid token => authenticated identity.
	raw := signRuntimeJWT(t, priv, jwt.MapClaims{
		"iss": "rt", "aud": "https://podium.acme.com", "sub": "alice", "act": "rt",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	req, _ := http.NewRequest(http.MethodPost, "http://x/v1/layers", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	if id := resolve(req); !id.IsAuthenticated || id.Sub != "alice" {
		t.Errorf("valid token resolved to %+v, want authenticated alice", id)
	}

	// Missing token => anonymous-public fallback.
	anon, _ := http.NewRequest(http.MethodPost, "http://x/v1/layers", nil)
	if id := resolve(anon); id.IsAuthenticated || !id.IsPublic {
		t.Errorf("missing token resolved to %+v, want anonymous-public", id)
	}

	// Nil verifier => anonymous-public fallback.
	if id := layerIdentityResolver(nil)(req); id.IsAuthenticated || !id.IsPublic {
		t.Errorf("nil verifier resolved to %+v, want anonymous-public", id)
	}
}

// Spec: §6.3.2 — injected-session-token mode must refuse to start without a
// configured audience: the verifier validates aud against this registry's
// endpoint on every call, and an unset audience would leave aud unchecked (a
// cross-registry token-confusion surface). Other providers are exempt.
func TestInjectedTokenAudienceGuard(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		provider string
		audience string
		wantErr  bool
	}{
		{"injected no audience refuses", "injected-session-token", "", true},
		{"injected blank audience refuses", "injected-session-token", "   ", true},
		{"injected with audience starts", "injected-session-token", "https://podium.acme.com", false},
		{"oauth-device-code exempt", "oauth-device-code", "", false},
		{"empty provider exempt", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := injectedTokenAudienceGuard(tc.provider, tc.audience)
			if tc.wantErr && err == nil {
				t.Fatalf("provider=%q audience=%q: want refusal, got nil", tc.provider, tc.audience)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("provider=%q audience=%q: want nil, got %v", tc.provider, tc.audience, err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "config.injected_token_audience_unset") {
				t.Errorf("error should carry the namespaced code, got %v", err)
			}
		})
	}
}

func TestBearerToken(t *testing.T) {
	t.Parallel()
	cases := []struct{ header, want string }{
		{"Bearer abc.def.ghi", "abc.def.ghi"},
		{"bearer lowercase", "lowercase"},
		{"BEARER UPPER", "UPPER"},
		{"Bearer   spaced  ", "spaced"},
		{"Basic xyz", ""},
		{"", ""},
	}
	for _, tc := range cases {
		req, _ := http.NewRequest(http.MethodGet, "http://x/", nil)
		if tc.header != "" {
			req.Header.Set("Authorization", tc.header)
		}
		if got := bearerToken(req); got != tc.want {
			t.Errorf("bearerToken(%q) = %q, want %q", tc.header, got, tc.want)
		}
	}
}
