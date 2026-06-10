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
