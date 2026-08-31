package e2e

// End-to-end tests for the §13.10 standalone serve flags added in batch
// fix-13.10-b: --web-ui, --no-embeddings, and
// --sign registry-key. Each drives the real `podium` binary
// through the shared standalone harness, which always binds a loopback
// address; the non-loopback web-UI refusal is covered by the
// serverboot/config unit tests, which do not need a bound listener.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// `podium serve --web-ui` mounts the bundled SPA at /app/.
func TestServerFlags_WebUIFlagMountsUI(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"my-skill/ARTIFACT.md": smallteamLowArtifact("ui artifact"),
	})
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir()},
		"serve", "--standalone", "--web-ui", "--layer-path", reg)

	st, body := getRaw(t, srv.BaseURL+"/app/")
	if st != 200 {
		t.Fatalf("GET /app/ status = %d, want 200\nlog:\n%s", st, srv.log())
	}
	if !strings.Contains(string(body), "<title>Podium</title>") {
		t.Errorf("UI response missing index marker: %.200s", body)
	}
}

// without --web-ui the UI is not mounted; /app/ is not served.
func TestServerFlags_NoWebUIByDefault(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"my-skill/ARTIFACT.md": smallteamLowArtifact("ui artifact"),
	})
	srv := startServer(t, reg)
	if st := getStatus(t, srv.BaseURL+"/app/"); st == 200 {
		t.Errorf("GET /app/ = 200 without --web-ui; the UI must be opt-in")
	}
}

// `podium serve --no-embeddings` boots into BM25-only search and
// search_artifacts still answers.
func TestServerFlags_NoEmbeddingsSearchWorks(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"variance-skill/ARTIFACT.md": smallteamLowArtifact("variance analysis skill"),
	})
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir()},
		"serve", "--standalone", "--no-embeddings", "--layer-path", reg)

	if st := getStatus(t, srv.BaseURL+"/v1/search_artifacts?query=variance"); st != 200 {
		t.Fatalf("search_artifacts status = %d, want 200 (BM25-only)\nlog:\n%s", st, srv.log())
	}
}

// `podium serve --sign registry-key` boots with ingest signing
// enabled, logs the registry-managed-key line, and generates the signing key
// under the standalone home.
func TestServerFlags_SignRegistryKey(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	reg := writeRegistry(t, map[string]string{
		"my-skill/ARTIFACT.md": smallteamLowArtifact("signed artifact"),
	})
	srv := startServerArgs(t, []string{"HOME=" + home},
		"serve", "--standalone", "--sign", "registry-key", "--layer-path", reg)

	if !strings.Contains(srv.log(), "ingest signing: registry-managed key") {
		t.Errorf("startup log missing the registry-managed signing line:\n%s", srv.log())
	}
	keyPath := filepath.Join(home, ".podium", "standalone", "registry-signing.key")
	if _, err := os.Stat(keyPath); err != nil {
		t.Errorf("registry signing key not created at %s: %v", keyPath, err)
	}
}

