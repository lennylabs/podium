// Package identity exposes the IdentityProvider SPI per spec §6.3, plus
// the two built-in providers oauth-device-code and injected-session-token.
//
// Phase 11 ships the SPI plus an injected-session-token implementation
// that verifies signed JWTs against a registered runtime key. The
// device-code flow lands alongside it; the OS keychain integration ships
// behind a build tag so unit tests can run hermetically.
package identity

import (
	"context"
	"errors"
	"time"
)

// Errors returned by Provider implementations.
var (
	// ErrUntrustedRuntime maps to auth.untrusted_runtime in §6.10.
	ErrUntrustedRuntime = errors.New("identity: untrusted_runtime")
	// ErrTokenExpired maps to auth.token_expired in §6.10.
	ErrTokenExpired = errors.New("identity: token_expired")
	// ErrDeviceCodeRequired surfaces the device-code flow URL and code
	// to the caller; the caller is responsible for displaying both.
	ErrDeviceCodeRequired = errors.New("identity: device_code_required")
)

// Identity is the OAuth-attested caller identity attached to every
// registry call (§6.3.1).
type Identity struct {
	Sub             string
	Email           string
	OrgID           string
	Groups          []string
	IsAuthenticated bool
}

// Provider is the SPI implementations satisfy.
type Provider interface {
	// ID returns the provider identifier (e.g., "oauth-device-code").
	ID() string
	// Resolve returns the current identity for outgoing registry calls.
	// Implementations may block to perform a token refresh.
	Resolve(ctx context.Context) (Identity, error)
}

// InjectedSessionToken is a Provider that reads a runtime-signed JWT
// from an env var or file path (§6.3.2). Phase 11 implementation parses
// the JWT and validates against a registered runtime key.
type InjectedSessionToken struct {
	// TokenSource returns the current token. Tests substitute a function
	// that returns a fixture; production wires an env / file watcher.
	TokenSource func() (string, error)
	// Verify checks the JWT claims and runtime registration. Must
	// reject unsigned tokens with ErrUntrustedRuntime per §6.3.2.
	Verify func(rawJWT string) (Identity, error)
}

// ID returns "injected-session-token".
func (InjectedSessionToken) ID() string { return "injected-session-token" }

// Resolve reads the current token and verifies it.
func (p InjectedSessionToken) Resolve(_ context.Context) (Identity, error) {
	if p.TokenSource == nil || p.Verify == nil {
		return Identity{}, errors.New("identity: provider not fully configured")
	}
	tok, err := p.TokenSource()
	if err != nil {
		return Identity{}, err
	}
	return p.Verify(tok)
}

// OAuthDeviceCode is a Provider stub for the device-code flow. The
// device-code endpoint integration lands in Phase 11; this stub returns
// ErrDeviceCodeRequired so callers can wire the surface today.
type OAuthDeviceCode struct {
	// VerificationURL is what callers display to the user.
	VerificationURL string
	// Code is the user-facing code.
	Code string
	// AcquireToken polls the IdP token endpoint until the device-code
	// flow completes or the deadline elapses.
	AcquireToken func(ctx context.Context) (Identity, error)
}

// ID returns "oauth-device-code".
func (OAuthDeviceCode) ID() string { return "oauth-device-code" }

// Resolve runs AcquireToken when wired; otherwise returns
// ErrDeviceCodeRequired so the caller can elicit the URL and code.
func (p OAuthDeviceCode) Resolve(ctx context.Context) (Identity, error) {
	if p.AcquireToken == nil {
		return Identity{}, ErrDeviceCodeRequired
	}
	return p.AcquireToken(ctx)
}

// TokenTTL is the recommended access-token lifetime (§6.3).
const TokenTTL = 15 * time.Minute
