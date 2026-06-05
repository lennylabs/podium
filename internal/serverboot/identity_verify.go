package serverboot

import (
	"fmt"
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

// layerIdentityResolver adapts a §6.3.2 request-time verifier into the
// resolver the §7.3.1 layer endpoint uses to attribute a user-defined layer
// to its registrant (§4.6) and to evaluate admin authorization (§4.7.2). The
// layer endpoint is mounted outside the meta-tool identity middleware, so it
// resolves the caller itself. A verified token yields the authenticated
// identity; a missing or invalid token resolves to the anonymous-public
// caller, which the endpoint then denies for admin-gated operations and
// rejects for user-defined registrations (fail-closed). A nil verifier (no
// server-side verification wired) always resolves anonymous.
func layerIdentityResolver(verify func(*http.Request) (layer.Identity, error)) func(*http.Request) layer.Identity {
	return func(r *http.Request) layer.Identity {
		if verify != nil {
			if id, err := verify(r); err == nil {
				return id
			}
		}
		return layer.Identity{IsPublic: true}
	}
}

// identityVisibilityGuard refuses startup when a configured identity
// provider cannot resolve callers to an Identity at request time.
//
// spec: §2.2, §6.3.1 — the registry "composes the caller's effective view
// from the configured layer list per OAuth identity, applies per-layer
// visibility." Resolving the caller requires a request-time verifier. Only
// injected-session-token wires one in this build (verifierInstalled);
// oauth-device-code (the other documented §6.3 built-in) needs the §6.3.1
// server-side OIDC verifier that the registry does not yet ship. Without a
// verifier the server falls back to the anonymous-public resolver, so every
// caller composes as anonymous and authenticated, organization, and private
// layers silently vanish from every effective view. Refuse to start in that
// state rather than serve a registry whose visibility never applies.
//
// The guard keys on providerSelected: a real provider resolved from the
// identity.Default registry (the documented oauth-device-code /
// injected-session-token built-ins, or an imported custom provider). A
// non-registered free-form label such as "oidc" yields providerSelected =
// false; those deployments front the registry with external auth and are
// exempt, matching selectIdentityProvider. Public mode opts out of identity
// by design (every layer visible); the empty/standalone default has no
// authenticated callers (the local operator is the de facto admin, §13.10).
func identityVisibilityGuard(identityProvider string, providerSelected, publicMode, verifierInstalled bool) error {
	if !providerSelected || publicMode || verifierInstalled {
		return nil
	}
	return fmt.Errorf("config.identity_provider_unverified: PODIUM_IDENTITY_PROVIDER=%q has no request-time token verifier wired, so the registry would resolve every caller as anonymous-public and never apply per-layer visibility (§2.2, §6.3.1); only injected-session-token is verified server-side in this build. Set PODIUM_PUBLIC_MODE=true to run an open registry, or use injected-session-token", identityProvider)
}

// injectedTokenAudienceGuard refuses startup when the injected-session-token
// provider is selected without a configured audience.
//
// spec: §6.3.2 — aud ("registry endpoint") is one of the claims the registry
// verifies on every injected-session-token call. The audience is the
// registry's own endpoint, so without PODIUM_OAUTH_AUDIENCE set the verifier
// cannot validate aud and would accept a runtime-signed token regardless of
// its audience, including one bound to a different registry. Because runtime
// signing keys are shared trust anchors, that is a cross-registry
// token-confusion surface. Fail closed at boot with an actionable error
// rather than reject every token at request time. Other providers (the
// audience is optional for oauth-device-code) are exempt.
func injectedTokenAudienceGuard(identityProvider, audience string) error {
	if identityProvider == "injected-session-token" && strings.TrimSpace(audience) == "" {
		return fmt.Errorf("config.injected_token_audience_unset: PODIUM_IDENTITY_PROVIDER=injected-session-token requires PODIUM_OAUTH_AUDIENCE set to this registry's endpoint so the required aud claim is verified on every token (§6.3.2)")
	}
	return nil
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