// (spec: §13.10 lines 116, 223) — a first-run standalone
// `podium serve` auto-bootstraps ~/.podium/sync.yaml with defaults.registry
// pointing at the local server, so a consumer resolves the registry without an
// extra env var. The bound address is a free loopback port chosen by the
// harness, so the written pointer must carry that exact bind.
func TestServerFlags_AutoBootstrapsSyncYAML(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	reg := writeRegistry(t, map[string]string{
		"my-skill/ARTIFACT.md": smallteamLowArtifact("bootstrap artifact"),
	})
	srv := startServerArgs(t, []string{"HOME=" + home},
		"serve", "--standalone", "--layer-path", reg)

	syncPath := filepath.Join(home, ".podium", "sync.yaml")
	body, err := os.ReadFile(syncPath)
	if err != nil {
		t.Fatalf("read %s: %v\nlog:\n%s", syncPath, err, srv.log())
	}
	want := "registry: " + srv.BaseURL
	if !strings.Contains(string(body), want) {
		t.Errorf("sync.yaml missing %q:\n%s", want, body)
	}
	if _, err := os.Stat(filepath.Join(home, ".podium", "registry.yaml")); err != nil {
		t.Errorf("registry.yaml not auto-bootstrapped: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(home, "podium-artifacts")); err != nil || !fi.IsDir() {
		t.Errorf("~/podium-artifacts not created: err=%v", err)
	}
}

// an unrecognized --sign value is refused at startup; the process
// exits non-zero before binding a listener.
func TestServerFlags_SignRejectsUnknown(t *testing.T) {
	t.Parallel()
	out := runPodium(t, "", []string{"HOME=" + t.TempDir()},
		"serve", "--standalone", "--sign", "sigstore")
	if out.Exit == 0 {
		t.Fatalf("serve --sign sigstore exit = 0, want non-zero\nstderr=%s", out.Stderr)
	}
	if !strings.Contains(out.Stderr, "config.invalid_sign_mode") {
		t.Errorf("stderr missing config.invalid_sign_mode: %s", out.Stderr)
	}
}

// Spec: §13.10 ("Browser-flow configuration guard") — one representative
// refusal driven through the binary: --web-ui-auth without --web-ui fails the
// web-UI conjunct, so the process exits non-zero before binding a listener and
// names config.web_ui_auth_unconfigured together with the conjunct that failed.
// The remaining conjuncts are covered by the pkg/registry/server guard table,
// which needs no bound listener.
func TestServe_WebUIAuthUnconfiguredRefused(t *testing.T) {
	t.Parallel()
	out := runPodium(t, "", []string{"HOME=" + t.TempDir()},
		"serve", "--standalone", "--web-ui-auth")
	if out.Exit == 0 {
		t.Fatalf("serve --web-ui-auth without --web-ui exit = 0, want non-zero\nstderr=%s", out.Stderr)
	}
	combined := out.Stderr + out.Stdout
	if !strings.Contains(combined, "config.web_ui_auth_unconfigured") {
		t.Errorf("output missing config.web_ui_auth_unconfigured:\n%s", combined)
	}
	if !strings.Contains(combined, "PODIUM_WEB_UI") {
		t.Errorf("refusal does not name the failed conjunct:\n%s", combined)
	}
}

// webUIAuthEnv returns the §6.3.4 acquisition values for a registry fronted by
// idp. They carry an environment variable and no flag, so every point of the
// route-mount product supplies them this way. The redirect URI is a loopback
// http URL, which the §13.10 redirect-URI conjunct admits; no case drives the
// callback, so it names no bound port of the server under test.
func webUIAuthEnv(idp *oidcTestIdP) []string {
	return []string{
		"PODIUM_IDENTITY_PROVIDER=oidc-jwt",
		"PODIUM_OAUTH_ISSUER=" + idp.srv.URL,
		"PODIUM_OAUTH_AUDIENCE=https://podium.acme.example",
		"SSL_CERT_FILE=" + idp.caFile,
		"PODIUM_WEB_UI_OAUTH_CLIENT_ID=podium-web-ui",
		"PODIUM_WEB_UI_OAUTH_CLIENT_SECRET=s3cr3t",
		"PODIUM_WEB_UI_REDIRECT_URI=http://127.0.0.1:8080/v1/ui/auth/callback",
		"PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT=" + idp.srv.URL + "/authorize",
		"PODIUM_WEB_UI_OAUTH_TOKEN_ENDPOINT=" + idp.srv.URL + "/token",
	}
}

// signInResponse issues the sign-in GET without following the redirect, so the
// test observes the status the route returns and the Set-Cookie it carries.
func signInResponse(t *testing.T, baseURL string, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequest(http.MethodGet, baseURL+"/v1/ui/auth/sign-in", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET sign-in: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// transactionMaxAge returns the Max-Age the response sets on the §7.3.4
// pre-authorization cookie, or -1 when the response sets no such cookie.
func transactionMaxAge(resp *http.Response) int {
	for _, c := range resp.Cookies() {
		if c.Name == "__Host-podium_auth" {
			return c.MaxAge
		}
	}
	return -1
}

// Spec: §7.3.4 / §13.10 — the browser authentication routes mount inside the
// block that already serves /app/, under the one enablement key. Each case
// starts one binary at one point of the enablement, key-carrier, and sign-in
// window axes, and observes whether an authentication route answers and what
// window the served pre-authorization cookie carries.
func TestServe_WebUIAuthRouteMount(t *testing.T) {
	t.Parallel()
	t.Run("flow off", webUIAuthFlowOff)
	t.Run("environment carrier, default window", webUIAuthEnvDefaultTTL)
	t.Run("flag carrier, configured window", webUIAuthFlagConfiguredTTL)
}

// webUIAuthFlowOff drives the flow-off point: a registry serving the UI with
// no browser flow registers none of the routes, and a stale session cookie
// against it resolves as anonymous rather than as a subject. It configures no
// identity provider, which is the stack fact that fixes the status of a path
// the registry does not register.
func webUIAuthFlowOff(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"my-skill/ARTIFACT.md": smallteamLowArtifact("ui artifact"),
	})
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir()},
		"serve", "--standalone", "--web-ui", "--layer-path", reg)

	stale := &http.Cookie{Name: "__Host-podium_session", Value: "stale.token.value"}
	if resp := signInResponse(t, srv.BaseURL); resp.StatusCode != 404 {
		t.Errorf("GET sign-in with the flow off = %d, want 404\nlog:\n%s", resp.StatusCode, srv.log())
	}
	st, body := gwHeaderGet(t, srv.BaseURL+"/v1/ui/session", map[string]string{"Cookie": stale.Name + "=" + stale.Value})
	if st != 200 {
		t.Fatalf("GET /v1/ui/session = %d, want 200\nbody: %s\nlog:\n%s", st, body, srv.log())
	}
	var posture struct {
		Subject     string `json:"subject"`
		BrowserAuth struct {
			Enabled bool `json:"enabled"`
		} `json:"browser_auth"`
	}
	if err := json.Unmarshal(body, &posture); err != nil {
		t.Fatalf("decode posture: %v (%s)", err, body)
	}
	if posture.BrowserAuth.Enabled {
		t.Errorf("browser_auth.enabled = true with the flow off: %s", body)
	}
	if posture.Subject != "" {
		t.Errorf("a stale session cookie resolved subject %q, want anonymous", posture.Subject)
	}
}

