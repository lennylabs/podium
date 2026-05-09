package server

import (
	"errors"
	"fmt"
)

// Errors that the §13.10 startup-time configuration guards may
// surface.
var (
	// ErrPublicModeWithIdP signals that PODIUM_PUBLIC_MODE and
	// PODIUM_IDENTITY_PROVIDER were both set; §13.10 mandates these
	// are mutually exclusive. Maps to config.public_mode_with_idp.
	ErrPublicModeWithIdP = errors.New("config.public_mode_with_idp")
)

// StartupConfig captures the pieces of the server config that need
// the §13.10 cross-validation guards. The bootstrap path constructs
// one before opening any backends so misconfigurations fail fast.
type StartupConfig struct {
	PublicMode       bool
	IdentityProvider string
}

// Validate enforces the §13.10 startup invariants:
//
//   - public_mode and an identity provider are mutually exclusive.
func (c StartupConfig) Validate() error {
	if c.PublicMode && c.IdentityProvider != "" && c.IdentityProvider != "none" {
		return fmt.Errorf("%w: PUBLIC_MODE and IDENTITY_PROVIDER (%q) cannot both be set",
			ErrPublicModeWithIdP, c.IdentityProvider)
	}
	return nil
}
