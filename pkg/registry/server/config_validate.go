package server

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// Errors that the §13.10 startup-time configuration guards may
// surface.
var (
	// ErrPublicModeWithIdP signals that PODIUM_PUBLIC_MODE and
	// PODIUM_IDENTITY_PROVIDER were both set; §13.10 mandates these
	// are mutually exclusive. Maps to config.public_mode_with_idp.
	ErrPublicModeWithIdP = errors.New("config.public_mode_with_idp")

	// ErrPublicBindNonLoopback signals that a registry that authenticates no
	// caller, one in public mode, one with no identity provider, or one with
	// PODIUM_IDENTITY_PROVIDER=external, was given a non-loopback bind address
	// and --allow-public-bind was not set. §13.10 ("Loopback bind by default",
	// "Loopback bind without an identity provider", "External
	// authentication") and §13.2.2 require such a registry to bind 127.0.0.1
	// unless the operator explicitly opts into a non-loopback bind. Maps to
	// config.public_bind_refused.
	ErrPublicBindNonLoopback = errors.New("config.public_bind_refused")

	// ErrWebUIPublicBindRefused signals that the §13.10 web UI was enabled on
	// a non-loopback bind without the explicit opt-in. §13.10 ("Behind a
	// flag") refuses to bind the UI to a non-loopback address unless
	// --web-ui-allow-public-bind is also passed and an identity provider is
	// configured, so a UI reachable beyond the loopback interface is served
	// only by a registry that resolves a caller's identity and filters what it
	// serves by that identity. Maps to config.web_ui_public_bind_refused.
	ErrWebUIPublicBindRefused = errors.New("config.web_ui_public_bind_refused")

	// ErrWebUIAuthUnconfigured signals that the §6.3.4 browser acquisition
	// flow was enabled on a configuration that does not satisfy the §13.10
	// browser-flow guard. The refusal names the conjunct that failed. Maps to
	// config.web_ui_auth_unconfigured.
	ErrWebUIAuthUnconfigured = errors.New("config.web_ui_auth_unconfigured")

	// ErrTrustedHeadersPublicBind signals that the §6.3.3 trusted-headers
	// provider was selected on a non-loopback bind without a proxy secret or
	// --allow-public-bind. Because trusted-headers reads identity from
	// unverified headers, the identity it trusts is exactly the set of clients
	// that can reach the bind address; a non-loopback bind must be backed by a
	// request-level proxy secret or an operator-declared upstream control.
	// Maps to config.trusted_headers_public_bind.
	ErrTrustedHeadersPublicBind = errors.New("config.trusted_headers_public_bind")

	// ErrTrustedHeadersMultitenantNoSecret signals that the §6.3.3
	// trusted-headers provider was selected on a multi-tenant registry without
	// a proxy secret. Because X-Podium-User-Org selects among tenants and a
	// co-resident process can reach a loopback bind, co-residency does not
	// authenticate the gateway, so the proxy secret is required on every
	// request regardless of bind address. Maps to
	// config.trusted_headers_multitenant_no_secret.
	ErrTrustedHeadersMultitenantNoSecret = errors.New("config.trusted_headers_multitenant_no_secret")
)

