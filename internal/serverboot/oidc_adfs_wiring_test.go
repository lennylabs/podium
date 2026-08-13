package serverboot

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// stubIdP serves a minimal OIDC discovery document and JWKS for one RSA key,
// with an optional access_token_issuer (the AD FS extension).
type stubIdP struct {
	srv               *httptest.Server
	key               *rsa.PrivateKey
	accessTokenIssuer string
}

func newStubIdP(t *testing.T, accessTokenIssuer string) *stubIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	idp := &stubIdP{key: key, accessTokenIssuer: accessTokenIssuer}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		doc := map[string]any{
			"issuer":   "http://" + r.Host,
			"jwks_uri": "http://" + r.Host + "/jwks",
		}
		if idp.accessTokenIssuer != "" {
			doc["access_token_issuer"] = idp.accessTokenIssuer
		}
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		pub := &idp.key.PublicKey
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "k1",
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}}})
	})
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

func (i *stubIdP) sign(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "k1"
	s, err := tok.SignedString(i.key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

// The AD FS deployment profile: the token carries the federation-service
// identifier as iss, idsub instead of sub, and group membership as a plain
// string under the full claim-type URI. newOIDCVerifierFromConfig wires the
// configured subject-claim and groups-claim overrides into the verifier.
func TestNewOIDCVerifierFromConfig_ADFSProfile(t *testing.T) {
	t.Parallel()
	const fsIdentifier = "http://adfs.acme.example/adfs/services/trust"
	const groupsClaim = "http://schemas.microsoft.com/ws/2008/06/identity/claims/groups"
	idp := newStubIdP(t, fsIdentifier)

	cfg := &Config{
		oauthIssuer:       idp.srv.URL,
		oauthAudience:     "microsoft:identityserver:client-1",
		oauthSubjectClaim: "idsub",
		oauthGroupsClaim:  groupsClaim,
	}
	verifier := newOIDCVerifierFromConfig(cfg)
	if err := verifier.Prime(); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	id, err := verifier.Verify(idp.sign(t, jwt.MapClaims{
		"iss":       fsIdentifier,
		"aud":       "microsoft:identityserver:client-1",
		"idsub":     "stable-pairwise-id",
		"email":     "alice@acme.com",
		groupsClaim: "oidc.podium.common",
		"exp":       time.Now().Add(time.Hour).Unix(),
		"iat":       time.Now().Unix(),
	}))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if id.Sub != "stable-pairwise-id" {
		t.Errorf("Sub = %q, want the idsub value", id.Sub)
	}
	if len(id.Groups) != 1 || id.Groups[0] != "oidc.podium.common" {
		t.Errorf("Groups = %v, want [oidc.podium.common]", id.Groups)
	}
}

// Without the overrides the verifier keeps the standard claim set.
func TestNewOIDCVerifierFromConfig_Defaults(t *testing.T) {
	t.Parallel()
	idp := newStubIdP(t, "")
	cfg := &Config{oauthIssuer: idp.srv.URL, oauthAudience: "aud-1"}
	verifier := newOIDCVerifierFromConfig(cfg)
	if err := verifier.Prime(); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	id, err := verifier.Verify(idp.sign(t, jwt.MapClaims{
		"iss":    idp.srv.URL,
		"aud":    "aud-1",
		"sub":    "alice@acme.com",
		"groups": []any{"engineering"},
		"exp":    time.Now().Add(time.Hour).Unix(),
		"iat":    time.Now().Unix(),
	}))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if id.Sub != "alice@acme.com" || len(id.Groups) != 1 || id.Groups[0] != "engineering" {
		t.Errorf("identity = %+v, want standard claims", id)
	}
}
