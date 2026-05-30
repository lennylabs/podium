package identity

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"errors"
	"fmt"
	"strings"
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

// UntrustedRuntimeError reports that an injected session token failed
// verification (§6.3.2). It wraps ErrUntrustedRuntime so existing
// errors.Is checks keep working, and carries the token's issuer so the
// HTTP boundary can populate the §6.10 envelope's details.runtime_iss.
// Issuer is "" when the token was malformed before the issuer could be
// read.
type UntrustedRuntimeError struct {
	Issuer string
	Reason string
}

func (e *UntrustedRuntimeError) Error() string {
	if e.Issuer != "" {
		return fmt.Sprintf("identity: untrusted_runtime: %s: %s", e.Issuer, e.Reason)
	}
	return "identity: untrusted_runtime: " + e.Reason
}

// Unwrap lets errors.Is(err, ErrUntrustedRuntime) match an
// *UntrustedRuntimeError.
func (e *UntrustedRuntimeError) Unwrap() error { return ErrUntrustedRuntime }

// untrusted builds an *UntrustedRuntimeError for issuer with reason.
func untrusted(issuer, reason string) error {
	return &UntrustedRuntimeError{Issuer: issuer, Reason: reason}
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

// RuntimeKeyVerifierStore is the runtime-key registry surface the server
// boot consumes: the admin register/list endpoint plus the request-time
// JWT verifier (§6.3.2). Both the in-memory RuntimeKeyRegistry and the
// file-persisted variant satisfy it.
type RuntimeKeyVerifierStore interface {
	Register(RuntimeKey) error
	All() []RuntimeKey
	JWTVerifier(audience string, clock func() jwt.NumericDate) func(string) (Identity, error)
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

// All returns every registered runtime key in deterministic
// insertion-order-independent order (sorted by issuer).
func (r *RuntimeKeyRegistry) All() []RuntimeKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RuntimeKey, 0, len(r.keys))
	issuers := make([]string, 0, len(r.keys))
	for iss := range r.keys {
		issuers = append(issuers, iss)
	}
	// stable order for tests
	for i := 0; i < len(issuers)-1; i++ {
		for j := i + 1; j < len(issuers); j++ {
			if issuers[j] < issuers[i] {
				issuers[i], issuers[j] = issuers[j], issuers[i]
			}
		}
	}
	for _, iss := range issuers {
		out = append(out, r.keys[iss])
	}
	return out
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
			return Identity{}, untrusted("", "empty token")
		}
		// First parse without verification to discover the issuer.
		parsed, _, err := jwt.NewParser().ParseUnverified(raw, jwt.MapClaims{})
		if err != nil {
			return Identity{}, untrusted("", err.Error())
		}
		claims, ok := parsed.Claims.(jwt.MapClaims)
		if !ok {
			return Identity{}, untrusted("", "claims missing")
		}
		issuer, _ := claims["iss"].(string)
		if issuer == "" {
			return Identity{}, untrusted("", "iss missing")
		}
		runtime, ok := r.Lookup(issuer)
		if !ok {
			// §6.3.2 / §6.9: "Without a registered signing key, the registry
			// rejects with auth.untrusted_runtime."
			return Identity{}, untrusted(issuer, "issuer is not a registered runtime")
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
			return Identity{}, untrusted(issuer, err.Error())
		}

		// act and sub are required per §6.3.2. §6.3.2 describes act as
		// "actor (the runtime itself)", and iss as the "runtime identifier
		// (must match a registered runtime)", so a conforming token names
		// the same runtime in both. Reject a token whose act does not
		// identify the verified runtime. The RFC 8693 nested object form
		// ({"act": {"sub": <runtime>}}) and the bare-string form are both
		// accepted. (spec: §6.3.2)
		if err := validateActor(claims["act"], issuer); err != nil {
			return Identity{}, untrusted(issuer, err.Error())
		}
		sub, _ := claims["sub"].(string)
		if sub == "" {
			return Identity{}, untrusted(issuer, "sub claim missing")
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
		// §6.3.1 fine-grained OAuth scopes: a token may carry "podium:*"
		// scope claims that narrow the caller's surface. Both the OAuth
		// "scope" (RFC 6749, space-delimited) and the Azure-style "scp"
		// claim are read, in either string or array form.
		id.Scopes = scopesFromClaims(claims)
		return id, nil
	}
}

// validateActor checks that the act claim identifies the runtime named by
// issuer (§6.3.2: act is "the runtime itself"). It accepts the bare-string
// form (act: "<runtime>") and the RFC 8693 nested form
// (act: {"sub": "<runtime>"}). A missing or mismatched actor is an error.
func validateActor(act any, issuer string) error {
	switch v := act.(type) {
	case nil:
		return errors.New("act claim missing")
	case string:
		if v == "" {
			return errors.New("act claim missing")
		}
		if v != issuer {
			return fmt.Errorf("act %q does not identify the runtime %q", v, issuer)
		}
		return nil
	case map[string]any:
		// RFC 8693 actor object: the actor's identifier is its sub.
		sub, _ := v["sub"].(string)
		if sub == "" {
			return errors.New("act object missing sub")
		}
		if sub != issuer {
			return fmt.Errorf("act sub %q does not identify the runtime %q", sub, issuer)
		}
		return nil
	default:
		return fmt.Errorf("act claim has unexpected type %T", act)
	}
}

// scopesFromClaims collects OAuth scope grants from the "scope" (RFC 6749,
// space-delimited string) and "scp" (Azure-style) claims, accepting either
// the string or array encoding. Order is preserved and duplicates are kept;
// callers parse "podium:*" entries (§6.3.1).
func scopesFromClaims(claims jwt.MapClaims) []string {
	var out []string
	add := func(raw any) {
		switch v := raw.(type) {
		case string:
			for _, f := range strings.Fields(v) {
				out = append(out, f)
			}
		case []any:
			for _, e := range v {
				if s, ok := e.(string); ok && s != "" {
					out = append(out, s)
				}
			}
		}
	}
	add(claims["scope"])
	add(claims["scp"])
	return out
}
