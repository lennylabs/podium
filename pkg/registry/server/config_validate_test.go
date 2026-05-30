package server_test

import (
	"errors"
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
