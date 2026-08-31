package server_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
)

// Spec: §13.10 / §6.10 — public mode is mutually exclusive with an
// identity provider; setting both fails startup with
// config.public_mode_with_idp.
// Matrix: §6.10 (config.public_mode_with_idp)
func TestStartupConfig_PublicModeWithIdPRejected(t *testing.T) {
	t.Parallel()
	cfg := server.StartupConfig{
		PublicMode:       true,
		IdentityProvider: "oauth-device-code",
	}
	err := cfg.Validate()
	if !errors.Is(err, server.ErrPublicModeWithIdP) {
		t.Errorf("got %v, want ErrPublicModeWithIdP", err)
	}
}

// Spec: §13.10 — public mode without an identity provider is allowed.
func TestStartupConfig_PublicModeAlone(t *testing.T) {
	t.Parallel()
	cfg := server.StartupConfig{PublicMode: true}
	if err := cfg.Validate(); err != nil {
		t.Errorf("public mode alone: %v", err)
	}
}

// Spec: §13.10 — an identity provider without public mode is allowed.
func TestStartupConfig_IdentityProviderAlone(t *testing.T) {
	t.Parallel()
	cfg := server.StartupConfig{IdentityProvider: "oauth-device-code"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("identity provider alone: %v", err)
	}
}

// Spec: §13.10 ("Loopback bind by default") / §13.2.2 — public mode with a
// non-loopback bind and no --allow-public-bind fails startup with
// config.public_bind_refused, naming the address.
func TestStartupConfig_PublicModeNonLoopbackRefused(t *testing.T) {
	t.Parallel()
	for _, bind := range []string{"0.0.0.0:8080", "192.168.1.10:8080", "[::]:8080", ":8080", "registry.acme.com:8080"} {
		cfg := server.StartupConfig{PublicMode: true, Bind: bind}
		err := cfg.Validate()
		if !errors.Is(err, server.ErrPublicBindNonLoopback) {
			t.Errorf("bind %q: got %v, want ErrPublicBindNonLoopback", bind, err)
		}
		if err != nil && !strings.Contains(err.Error(), bind) {
			t.Errorf("bind %q: error does not name the address: %v", bind, err)
		}
	}
}

// Spec: §6.3.3 / §13.10 ("Bind restriction under trusted-headers") — the
// trusted-headers provider on a non-loopback bind without a proxy secret or
// --allow-public-bind fails startup with config.trusted_headers_public_bind,
// naming the address.
// Matrix: §6.10 (config.trusted_headers_public_bind)
func TestStartupConfig_TrustedHeadersNonLoopbackRefused(t *testing.T) {
	t.Parallel()
	for _, bind := range []string{"0.0.0.0:8080", "192.168.1.10:8080", "[::]:8080", ":8080", "registry.acme.com:8080"} {
		cfg := server.StartupConfig{IdentityProvider: "trusted-headers", Bind: bind}
		err := cfg.Validate()
		if !errors.Is(err, server.ErrTrustedHeadersPublicBind) {
			t.Errorf("bind %q: got %v, want ErrTrustedHeadersPublicBind", bind, err)
		}
		if err != nil && !strings.Contains(err.Error(), bind) {
			t.Errorf("bind %q: error does not name the address: %v", bind, err)
		}
	}
}

// Spec: §6.3.3 — a loopback bind under trusted-headers is always allowed
// (only a co-located process can connect); a proxy secret or --allow-public-bind
// permits a non-loopback bind.
func TestStartupConfig_TrustedHeadersBindAllowed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  server.StartupConfig
	}{
		{"loopback needs nothing", server.StartupConfig{IdentityProvider: "trusted-headers", Bind: "127.0.0.1:8080"}},
		{"localhost is loopback", server.StartupConfig{IdentityProvider: "trusted-headers", Bind: "localhost:8080"}},
		{"non-loopback with proxy secret", server.StartupConfig{IdentityProvider: "trusted-headers", Bind: "0.0.0.0:8080", TrustedProxySecret: "s3cr3t"}},
		{"non-loopback with allow-public-bind", server.StartupConfig{IdentityProvider: "trusted-headers", Bind: "0.0.0.0:8080", AllowPublicBind: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.Validate(); err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
		})
	}
}

