package server

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// Errors that the §13.10 startup-time configuration guards may
// surface.
var (
	// ErrPublicModeWithIdP signals that PODIUM_PUBLIC_MODE and
	// PODIUM_IDENTITY_PROVIDER were both set; §13.10 mandates these
	// are mutually exclusive. Maps to config.public_mode_with_idp.
	ErrPublicModeWithIdP = errors.New("config.public_mode_with_idp")

	// ErrPublicBindNonLoopback signals that public mode was engaged with a
	// non-loopback bind address and --allow-public-bind was not set. §13.10
	// ("Loopback bind by default") and §13.2.2 require public mode to bind
	// 127.0.0.1 unless the operator explicitly opts into a non-loopback bind.
	// Maps to config.public_bind_refused.
	ErrPublicBindNonLoopback = errors.New("config.public_bind_refused")
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
}

// Validate enforces the §13.10 startup invariants:
//
//   - public_mode and an identity provider are mutually exclusive.
//   - public_mode binds a loopback address unless --allow-public-bind is set.
func (c StartupConfig) Validate() error {
	if c.PublicMode && c.IdentityProvider != "" && c.IdentityProvider != "none" {
		return fmt.Errorf("%w: PUBLIC_MODE and IDENTITY_PROVIDER (%q) cannot both be set",
			ErrPublicModeWithIdP, c.IdentityProvider)
	}
	// §13.10 "Loopback bind by default": public mode serves every artifact to
	// every caller, so a non-loopback bind without the explicit opt-in is a
	// misconfiguration the registry refuses at startup, naming the address.
	if c.PublicMode && !c.AllowPublicBind && !isLoopbackBind(c.Bind) {
		return fmt.Errorf("%w: public mode binds 127.0.0.1 by default; %q is not a loopback address (pass --allow-public-bind to override)",
			ErrPublicBindNonLoopback, c.Bind)
	}
	return nil
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
	host = strings.TrimSpace(host)
	if host == "" {
		// ":8080" or "" binds all interfaces.
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// A non-IP, non-localhost hostname could resolve anywhere; treat it
		// as non-loopback so the operator must opt in explicitly.
		return false
	}
	return ip.IsLoopback()
}
