package e2e

// End-to-end coverage for the §6.3.3 oidc-jwt happy path against a live https
// IdP, driving the real podium binary. The IdP is an in-test TLS server whose
// certificate the spawned binary trusts through SSL_CERT_FILE, so the boot
// sequence past the startup guards runs for real: the discovery fetch, the
// JWKS prime that refuses boot when the issuer is unreachable, the verifier
// install, and the accepted-issuer log line. Those lines run only inside the
// spawned process, so a package test cannot reach them.

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// adfsGroupClaim is the full claim-type URI an AD FS issuance rule emits for
// group membership unless the rule is authored with a short name.
const adfsGroupClaim = "http://schemas.microsoft.com/ws/2008/06/identity/claims/groups"

// adfsTokenIssuer is the federation-service identifier an AD FS access token
// carries as iss. It differs from the discovery document's issuer and uses the
// http scheme, which is why it can never be the configured issuer: the §6.3.3
// startup guard requires https on the value the registry resolves discovery
// from. It is compared as a string and never dereferenced.
const adfsTokenIssuer = "http://adfs.acme.example/adfs/services/trust"

// oidcTestIdP is an https OIDC endpoint serving a discovery document and a
// JWKS, plus the signing key so a test can mint tokens it accepts.
type oidcTestIdP struct {
	srv    *httptest.Server
	key    *rsa.PrivateKey
	caFile string
}

// requireCustomTrustStore skips when the spawned binary cannot be told to
// trust the test IdP's certificate. Go reads SSL_CERT_FILE only on Unix
// systems other than macOS (crypto/x509/root_unix.go build tags, documented at
// crypto/x509/cert_pool.go), and on darwin it consults the system keychain,
// which a test must not modify. CI runs ubuntu-latest, so these cases execute
// there; the in-process equivalents in internal/serverboot cover the same
// verification behavior on every platform, and what is exercised only here is
// the boot wiring inside the spawned process.
func requireCustomTrustStore(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("darwin ignores SSL_CERT_FILE, so the spawned binary cannot trust the test IdP without touching the system keychain")
	}
}

// startOIDCTestIdP starts the TLS IdP. accessTokenIssuer, when non-empty, is
// published as the discovery document's access_token_issuer, the AD FS
// extension §6.3.3 names as the second accepted issuer.
func startOIDCTestIdP(t *testing.T, accessTokenIssuer string) *oidcTestIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	idp := &oidcTestIdP{key: key}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		doc := map[string]any{
			"issuer":   idp.srv.URL,
			"jwks_uri": idp.srv.URL + "/jwks",
		}
		if accessTokenIssuer != "" {
			doc["access_token_issuer"] = accessTokenIssuer
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{"kty": "RSA", "kid": "test-1", "alg": "RS256", "use": "sig", "n": n, "e": e}},
		})
	})
	idp.srv = httptest.NewTLSServer(mux)
	t.Cleanup(idp.srv.Close)

	// The spawned binary is a separate process with its own trust store, so the
	// server certificate is written out and named through SSL_CERT_FILE.
	caPath := filepath.Join(t.TempDir(), "idp-ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: idp.srv.Certificate().Raw})
	if err := os.WriteFile(caPath, pemBytes, 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}
	idp.caFile = caPath
	return idp
}