// Spec: §6.3.3 — oidc-jwt carries no bind restriction (it verifies every token
// against the issuer's signing key regardless of the network path).
func TestStartupConfig_OIDCJWTNoBindRestriction(t *testing.T) {
	t.Parallel()
	cfg := server.StartupConfig{IdentityProvider: "oidc-jwt", Bind: "0.0.0.0:8080"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("oidc-jwt non-loopback bind: %v", err)
	}
}

// Spec: §6.3.3 — trusted-headers on a multi-tenant registry requires a proxy
// secret on every request regardless of bind address, because X-Podium-User-Org
// selects among tenants and co-residency does not authenticate the gateway.
// Matrix: §6.10 (config.trusted_headers_multitenant_no_secret)
func TestStartupConfig_TrustedHeadersMultitenantRequiresSecret(t *testing.T) {
	t.Parallel()
	// No secret fails regardless of bind, including loopback.
	for _, bind := range []string{"127.0.0.1:8080", "0.0.0.0:8080"} {
		cfg := server.StartupConfig{IdentityProvider: "trusted-headers", MultiTenant: true, Bind: bind}
		if err := cfg.Validate(); !errors.Is(err, server.ErrTrustedHeadersMultitenantNoSecret) {
			t.Errorf("bind %q: got %v, want ErrTrustedHeadersMultitenantNoSecret", bind, err)
		}
	}
	// With a secret it is allowed even on a non-loopback bind.
	if err := (server.StartupConfig{IdentityProvider: "trusted-headers", MultiTenant: true, Bind: "0.0.0.0:8080", TrustedProxySecret: "s"}).Validate(); err != nil {
		t.Errorf("multi-tenant with secret: %v", err)
	}
	// --allow-public-bind does not substitute for the secret in multi-tenant mode.
	if err := (server.StartupConfig{IdentityProvider: "trusted-headers", MultiTenant: true, Bind: "0.0.0.0:8080", AllowPublicBind: true}).Validate(); !errors.Is(err, server.ErrTrustedHeadersMultitenantNoSecret) {
		t.Errorf("--allow-public-bind must not substitute for the secret in multi-tenant mode: got %v", err)
	}
}

// Spec: §13.10 — the --allow-public-bind escape hatch permits a non-loopback
// public-mode bind.
func TestStartupConfig_PublicModeNonLoopbackAllowed(t *testing.T) {
	t.Parallel()
	cfg := server.StartupConfig{PublicMode: true, Bind: "0.0.0.0:8080", AllowPublicBind: true}
	if err := cfg.Validate(); err != nil {
		t.Errorf("non-loopback bind with --allow-public-bind: %v", err)
	}
}

// Spec: §13.10 — public mode binds a loopback address by default without the
// escape hatch.
func TestStartupConfig_PublicModeLoopbackDefault(t *testing.T) {
	t.Parallel()
	for _, bind := range []string{"127.0.0.1:8080", "localhost:8080", "[::1]:8080", "127.0.0.5:9000"} {
		cfg := server.StartupConfig{PublicMode: true, Bind: bind}
		if err := cfg.Validate(); err != nil {
			t.Errorf("loopback bind %q rejected: %v", bind, err)
		}
	}
}

// Spec: §13.10 — the loopback guard only applies in public mode; a standard
// deployment binds any address.
func TestStartupConfig_NonPublicNonLoopbackAllowed(t *testing.T) {
	t.Parallel()
	cfg := server.StartupConfig{Bind: "0.0.0.0:8080"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("non-public non-loopback bind rejected: %v", err)
	}
}

// Spec: §13.10 — the web UI on a non-loopback bind is refused
// unless --web-ui-allow-public-bind is set AND an identity provider is
// configured. Each missing condition on a non-loopback bind fails startup with
// config.web_ui_public_bind_refused.
func TestStartupConfig_WebUINonLoopbackRefused(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  server.StartupConfig
	}{
		{"no opt-in, no idp", server.StartupConfig{WebUI: true, Bind: "0.0.0.0:8080"}},
		{"opt-in but no idp", server.StartupConfig{WebUI: true, WebUIAllowPublicBind: true, Bind: "0.0.0.0:8080"}},
		{"idp but no opt-in", server.StartupConfig{WebUI: true, IdentityProvider: "oidc", Bind: "0.0.0.0:8080"}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if err := c.cfg.Validate(); !errors.Is(err, server.ErrWebUIPublicBindRefused) {
				t.Errorf("got %v, want ErrWebUIPublicBindRefused", err)
			}
		})
	}
}