// StartupConfig captures the pieces of the server config that need
// the §13.10 cross-validation guards. The bootstrap path constructs
// one before opening any backends so misconfigurations fail fast.
type StartupConfig struct {
	PublicMode       bool
	IdentityProvider string
	// Bind is the resolved listen address (host:port). The §13.10 loopback
	// guard inspects it when public mode is engaged.
	Bind string
	// AllowPublicBind is the §13.10 escape hatch (--allow-public-bind /
	// PODIUM_ALLOW_PUBLIC_BIND). When false, public mode refuses a
	// non-loopback bind.
	AllowPublicBind bool
	// WebUI reports whether the §13.10 web UI is mounted (--web-ui /
	// PODIUM_WEB_UI). The non-loopback guard only applies when it is on.
	WebUI bool
	// WebUIAllowPublicBind is the §13.10 web-UI escape hatch
	// (--web-ui-allow-public-bind / PODIUM_WEB_UI_ALLOW_PUBLIC_BIND). The UI
	// may bind a non-loopback address only when this is set and an identity
	// provider is configured.
	WebUIAllowPublicBind bool
	// WebUIAuth reports whether the §6.3.4 browser acquisition flow is
	// enabled (--web-ui-auth / PODIUM_WEB_UI_AUTH). It is the one enablement
	// key: the §7.3.4 authentication routes are mounted only when it is set,
	// and only then does the oidc-jwt verifier read __Host-podium_session.
	WebUIAuth bool
	// WebUIAuthTransactionTTL is the sign-in window
	// (--web-ui-auth-transaction-ttl / PODIUM_WEB_UI_AUTH_TRANSACTION_TTL),
	// carried as __Host-podium_auth's Max-Age (§7.3.4). It carries a default,
	// so no configuration leaves it unset.
	WebUIAuthTransactionTTL time.Duration
	// The §6.3.4 acquisition values: the OAuth client identifier, the client
	// credential, the redirect URI, and the identity provider's authorization
	// and token endpoints. Each is required where the browser flow is
	// enabled.
	WebUIOAuthClientID              string
	WebUIOAuthClientSecret          string
	WebUIRedirectURI                string
	WebUIOAuthAuthorizationEndpoint string
	WebUIOAuthTokenEndpoint         string
	// WebUIOAuthScopes is the scope set the sign-in redirect sends
	// (PODIUM_WEB_UI_OAUTH_SCOPES). It carries a default and is no
	// acquisition value.
	WebUIOAuthScopes []string
	// WebUIOAuthExchangeTimeout bounds the callback's token-endpoint request
	// (PODIUM_WEB_UI_OAUTH_EXCHANGE_TIMEOUT). It carries a default and is no
	// acquisition value.
	WebUIOAuthExchangeTimeout time.Duration
	// TrustedProxySecret is the §6.3.3 PODIUM_TRUSTED_PROXY_SECRET. When set, a
	// non-loopback trusted-headers bind is permitted because the secret gates
	// header trust at the request level.
	TrustedProxySecret string
	// MultiTenant reports whether the registry runs in §6.3.1 multi-tenant mode
	// (PODIUM_MULTI_TENANT). Under trusted-headers, multi-tenant mode requires a
	// proxy secret on every request regardless of bind address.
	MultiTenant bool
}

