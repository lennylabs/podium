package identity

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// OIDCJWT is the server-side oidc-jwt provider (§6.3.3). It verifies a
// gateway-forwarded IdP-signed JWT against the issuer's JWKS on every request.
// Identity is resolved from the request by the request-time verifier
// (OIDCVerifier.Verify), so Resolve is never called on the server path; it
// returns an error to make a stray client-side use loud rather than silent.
type OIDCJWT struct{}

// ID returns "oidc-jwt".
func (OIDCJWT) ID() string { return "oidc-jwt" }

// Resolve reports that oidc-jwt resolves identity from the inbound request
// rather than acquiring a token to present.
func (OIDCJWT) Resolve(context.Context) (Identity, error) {
	return Identity{}, errServerSideProvider
}

// errServerSideProvider is returned by the Resolve method of the
// registry-process providers (oidc-jwt, trusted-headers), which resolve the
// caller from the inbound request and have no token of their own to present.
var errServerSideProvider = errors.New("identity: server-side provider resolves the caller from the request, not via Resolve")

// UntrustedTokenError reports that a gateway-forwarded oidc-jwt token failed
// verification (§6.3.3). It wraps ErrUntrustedToken so existing errors.Is
// checks keep working, and carries the token's issuer so the HTTP boundary can
// populate the §6.10 envelope's details.token_iss. Issuer is "" when the token
// was malformed before the iss claim could be read.
type UntrustedTokenError struct {
	Issuer string
	Reason string
}

func (e *UntrustedTokenError) Error() string {
	if e.Issuer != "" {
		return fmt.Sprintf("identity: untrusted_token: %s: %s", e.Issuer, e.Reason)
	}
	return "identity: untrusted_token: " + e.Reason
}

// Unwrap lets errors.Is(err, ErrUntrustedToken) match an *UntrustedTokenError.
func (e *UntrustedTokenError) Unwrap() error { return ErrUntrustedToken }

// ErrKeySetUnavailable reports that the issuer JWKS could not be fetched and no
// cached key set is available, so a forwarded token cannot be verified. Per
// §6.3.3 the request is then treated as anonymous (public visibility only)
// rather than rejected, distinguishing a transient IdP outage from a token
// that actually fails verification.
var ErrKeySetUnavailable = errors.New("identity: key_set_unavailable")

// errJWKSFetch is the internal sentinel for a JWKS/discovery fetch failure with
// no usable cache; Verify maps it to ErrKeySetUnavailable.
var errJWKSFetch = errors.New("jwks fetch failed")

// untrustedToken builds an *UntrustedTokenError for issuer with reason.
func untrustedToken(issuer, reason string) error {
	return &UntrustedTokenError{Issuer: issuer, Reason: reason}
}

// oidcAllowedAlgs is the set of JWT signing algorithms the oidc-jwt verifier
// accepts. Symmetric (HS*) and "none" are excluded: a JWKS publishes
// asymmetric public keys, and accepting HS* would let an attacker sign with
// the public key as the HMAC secret.
var oidcAllowedAlgs = []string{"RS256", "RS384", "RS512", "ES256", "ES384", "ES512", "EdDSA"}

// OIDCVerifier verifies a gateway-forwarded OIDC JWT against the issuer's JWKS
// (§6.3.3 oidc-jwt). It resolves the JWKS URI from the issuer's OIDC discovery
// document at ${issuer}/.well-known/openid-configuration and caches the key set
// for cacheTTL, refreshing when the cache is older than cacheTTL or when a
// token presents a kid absent from the cached set (key rotation).
type OIDCVerifier struct {
	issuer   string
	audience string
	cacheTTL time.Duration

	// subjectClaim optionally names the claim read as the caller's subject
	// when the token carries no `sub` (AD FS access tokens carry `idsub`
	// and no `sub`). Empty keeps the standard `sub` requirement.
	subjectClaim string
	// groupsClaim optionally names the claim read for group membership.
	// Empty reads the standard `groups` claim. AD FS issuance rules emit
	// the full claim-type URI unless the rule is authored with a short
	// name.
	groupsClaim string

	// httpc and clock are overridable by tests in-package.
	httpc *http.Client
	clock func() time.Time

	mu      sync.Mutex
	jwksURI string
	// accessTokenIssuer is the discovery document's access_token_issuer, an
	// AD FS extension: AD FS access tokens carry the federation-service
	// identifier as iss, which differs from the discovery `issuer`. A token
	// iss matching either value is accepted; both come from the https
	// discovery document, so the trust root is unchanged.
	accessTokenIssuer string
	keys              map[string]any // kid -> *rsa.PublicKey | *ecdsa.PublicKey | ed25519.PublicKey
	fetchedAt         time.Time
}

