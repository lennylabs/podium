package serverboot

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net/http"
	"slices"
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

// registeredRuntimeKey returns a signing key and a §6.3.2 runtime-key
// registry that trusts its public half under the issuer "rt".
func registeredRuntimeKey(t *testing.T) (*rsa.PrivateKey, *identity.RuntimeKeyRegistry) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	reg := identity.NewRuntimeKeyRegistry()
	if err := reg.Register(identity.RuntimeKey{Issuer: "rt", Algorithm: "RS256", Key: &priv.PublicKey}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return priv, reg
}

// Spec: §6.3.1 / §6.3.2 — the boot-wired verifier maps verified claims to a
// layer.Identity, applies the IdpGroupMapping to the token group claims, and
// carries the OAuth scopes.
func TestInjectedTokenVerifier_MapsClaims(t *testing.T) {
	t.Parallel()
	priv, reg := registeredRuntimeKey(t)
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

// Spec: §6.3.1 / §6.3.2 — the group claim is read in the multi-value form and
// in the single-string form an IdP emits for a caller in exactly one group.
// The boot-wired injected-session-token verifier forwards the derived groups
// through the IdpGroupMapping into the caller layer.Identity, so both
// encodings reach the same mapped group names.
func TestInjectedTokenVerifier_GroupClaimEncodings(t *testing.T) {
	t.Parallel()
	priv, reg := registeredRuntimeKey(t)
	mapping := identity.NewIdpGroupMapping(map[string]string{"00gFinanceOID": "finance"})

	cases := []struct {
		name    string
		groups  any
		mapping *identity.IdpGroupMapping
		want    []string
	}{
		{"array form maps and passes through", []string{"00gFinanceOID", "already-named"}, mapping, []string{"already-named", "finance"}},
		{"single string is mapped", "00gFinanceOID", mapping, []string{"finance"}},
		{"single string with no table entry passes through", "already-named", mapping, []string{"already-named"}},
		{"single string with no mapping configured", "already-named", nil, []string{"already-named"}},
		// The trusted-headers group header is comma-separated (§6.3.3); the
		// JWT group claim is not, so the whole value is one group name.
		{"single string is not split on a separator", "finance, engineering", mapping, []string{"finance, engineering"}},
		{"empty single string yields no group", "", mapping, nil},
		{"absent claim yields no group", nil, mapping, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			claims := jwt.MapClaims{
				"iss": "rt", "aud": "https://podium.acme.com", "sub": "alice", "act": "rt",
				"exp": time.Now().Add(5 * time.Minute).Unix(),
			}
			if tc.groups != nil {
				claims["groups"] = tc.groups
			}
			req, _ := http.NewRequest(http.MethodGet, "http://x/v1/search_artifacts", nil)
			req.Header.Set("Authorization", "Bearer "+signRuntimeJWT(t, priv, claims))

			id, err := injectedTokenVerifier(reg, "https://podium.acme.com", tc.mapping)(req)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			slices.Sort(id.Groups)
			if !slices.Equal(id.Groups, tc.want) {
				t.Errorf("Groups = %v, want %v", id.Groups, tc.want)
			}
			if id.Sub != "alice" || !id.IsAuthenticated {
				t.Errorf("identity = %+v, want authenticated alice", id)
			}
		})
	}
}