// Validate enforces the §13.10 startup invariants:
//
//   - public_mode and an identity provider are mutually exclusive.
//   - public_mode binds a loopback address unless --allow-public-bind is set.
//   - a registry with no identity provider, or with the external value,
//     binds a loopback address unless --allow-public-bind is set.
//   - the §6.3.4 browser flow is enabled only on a configuration that
//     satisfies every conjunct of the browser-flow guard.
func (c StartupConfig) Validate() error {
	if c.PublicMode && ConfiguresIdentityProvider(c.IdentityProvider) {
		return fmt.Errorf("%w: PUBLIC_MODE and IDENTITY_PROVIDER (%q) cannot both be set",
			ErrPublicModeWithIdP, c.IdentityProvider)
	}
	// §13.10 "Loopback bind by default": public mode serves every artifact to
	// every caller, so a non-loopback bind without the explicit opt-in is a
	// misconfiguration the registry refuses at startup, naming the address.
	if c.PublicMode && !c.AllowPublicBind && !isLoopbackBind(c.Bind) {
		return fmt.Errorf("%w: public mode binds 127.0.0.1 by default; %q is not a loopback address (set PODIUM_ALLOW_PUBLIC_BIND=true or pass --allow-public-bind to override)",
			ErrPublicBindNonLoopback, c.Bind)
	}
	// §13.10 "Behind a flag": a registry with no identity provider resolves
	// every UI caller as anonymous, so a UI reachable beyond the loopback
	// interface requires both the explicit --web-ui-allow-public-bind opt-in
	// and a configured identity provider that resolves a caller's identity and
	// filters what it serves by that identity. Either condition missing on a
	// non-loopback bind is refused.
	if c.WebUI && !isLoopbackBind(c.Bind) {
		if !c.WebUIAllowPublicBind || !ConfiguresIdentityProvider(c.IdentityProvider) {
			return fmt.Errorf("%w: the web UI on a non-loopback bind (%q) requires --web-ui-allow-public-bind and a configured identity provider",
				ErrWebUIPublicBindRefused, c.Bind)
		}
	}
	// §13.10 "Loopback bind without an identity provider": such a registry
	// authenticates no caller and admits every caller on the admin arm, which
	// covers every layer write, the host-path reads, and erasure, so a
	// non-loopback bind needs the same explicit opt-in public mode needs. It
	// runs after the web UI guard so a UI on a non-loopback bind keeps that
	// guard's own code.
	if !c.PublicMode && !ConfiguresIdentityProvider(c.IdentityProvider) && !c.AllowPublicBind && !isLoopbackBind(c.Bind) {
		return fmt.Errorf("%w: a registry with no identity provider admits every caller as an admin, so it binds a loopback address; %q is not one (configure an identity provider, or set PODIUM_ALLOW_PUBLIC_BIND=true or pass --allow-public-bind when something else limits who can reach it)",
			ErrPublicBindNonLoopback, c.Bind)
	}
	// §13.10 "External authentication": external wires no request-time
	// verifier, so every caller that reaches the address sees every layer. The
	// registry cannot tell whether the fronting authentication is the only
	// path to it, so a non-loopback bind takes the same explicit opt-in.
	if c.IdentityProvider == ExternalIdentityProvider && !c.AllowPublicBind && !isLoopbackBind(c.Bind) {
		return fmt.Errorf("%w: PODIUM_IDENTITY_PROVIDER=external verifies no caller, so every caller that reaches the registry sees every layer, and it binds a loopback address; %q is not one (set PODIUM_ALLOW_PUBLIC_BIND=true or pass --allow-public-bind when the authentication in front of the registry is the only path to it)",
			ErrPublicBindNonLoopback, c.Bind)
	}
	// §6.3.3 / §13.10 "Bind restriction under trusted-headers": the provider
	// trusts unverified identity headers, so the identity it trusts is exactly
	// the set of clients that can reach the bind address. A loopback bind is
	// always allowed (only a co-located process can connect); a non-loopback
	// bind must be backed by a request-level proxy secret or the operator's
	// explicit --allow-public-bind declaration that an upstream control keeps
	// the registry reachable only through the gateway.
	if c.IdentityProvider == "trusted-headers" {
		if c.MultiTenant {
			// §6.3.3: on a multi-tenant registry X-Podium-User-Org selects among
			// tenants, and a co-resident process can reach a loopback bind, so the
			// proxy secret is required on every request regardless of bind address.
			if c.TrustedProxySecret == "" {
				return fmt.Errorf("%w: trusted-headers on a multi-tenant registry requires PODIUM_TRUSTED_PROXY_SECRET on every request, because X-Podium-User-Org selects among tenants and co-residency does not authenticate the gateway",
					ErrTrustedHeadersMultitenantNoSecret)
			}
		} else if !isLoopbackBind(c.Bind) && c.TrustedProxySecret == "" && !c.AllowPublicBind {
			return fmt.Errorf("%w: trusted-headers reads unverified identity headers, so a non-loopback bind (%q) requires PODIUM_TRUSTED_PROXY_SECRET or --allow-public-bind",
				ErrTrustedHeadersPublicBind, c.Bind)
		}
	}
	// §13.10 "Browser-flow configuration guard". It runs after the public-mode
	// exclusion above rather than ahead of it, so a registry configured for
	// public mode with an identity provider keeps failing with
	// config.public_mode_with_idp.
	return c.validateBrowserFlow()
}

// ConfiguresIdentityProvider reports whether a PODIUM_IDENTITY_PROVIDER value
// configures an identity provider; the empty value and "none" configure none.
// Every startup and request-time decision that depends on whether a registry
// has an identity provider reads this one predicate, so a "none" registry
// cannot pass the loopback-bind refusal as unconfigured and then close the
// admin arm as configured.
//
// Spec: §13.10 (Loopback bind without an identity provider)
func ConfiguresIdentityProvider(provider string) bool {
	return provider != "" && provider != "none"
}

// ExternalIdentityProvider is the PODIUM_IDENTITY_PROVIDER value that declares
// authentication in front of the registry decides who reaches it. The registry
// wires no request-time verifier for it, so it resolves every caller as
// anonymous-public (§4.6 No-identity bypass). It is the one value outside the
// recognized providers that a registry starts with: any other unrecognized
// value is refused, so a mistyped provider name cannot start a registry that
// verifies no caller.
//
// ConfiguresIdentityProvider reports it as configured, so the admin arm, the
// default layer visibility, and the session posture treat it as a configured
// provider. The loopback-bind refusal in Validate applies to it explicitly.
//
// Spec: §13.10 (External authentication)
const ExternalIdentityProvider = "external"