// Spec: §13.10 — the web UI binds a non-loopback address when both
// the escape hatch and an identity provider are present, and binds a loopback
// address (the standalone default) with no opt-in at all.
func TestStartupConfig_WebUIAllowed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  server.StartupConfig
	}{
		{"non-loopback with opt-in and idp", server.StartupConfig{WebUI: true, WebUIAllowPublicBind: true, IdentityProvider: "oidc", Bind: "0.0.0.0:8080"}},
		{"loopback standalone, no opt-in", server.StartupConfig{WebUI: true, Bind: "127.0.0.1:8080"}},
		{"loopback default bind", server.StartupConfig{WebUI: true, Bind: ""}},
		{"ui off, non-loopback bind", server.StartupConfig{WebUI: false, Bind: "0.0.0.0:8080"}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if err := c.cfg.Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

// browserFlowConfig returns a StartupConfig that satisfies every conjunct of
// the §13.10 browser-flow guard. Each refusal case below breaks exactly one
// conjunct of it, so the case names the conjunct under test.
func browserFlowConfig() server.StartupConfig {
	return server.StartupConfig{
		WebUIAuth:                       true,
		WebUI:                           true,
		IdentityProvider:                "oidc-jwt",
		WebUIOAuthClientID:              "podium-web-ui",
		WebUIOAuthClientSecret:          "s3cr3t",
		WebUIRedirectURI:                "https://podium.acme.com/v1/ui/auth/callback",
		WebUIOAuthAuthorizationEndpoint: "https://idp.acme.com/authorize",
		WebUIOAuthTokenEndpoint:         "https://idp.acme.com/token",
	}
}

// Spec: §13.10 ("Browser-flow configuration guard") / §6.3.4 — enabling the
// browser flow on a configuration that fails one conjunct fails startup with
// config.web_ui_auth_unconfigured, naming the failed conjunct.
func TestStartupConfig_BrowserFlowRefused(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// mutate breaks one conjunct of the satisfied configuration.
		mutate func(*server.StartupConfig)
		// names is the substring the refusal carries so the operator can see
		// which conjunct failed.
		names string
	}{
		{"web UI disabled", func(c *server.StartupConfig) { c.WebUI = false }, "PODIUM_WEB_UI"},
		{"no identity provider", func(c *server.StartupConfig) { c.IdentityProvider = "" }, "oidc-jwt"},
		{"public mode with no provider", func(c *server.StartupConfig) {
			c.IdentityProvider = ""
			c.PublicMode = true
		}, "oidc-jwt"},
		{"trusted-headers", func(c *server.StartupConfig) { c.IdentityProvider = "trusted-headers" }, "oidc-jwt"},
		{"injected-session-token", func(c *server.StartupConfig) { c.IdentityProvider = "injected-session-token" }, "oidc-jwt"},
		{"no client id", func(c *server.StartupConfig) { c.WebUIOAuthClientID = "" }, "PODIUM_WEB_UI_OAUTH_CLIENT_ID"},
		{"no client secret", func(c *server.StartupConfig) { c.WebUIOAuthClientSecret = "" }, "PODIUM_WEB_UI_OAUTH_CLIENT_SECRET"},
		{"no redirect uri", func(c *server.StartupConfig) { c.WebUIRedirectURI = "" }, "PODIUM_WEB_UI_REDIRECT_URI"},
		{"no authorization endpoint", func(c *server.StartupConfig) { c.WebUIOAuthAuthorizationEndpoint = "" }, "PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT"},
		{"no token endpoint", func(c *server.StartupConfig) { c.WebUIOAuthTokenEndpoint = "" }, "PODIUM_WEB_UI_OAUTH_TOKEN_ENDPOINT"},
		{"redirect uri is non-loopback http", func(c *server.StartupConfig) {
			c.WebUIRedirectURI = "http://podium.acme.com/v1/ui/auth/callback"
		}, "PODIUM_WEB_UI_REDIRECT_URI"},
		{"redirect uri is no URL", func(c *server.StartupConfig) {
			c.WebUIRedirectURI = "podium.acme.com/v1/ui/auth/callback"
		}, "PODIUM_WEB_UI_REDIRECT_URI"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			cfg := browserFlowConfig()
			c.mutate(&cfg)
			err := cfg.Validate()
			if !errors.Is(err, server.ErrWebUIAuthUnconfigured) {
				t.Fatalf("got %v, want ErrWebUIAuthUnconfigured", err)
			}
			if !strings.Contains(err.Error(), c.names) {
				t.Errorf("refusal does not name the failed conjunct %q: %v", c.names, err)
			}
		})
	}
}