// Spec: §4.6 / §6.3.2 — full-stack: a runtime-signed token whose group claim
// arrives as a plain string resolves layer visibility through the meta-tool
// server. The caller in exactly one group reaches the engineering layer, and a
// comma-joined value stays one group name that matches no layer.
func TestGatewayIntegration_InjectedTokenSingleStringGroupClaim(t *testing.T) {
	t.Parallel()
	priv, reg := registeredRuntimeKey(t)
	mapping := identity.NewIdpGroupMapping(map[string]string{"00g1engOID": "engineering"})
	ts := gatewayServer(t, injectedTokenVerifier(reg, gwAudience, mapping))

	token := func(sub string, groups any) string {
		return signRuntimeJWT(t, priv, jwt.MapClaims{
			"iss": "rt", "aud": gwAudience, "sub": sub, "act": "rt",
			"groups": groups,
			"exp":    time.Now().Add(5 * time.Minute).Unix(),
		})
	}

	// The single-string claim yields one group, which the IdpGroupMapping
	// rewrites to the layer's group name.
	alice := token("alice@acme.com", "00g1engOID")
	if st, body := loadArtifact(t, ts.URL, "eng/secret", bearer(alice)); st != 200 {
		t.Errorf("single-string group caller load eng/secret = %d, want 200\nbody: %s", st, body)
	}
	// An unmapped single-string value passes through and matches the layer
	// group directly.
	carol := token("carol@acme.com", "engineering")
	if st, body := loadArtifact(t, ts.URL, "eng/secret", bearer(carol)); st != 200 {
		t.Errorf("unmapped single-string group caller load eng/secret = %d, want 200\nbody: %s", st, body)
	}
	// The string form is not split, so a comma-joined value is a single group
	// name that matches no layer.
	bob := token("bob@acme.com", "engineering,ops")
	if st, _ := loadArtifact(t, ts.URL, "eng/secret", bearer(bob)); st != 404 {
		t.Errorf("comma-joined group caller load eng/secret = %d, want 404 (value is one group name)", st)
	}
	// The public layer stays visible to every verified caller.
	if st, _ := loadArtifact(t, ts.URL, "pub/welcome", bearer(bob)); st != 200 {
		t.Errorf("verified caller load pub/welcome = %d, want 200", st)
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

// Spec: §6.3.2, §6.3.3, §7.3.1 — the resolver the layer endpoint reads
// returns the verifier's error verbatim, so the endpoint refuses a credential
// that failed verification and serves a caller the provider resolves as
// anonymous. Whether an absent credential is a failure is the provider's own
// rule: injected-session-token reports one, oidc-jwt does not.
func TestLayerCallerResolver(t *testing.T) {
	t.Parallel()
	priv, reg := registeredRuntimeKey(t)
	injected := layerCallerResolver(injectedTokenVerifier(reg, "https://podium.acme.com", nil))

	// A valid runtime-signed token: the authenticated identity, no error.
	raw := signRuntimeJWT(t, priv, jwt.MapClaims{
		"iss": "rt", "aud": "https://podium.acme.com", "sub": "alice", "act": "rt",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	req, _ := http.NewRequest(http.MethodGet, "http://x/v1/layers", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	id, err := injected(req)
	if err != nil {
		t.Fatalf("valid token: got err %v, want nil", err)
	}
	if !id.IsAuthenticated || id.Sub != "alice" {
		t.Errorf("valid token resolved to %+v, want authenticated alice", id)
	}

	// No Authorization header under injected-session-token: a verification
	// failure, which the endpoint refuses.
	anon, _ := http.NewRequest(http.MethodGet, "http://x/v1/layers", nil)
	if _, err := injected(anon); !errors.Is(err, identity.ErrUntrustedRuntime) {
		t.Errorf("missing token: got %v, want ErrUntrustedRuntime", err)
	}

	// No credential under oidc-jwt: the anonymous caller and no error, which
	// keeps a signed-out browser out of the refusal.
	oidc := layerCallerResolver(oidcJWTVerifier(identity.NewOIDCVerifier("https://issuer.example", []string{"aud"}, 0), "", nil, false))
	id, err = oidc(anon)
	if err != nil {
		t.Fatalf("oidc-jwt with no credential: got err %v, want nil", err)
	}
	if id.IsAuthenticated || id.IsPublic {
		t.Errorf("oidc-jwt with no credential resolved to %+v, want the zero identity", id)
	}

	// A nil verifier: the anonymous-public caller and no error, which is the
	// no-provider, public-mode, and free-form-label deployment.
	id, err = layerCallerResolver(nil)(req)
	if err != nil {
		t.Fatalf("nil verifier: got err %v, want nil", err)
	}
	if id.IsAuthenticated || !id.IsPublic {
		t.Errorf("nil verifier resolved to %+v, want anonymous-public", id)
	}
}

// Spec: §4.6 — the swallowing resolver read by the §4.7.2 admin gate and the
// §7.3.4 posture read, neither of which refuses a request for lack of a
// credential. A verified token yields the authenticated identity used to
// evaluate admin authorization; a missing or invalid token, or a nil
// verifier, resolves to the anonymous-public caller (fail-closed).
func TestLayerIdentityResolver(t *testing.T) {
	t.Parallel()
	priv, reg := registeredRuntimeKey(t)
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

// Spec: §6.3.2 — the trusted runtime key set comes from configuration read
// at startup and there is no request-time registration API, so an
// injected-session-token registry over an empty key set rejects every call
// with auth.untrusted_runtime and can accept no first key. The guard refuses
// to start with config.runtime_keys_unavailable, covering the unset path and
// the path that names a file carrying no key. Providers that never consult
// the store are exempt.
func TestRuntimeKeyBootstrapGuard(t *testing.T) {
	t.Parallel()
	_, seeded := registeredRuntimeKey(t)
	empty := identity.NewRuntimeKeyRegistry()
	cases := []struct {
		name     string
		provider string
		path     string
		keys     identity.RuntimeKeyVerifierStore
		wantErr  bool
	}{
		{"injected with a seeded key starts", "injected-session-token", "/etc/podium/keys.json", seeded, false},
		{"injected over an empty file refuses", "injected-session-token", "/etc/podium/keys.json", empty, true},
		{"injected with an unset path refuses", "injected-session-token", "", empty, true},
		{"injected with a nil store refuses", "injected-session-token", "", nil, true},
		{"oidc-jwt over an empty set is exempt", "oidc-jwt", "", empty, false},
		{"trusted-headers over an empty set is exempt", "trusted-headers", "", empty, false},
		{"standalone is exempt", "", "", empty, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runtimeKeyBootstrapGuard(tc.provider, tc.path, tc.keys)
			if tc.wantErr && err == nil {
				t.Fatalf("provider=%q path=%q: want refusal, got nil", tc.provider, tc.path)
			}
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("provider=%q path=%q: want nil, got %v", tc.provider, tc.path, err)
				}
				return
			}
			if !strings.Contains(err.Error(), "config.runtime_keys_unavailable") {
				t.Errorf("error should carry the namespaced code, got %v", err)
			}
			if !strings.Contains(err.Error(), "PODIUM_RUNTIME_KEYS_PATH") {
				t.Errorf("error should name the variable, got %v", err)
			}
			if !strings.Contains(err.Error(), "--keys-file") {
				t.Errorf("error should name the --keys-file remediation, got %v", err)
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
