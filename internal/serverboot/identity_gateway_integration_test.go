package serverboot

// Full-stack integration coverage for the §6.3.3 gateway-delegated providers:
// a real core.Registry behind a real meta-tool server, with the production
// oidcJWTVerifier / trustedHeadersVerifier wired via server.WithIdentityVerifier,
// driven over HTTP. Asserts that per-layer visibility (§4.6) is enforced from
// the resolved identity, that anonymous callers see public layers only, that a
// forwarded token that fails verification maps to the §6.10 envelope, and that
// the trusted-headers proxy secret gates header trust.

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

const gwAudience = "https://podium.gateway.test"

// jwksIdP is a single-key OIDC issuer stub (discovery + JWKS).
type jwksIdP struct {
	srv  *httptest.Server
	priv *rsa.PrivateKey
	// accessTokenIssuer is published in the discovery document when set, the
	// §6.3.3 second accepted issuer. It is written by an option before the
	// stub starts serving, so no request observes it changing.
	accessTokenIssuer string
	// selfAccessTokenIssuer publishes the stub's own issuer as the
	// access_token_issuer. The stub's base URL is known only inside the
	// handler, so this flag stands in for a value the option cannot name.
	selfAccessTokenIssuer bool
}

// withAccessTokenIssuer publishes iss as the stub's access_token_issuer, the
// AD FS arrangement §6.3.3 covers: discovery is served under one value while
// the access token is stamped with the federation-service identifier.
func withAccessTokenIssuer(iss string) func(*jwksIdP) {
	return func(i *jwksIdP) { i.accessTokenIssuer = iss }
}

// withAccessTokenIssuerEqualToIssuer publishes the stub's own issuer, with a
// trailing slash, as the access_token_issuer. The published value trims to the
// configured issuer, so the accepted set stays one value.
func withAccessTokenIssuerEqualToIssuer() func(*jwksIdP) {
	return func(i *jwksIdP) { i.selfAccessTokenIssuer = true }
}

func newJWKSIdP(t *testing.T, opts ...func(*jwksIdP)) *jwksIdP {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	idp := &jwksIdP{priv: priv}
	for _, opt := range opts {
		opt(idp)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		doc := map[string]any{"issuer": base, "jwks_uri": base + "/jwks"}
		switch {
		case idp.selfAccessTokenIssuer:
			doc["access_token_issuer"] = base + "/"
		case idp.accessTokenIssuer != "":
			doc["access_token_issuer"] = idp.accessTokenIssuer
		}
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		pub := &idp.priv.PublicKey
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "gw-1",
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}}})
	})
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

func (i *jwksIdP) issuer() string { return i.srv.URL }

