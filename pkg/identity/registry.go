package identity

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Registry is the process-global registration seam for the §9.1
// IdentityProvider SPI, distributed per §9.2 as an in-process Go module
// imported into a registry build. A deployment that needs a custom
// IdentityProvider imports a package whose init calls Default.Register,
// and the server selects the provider by id at startup. This mirrors the
// TypeProvider seam (pkg/typeprovider) so every SPI §9.2 names by example
// has the same compile-time, in-process registration mechanism.
//
// spec: §9.1 (IdentityProvider), §9.2 (plugins ship as Go modules
// imported into a registry build).
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// Factory constructs a Provider from the deployment configuration. The
// server passes a Config carrying the resolved settings the built-in and
// custom providers consume (audience, token source, verifier). Returning
// an error fails startup loud rather than serving callers anonymously.
type Factory func(Config) (Provider, error)

// Config carries the resolved §6.3 / §13.12 identity settings the server
// hands a Factory at selection time. Fields are the wire-serializable
// settings a provider needs; an out-of-process provider (§9.3) would
// receive the same values.
type Config struct {
	// Audience is the §6.3.2 `aud` claim the injected-session-token
	// verifier requires. Empty disables audience checking.
	Audience string
	// AuthorizationEndpoint is the §6.3 device-code authorization endpoint.
	AuthorizationEndpoint string
	// TokenSource returns the current runtime-signed JWT for the
	// injected-session-token provider. Nil when the provider does not read
	// an injected token.
	TokenSource func() (string, error)
	// Verify checks a runtime-signed JWT and returns the attested Identity.
	// Nil when the provider does not verify injected tokens.
	Verify func(rawJWT string) (Identity, error)
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{factories: map[string]Factory{}}
}

// Default is the process-global identity-provider registry the server
// consults when selecting a provider for PODIUM_IDENTITY_PROVIDER. It is
// seeded with the built-in oauth-device-code and injected-session-token
// providers; deployers add custom providers via Default.Register.
var Default = newDefault()

func newDefault() *Registry {
	r := NewRegistry()
	_ = r.Register("oauth-device-code", func(c Config) (Provider, error) {
		return OAuthDeviceCode{VerificationURL: c.AuthorizationEndpoint}, nil
	})
	_ = r.Register("injected-session-token", func(c Config) (Provider, error) {
		return InjectedSessionToken{TokenSource: c.TokenSource, Verify: c.Verify}, nil
	})
	return r
}

// Register adds a Factory under id. Returns an error when id is empty or
// already registered so two providers cannot silently claim the same id.
func (r *Registry) Register(id string, f Factory) error {
	if id == "" {
		return errors.New("identity: empty provider id")
	}
	if f == nil {
		return errors.New("identity: nil factory")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.factories[id]; ok {
		return fmt.Errorf("identity: provider %q already registered", id)
	}
	r.factories[id] = f
	return nil
}

// New constructs the provider registered under id using cfg. Returns an
// error wrapping ErrUnknownProvider when no provider is registered.
func (r *Registry) New(id string, cfg Config) (Provider, error) {
	r.mu.RLock()
	f, ok := r.factories[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, id)
	}
	return f(cfg)
}

// Has reports whether a provider is registered under id.
func (r *Registry) Has(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.factories[id]
	return ok
}

// IDs returns every registered provider id, sorted.
func (r *Registry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.factories))
	for id := range r.factories {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ErrUnknownProvider signals a PODIUM_IDENTITY_PROVIDER value with no
// registered factory. The server treats it as a fatal misconfiguration.
var ErrUnknownProvider = errors.New("identity: unknown_provider")
