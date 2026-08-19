package e2e

// Shared helpers for the §6.3.2 injected-session-token end-to-end tests:
// generating a runtime signing key, seeding it into the trusted key set the
// registry reads at startup, and minting signed JWTs. These let the standalone
// harness exercise the runtime trust model that earlier required an OIDC
// standard-mode deployment.

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	injIssuer   = "e2e-runtime"
	injAudience = "https://podium.e2e"
)

// injKeyPair generates an RSA-2048 key pair and writes the public key as
// PKIX PEM to a temp file, returning the private key and the PEM path.
func injKeyPair(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal pub key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "runtime-pub.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write pem: %v", err)
	}
	return priv, path
}

// injSignJWT signs claims with priv using RS256.
func injSignJWT(t *testing.T, priv *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return signed
}

// injClaims returns a baseline valid claim set for injIssuer / injAudience.
// Callers extend it (for example adding "groups" or "scope") before signing.
func injClaims(sub string) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": injIssuer,
		"aud": injAudience,
		"sub": sub,
		"act": injIssuer,
		"exp": time.Now().Add(10 * time.Minute).Unix(),
	}
}

// injSeedRuntimeKeys writes the §6.3.2 trusted runtime key set holding
// (injIssuer, RS256, the PKIX PEM at pemPath) to a temp file and returns its
// path. The registry reads the set at startup, so the boot that consumes the
// path must also carry "PODIUM_RUNTIME_KEYS_PATH=" + path in its environment.
//
// Spec: §6.3.2, §13.12.
func injSeedRuntimeKeys(t *testing.T, pemPath string) string {
	t.Helper()
	pubPEM, err := os.ReadFile(pemPath)
	if err != nil {
		t.Fatalf("read runtime public key %s: %v", pemPath, err)
	}
	body, err := json.Marshal([]map[string]string{{
		"issuer":         injIssuer,
		"algorithm":      "RS256",
		"public_key_pem": string(pubPEM),
	}})
	if err != nil {
		t.Fatalf("encode runtime key set: %v", err)
	}
	path := filepath.Join(t.TempDir(), "runtime-keys.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write runtime key set: %v", err)
	}
	return path
}

// injServer starts a standalone registry in injected-session-token mode over
// the filesystem registry reg, seeded with the runtime signing key at pemPath,
// and returns the running server. The registry reads the seeded key set before
// it binds a listener, so a token signed with the matching private key verifies
// on the first request.
func injServer(t *testing.T, reg string, priv *rsa.PrivateKey, pemPath string) *serverProc {
	t.Helper()
	keysPath := injSeedRuntimeKeys(t, pemPath)
	return startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_IDENTITY_PROVIDER=injected-session-token",
		"PODIUM_OAUTH_AUDIENCE=" + injAudience,
		"PODIUM_RUNTIME_KEYS_PATH=" + keysPath,
		// These tests exercise the verification gate, not visibility. The
		// layer is public so a verified caller sees the artifacts; an
		// unverified caller is still rejected before visibility is consulted.
		"PODIUM_DEFAULT_LAYER_VISIBILITY=public",
	}, "serve", "--standalone", "--layer-path", reg)
}

// injGet issues a GET with an optional Bearer token and returns the status
// code and body.
func injGet(t *testing.T, url, token string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, body
}