// token mints an RS256 token signed by the IdP's key.
func (i *oidcTestIdP) token(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "test-1"
	signed, err := tok.SignedString(i.key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

// gwOIDCServer starts a standalone registry under oidc-jwt against idp, with a
// public layer and an engineering-group layer so the test can assert §4.6
// visibility from the verified token. extraEnv carries the claim-name settings.
func gwOIDCServer(t *testing.T, idp *oidcTestIdP, extraEnv ...string) *serverProc {
	t.Helper()
	home := t.TempDir()
	pubRoot := writeRegistry(t, map[string]string{"welcome/ARTIFACT.md": contextArtifact("public welcome")})
	engRoot := writeRegistry(t, map[string]string{"secret/ARTIFACT.md": contextArtifact("engineering secret")})
	cfg := "" +
		"registry:\n" +
		"  layers:\n" +
		"    - id: public-layer\n" +
		"      source:\n" +
		"        local:\n" +
		"          path: " + pubRoot + "\n" +
		"      visibility:\n" +
		"        public: true\n" +
		"    - id: eng-layer\n" +
		"      source:\n" +
		"        local:\n" +
		"          path: " + engRoot + "\n" +
		"      visibility:\n" +
		"        groups: [engineering]\n"
	cfgPath := filepath.Join(home, "registry.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}
	env := append([]string{
		"HOME=" + home,
		"PODIUM_CONFIG_FILE=" + cfgPath,
		"PODIUM_INGEST_OFFLINE=true",
		"PODIUM_IDENTITY_PROVIDER=oidc-jwt",
		"PODIUM_OAUTH_ISSUER=" + idp.srv.URL,
		"PODIUM_OAUTH_AUDIENCE=https://podium.acme.example",
		"SSL_CERT_FILE=" + idp.caFile,
	}, extraEnv...)
	return startServerArgs(t, env, "serve", "--standalone")
}

// bearer returns the Authorization header map for tok.
func bearer(tok string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + tok}
}

// Spec: §6.3.3 — the registry boots under oidc-jwt against a reachable https
// issuer, primes discovery and the JWKS, installs the verifier, and resolves
// §4.6 visibility from a verified token. The token carries the standard `sub`
// and an array `groups` claim, so this is the profile a deployment gets with
// neither claim-name setting configured.
func TestOIDCJWT_DefaultClaimsVisibility(t *testing.T) {
	t.Parallel()
	requireCustomTrustStore(t)
	idp := startOIDCTestIdP(t, "")
	srv := gwOIDCServer(t, idp)

	member := idp.token(t, jwt.MapClaims{
		"iss":    idp.srv.URL,
		"aud":    "https://podium.acme.example",
		"sub":    "alice@acme.com",
		"groups": []string{"engineering"},
		"exp":    time.Now().Add(time.Hour).Unix(),
	})
	outsider := idp.token(t, jwt.MapClaims{
		"iss": idp.srv.URL,
		"aud": "https://podium.acme.example",
		"sub": "bob@acme.com",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	if st, body := gwHeaderGet(t, srv.BaseURL+"/v1/load_artifact?id=welcome", bearer(member)); st != 200 {
		t.Errorf("member load public welcome = %d, want 200\nbody: %s\nlog:\n%s", st, body, srv.log())
	}
	if st, body := gwHeaderGet(t, srv.BaseURL+"/v1/load_artifact?id=secret", bearer(member)); st != 200 {
		t.Errorf("member load engineering secret = %d, want 200\nbody: %s\nlog:\n%s", st, body, srv.log())
	}
	if st, _ := gwHeaderGet(t, srv.BaseURL+"/v1/load_artifact?id=secret", bearer(outsider)); st != 404 {
		t.Errorf("non-member load engineering secret = %d, want 404", st)
	}
	if st, _ := gwHeaderGet(t, srv.BaseURL+"/v1/load_artifact?id=secret", nil); st != 404 {
		t.Errorf("anonymous load engineering secret = %d, want 404", st)
	}
	if got := srv.log(); !strings.Contains(got, "accepted issuers "+idp.srv.URL) {
		t.Errorf("boot log does not name the accepted issuer:\n%s", got)
	}
}

// Spec: §6.3.3 — the AD FS profile end to end: the token's iss is the
// discovery document's access_token_issuer rather than the configured issuer,
// the subject comes from the claim PODIUM_OAUTH_SUBJECT_CLAIM names, and group
// membership comes from the claim PODIUM_OAUTH_GROUPS_CLAIM names in the
// single-string form an IdP emits for a caller in exactly one group.
func TestOIDCJWT_ADFSProfileVisibility(t *testing.T) {
	t.Parallel()
	requireCustomTrustStore(t)
	idp := startOIDCTestIdP(t, adfsTokenIssuer)
	srv := gwOIDCServer(t, idp,
		"PODIUM_OAUTH_SUBJECT_CLAIM=idsub",
		"PODIUM_OAUTH_GROUPS_CLAIM="+adfsGroupClaim,
	)

	member := idp.token(t, jwt.MapClaims{
		"iss":          adfsTokenIssuer,
		"aud":          "https://podium.acme.example",
		"idsub":        "S-1-5-21-alice",
		adfsGroupClaim: "engineering", // single group: a string, not an array
		"exp":          time.Now().Add(time.Hour).Unix(),
	})

	if st, body := gwHeaderGet(t, srv.BaseURL+"/v1/load_artifact?id=secret", bearer(member)); st != 200 {
		t.Errorf("AD FS member load engineering secret = %d, want 200\nbody: %s\nlog:\n%s", st, body, srv.log())
	}

	// The configured issuer stays accepted alongside the second value.
	viaConfigured := idp.token(t, jwt.MapClaims{
		"iss":          idp.srv.URL,
		"aud":          "https://podium.acme.example",
		"idsub":        "S-1-5-21-alice",
		adfsGroupClaim: []string{"engineering"},
		"exp":          time.Now().Add(time.Hour).Unix(),
	})
	if st, body := gwHeaderGet(t, srv.BaseURL+"/v1/load_artifact?id=secret", bearer(viaConfigured)); st != 200 {
		t.Errorf("token under the configured issuer = %d, want 200\nbody: %s\nlog:\n%s", st, body, srv.log())
	}

	// A token from neither accepted issuer is rejected, so the widened set is
	// two values rather than any value.
	foreign := idp.token(t, jwt.MapClaims{
		"iss":          "https://evil.example/adfs",
		"aud":          "https://podium.acme.example",
		"idsub":        "S-1-5-21-mallory",
		adfsGroupClaim: "engineering",
		"exp":          time.Now().Add(time.Hour).Unix(),
	})
	if st, _ := gwHeaderGet(t, srv.BaseURL+"/v1/load_artifact?id=secret", bearer(foreign)); st != 404 {
		t.Errorf("token from an unaccepted issuer = %d, want 404 (anonymous)", st)
	}

	// A token carrying only `sub` is rejected once a subject claim is
	// configured, because the configured claim has no fallback.
	subOnly := idp.token(t, jwt.MapClaims{
		"iss":          adfsTokenIssuer,
		"aud":          "https://podium.acme.example",
		"sub":          "alice@acme.com",
		adfsGroupClaim: "engineering",
		"exp":          time.Now().Add(time.Hour).Unix(),
	})
	if st, _ := gwHeaderGet(t, srv.BaseURL+"/v1/load_artifact?id=secret", bearer(subOnly)); st != 404 {
		t.Errorf("token carrying only sub under a configured subject claim = %d, want 404 (anonymous)", st)
	}

	log := srv.log()
	for _, want := range []string{
		"accepted issuers",
		adfsTokenIssuer,
		`subject from claim "idsub"`,
		fmt.Sprintf("group membership from claim %q", adfsGroupClaim),
	} {
		if !strings.Contains(log, want) {
			t.Errorf("boot log missing %q:\n%s", want, log)
		}
	}
}

// Spec: §6.3.3 — the registry refuses to start when the issuer's discovery
// document is unreachable, rather than serving a registry whose tokens it
// cannot verify. This drives the Prime failure branch through the binary.
func TestOIDCJWT_UnreachableIssuerRefusesBoot(t *testing.T) {
	t.Parallel()
	idp := startOIDCTestIdP(t, "")
	unreachable := idp.srv.URL
	idp.srv.Close() // the URL still parses as https and now refuses connections

	gwExpectStartupFailure(t, "refusing to start",
		"PODIUM_IDENTITY_PROVIDER=oidc-jwt",
		"PODIUM_OAUTH_ISSUER="+unreachable,
		"PODIUM_OAUTH_AUDIENCE=https://podium.acme.example",
		"SSL_CERT_FILE="+idp.caFile,
	)
}
