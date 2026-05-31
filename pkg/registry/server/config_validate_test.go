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
// config.public_bind_refused, naming the address. F-13.2.1.
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

// Spec: §13.10 — the --allow-public-bind escape hatch permits a non-loopback
// public-mode bind. F-13.2.1.
func TestStartupConfig_PublicModeNonLoopbackAllowed(t *testing.T) {
	t.Parallel()
	cfg := server.StartupConfig{PublicMode: true, Bind: "0.0.0.0:8080", AllowPublicBind: true}
	if err := cfg.Validate(); err != nil {
		t.Errorf("non-loopback bind with --allow-public-bind: %v", err)
	}
}

// Spec: §13.10 — public mode binds a loopback address by default without the
// escape hatch. F-13.2.1.
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
// deployment binds any address. F-13.2.1.
func TestStartupConfig_NonPublicNonLoopbackAllowed(t *testing.T) {
	t.Parallel()
	cfg := server.StartupConfig{Bind: "0.0.0.0:8080"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("non-public non-loopback bind rejected: %v", err)
	}
}