// Spec: §6.3.4 — the device-code flow's PODIUM_OAUTH_AUTHORIZATION_ENDPOINT is
// not accepted in place of PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT. The
// guard reads no device-code value at all, so an operator who sets only that
// key gets a startup refusal naming the key the browser flow needs rather than
// a redirect to nowhere. Not parallel: it sets a process-wide variable.
func TestStartupConfig_BrowserFlowRejectsDeviceCodeEndpoint(t *testing.T) {
	t.Setenv("PODIUM_OAUTH_AUTHORIZATION_ENDPOINT", "https://idp.acme.com/device/authorize")
	cfg := browserFlowConfig()
	cfg.WebUIOAuthAuthorizationEndpoint = ""
	err := cfg.Validate()
	if !errors.Is(err, server.ErrWebUIAuthUnconfigured) {
		t.Fatalf("got %v, want ErrWebUIAuthUnconfigured", err)
	}
	if !strings.Contains(err.Error(), "PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT") {
		t.Errorf("refusal does not name the key the browser flow reads: %v", err)
	}
}

// Spec: §13.10 ("The guard's ordering") — the browser-flow guard runs after
// the shipped public-mode exclusion, so public mode alongside oidc-jwt keeps
// failing with config.public_mode_with_idp even with the flow enabled.
// Matrix: §6.10 (config.public_mode_with_idp)
func TestStartupConfig_BrowserFlowOrdersAfterPublicModeExclusion(t *testing.T) {
	t.Parallel()
	cfg := browserFlowConfig()
	cfg.PublicMode = true
	if err := cfg.Validate(); !errors.Is(err, server.ErrPublicModeWithIdP) {
		t.Errorf("got %v, want ErrPublicModeWithIdP", err)
	}
}

// Spec: §13.10 / §6.3.4 — a configuration satisfying every conjunct starts.
// The redirect-URI conjunct admits an https URL and an http URL whose host is
// a loopback address, which are the two forms a browser treats as a secure
// context.
func TestStartupConfig_BrowserFlowAccepted(t *testing.T) {
	t.Parallel()
	for _, redirect := range []string{
		"https://podium.acme.com/v1/ui/auth/callback",
		"http://127.0.0.1:8080/v1/ui/auth/callback",
		"http://localhost:8080/v1/ui/auth/callback",
	} {
		redirect := redirect
		t.Run(redirect, func(t *testing.T) {
			t.Parallel()
			cfg := browserFlowConfig()
			cfg.WebUIRedirectURI = redirect
			if err := cfg.Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

// Spec: §13.10 — a configuration that enables no browser flow reaches no
// conjunct of the guard, which includes the shipped web-UI-only configuration
// (--web-ui alone).
func TestStartupConfig_BrowserFlowDisabledReachesNoConjunct(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  server.StartupConfig
	}{
		{"web UI alone", server.StartupConfig{WebUI: true}},
		{"nothing set", server.StartupConfig{}},
		{"web UI under oidc-jwt with no acquisition values", server.StartupConfig{WebUI: true, IdentityProvider: "oidc-jwt"}},
		{"web UI under trusted-headers", server.StartupConfig{WebUI: true, IdentityProvider: "trusted-headers"}},
		{"web UI in public mode", server.StartupConfig{WebUI: true, PublicMode: true}},
		{"acquisition values without the enablement key", func() server.StartupConfig {
			c := browserFlowConfig()
			c.WebUIAuth = false
			c.WebUI = false
			return c
		}()},
		{"a redirect URI the conjunct would refuse, flow off", func() server.StartupConfig {
			c := browserFlowConfig()
			c.WebUIAuth = false
			c.WebUIRedirectURI = "http://podium.acme.com/v1/ui/auth/callback"
			return c
		}()},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if err := c.cfg.Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}