// webUIAuthEnvDefaultTTL drives the environment-carrier point at the default
// window: with the flow enabled through the environment variables the sign-in route answers, and the pre-authorization cookie
// carries the default sign-in window as its Max-Age.
func webUIAuthEnvDefaultTTL(t *testing.T) {
	t.Parallel()
	requireCustomTrustStore(t)
	idp := startOIDCTestIdP(t, "")
	env := append([]string{"HOME=" + t.TempDir(), "PODIUM_WEB_UI=true", "PODIUM_WEB_UI_AUTH=true"}, webUIAuthEnv(idp)...)
	srv := startServerArgs(t, env, "serve", "--standalone")

	resp := signInResponse(t, srv.BaseURL)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("GET sign-in = %d, want 302\nlog:\n%s", resp.StatusCode, srv.log())
	}
	if got := transactionMaxAge(resp); got != 600 {
		t.Errorf("__Host-podium_auth Max-Age = %d, want the 10m default (600)", got)
	}
}

// webUIAuthFlagConfiguredTTL drives the flag-carrier point at a configured
// window. The enablement key and the sign-in window carry both
// a flag and an environment variable, so this point reaches the field through
// `podium serve` flag registration, which no environment point reaches, and it
// pins the configured window on the served cookie.
func webUIAuthFlagConfiguredTTL(t *testing.T) {
	t.Parallel()
	requireCustomTrustStore(t)
	idp := startOIDCTestIdP(t, "")
	env := append([]string{"HOME=" + t.TempDir()}, webUIAuthEnv(idp)...)
	srv := startServerArgs(t, env,
		"serve", "--standalone", "--web-ui", "--web-ui-auth", "--web-ui-auth-transaction-ttl", "90s")

	resp := signInResponse(t, srv.BaseURL)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("GET sign-in = %d, want 302\nlog:\n%s", resp.StatusCode, srv.log())
	}
	if got := transactionMaxAge(resp); got != 90 {
		t.Errorf("__Host-podium_auth Max-Age = %d, want the configured 90", got)
	}
}