// NewOIDCVerifier returns a verifier for issuer that validates the aud claim
// against audience and caches the issuer JWKS for cacheTTL. A non-positive
// cacheTTL falls back to the §13.12 default of 300 seconds. The caller is
// responsible for the §13.12 config.invalid_issuer_scheme (https) and
// config.oidc_jwt_audience_unset startup checks; the verifier itself fails
// closed on an empty audience at request time as a defense in depth.
func NewOIDCVerifier(issuer, audience string, cacheTTL time.Duration) *OIDCVerifier {
	if cacheTTL <= 0 {
		cacheTTL = 300 * time.Second
	}
	return &OIDCVerifier{
		issuer:   strings.TrimRight(issuer, "/"),
		audience: audience,
		cacheTTL: cacheTTL,
		httpc:    &http.Client{Timeout: 10 * time.Second},
		clock:    time.Now,
	}
}

// SetSubjectClaim names the claim read as the subject when the token has no
// `sub` claim. An empty name keeps the standard `sub` requirement.
func (v *OIDCVerifier) SetSubjectClaim(name string) { v.subjectClaim = name }

// SetGroupsClaim names the claim read for group membership instead of the
// standard `groups` claim. An empty name keeps the default.
func (v *OIDCVerifier) SetGroupsClaim(name string) { v.groupsClaim = name }

// issuerAccepted reports whether iss matches the configured issuer or the
// discovery document's access_token_issuer (AD FS emits the latter on access
// tokens).
func (v *OIDCVerifier) issuerAccepted(iss string) bool {
	if iss == v.issuer {
		return true
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.accessTokenIssuer != "" && iss == v.accessTokenIssuer
}

// Prime fetches the issuer's discovery document and JWKS once, so a boot-time
// failure to reach the IdP is surfaced at startup rather than on the first
// request (§6.3.3: "If the IdP's OIDC discovery document or JWKS ... is
// unreachable at startup, the registry fails to start").
func (v *OIDCVerifier) Prime() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.refreshLocked()
}

// Verify checks raw against the issuer's JWKS and returns the attested
// Identity. It returns ErrTokenExpired for an expired token and an
// *UntrustedTokenError for any other verification failure (bad signature,
// wrong iss, wrong/missing aud, malformed token).
func (v *OIDCVerifier) Verify(raw string) (Identity, error) {
	if raw == "" {
		return Identity{}, untrustedToken("", "empty token")
	}
	// Parse without verification to read iss and select the issuer before
	// trusting any signature, mirroring the §6.3.2 runtime verifier.
	parsed, _, err := jwt.NewParser().ParseUnverified(raw, jwt.MapClaims{})
	if err != nil {
		return Identity{}, untrustedToken("", err.Error())
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return Identity{}, untrustedToken("", "claims missing")
	}
	issuer, _ := claims["iss"].(string)
	if issuer == "" {
		return Identity{}, untrustedToken("", "iss claim missing")
	}
	if !v.issuerAccepted(issuer) {
		return Identity{}, untrustedToken(issuer, fmt.Sprintf("iss %q matches neither the configured issuer %q nor the discovery document's access_token_issuer", issuer, v.issuer))
	}
	if v.audience == "" {
		// Defense in depth: the §13.12 config.oidc_jwt_audience_unset startup
		// guard should already have refused boot. An unverifiable aud would
		// accept a token issued for any relying party that shares the issuer.
		return Identity{}, untrustedToken(issuer, "registry audience is not configured; the required aud claim cannot be verified")
	}

	// Resolve the signing key before verifying so a JWKS-fetch failure (a
	// transient IdP outage) is distinguished from a token that fails
	// verification: the former is anonymous, the latter is auth.untrusted_token.
	kid, _ := parsed.Header["kid"].(string)
	key, err := v.keyForKID(kid)
	if err != nil {
		if errors.Is(err, errJWKSFetch) {
			return Identity{}, fmt.Errorf("%w: %v", ErrKeySetUnavailable, err)
		}
		return Identity{}, untrustedToken(issuer, err.Error())
	}

	// The issuer was checked above (configured issuer or access_token_issuer),
	// so the parser does not re-check it with jwt.WithIssuer.
	opts := []jwt.ParserOption{
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods(oidcAllowedAlgs),
	}
	if v.clock != nil {
		opts = append(opts, jwt.WithTimeFunc(func() (out jwtTime) {
			return jwtTime(v.clock())
		}))
	}
	if _, err := jwt.NewParser(opts...).Parse(raw, func(*jwt.Token) (any, error) { return key, nil }); err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return Identity{}, fmt.Errorf("%w: %v", ErrTokenExpired, err)
		}
		return Identity{}, untrustedToken(issuer, err.Error())
	}

	id, err := v.oidcClaimIdentity(claims)
	if err != nil {
		return Identity{}, untrustedToken(issuer, err.Error())
	}
	return id, nil
}

