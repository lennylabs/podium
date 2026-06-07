package e2e

// End-to-end coverage for the §6.3.3 gateway-delegated identity providers
// (oidc-jwt, trusted-headers) driving the real podium binary. trusted-headers
// is exercised through its full happy path (the gateway-injected identity
// headers drive §4.6 visibility); oidc-jwt is exercised through its startup
// guards (an https issuer and a configured audience are required), because a
// full oidc-jwt happy path needs an https IdP with a JWKS that the binary's
// trust store accepts, which is covered in-process by the serverboot
// integration tests instead.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// gwHeaderGet issues a GET with arbitrary request headers.
func gwHeaderGet(t *testing.T, url string, headers map[string]string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

// gwTrustedHeadersServer starts a standalone registry in trusted-headers mode
// over a registry.yaml that declares a public layer and an engineering-group
// layer, so the test can assert per-caller §4.6 visibility from the injected
// identity headers. proxySecret, when non-empty, sets PODIUM_TRUSTED_PROXY_SECRET.
func gwTrustedHeadersServer(t *testing.T, proxySecret string) *serverProc {
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
	env := []string{
		"HOME=" + home,
		"PODIUM_CONFIG_FILE=" + cfgPath,
		"PODIUM_INGEST_OFFLINE=true",
		"PODIUM_IDENTITY_PROVIDER=trusted-headers",
	}
	if proxySecret != "" {
		env = append(env, "PODIUM_TRUSTED_PROXY_SECRET="+proxySecret)
	}
	return startServerArgs(t, env, "serve", "--standalone")
}

// Spec: §6.3.3 — trusted-headers resolves the caller from gateway-injected
// X-Podium-User-* headers and applies §4.6 per-layer visibility. An engineering
// caller sees the engineering layer; a non-member and an anonymous caller see
// the public layer only.
func TestGateway_TrustedHeadersVisibility(t *testing.T) {
	t.Parallel()
	srv := gwTrustedHeadersServer(t, "")

	alice := map[string]string{
		"X-Podium-User-Sub":    "alice@acme.com",
		"X-Podium-User-Groups": "engineering",
	}
	bob := map[string]string{"X-Podium-User-Sub": "bob@acme.com"}

	// Public artifact: visible to the engineering caller, the non-member, and
	// the anonymous caller.
	for name, hdr := range map[string]map[string]string{"alice": alice, "bob": bob, "anonymous": nil} {
		if st, body := gwHeaderGet(t, srv.BaseURL+"/v1/load_artifact?id=welcome", hdr); st != 200 {
			t.Errorf("%s load public welcome = %d, want 200\nbody: %s\nlog:\n%s", name, st, body, srv.log())
		}
	}

	// Engineering artifact: visible to the engineering caller only.
	if st, body := gwHeaderGet(t, srv.BaseURL+"/v1/load_artifact?id=secret", alice); st != 200 {
		t.Errorf("alice (engineering) load secret = %d, want 200\nbody: %s\nlog:\n%s", st, body, srv.log())
	}
	if st, _ := gwHeaderGet(t, srv.BaseURL+"/v1/load_artifact?id=secret", bob); st != 404 {
		t.Errorf("bob (no group) load secret = %d, want 404", st)
	}
	if st, _ := gwHeaderGet(t, srv.BaseURL+"/v1/load_artifact?id=secret", nil); st != 404 {
		t.Errorf("anonymous load secret = %d, want 404", st)
	}
}

// Spec: §6.3.3 — when PODIUM_TRUSTED_PROXY_SECRET is set, the identity headers
// are honored only on a request whose X-Podium-Proxy-Secret matches.
func TestGateway_TrustedHeadersProxySecret(t *testing.T) {
	t.Parallel()
	srv := gwTrustedHeadersServer(t, "s3cr3t")

	// Identity headers without the matching secret are discarded: anonymous,
	// so the engineering layer is not visible.
	noSecret := map[string]string{
		"X-Podium-User-Sub":    "alice@acme.com",
		"X-Podium-User-Groups": "engineering",
	}
	if st, _ := gwHeaderGet(t, srv.BaseURL+"/v1/load_artifact?id=secret", noSecret); st != 404 {
		t.Errorf("headers without proxy secret load secret = %d, want 404", st)
	}

	// With the matching secret the identity is honored.
	withSecret := map[string]string{
		"X-Podium-User-Sub":     "alice@acme.com",
		"X-Podium-User-Groups":  "engineering",
		"X-Podium-Proxy-Secret": "s3cr3t",
	}
	if st, body := gwHeaderGet(t, srv.BaseURL+"/v1/load_artifact?id=secret", withSecret); st != 200 {
		t.Errorf("headers with proxy secret load secret = %d, want 200\nbody: %s\nlog:\n%s", st, body, srv.log())
	}
}

// gwExpectStartupFailure runs `podium serve` with the given extra env and
// asserts the process exits non-zero with wantCode in its combined output.
func gwExpectStartupFailure(t *testing.T, wantCode string, extraEnv ...string) {
	t.Helper()
	bin := cmdharness.Bin(t, "podium")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	reg := writeRegistry(t, map[string]string{"seed/ARTIFACT.md": contextArtifact("seed")})
	cmd := exec.CommandContext(ctx, bin, "serve", "--standalone", "--layer-path", reg)
	cmd.Env = mergeEnv(append([]string{"HOME=" + t.TempDir()}, extraEnv...)...)
	cmd.Stdin = bytes.NewReader(nil)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected non-zero exit for %s, but process exited 0\noutput:\n%s", wantCode, out.String())
	}
	if !strings.Contains(out.String(), wantCode) {
		t.Errorf("output missing %q:\n%s", wantCode, out.String())
	}
}

