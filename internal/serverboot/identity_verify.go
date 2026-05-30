package serverboot

import (
	"net/http"
	"strings"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
)

// injectedTokenVerifier builds the §6.3.2 request-time verifier for the
// injected-session-token provider. It extracts the bearer token from the
// Authorization header, verifies its signature against the registered
// runtime keys for the configured audience, applies the §6.3.1
// IdpGroupMapping, and returns the caller layer.Identity carrying the
// verified claims (including any "podium:*" scopes for the §6.3.1 scope
// narrowing). A verification failure is returned verbatim so the server
// maps identity.ErrUntrustedRuntime / identity.ErrTokenExpired (and the
// typed *identity.UntrustedRuntimeError carrying the issuer) to the §6.10
// envelope.
func injectedTokenVerifier(keys identity.RuntimeKeyVerifierStore, audience string, groups *identity.IdpGroupMapping) func(*http.Request) (layer.Identity, error) {
	verify := keys.JWTVerifier(audience, nil)
	return func(r *http.Request) (layer.Identity, error) {
		id, err := verify(bearerToken(r))
		if err != nil {
			return layer.Identity{}, err
		}
		mapped := id.Groups
		if groups != nil && !groups.Empty() {
			mapped = groups.Map(id.Groups)
		}
		return layer.Identity{
			Sub:             id.Sub,
			Email:           id.Email,
			OrgID:           id.OrgID,
			Groups:          mapped,
			Scopes:          id.Scopes,
			IsAuthenticated: true,
		}, nil
	}
}

// selectIdentityProvider resolves the §9.1 IdentityProvider for
// cfg.identityProvider from the process-global identity.Default registry
// (§9.2). It returns the instantiated provider when the id is registered
// (the built-in oauth-device-code / injected-session-token, or a custom
// provider an imported plugin registered), nil when the id is not a
// registered MCP-server provider (the empty standalone default, a
// server-side mode such as "oidc", or public mode), and an error only when
// a registered provider's factory fails. This is the build-path consumer
// that makes "import a custom IdentityProvider into a source build" change
// behavior, the gap §9.2 names by example.
func selectIdentityProvider(cfg *Config) (identity.Provider, error) {
	if cfg.identityProvider == "" || !identity.Default.Has(cfg.identityProvider) {
		return nil, nil
	}
	return identity.Default.New(cfg.identityProvider, identity.Config{
		Audience:              cfg.oauthAudience,
		AuthorizationEndpoint: cfg.oauthAuthorizationEndpoint,
	})
}

// bearerToken returns the token from an "Authorization: Bearer <token>"
// header, or "" when the header is absent or not a bearer credential. The
// empty string drives the verifier's empty-token rejection
// (auth.untrusted_runtime), so an unauthenticated call in
// injected-session-token mode is rejected rather than served anonymously.
func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) >= len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}
