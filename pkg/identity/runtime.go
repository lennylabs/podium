package identity

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"errors"
	"fmt"
	"sync"

	"github.com/golang-jwt/jwt/v5"
)

// RuntimeKey is one (issuer, public key) pair registered with the
// registry per §6.3.2. The Algorithm field names the signing algorithm
// (RS256, ES256, EdDSA, etc.) the runtime uses.
type RuntimeKey struct {
	Issuer    string
	Algorithm string
	Key       any // *rsa.PublicKey | *ecdsa.PublicKey | ed25519.PublicKey
}

// RuntimeKeyRegistry maps an issuer to its registered signing key.
// The registry consults this on every call to verify the injected
// JWT (§6.3.2).
type RuntimeKeyRegistry struct {
	mu   sync.RWMutex
	keys map[string]RuntimeKey
}

// NewRuntimeKeyRegistry returns an empty registry.
func NewRuntimeKeyRegistry() *RuntimeKeyRegistry {
	return &RuntimeKeyRegistry{keys: map[string]RuntimeKey{}}
}

// Register adds or replaces a runtime's key.
func (r *RuntimeKeyRegistry) Register(rk RuntimeKey) error {
	if rk.Issuer == "" {
		return errors.New("runtime: issuer required")
	}
	if rk.Algorithm == "" {
		return errors.New("runtime: algorithm required")
	}
	if rk.Key == nil {
		return errors.New("runtime: key required")
	}
	switch rk.Key.(type) {
	case *rsa.PublicKey, *ecdsa.PublicKey, ed25519.PublicKey:
	default:
		return fmt.Errorf("runtime: unsupported key type %T", rk.Key)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys[rk.Issuer] = rk
	return nil
}

// Lookup returns the key for issuer or false when unregistered.
func (r *RuntimeKeyRegistry) Lookup(issuer string) (RuntimeKey, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	k, ok := r.keys[issuer]
	return k, ok
}

// JWTVerifier returns the verifier closure for InjectedSessionToken.
//
// audience is the registry endpoint the runtime is calling. clock is
// optional; when nil the verifier uses the system clock.
func (r *RuntimeKeyRegistry) JWTVerifier(audience string, clock func() jwt.NumericDate) func(string) (Identity, error) {
	return func(raw string) (Identity, error) {
		if raw == "" {
			return Identity{}, fmt.Errorf("%w: empty token", ErrUntrustedRuntime)
		}
		// First parse without verification to discover the issuer.
		parsed, _, err := jwt.NewParser().ParseUnverified(raw, jwt.MapClaims{})
		if err != nil {
			return Identity{}, fmt.Errorf("%w: %v", ErrUntrustedRuntime, err)
		}
		claims, ok := parsed.Claims.(jwt.MapClaims)
		if !ok {
			return Identity{}, fmt.Errorf("%w: claims missing", ErrUntrustedRuntime)
		}
		issuer, _ := claims["iss"].(string)
		if issuer == "" {
			return Identity{}, fmt.Errorf("%w: iss missing", ErrUntrustedRuntime)
		}
		runtime, ok := r.Lookup(issuer)
		if !ok {
			return Identity{}, fmt.Errorf("%w: %q", ErrUntrustedRuntime, issuer)
		}

		opts := []jwt.ParserOption{
			jwt.WithIssuer(issuer),
			jwt.WithValidMethods([]string{runtime.Algorithm}),
			jwt.WithExpirationRequired(),
		}
		if audience != "" {
			opts = append(opts, jwt.WithAudience(audience))
		}
		if clock != nil {
			opts = append(opts, jwt.WithTimeFunc(func() (out jwtTime) {
				nd := clock()
				return jwtTime(nd.Time)
			}))
		}
		_, err = jwt.NewParser(opts...).Parse(raw, func(token *jwt.Token) (any, error) {
			if token.Method.Alg() != runtime.Algorithm {
				return nil, fmt.Errorf("alg %q != registered %q",
					token.Method.Alg(), runtime.Algorithm)
			}
			return runtime.Key, nil
		})
		if err != nil {
			if errors.Is(err, jwt.ErrTokenExpired) {
				return Identity{}, fmt.Errorf("%w: %v", ErrTokenExpired, err)
			}
			return Identity{}, fmt.Errorf("%w: %v", ErrUntrustedRuntime, err)
		}

		// act and sub are required per §6.3.2.
		if _, ok := claims["act"]; !ok {
			return Identity{}, fmt.Errorf("%w: act claim missing", ErrUntrustedRuntime)
		}
		sub, _ := claims["sub"].(string)
		if sub == "" {
			return Identity{}, fmt.Errorf("%w: sub claim missing", ErrUntrustedRuntime)
		}

		id := Identity{
			Sub:             sub,
			IsAuthenticated: true,
		}
		if email, ok := claims["email"].(string); ok {
			id.Email = email
		}
		if org, ok := claims["org_id"].(string); ok {
			id.OrgID = org
		}
		if groups, ok := claims["groups"].([]any); ok {
			for _, g := range groups {
				if s, ok := g.(string); ok {
					id.Groups = append(id.Groups, s)
				}
			}
		}
		return id, nil
	}
}