// validateBrowserFlow enforces the §13.10 browser-flow configuration guard.
// The browser flow returns an IdP-signed token in __Host-podium_session and
// the registry verifies it on the next request, so the flow runs only on a
// registry that verifies that token: the web UI is mounted, the provider is
// oidc-jwt, public mode is off, every §6.3.4 acquisition value is set, and the
// redirect URI names a secure origin. A configuration that enables no browser
// flow reaches no conjunct.
//
// The transaction TTL, the scope set, and the exchange bound are no conjuncts:
// each carries a default, so no configuration leaves one unset.
func (c StartupConfig) validateBrowserFlow() error {
	if !c.WebUIAuth {
		return nil
	}
	if !c.WebUI {
		return fmt.Errorf("%w: the browser flow requires the web UI (--web-ui / PODIUM_WEB_UI)",
			ErrWebUIAuthUnconfigured)
	}
	if c.IdentityProvider != "oidc-jwt" {
		return fmt.Errorf("%w: the browser flow requires PODIUM_IDENTITY_PROVIDER=oidc-jwt (got %q), because the registry verifies the token it returns in __Host-podium_session against the issuer JWKS",
			ErrWebUIAuthUnconfigured, c.IdentityProvider)
	}
	// Unreachable: the public-mode exclusion above refuses public mode
	// together with oidc-jwt, and without an identity provider the oidc-jwt
	// conjunct fails first. The conjunct is carried because it states the full
	// set of conditions the flow requires and the shipped exclusion is keyed on
	// PODIUM_IDENTITY_PROVIDER alone, so that exclusion does not fire on
	// PODIUM_WEB_UI_AUTH.
	if c.PublicMode {
		return fmt.Errorf("%w: the browser flow requires public mode off (PODIUM_PUBLIC_MODE)",
			ErrWebUIAuthUnconfigured)
	}
	// §6.3.4: the acquisition values. PODIUM_OAUTH_AUTHORIZATION_ENDPOINT is
	// the device-code flow's endpoint and is not read here, so a configuration
	// that sets it alone fails the authorization-endpoint conjunct.
	acquisition := []struct{ key, value string }{
		{"PODIUM_WEB_UI_OAUTH_CLIENT_ID", c.WebUIOAuthClientID},
		{"PODIUM_WEB_UI_OAUTH_CLIENT_SECRET", c.WebUIOAuthClientSecret},
		{"PODIUM_WEB_UI_REDIRECT_URI", c.WebUIRedirectURI},
		{"PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT", c.WebUIOAuthAuthorizationEndpoint},
		{"PODIUM_WEB_UI_OAUTH_TOKEN_ENDPOINT", c.WebUIOAuthTokenEndpoint},
	}
	for _, v := range acquisition {
		if strings.TrimSpace(v.value) == "" {
			return fmt.Errorf("%w: the browser flow requires %s", ErrWebUIAuthUnconfigured, v.key)
		}
	}
	return validateWebUIRedirectURI(c.WebUIRedirectURI)
}

// validateWebUIRedirectURI enforces the §13.10 secure-origin requirement on
// PODIUM_WEB_UI_REDIRECT_URI. Every §7.3.4 cookie carries the __Host- prefix,
// the prefix forces Secure, and a browser neither stores nor returns a Secure
// cookie on a non-secure origin, so the callback's origin is an https URL or a
// loopback http URL, which a browser treats as a secure context. Absent the
// check, a plain http non-loopback deployment would set the pre-authorization
// cookie, receive none back, and refuse every sign-in with auth.csrf_invalid.
func validateWebUIRedirectURI(raw string) error {
	refuse := fmt.Errorf("%w: PODIUM_WEB_UI_REDIRECT_URI (%q) must be an https URL or an http URL whose host is a loopback address, because the __Host- session cookie is Secure and a browser does not return it on a non-secure origin",
		ErrWebUIAuthUnconfigured, raw)
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return refuse
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && isLoopbackHost(u.Hostname()) {
		return nil
	}
	return refuse
}

// isLoopbackBind reports whether a host:port bind address listens only on a
// loopback interface. An empty host or a wildcard address (0.0.0.0, ::) binds
// every interface and is therefore not loopback. "localhost" and any loopback
// IP literal (127.0.0.0/8, ::1) are loopback.
func isLoopbackBind(bind string) bool {
	// An entirely unset bind means the resolved default (127.0.0.1) applies,
	// which is loopback. A literal ":8080" (empty host with a port) is the
	// wildcard bind and is handled below as non-loopback.
	if bind == "" {
		return true
	}
	host := bind
	if h, _, err := net.SplitHostPort(bind); err == nil {
		host = h
	}
	// ":8080" or "" binds all interfaces, which isLoopbackHost reports false
	// for.
	return isLoopbackHost(host)
}

// isLoopbackHost reports whether a bare host (no port) names a loopback
// interface. "localhost" and any loopback IP literal (127.0.0.0/8, ::1) are
// loopback. A non-IP, non-localhost hostname could resolve anywhere and is
// treated as non-loopback, which is the fail-closed answer for both callers.
func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
