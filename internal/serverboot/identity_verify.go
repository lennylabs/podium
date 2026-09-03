package serverboot

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
)

// injectedTokenVerifier builds the §6.3.2 request-time verifier for the
// injected-session-token provider. It extracts the bearer token from the
// Authorization header, verifies its signature against the registered
// runtime keys for the accepted-audience set, applies the §6.3.1
// IdpGroupMapping, and returns the caller layer.Identity carrying the
// verified claims (including any "podium:*" scopes for the §6.3.1 scope
// narrowing). A verification failure is returned verbatim so the server
// maps identity.ErrUntrustedRuntime / identity.ErrTokenExpired (and the
// typed *identity.UntrustedRuntimeError carrying the issuer) to the §6.10
// envelope.
func injectedTokenVerifier(keys identity.RuntimeKeyVerifierStore, audiences []string, groups *identity.IdpGroupMapping) func(*http.Request) (layer.Identity, error) {
	verify := keys.JWTVerifier(audiences, nil)
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

// layerCallerResolver adapts a §6.3.2/§6.3.3 request-time verifier into the
// resolver the §7.3.1 layer endpoint reads. It returns the verifier's error
// verbatim, so the endpoint refuses a credential that failed verification
// with the §6.10 envelope the server already maps that error to, and it
// returns the anonymous-public caller with no error when no verifier is
// wired, which is the no-provider, public-mode, and free-form-label
// deployment. Whether an absent credential is a failure is the provider's
// own rule and is not decided here: injectedTokenVerifier reports one,
// oidcJWTVerifier and trustedHeadersVerifier do not.
func layerCallerResolver(verify func(*http.Request) (layer.Identity, error)) func(*http.Request) (layer.Identity, error) {
	return func(r *http.Request) (layer.Identity, error) {
		if verify == nil {
			return layer.Identity{IsPublic: true}, nil
		}
		return verify(r)
	}
}

// layerIdentityResolver adapts a §6.3.2 request-time verifier into a resolver
// that never fails. It is the swallowing form: a verified token yields the
// authenticated identity, and a missing or invalid token, or a nil verifier
// (no server-side verification wired), resolves to the anonymous-public
// caller. It is read by the §4.7.2 admin gate and by the §7.3.4 posture read,
// which refuse no request for lack of a credential
// (pkg/registry/server/webui_session.go:12-14). The §7.3.1 layer endpoint
// reads layerCallerResolver instead and refuses a verification failure
// itself.
func layerIdentityResolver(verify func(*http.Request) (layer.Identity, error)) func(*http.Request) layer.Identity {
	resolve := layerCallerResolver(verify)
	return func(r *http.Request) layer.Identity {
		if id, err := resolve(r); err == nil {
			return id
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
// verifiedProviders names the identity providers this build verifies at
// request time, in the order the guard message offers them. The message is
// built from this list so the two cannot drift: the previous message named
// injected-session-token as the only verified provider long after oidc-jwt
// (§6.3.3) and trusted-headers (§6.3.3) gained verifiers, which sent an
// operator toward the one provider that needs a runtime key registered first.
//
// A provider that gains or loses a request-time verifier in serverboot must be
// added to or removed from this list in the same change.
var verifiedProviders = []string{"injected-session-token", "oidc-jwt", "trusted-headers"}

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
	return fmt.Errorf("config.identity_provider_unverified: PODIUM_IDENTITY_PROVIDER=%q has no request-time token verifier wired, so the registry would resolve every caller as anonymous-public and never apply per-layer visibility (§2.2, §6.3.1); the registry verifies %s server-side. Set PODIUM_PUBLIC_MODE=true to run an open registry, or select one of those", identityProvider, strings.Join(verifiedProviders, ", "))
}

// canonicalAudience returns the canonical entry of the accepted-audience set
// (§6.3.4): the first entry, which is the audience the registry asks for when
// it initiates a flow itself. A set that resolves to no entry yields "".
func canonicalAudience(audiences []string) string {
	if len(audiences) == 0 {
		return ""
	}
	return audiences[0]
}

// injectedTokenAudienceGuard refuses startup when the injected-session-token
// provider is selected over an accepted-audience set that resolves to no
// entry.
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
func injectedTokenAudienceGuard(identityProvider string, audiences []string) error {
	if identityProvider == "injected-session-token" && len(identity.NormalizeAudiences(audiences)) == 0 {
		return fmt.Errorf("config.injected_token_audience_unset: PODIUM_IDENTITY_PROVIDER=injected-session-token requires PODIUM_OAUTH_AUDIENCE set to this registry's endpoint so the required aud claim is verified on every token (§6.3.2)")
	}
	return nil
}

// runtimeKeyBootstrapGuard refuses startup when the injected-session-token
// provider is selected over an empty trusted key set.
//
// spec: §6.3.2 — the trusted key set comes from operator-supplied
// configuration read at startup, and there is no request-time registration
// API. A registry that verifies runtime-signed tokens against no key rejects
// every call with auth.untrusted_runtime and can accept no first key, so it
// serves nothing. Fail closed at boot instead. The unset-path case falls
// through this arm, because an unset PODIUM_RUNTIME_KEYS_PATH leaves the
// in-memory registry empty. Other providers never consult the store, so an
// empty set is not a failure for them.
func runtimeKeyBootstrapGuard(identityProvider, path string, keys identity.RuntimeKeyVerifierStore) error {
	if identityProvider != "injected-session-token" {
		return nil
	}
	if keys != nil && len(keys.All()) > 0 {
		return nil
	}
	return fmt.Errorf("config.runtime_keys_unavailable: PODIUM_IDENTITY_PROVIDER=injected-session-token requires PODIUM_RUNTIME_KEYS_PATH to name a file carrying at least one trusted runtime signing key (§6.3.2), and the configured path %q carries none; write one with 'podium admin runtime register --keys-file', then restart the registry", path)
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
		Audiences:             identity.NormalizeAudiences(cfg.oauthAudiences),
		AuthorizationEndpoint: cfg.oauthAuthorizationEndpoint,
	})
}

// bearerToken returns the token from an "Authorization: Bearer <token>"
// header, or "" when the header is absent or not a bearer credential. The
// empty string drives the verifier's empty-token rejection
// (auth.untrusted_runtime), so an unauthenticated call in
// injected-session-token mode is rejected rather than served anonymously.
func bearerToken(r *http.Request) string {
	return bearerTokenFromHeader(r, "Authorization")
}

// bearerTokenFromHeader returns the bearer credential from the named header,
// or "" when the header is absent or not a bearer credential. The registry
// parses the named header's value as "Bearer <token>" regardless of the header
// name (§6.3.3 oidc-jwt token_header): the prefix is matched case-insensitively
// and surrounding whitespace is trimmed. An empty headerName defaults to
// Authorization.
func bearerTokenFromHeader(r *http.Request, headerName string) string {
	if headerName == "" {
		headerName = "Authorization"
	}
	const prefix = "Bearer "
	h := r.Header.Get(headerName)
	if len(h) >= len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

// oidcJWTVerifier builds the §6.3.3 request-time verifier for the oidc-jwt
// provider. It reads the caller's JWT from the configured token header,
// verifies it against the issuer's JWKS, applies the §6.3.1 IdpGroupMapping,
// and returns the caller layer.Identity. A token that fails verification is
// returned as an error so the server maps identity.ErrTokenExpired /
// *identity.UntrustedTokenError to the §6.10 envelope (auth.token_expired /
// auth.untrusted_token).
//
// sessionCookie carries the §6.3.4 browser-flow enablement. It is the whole
// condition on the second accepted credential location: with it set, the
// verifier reads __Host-podium_session when the configured token header
// carries no bearer credential, and with it clear the verifier reads no
// cookie at all, so a stale session cookie sent to such a registry carries
// nothing. The configured token header is read first either way, and the two
// locations are never merged, because a gateway that authenticated the
// request is the authority in that deployment.
//
// A request whose configured token header carries no bearer credential is
// anonymous rather than rejected, and it sees public visibility only (§4.6).
// Where the browser flow is enabled, such a request is anonymous only when it
// also presents no valid __Host-podium_session cookie.
func oidcJWTVerifier(verifier *identity.OIDCVerifier, tokenHeader string, groups *identity.IdpGroupMapping, sessionCookie bool) func(*http.Request) (layer.Identity, error) {
	return func(r *http.Request) (layer.Identity, error) {
		raw := bearerTokenFromHeader(r, tokenHeader)
		if raw == "" && sessionCookie {
			if c, err := r.Cookie(server.CookieSession); err == nil {
				raw = strings.TrimSpace(c.Value)
			}
		}
		if raw == "" {
			return layer.Identity{}, nil
		}
		id, err := verifier.Verify(raw)
		if err != nil {
			if errors.Is(err, identity.ErrKeySetUnavailable) {
				// §6.3.3: while the key set is unavailable at runtime,
				// verification fails closed and the request is anonymous (it sees
				// public visibility only) rather than being rejected.
				return layer.Identity{}, nil
			}
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

// trustedHeadersVerifier builds the §6.3.3 request-time verifier for the
// trusted-headers provider. It reads the gateway-injected identity headers,
// gated by the proxy secret when configured, and never returns an error: a
// missing or distrusted identity yields the anonymous, public-only caller
// rather than a rejection (§6.3.3). Groups come from X-Podium-User-Groups
// directly; SCIM and the IdpGroupMapping adapter are not consulted.
func trustedHeadersVerifier(proxySecret string) func(*http.Request) (layer.Identity, error) {
	return func(r *http.Request) (layer.Identity, error) {
		id := identity.IdentityFromTrustedHeaders(
			r.Header.Get(identity.HeaderUserSub),
			r.Header.Get(identity.HeaderUserEmail),
			r.Header.Get(identity.HeaderUserGroups),
			r.Header.Get(identity.HeaderUserOrg),
			proxySecret,
			r.Header.Get(identity.HeaderProxySecret),
		)
		return layer.Identity{
			Sub:             id.Sub,
			Email:           id.Email,
			OrgID:           id.OrgID,
			Groups:          id.Groups,
			IsAuthenticated: id.IsAuthenticated,
		}, nil
	}
}

// oidcJWTConfigGuard refuses startup when the oidc-jwt provider is selected
// without a usable issuer and audience.
//
// spec: §6.3.3, §13.12 — oidc-jwt verifies the forwarded token's aud against
// PODIUM_OAUTH_AUDIENCE (so an unverifiable aud cannot accept a token issued
// for any relying party that shares the issuer) and fetches the OIDC discovery
// document and JWKS from PODIUM_OAUTH_ISSUER over https (so a man-in-the-middle
// cannot substitute a signing key over an http endpoint). A loopback issuer
// (https://127.0.0.1, https://localhost) is permitted for local IdP testing and
// still requires https. Other providers are exempt.
func oidcJWTConfigGuard(identityProvider, issuer string, audiences []string) error {
	if identityProvider != "oidc-jwt" {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(issuer))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("config.invalid_issuer_scheme: PODIUM_IDENTITY_PROVIDER=oidc-jwt requires PODIUM_OAUTH_ISSUER to be an https URL (got %q); the registry fetches the OIDC discovery document and JWKS over this URL and an http endpoint lets a man-in-the-middle substitute a signing key (§6.3.3, §13.12)", issuer)
	}
	if len(identity.NormalizeAudiences(audiences)) == 0 {
		return fmt.Errorf("config.oidc_jwt_audience_unset: PODIUM_IDENTITY_PROVIDER=oidc-jwt requires PODIUM_OAUTH_AUDIENCE set to this registry's endpoint so the required aud claim is verified on every forwarded token (§6.3.3, §13.12)")
	}
	return nil
}