// oidcClaimIdentity builds the caller Identity from an IdP-signed token,
// honoring the verifier's subject-claim and groups-claim overrides. Group
// values are accepted in array and single-string form: an IdP with exactly
// one group for the user (AD FS, Auth0) serializes the claim as a plain
// string.
func (v *OIDCVerifier) oidcClaimIdentity(claims jwt.MapClaims) (Identity, error) {
	var sub string
	if v.subjectClaim != "" {
		sub, _ = claims[v.subjectClaim].(string)
	}
	if sub == "" {
		sub, _ = claims["sub"].(string)
	}
	if sub == "" {
		if v.subjectClaim != "" {
			return Identity{}, fmt.Errorf("subject claim %q and sub claim both missing", v.subjectClaim)
		}
		return Identity{}, errors.New("sub claim missing")
	}
	id := Identity{Sub: sub, IsAuthenticated: true}
	if email, ok := claims["email"].(string); ok {
		id.Email = email
	}
	if org, ok := claims["org_id"].(string); ok {
		id.OrgID = org
	}
	groupsKey := "groups"
	if v.groupsClaim != "" {
		groupsKey = v.groupsClaim
	}
	switch g := claims[groupsKey].(type) {
	case []any:
		for _, one := range g {
			if s, ok := one.(string); ok {
				id.Groups = append(id.Groups, s)
			}
		}
	case string:
		if g != "" {
			id.Groups = []string{g}
		}
	}
	id.Scopes = scopesFromClaims(claims)
	return id, nil
}

// keyForKID returns the verification key for kid, refreshing the JWKS once on a
// cache miss to absorb key rotation. It returns an error wrapping errJWKSFetch
// when the key set cannot be fetched and no cache is available (the §6.3.3
// anonymous-on-unavailable case), and a plain "no key" error when the JWKS is
// reachable but does not publish the kid.
func (v *OIDCVerifier) keyForKID(kid string) (any, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.keys == nil || v.clock().Sub(v.fetchedAt) >= v.cacheTTL {
		if err := v.refreshLocked(); err != nil && v.keys == nil {
			return nil, fmt.Errorf("%w: %v", errJWKSFetch, err)
		}
	}
	if key := v.selectKey(kid); key != nil {
		return key, nil
	}
	// A kid absent from the cached set forces a refresh before rejection
	// (§13.12 PODIUM_OAUTH_JWKS_CACHE_TTL_SECONDS).
	if err := v.refreshLocked(); err != nil {
		if v.keys == nil {
			return nil, fmt.Errorf("%w: %v", errJWKSFetch, err)
		}
		return nil, fmt.Errorf("no JWKS key for kid %q (refresh failed: %v)", kid, err)
	}
	if key := v.selectKey(kid); key != nil {
		return key, nil
	}
	return nil, fmt.Errorf("no JWKS key for kid %q", kid)
}

// selectKey returns the key for kid, or the sole key when the token carries no
// kid and the JWKS publishes exactly one key. Caller holds v.mu.
func (v *OIDCVerifier) selectKey(kid string) any {
	if key, ok := v.keys[kid]; ok {
		return key
	}
	if kid == "" && len(v.keys) == 1 {
		for _, key := range v.keys {
			return key
		}
	}
	return nil
}

// refreshLocked re-resolves the JWKS URI (when unknown) and re-fetches the key
// set. Caller holds v.mu.
func (v *OIDCVerifier) refreshLocked() error {
	if v.jwksURI == "" {
		uri, err := v.discoverJWKSURI()
		if err != nil {
			return err
		}
		v.jwksURI = uri
	}
	keys, err := v.fetchJWKS(v.jwksURI)
	if err != nil {
		return err
	}
	v.keys = keys
	v.fetchedAt = v.clock()
	return nil
}