func (i *jwksIdP) sign(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "gw-1"
	s, err := tok.SignedString(i.priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func gwClaims(iss, sub string, groups []string) jwt.MapClaims {
	c := jwt.MapClaims{
		"iss": iss, "aud": gwAudience, "sub": sub,
		"exp": time.Now().Add(10 * time.Minute).Unix(),
	}
	if len(groups) > 0 {
		gs := make([]any, len(groups))
		for i, g := range groups {
			gs[i] = g
		}
		c["groups"] = gs
	}
	return c
}

// gatewayServer stands up a registry with a public layer and an
// engineering-group layer, behind the given identity verifier.
func gatewayServer(t *testing.T, verify func(*http.Request) (layer.Identity, error)) *httptest.Server {
	t.Helper()
	st := store.NewMemory()
	const tenant = "gw-tenant"
	if err := st.CreateTenant(t.Context(), store.Tenant{ID: tenant, Name: tenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	layers := []layer.Layer{
		{ID: "pub", Precedence: 1, Visibility: layer.Visibility{Public: true}},
		{ID: "eng", Precedence: 2, Visibility: layer.Visibility{Groups: []string{"engineering"}}},
	}
	put := func(artifactID, layerID string) {
		t.Helper()
		if err := st.PutManifest(t.Context(), store.ManifestRecord{
			TenantID: tenant, ArtifactID: artifactID, Version: "1.0.0",
			ContentHash: "sha256:" + artifactID, Type: "context",
			Description: artifactID, Layer: layerID,
			IngestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("PutManifest(%s): %v", artifactID, err)
		}
	}
	put("pub/welcome", "pub")
	put("eng/secret", "eng")

	reg := core.New(st, tenant, layers)
	srv := server.New(reg, server.WithIdentityVerifier(verify))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func gwGet(t *testing.T, url string, headers map[string]string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func loadArtifact(t *testing.T, base, id string, headers map[string]string) (int, []byte) {
	return gwGet(t, base+"/v1/load_artifact?id="+id, headers)
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

func TestGatewayIntegration_OIDCJWTVisibility(t *testing.T) {
	t.Parallel()
	idp := newJWKSIdP(t)
	verifier := identity.NewOIDCVerifier(idp.issuer(), gwAudience, 0)
	ts := gatewayServer(t, oidcJWTVerifier(verifier, "", nil, false))

	alice := idp.sign(t, gwClaims(idp.issuer(), "alice@acme.com", []string{"engineering"}))
	bob := idp.sign(t, gwClaims(idp.issuer(), "bob@acme.com", nil))

	// Public artifact visible to everyone, including anonymous.
	if st, _ := loadArtifact(t, ts.URL, "pub/welcome", nil); st != 200 {
		t.Errorf("anonymous load pub/welcome = %d, want 200", st)
	}
	if st, _ := loadArtifact(t, ts.URL, "eng/secret", nil); st != 404 {
		t.Errorf("anonymous load eng/secret = %d, want 404 (public-only)", st)
	}

	// Engineering caller sees the engineering layer.
	if st, _ := loadArtifact(t, ts.URL, "eng/secret", bearer(alice)); st != 200 {
		t.Errorf("alice (engineering) load eng/secret = %d, want 200", st)
	}
	// Non-member does not.
	if st, _ := loadArtifact(t, ts.URL, "eng/secret", bearer(bob)); st != 404 {
		t.Errorf("bob (no group) load eng/secret = %d, want 404", st)
	}
}

func TestGatewayIntegration_OIDCJWTVerificationErrors(t *testing.T) {
	t.Parallel()
	idp := newJWKSIdP(t)
	verifier := identity.NewOIDCVerifier(idp.issuer(), gwAudience, 0)
	ts := gatewayServer(t, oidcJWTVerifier(verifier, "", nil, false))

	// Wrong audience -> 401 auth.untrusted_token, with details.token_iss.
	wrongAud := gwClaims(idp.issuer(), "alice@acme.com", nil)
	wrongAud["aud"] = "https://other.example"
	st, body := loadArtifact(t, ts.URL, "pub/welcome", bearer(idp.sign(t, wrongAud)))
	if st != 401 {
		t.Fatalf("wrong-aud token = %d, want 401\nbody: %s", st, body)
	}
	var env struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	}
	_ = json.Unmarshal(body, &env)
	if env.Code != "auth.untrusted_token" {
		t.Errorf("code = %q, want auth.untrusted_token", env.Code)
	}
	if env.Details["token_iss"] != idp.issuer() {
		t.Errorf("details.token_iss = %v, want %q", env.Details["token_iss"], idp.issuer())
	}

	// Expired token -> 401 auth.token_expired.
	expired := gwClaims(idp.issuer(), "alice@acme.com", nil)
	expired["exp"] = time.Now().Add(-time.Hour).Unix()
	st, body = loadArtifact(t, ts.URL, "pub/welcome", bearer(idp.sign(t, expired)))
	if st != 401 {
		t.Fatalf("expired token = %d, want 401\nbody: %s", st, body)
	}
	_ = json.Unmarshal(body, &env)
	if env.Code != "auth.token_expired" {
		t.Errorf("code = %q, want auth.token_expired", env.Code)
	}

	// A malformed token (the issuer cannot be read) -> 401 auth.untrusted_token
	// with no token_iss detail, exercising writeIdentityError's no-issuer branch.
	st, body = loadArtifact(t, ts.URL, "pub/welcome", bearer("not.a.jwt"))
	if st != 401 {
		t.Fatalf("malformed token = %d, want 401\nbody: %s", st, body)
	}
	env.Details = nil
	_ = json.Unmarshal(body, &env)
	if env.Code != "auth.untrusted_token" {
		t.Errorf("code = %q, want auth.untrusted_token", env.Code)
	}
	if _, ok := env.Details["token_iss"]; ok {
		t.Errorf("malformed token should carry no token_iss, got %v", env.Details["token_iss"])
	}
}

func TestGatewayIntegration_OIDCJWTGroupMapping(t *testing.T) {
	t.Parallel()
	idp := newJWKSIdP(t)
	verifier := identity.NewOIDCVerifier(idp.issuer(), gwAudience, 0)
	mapping := identity.NewIdpGroupMapping(map[string]string{"00g1engOID": "engineering"})
	ts := gatewayServer(t, oidcJWTVerifier(verifier, "", mapping, false))

	// A token carrying only the raw IdP group value sees the engineering layer,
	// because the IdpGroupMapping rewrites 00g1engOID -> engineering (§6.3.1).
	mapped := idp.sign(t, gwClaims(idp.issuer(), "alice@acme.com", []string{"00g1engOID"}))
	if st, _ := loadArtifact(t, ts.URL, "eng/secret", bearer(mapped)); st != 200 {
		t.Errorf("mapped group caller load eng/secret = %d, want 200", st)
	}
	// An unmapped group passes through and does not match.
	unmapped := idp.sign(t, gwClaims(idp.issuer(), "bob@acme.com", []string{"00g9otherOID"}))
	if st, _ := loadArtifact(t, ts.URL, "eng/secret", bearer(unmapped)); st != 404 {
		t.Errorf("unmapped group caller load eng/secret = %d, want 404", st)
	}

	// The adapter maps group values and names no claim (§6.3.1), so it composes
	// with a named group claim: the same raw value arriving as a single string
	// under the configured claim name maps to engineering too.
	named := identity.NewOIDCVerifier(idp.issuer(), gwAudience, 0, identity.WithGroupsClaim(adfsGroupsClaimURI))
	namedTS := gatewayServer(t, oidcJWTVerifier(named, "", mapping, false))
	claims := gwClaims(idp.issuer(), "carol@acme.com", nil)
	claims[adfsGroupsClaimURI] = "00g1engOID"
	if st, _ := loadArtifact(t, namedTS.URL, "eng/secret", bearer(idp.sign(t, claims))); st != 200 {
		t.Errorf("mapped single-string group under a named claim load eng/secret = %d, want 200", st)
	}
}

func TestGatewayIntegration_OIDCJWTCustomTokenHeader(t *testing.T) {
	t.Parallel()
	idp := newJWKSIdP(t)
	verifier := identity.NewOIDCVerifier(idp.issuer(), gwAudience, 0)
	ts := gatewayServer(t, oidcJWTVerifier(verifier, "X-Forwarded-Access-Token", nil, false))

	alice := idp.sign(t, gwClaims(idp.issuer(), "alice@acme.com", []string{"engineering"}))
	// The token must arrive in the configured header, parsed as "Bearer <token>".
	hdr := map[string]string{"X-Forwarded-Access-Token": "Bearer " + alice}
	if st, _ := loadArtifact(t, ts.URL, "eng/secret", hdr); st != 200 {
		t.Errorf("custom-header caller load eng/secret = %d, want 200", st)
	}
	// The same token in the default Authorization header is ignored under a
	// custom token_header, so the caller is anonymous.
	if st, _ := loadArtifact(t, ts.URL, "eng/secret", bearer(alice)); st != 404 {
		t.Errorf("token in wrong header should be anonymous, load eng/secret = %d, want 404", st)
	}
}

// Spec: §6.3.3 / §4.6 — the arrangement the boot path wires for an AD FS
// deployment: the discovery document publishes a second accepted issuer, the
// access token is stamped with that value, the subject arrives under
// PODIUM_OAUTH_SUBJECT_CLAIM, and group membership arrives as a single string
// under PODIUM_OAUTH_GROUPS_CLAIM. The verifier is constructed the way
// runRegistry constructs it, and the assertion is the observable §4.6 result.
// runRegistry's own oidc-jwt branch refuses a non-https issuer, which no
// in-process stub can present, so the construction is mirrored here and the
// startup log line is checked by the manual-validation scenario.
func TestGatewayIntegration_OIDCJWTSplitIssuerAndClaimNames(t *testing.T) {
	t.Parallel()
	const fedIssuer = "http://adfs.acme.test/adfs/services/trust"
	const subjectClaim = "idsub"
	idp := newJWKSIdP(t, withAccessTokenIssuer(fedIssuer))
	verifier := identity.NewOIDCVerifier(idp.issuer(), gwAudience, 0,
		identity.WithSubjectClaim(subjectClaim),
		identity.WithGroupsClaim(adfsGroupsClaimURI))
	if err := verifier.Prime(); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	// The boot line names every accepted value; both are reported once the
	// discovery document is resolved.
	if got, want := verifier.AcceptedIssuers(), []string{idp.issuer(), fedIssuer}; !slices.Equal(got, want) {
		t.Fatalf("AcceptedIssuers() = %v, want %v", got, want)
	}
	ts := gatewayServer(t, oidcJWTVerifier(verifier, "", nil, false))

	adfsToken := func(sub, group string) string {
		t.Helper()
		c := jwt.MapClaims{
			"iss": fedIssuer, "aud": gwAudience,
			"exp": time.Now().Add(10 * time.Minute).Unix(),
		}
		if sub != "" {
			c[subjectClaim] = sub
		}
		if group != "" {
			c[adfsGroupsClaimURI] = group
		}
		return idp.sign(t, c)
	}

	// A caller in the engineering group sees the engineering layer, so the
	// second issuer, the named subject claim, and the single-string group
	// encoding all resolve.
	if st, body := loadArtifact(t, ts.URL, "eng/secret", bearer(adfsToken("alice-pairwise", "engineering"))); st != 200 {
		t.Errorf("alice load eng/secret = %d, want 200\nbody: %s", st, body)
	}
	// A caller in no group is authenticated and sees the public layer alone.
	bob := adfsToken("bob-pairwise", "")
	if st, _ := loadArtifact(t, ts.URL, "eng/secret", bearer(bob)); st != 404 {
		t.Errorf("bob (no group) load eng/secret = %d, want 404", st)
	}
	if st, _ := loadArtifact(t, ts.URL, "pub/welcome", bearer(bob)); st != 200 {
		t.Errorf("bob load pub/welcome = %d, want 200", st)
	}
	// The named subject claim has no fallback: a token carrying sub alone is
	// rejected with auth.untrusted_token rather than authenticating as sub.
	noSubject := gwClaims(fedIssuer, "carol@acme.com", []string{"engineering"})
	st, body := loadArtifact(t, ts.URL, "eng/secret", bearer(idp.sign(t, noSubject)))
	if st != 401 {
		t.Fatalf("token without the named subject claim = %d, want 401\nbody: %s", st, body)
	}
	var env struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &env)
	if env.Code != "auth.untrusted_token" {
		t.Errorf("code = %q, want auth.untrusted_token", env.Code)
	}
}

// Spec: §6.3.3 — runRegistry passes both claim-name options on every oidc-jwt
// boot, so the unset pair has to reproduce the default derivation: the subject
// comes from sub and group membership from groups, and the configured issuer is
// the sole accepted value.
func TestGatewayIntegration_OIDCJWTEmptyClaimNamesKeepDefaults(t *testing.T) {
	t.Parallel()
	idp := newJWKSIdP(t)
	verifier := identity.NewOIDCVerifier(idp.issuer(), gwAudience, 0,
		identity.WithSubjectClaim(""),
		identity.WithGroupsClaim(""))
	if err := verifier.Prime(); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	if got, want := verifier.AcceptedIssuers(), []string{idp.issuer()}; !slices.Equal(got, want) {
		t.Fatalf("AcceptedIssuers() = %v, want %v", got, want)
	}
	ts := gatewayServer(t, oidcJWTVerifier(verifier, "", nil, false))

	alice := idp.sign(t, gwClaims(idp.issuer(), "alice@acme.com", []string{"engineering"}))
	if st, _ := loadArtifact(t, ts.URL, "eng/secret", bearer(alice)); st != 200 {
		t.Errorf("alice (engineering) load eng/secret = %d, want 200", st)
	}
	bob := idp.sign(t, gwClaims(idp.issuer(), "bob@acme.com", nil))
	if st, _ := loadArtifact(t, ts.URL, "eng/secret", bearer(bob)); st != 404 {
		t.Errorf("bob (no group) load eng/secret = %d, want 404", st)
	}
}

// Spec: §6.3.3 — the boot line reports the accepted set the registry applies.
// A discovery document that publishes an access_token_issuer equal to the
// configured issuer widens nothing, so the reported set stays one value and
// verification against the configured issuer is unchanged.
func TestGatewayIntegration_OIDCJWTAccessTokenIssuerEqualsIssuer(t *testing.T) {
	t.Parallel()
	idp := newJWKSIdP(t, withAccessTokenIssuerEqualToIssuer())
	verifier := identity.NewOIDCVerifier(idp.issuer(), gwAudience, 0)
	if err := verifier.Prime(); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	if got, want := verifier.AcceptedIssuers(), []string{idp.issuer()}; !slices.Equal(got, want) {
		t.Fatalf("AcceptedIssuers() = %v, want %v", got, want)
	}
	ts := gatewayServer(t, oidcJWTVerifier(verifier, "", nil, false))

	alice := idp.sign(t, gwClaims(idp.issuer(), "alice@acme.com", []string{"engineering"}))
	if st, _ := loadArtifact(t, ts.URL, "eng/secret", bearer(alice)); st != 200 {
		t.Errorf("alice (engineering) load eng/secret = %d, want 200", st)
	}
}

func TestGatewayIntegration_TrustedHeadersVisibility(t *testing.T) {
	t.Parallel()
	ts := gatewayServer(t, trustedHeadersVerifier(""))

	eng := map[string]string{
		identity.HeaderUserSub:    "alice@acme.com",
		identity.HeaderUserGroups: "engineering",
	}
	// Engineering caller sees the engineering layer.
	if st, _ := loadArtifact(t, ts.URL, "eng/secret", eng); st != 200 {
		t.Errorf("engineering headers load eng/secret = %d, want 200", st)
	}
	// No headers: anonymous, public-only.
	if st, _ := loadArtifact(t, ts.URL, "eng/secret", nil); st != 404 {
		t.Errorf("anonymous load eng/secret = %d, want 404", st)
	}
	if st, _ := loadArtifact(t, ts.URL, "pub/welcome", nil); st != 200 {
		t.Errorf("anonymous load pub/welcome = %d, want 200", st)
	}
}

func TestGatewayIntegration_TrustedHeadersProxySecret(t *testing.T) {
	t.Parallel()
	ts := gatewayServer(t, trustedHeadersVerifier("s3cr3t"))

	base := map[string]string{
		identity.HeaderUserSub:    "alice@acme.com",
		identity.HeaderUserGroups: "engineering",
	}
	// Without the matching proxy secret the identity headers are discarded:
	// anonymous, so the engineering layer is not visible.
	if st, _ := loadArtifact(t, ts.URL, "eng/secret", base); st != 404 {
		t.Errorf("headers without proxy secret load eng/secret = %d, want 404", st)
	}
	// With the matching secret the identity is honored.
	withSecret := map[string]string{
		identity.HeaderUserSub:     "alice@acme.com",
		identity.HeaderUserGroups:  "engineering",
		identity.HeaderProxySecret: "s3cr3t",
	}
	if st, _ := loadArtifact(t, ts.URL, "eng/secret", withSecret); st != 200 {
		t.Errorf("headers with proxy secret load eng/secret = %d, want 200", st)
	}
}
