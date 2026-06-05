package e2e

// Shared helpers for the §6.3.2 injected-session-token end-to-end tests:
// generating a runtime signing key, registering it with a running registry,
// and minting signed JWTs. These let the standalone harness exercise the
// runtime trust model that earlier required an OIDC standard-mode
// deployment.

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
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

// injRegisterRuntime registers (injIssuer, RS256, pemPath) with a running
// registry via the admin CLI, failing the test on a non-zero exit.
func injRegisterRuntime(t *testing.T, srv *serverProc, pemPath string) {
	t.Helper()
	res := runPodium(t, "", nil, "admin", "runtime", "register",
		"--registry", srv.BaseURL,
		"--issuer", injIssuer,
		"--algorithm", "RS256",
		"--public-key-file", pemPath,
	)
	if res.Exit != 0 {
		t.Fatalf("admin runtime register: exit=%d stderr=%s stdout=%s", res.Exit, res.Stderr, res.Stdout)
	}
}

// injServer starts a standalone registry in injected-session-token mode over
// the filesystem registry reg, registers the runtime signing key, and returns
// the running server. The verifier consults the same in-memory key store the
// admin endpoint writes, so the registration is live immediately.
func injServer(t *testing.T, reg string, priv *rsa.PrivateKey, pemPath string) *serverProc {
	t.Helper()
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_IDENTITY_PROVIDER=injected-session-token",
		"PODIUM_OAUTH_AUDIENCE=" + injAudience,
		// These tests exercise the verification gate, not visibility. The
		// layer is public so a verified caller sees the artifacts; an
		// unverified caller is still rejected before visibility is consulted.
		"PODIUM_DEFAULT_LAYER_VISIBILITY=public",
	}, "serve", "--standalone", "--layer-path", reg)
	injRegisterRuntime(t, srv, pemPath)
	return srv
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