// discoverJWKSURI reads jwks_uri from the issuer's OIDC discovery document.
// It also captures the document's access_token_issuer (an AD FS extension)
// so Verify accepts access tokens carrying that iss. Caller holds v.mu.
func (v *OIDCVerifier) discoverJWKSURI() (string, error) {
	url := v.issuer + "/.well-known/openid-configuration"
	body, err := v.getJSON(url)
	if err != nil {
		return "", fmt.Errorf("fetch discovery document %s: %w", url, err)
	}
	var doc struct {
		JWKSURI           string `json:"jwks_uri"`
		AccessTokenIssuer string `json:"access_token_issuer"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("parse discovery document %s: %w", url, err)
	}
	if doc.JWKSURI == "" {
		return "", fmt.Errorf("discovery document %s has no jwks_uri", url)
	}
	v.accessTokenIssuer = doc.AccessTokenIssuer
	return doc.JWKSURI, nil
}

// jwk is one JSON Web Key from a JWKS (RFC 7517). Only the fields needed to
// reconstruct an asymmetric public key are decoded.
type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// fetchJWKS fetches and parses the JWKS at uri into a kid->public-key map.
func (v *OIDCVerifier) fetchJWKS(uri string) (map[string]any, error) {
	body, err := v.getJSON(uri)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS %s: %w", uri, err)
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("parse JWKS %s: %w", uri, err)
	}
	out := make(map[string]any, len(set.Keys))
	for _, k := range set.Keys {
		key, err := k.publicKey()
		if err != nil {
			// Skip keys this verifier cannot use rather than failing the whole
			// set; an IdP may publish key types beyond the accepted algorithms.
			continue
		}
		out[k.Kid] = key
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("JWKS %s has no usable asymmetric keys", uri)
	}
	return out, nil
}

// publicKey reconstructs the asymmetric public key from a JWK.
func (k jwk) publicKey() (any, error) {
	switch k.Kty {
	case "RSA":
		n, err := b64uBigInt(k.N)
		if err != nil {
			return nil, fmt.Errorf("rsa n: %w", err)
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return nil, fmt.Errorf("rsa e: %w", err)
		}
		e := new(big.Int).SetBytes(eBytes)
		if !e.IsInt64() || e.Int64() < 2 {
			return nil, errors.New("rsa e out of range")
		}
		return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
	case "EC":
		curve, byteLen, err := ecCurve(k.Crv)
		if err != nil {
			return nil, err
		}
		x, err := b64uBigInt(k.X)
		if err != nil {
			return nil, fmt.Errorf("ec x: %w", err)
		}
		y, err := b64uBigInt(k.Y)
		if err != nil {
			return nil, fmt.Errorf("ec y: %w", err)
		}
		if len(x.Bytes()) > byteLen || len(y.Bytes()) > byteLen {
			return nil, errors.New("ec coordinate out of range")
		}
		if !curve.IsOnCurve(x, y) {
			return nil, errors.New("ec point not on curve")
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
	case "OKP":
		if k.Crv != "Ed25519" {
			return nil, fmt.Errorf("unsupported OKP curve %q", k.Crv)
		}
		x, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, fmt.Errorf("okp x: %w", err)
		}
		if len(x) != ed25519.PublicKeySize {
			return nil, errors.New("okp x wrong length")
		}
		return ed25519.PublicKey(x), nil
	default:
		return nil, fmt.Errorf("unsupported kty %q", k.Kty)
	}
}

func ecCurve(crv string) (elliptic.Curve, int, error) {
	switch crv {
	case "P-256":
		return elliptic.P256(), 32, nil
	case "P-384":
		return elliptic.P384(), 48, nil
	case "P-521":
		return elliptic.P521(), 66, nil
	default:
		return nil, 0, fmt.Errorf("unsupported EC curve %q", crv)
	}
}

func b64uBigInt(s string) (*big.Int, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(b), nil
}

// getJSON fetches uri and returns the body, bounding the response size so a
// hostile or misbehaving endpoint cannot exhaust memory.
func (v *OIDCVerifier) getJSON(uri string) ([]byte, error) {
	resp, err := v.httpc.Get(uri)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