// Spec: §6.3.3 / §13.12 — oidc-jwt requires an https issuer; an http issuer
// fails startup with config.invalid_issuer_scheme.
func TestGateway_OIDCJWTHttpIssuerRefused(t *testing.T) {
	gwExpectStartupFailure(t, "config.invalid_issuer_scheme",
		"PODIUM_IDENTITY_PROVIDER=oidc-jwt",
		"PODIUM_OAUTH_ISSUER=http://acme.okta.example/oauth2/default",
		"PODIUM_OAUTH_AUDIENCE=https://podium.acme.example",
	)
}

// Spec: §6.3.3 / §13.12 — oidc-jwt requires PODIUM_OAUTH_AUDIENCE; an unset
// audience fails startup with config.oidc_jwt_audience_unset.
func TestGateway_OIDCJWTMissingAudienceRefused(t *testing.T) {
	gwExpectStartupFailure(t, "config.oidc_jwt_audience_unset",
		"PODIUM_IDENTITY_PROVIDER=oidc-jwt",
		"PODIUM_OAUTH_ISSUER=https://acme.okta.example/oauth2/default",
		"PODIUM_OAUTH_AUDIENCE=",
	)
}

// Spec: §6.3.3 — trusted-headers on a multi-tenant registry requires a proxy
// secret regardless of bind; an unset secret fails startup with
// config.trusted_headers_multitenant_no_secret.
func TestGateway_TrustedHeadersMultitenantNoSecretRefused(t *testing.T) {
	gwExpectStartupFailure(t, "config.trusted_headers_multitenant_no_secret",
		"PODIUM_IDENTITY_PROVIDER=trusted-headers",
		"PODIUM_MULTI_TENANT=true",
	)
}

// Spec: §6.3.3 / §13.10 — trusted-headers on a non-loopback bind without a
// proxy secret or --allow-public-bind fails startup with
// config.trusted_headers_public_bind.
func TestGateway_TrustedHeadersNonLoopbackBindRefused(t *testing.T) {
	bin := cmdharness.Bin(t, "podium")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	reg := writeRegistry(t, map[string]string{"seed/ARTIFACT.md": contextArtifact("seed")})
	cmd := exec.CommandContext(ctx, bin, "serve", "--standalone", "--layer-path", reg, "--bind", "0.0.0.0:0")
	cmd.Env = mergeEnv("HOME="+t.TempDir(), "PODIUM_IDENTITY_PROVIDER=trusted-headers")
	cmd.Stdin = bytes.NewReader(nil)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err == nil {
		t.Fatalf("expected non-zero exit for non-loopback trusted-headers bind, exited 0\noutput:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "config.trusted_headers_public_bind") {
		t.Errorf("output missing 'config.trusted_headers_public_bind':\n%s", out.String())
	}
}
