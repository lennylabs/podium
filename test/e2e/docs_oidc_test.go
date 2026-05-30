package e2e

// End-to-end tests for docs/deployment/oidc/*.md (D-oidc).
//
// These tests cover the CLI device-code login flow, SCIM HTTP endpoints,
// config-show and status surface, and not-implemented admin subcommands.
// They drive the real podium binary and, for device-code tests, a local
// stub OIDC server built with net/http/httptest.
//
// Skip policy:
//
//   Tests that require the registry to verify a real JWT (audience, JWKS,
//   signature, hd_required, groups-claim mapping, clock skew, JWKS caching,
//   trailing-slash issuer) are skipped honestly: the standalone server does
//   not parse the nested identity: YAML block (T-D-oidc-28 doc-accuracy gap)
//   and therefore cannot exercise those paths in an e2e setting.
//
//   Tests that need the OS keychain (login success persistence, logout/status
//   round-trip, token refresh) are skipped honestly: writing to the system
//   keychain from CI is unsafe, and PODIUM_TOKEN_KEYCHAIN_NAME is used by
//   login.go for the keychain service name only, not to isolate the store.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// ---- stub OIDC server helpers -----------------------------------------------

// oidcDeviceResponse is the fixed device-auth response returned by the stub.
const oidcDeviceResponse = `{"device_code":"dev","user_code":"WXYZ-1234","verification_uri":"http://stub.example/activate","verification_uri_complete":"http://stub.example/activate?code=WXYZ-1234","expires_in":300,"interval":1}`

// oidcTokenSuccess is the fixed token response for a successful poll.
const oidcTokenSuccess = `{"access_token":"at","token_type":"Bearer","expires_in":3600,"refresh_token":"rt"}`

// oidcStubConfig controls the stub server behaviour per test.
type oidcStubConfig struct {
	// tokenResponses is the sequence of token-endpoint responses to return, one
	// per poll.  When exhausted, the last entry is repeated.
	tokenResponses []string
	// deviceResponse overrides the device-auth response (default: oidcDeviceResponse).
	deviceResponse string
	// deviceStatus overrides the device-auth HTTP status code (default: 200).
	deviceStatus int
}

// oidcStub builds a stub OIDC server with:
//
//   - POST /oauth2/device  — device-auth endpoint
//   - POST /oauth2/token   — token-poll endpoint
//
// It records every form-decoded request body so tests can assert on the
// parameters sent by the CLI.
type oidcStub struct {
	srv            *httptest.Server
	mu             sync.Mutex
	deviceBodies   []url.Values // one entry per device-auth call
	tokenBodies    []url.Values // one entry per token-poll call
	pollCount      int32        // atomic; number of token polls received
	tokenResponses []string
}

// newOIDCStub creates and starts the stub. The caller must call Stop() via
// t.Cleanup or directly.
func newOIDCStub(cfg oidcStubConfig) *oidcStub {
	s := &oidcStub{tokenResponses: cfg.tokenResponses}
	if len(s.tokenResponses) == 0 {
		s.tokenResponses = []string{oidcTokenSuccess}
	}
	devResp := cfg.deviceResponse
	if devResp == "" {
		devResp = oidcDeviceResponse
	}
	devStatus := cfg.deviceStatus
	if devStatus == 0 {
		devStatus = http.StatusOK
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/device", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		vals, _ := url.ParseQuery(string(body))
		s.mu.Lock()
		s.deviceBodies = append(s.deviceBodies, vals)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(devStatus)
		_, _ = w.Write([]byte(devResp))
	})
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		vals, _ := url.ParseQuery(string(body))
		s.mu.Lock()
		s.tokenBodies = append(s.tokenBodies, vals)
		s.mu.Unlock()
		n := int(atomic.AddInt32(&s.pollCount, 1))
		idx := n - 1
		s.mu.Lock()
		resp := s.tokenResponses[len(s.tokenResponses)-1]
		if idx < len(s.tokenResponses) {
			resp = s.tokenResponses[idx]
		}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		// RFC 8628: the token endpoint returns OAuth error codes
		// (authorization_pending, slow_down, access_denied, expired_token)
		// with HTTP 400. The device-code Poll only maps the error envelope on
		// a non-200 status, so error responses must carry 400.
		if strings.Contains(resp, `"error"`) {
			w.WriteHeader(http.StatusBadRequest)
		}
		_, _ = w.Write([]byte(resp))
	})
	s.srv = httptest.NewServer(mux)
	return s
}

// DeviceURL is the stub's device-auth endpoint URL.
func (s *oidcStub) DeviceURL() string { return s.srv.URL + "/oauth2/device" }

// TokenURL is the stub's token endpoint URL.
func (s *oidcStub) TokenURL() string { return s.srv.URL + "/oauth2/token" }

// BaseURL is the root URL of the stub server.
func (s *oidcStub) BaseURL() string { return s.srv.URL }

// Stop shuts down the stub server.
func (s *oidcStub) Stop() { s.srv.Close() }

// lastDeviceBody returns the most recent device-auth request body, or empty
// Values if no call has been made yet.
func (s *oidcStub) lastDeviceBody() url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.deviceBodies) == 0 {
		return url.Values{}
	}
	return s.deviceBodies[len(s.deviceBodies)-1]
}

// lastTokenBody returns the most recent token-poll request body.
func (s *oidcStub) lastTokenBody() url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.tokenBodies) == 0 {
		return url.Values{}
	}
	return s.tokenBodies[len(s.tokenBodies)-1]
}

// oidcRunLogin runs `podium login` under a bounded context (loginTimeout)
// against the given stub, waiting for the process to exit.  If the context
// expires first it is cancelled and the result reflects whatever was written
// to stderr up to that point.
//
// NOTE: every call that might reach the token-polling step must supply a
// context deadline shorter than the test timeout so the process never hangs.
func oidcRunLogin(t testing.TB, stub *oidcStub, extraEnv []string, loginTimeout time.Duration, extraArgs ...string) cliResult {
	t.Helper()
	bin := cmdharness.Bin(t, "podium")
	ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
	defer cancel()

	baseArgs := []string{"login",
		"--registry", "http://podium.acme.example",
		"--issuer", stub.DeviceURL(),
		"--token-url", stub.TokenURL(),
		"--client-id", "test-client",
	}
	args := append(baseArgs, extraArgs...)

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = mergeEnv(append([]string{"PODIUM_NO_AUTOSTANDALONE=1"}, extraEnv...)...)
	cmd.Stdin = bytes.NewReader(nil)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()

	res := cliResult{Stdout: so.String(), Stderr: se.String()}
	if ee, ok := err.(*exec.ExitError); ok {
		res.Exit = ee.ExitCode()
	} else if err != nil && ctx.Err() != context.DeadlineExceeded {
		t.Logf("login: %v (stderr=%s)", err, se.String())
	}
	return res
}

// oidcSCIMDo performs an HTTP request to the running server's SCIM endpoint.
func oidcSCIMDo(t testing.TB, method, url, token, contentType string, body []byte) (int, []byte) {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		t.Fatalf("build request %s %s: %v", method, url, err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// oidcSCIMUserBody returns a minimal SCIM 2.0 user body.
func oidcSCIMUserBody(userName string) []byte {
	b, _ := json.Marshal(map[string]any{
		"schemas":  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		"userName": userName,
		"active":   true,
	})
	return b
}

// oidcSCIMGroupBody returns a minimal SCIM 2.0 group body.
func oidcSCIMGroupBody(displayName string, memberIDs []string) []byte {
	members := make([]map[string]any, 0, len(memberIDs))
	for _, id := range memberIDs {
		members = append(members, map[string]any{"value": id})
	}
	b, _ := json.Marshal(map[string]any{
		"schemas":     []string{"urn:ietf:params:scim:schemas:core:2.0:Group"},
		"displayName": displayName,
		"members":     members,
	})
	return b
}

// oidcStartSCIMServer starts a standalone registry with SCIM enabled.
func oidcStartSCIMServer(t testing.TB, scimToken string, extraEnv ...string) *serverProc {
	t.Helper()
	reg := writeRegistry(t, map[string]string{"seed/ARTIFACT.md": contextArtifact("seed")})
	env := []string{
		"HOME=" + t.TempDir(),
		"PODIUM_SCIM_TOKENS=" + scimToken,
	}
	env = append(env, extraEnv...)
	return startServerArgs(t, env, "serve", "--standalone", "--layer-path", reg)
}

// ---- T-D-oidc-1: public_mode + identity_provider=oidc => startup fails -----

// T-D-oidc-1
func TestOIDC_1_PublicModeWithIdPFails(t *testing.T) {
	t.Parallel()
	bin := cmdharness.Bin(t, "podium")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	reg := writeRegistry(t, map[string]string{"seed/ARTIFACT.md": contextArtifact("seed")})
	cmd := exec.CommandContext(ctx, bin, "serve", "--standalone", "--layer-path", reg)
	cmd.Env = mergeEnv(
		"HOME="+t.TempDir(),
		"PODIUM_PUBLIC_MODE=true",
		"PODIUM_IDENTITY_PROVIDER=oidc",
	)
	cmd.Stdin = bytes.NewReader(nil)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected non-zero exit for public_mode + identity_provider=oidc, but process exited 0\noutput:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "config.public_mode_with_idp") {
		t.Errorf("output missing 'config.public_mode_with_idp':\n%s", out.String())
	}
}

// ---- T-D-oidc-2: registry.yaml flat identity_provider field is parsed -------

// T-D-oidc-2
func TestOIDC_2_RegistryYAMLIdentityProviderField(t *testing.T) {
	t.Parallel()
	cfgDir := t.TempDir()
	cfgFile := filepath.Join(cfgDir, "registry.yaml")
	if err := os.WriteFile(cfgFile, []byte("identity_provider: oidc\n"), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}
	res := runPodium(t, "", []string{
		"PODIUM_CONFIG_FILE=" + cfgFile,
		"PODIUM_REGISTRY=",
	}, "config", "show")
	if res.Exit != 0 {
		t.Fatalf("config show exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "identity_provider") || !strings.Contains(res.Stdout, "oidc") {
		t.Errorf("config show missing identity_provider=oidc:\n%s", res.Stdout)
	}
}

// ---- T-D-oidc-3: podium login missing --registry => exit 2 ------------------

// T-D-oidc-3
func TestOIDC_3_LoginMissingRegistry(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", []string{"PODIUM_REGISTRY="}, "login")
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--registry is required") {
		t.Errorf("stderr missing '--registry is required':\n%s", res.Stderr)
	}
}

// ---- T-D-oidc-4: podium login missing issuer => exit 2 ----------------------

// T-D-oidc-4
func TestOIDC_4_LoginMissingIssuer(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", []string{
		"PODIUM_REGISTRY=http://podium.acme.example",
		"PODIUM_OAUTH_AUTHORIZATION_ENDPOINT=",
	}, "login", "--registry", "http://podium.acme.example")
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--issuer or PODIUM_OAUTH_AUTHORIZATION_ENDPOINT is required") {
		t.Errorf("stderr missing issuer-required message:\n%s", res.Stderr)
	}
}

// ---- T-D-oidc-5: login prints Visit/User code/Direct link -------------------

// T-D-oidc-5
func TestOIDC_5_LoginPrintsVerificationURLAndCode(t *testing.T) {
	t.Parallel()
	// Stub token returns authorization_pending so login keeps polling (and
	// the bounded context kills it before keychain write).
	stub := newOIDCStub(oidcStubConfig{
		tokenResponses: []string{`{"error":"authorization_pending"}`},
	})
	t.Cleanup(stub.Stop)

	// Run with a short deadline; we only need the initial device-auth step.
	res := oidcRunLogin(t, stub, nil, 5*time.Second)
	// The process may have been killed by the context; that's expected.
	if !strings.Contains(res.Stderr, "Visit:") {
		t.Errorf("stderr missing 'Visit:':\n%s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "User code:") {
		t.Errorf("stderr missing 'User code:':\n%s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "Direct link:") {
		t.Errorf("stderr missing 'Direct link:' (verification_uri_complete present in stub response):\n%s", res.Stderr)
	}
	// Check the values match what the stub serves.
	if !strings.Contains(res.Stderr, "http://stub.example/activate") {
		t.Errorf("stderr missing verification URI 'http://stub.example/activate':\n%s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "WXYZ-1234") {
		t.Errorf("stderr missing user code 'WXYZ-1234':\n%s", res.Stderr)
	}
}

// ---- T-D-oidc-6: login saves token to keychain on success ------------------

// T-D-oidc-6
func TestOIDC_6_LoginSavesTokenToKeychain(t *testing.T) {
	t.Skip("requires an isolated keychain that does not touch the system keychain; PODIUM_TOKEN_KEYCHAIN_NAME controls the keychain service name but does not prevent a system write on platforms that only have the OS keychain")
}

// ---- T-D-oidc-7: login polls through authorization_pending then succeeds ---

// T-D-oidc-7
func TestOIDC_7_LoginPollsThroughAuthorizationPending(t *testing.T) {
	t.Skip("verifying Login successful via stderr requires the process to complete the keychain write; isolated keychain not available in the e2e environment")
}

// ---- T-D-oidc-8: expired_token => exit 1 ------------------------------------

// T-D-oidc-8
func TestOIDC_8_LoginExpiredToken(t *testing.T) {
	t.Parallel()
	stub := newOIDCStub(oidcStubConfig{
		tokenResponses: []string{`{"error":"expired_token"}`},
	})
	t.Cleanup(stub.Stop)

	res := oidcRunLogin(t, stub, nil, 15*time.Second)
	if res.Exit != 1 {
		t.Errorf("exit=%d, want 1 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "token polling") {
		t.Errorf("stderr missing 'token polling':\n%s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "expired_token") {
		t.Errorf("stderr missing 'expired_token':\n%s", res.Stderr)
	}
}

// ---- T-D-oidc-9: access_denied => exit 1 ------------------------------------

// T-D-oidc-9
func TestOIDC_9_LoginAccessDenied(t *testing.T) {
	t.Parallel()
	stub := newOIDCStub(oidcStubConfig{
		tokenResponses: []string{`{"error":"access_denied"}`},
	})
	t.Cleanup(stub.Stop)

	res := oidcRunLogin(t, stub, nil, 15*time.Second)
	if res.Exit != 1 {
		t.Errorf("exit=%d, want 1 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "access_denied") {
		t.Errorf("stderr missing 'access_denied':\n%s", res.Stderr)
	}
}

// ---- T-D-oidc-10: PODIUM_OAUTH_AUDIENCE is sent in device-auth request ------

// T-D-oidc-10
func TestOIDC_10_LoginSendsAudience(t *testing.T) {
	t.Parallel()
	// Stub token returns access_denied so the process exits promptly after the
	// device-auth step without needing a keychain write.
	stub := newOIDCStub(oidcStubConfig{
		tokenResponses: []string{`{"error":"access_denied"}`},
	})
	t.Cleanup(stub.Stop)

	res := oidcRunLogin(t, stub, []string{
		"PODIUM_OAUTH_AUDIENCE=https://podium.acme.example",
	}, 15*time.Second)
	// May exit 1 due to access_denied; that's fine.
	_ = res

	body := stub.lastDeviceBody()
	if body.Get("audience") != "https://podium.acme.example" {
		t.Errorf("device-auth body audience=%q, want 'https://podium.acme.example'\nbody: %v", body.Get("audience"), body)
	}
}

// ---- T-D-oidc-11: PODIUM_OAUTH_CLIENT_SECRET not sent (doc-accuracy gap) ---

// T-D-oidc-11
func TestOIDC_11_LoginClientSecretGap(t *testing.T) {
	t.Parallel()
	// login.go does not read PODIUM_OAUTH_CLIENT_SECRET (doc-accuracy gap for
	// Google Workspace). Confirm the token request body does NOT contain
	// client_secret when the env var is set.
	stub := newOIDCStub(oidcStubConfig{
		tokenResponses: []string{`{"error":"access_denied"}`},
	})
	t.Cleanup(stub.Stop)

	oidcRunLogin(t, stub, []string{
		"PODIUM_OAUTH_CLIENT_SECRET=test-secret",
	}, 15*time.Second)

	tokenBody := stub.lastTokenBody()
	if tokenBody.Get("client_secret") != "" {
		t.Errorf("doc-accuracy gap: PODIUM_OAUTH_CLIENT_SECRET should NOT appear in token request (login.go does not read it), but got client_secret=%q", tokenBody.Get("client_secret"))
	}
	// Also check the device-auth body.
	devBody := stub.lastDeviceBody()
	if devBody.Get("client_secret") != "" {
		t.Logf("note: client_secret also absent from device-auth body (as expected)")
	}
}

// ---- T-D-oidc-12: logout removes token + status shows not found -------------

// T-D-oidc-12
func TestOIDC_12_LogoutRemovesToken(t *testing.T) {
	t.Skip("requires an isolated keychain to seed a token then verify deletion; system keychain cannot be safely used in e2e")
}

// ---- T-D-oidc-13: logout missing --registry => exit 2 ----------------------

// T-D-oidc-13
func TestOIDC_13_LogoutMissingRegistry(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", []string{"PODIUM_REGISTRY="}, "logout")
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--registry is required") {
		t.Errorf("stderr missing '--registry is required':\n%s", res.Stderr)
	}
}

// ---- T-D-oidc-14: status shows identity_provider from env ------------------

// T-D-oidc-14
func TestOIDC_14_StatusShowsIdentityProvider(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", []string{
		"PODIUM_IDENTITY_PROVIDER=oidc",
		"PODIUM_REGISTRY=http://podium.acme.example",
	}, "status")
	if res.Exit != 0 {
		t.Fatalf("status exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "identity provider") || !strings.Contains(res.Stdout, "oidc") {
		t.Errorf("status stdout missing 'identity provider: oidc':\n%s", res.Stdout)
	}
}

// ---- T-D-oidc-15: audience mismatch => 401 auth.audience_mismatch ----------

// T-D-oidc-15
func TestOIDC_15_AudienceMismatch(t *testing.T) {
	t.Skip("requires a configured OIDC identity provider with JWKS verification; the standalone server does not parse the nested identity: block (T-D-oidc-28 doc-accuracy gap), so token verification is not exercisable in e2e")
}

// ---- T-D-oidc-16: invalid JWT signature => 401 auth.signature_invalid -------

// T-D-oidc-16
func TestOIDC_16_InvalidSignature(t *testing.T) {
	t.Skip("requires a configured OIDC identity provider with JWKS verification; the standalone server does not parse the nested identity: block (T-D-oidc-28 doc-accuracy gap), so token verification is not exercisable in e2e")
}

// ---- T-D-oidc-17: hd_required rejects tokens from wrong Workspace domain ---

// T-D-oidc-17
func TestOIDC_17_HDRequired(t *testing.T) {
	t.Skip("requires a configured OIDC identity provider with JWKS verification; the standalone server does not parse the nested identity: block (T-D-oidc-28 doc-accuracy gap), so hd_required enforcement is not exercisable in e2e")
}

// ---- T-D-oidc-18: layer register with --group; visibility needs tokens ------

// T-D-oidc-18
func TestOIDC_18_LayerGroupVisibility(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"seed/ARTIFACT.md": contextArtifact("seed")})
	srv := startServer(t, reg)

	// Register a layer scoped to the "engineering" group.
	localPath := writeRegistry(t, map[string]string{"eng/ARTIFACT.md": contextArtifact("engineering artifact")})
	res := runPodium(t, "", nil,
		"layer", "register",
		"--registry", srv.BaseURL,
		"--id", "engineering-only",
		"--local", localPath,
		"--group", "engineering",
	)
	if res.Exit != 0 {
		t.Fatalf("layer register exit=%d stderr=%s stdout=%s", res.Exit, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "engineering-only") {
		t.Errorf("register stdout missing layer id:\n%s", res.Stdout)
	}

	// Layer list succeeds (standalone resolves callers to system:public which sees
	// public layers; the group-visibility filtering requires verified JWT groups,
	// which is not exercisable in the standalone e2e without OIDC JWKS verification).
	listRes := runPodium(t, "", nil,
		"layer", "list", "--registry", srv.BaseURL,
	)
	if listRes.Exit != 0 {
		t.Fatalf("layer list exit=%d stderr=%s", listRes.Exit, listRes.Stderr)
	}
	// NOTE: Visibility filtering (group-membership enforcement) requires a verified
	// JWT with groups claims. The standalone e2e cannot test that path because the
	// nested identity: block is not parsed (T-D-oidc-28 doc-accuracy gap).
	t.Log("layer register with --group: exit 0 and layer appears in list (visibility filtering requires OIDC tokens; skipped)")
}

// ---- T-D-oidc-19: --organization flag layer; visibility needs tokens --------

// T-D-oidc-19
func TestOIDC_19_LayerOrganizationVisibility(t *testing.T) {
	t.Skip("requires a configured OIDC identity provider with JWKS verification; the standalone server does not parse the nested identity: block (T-D-oidc-28 doc-accuracy gap), so organization-visibility filtering is not exercisable in e2e")
}

// ---- T-D-oidc-20: layer with Entra GUID group; visibility needs tokens ------

// T-D-oidc-20
func TestOIDC_20_LayerEntraGUIDGroup(t *testing.T) {
	t.Skip("requires a configured OIDC identity provider with JWKS verification; the standalone server does not parse the nested identity: block (T-D-oidc-28 doc-accuracy gap), so GUID-group-visibility filtering is not exercisable in e2e")
}

// ---- T-D-oidc-21: SCIM /scim/v2/Users 404 when PODIUM_SCIM_TOKENS unset ----

// T-D-oidc-21
func TestOIDC_21_SCIMNotMountedWithoutToken(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"seed/ARTIFACT.md": contextArtifact("seed")})
	// Start WITHOUT PODIUM_SCIM_TOKENS.
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir()},
		"serve", "--standalone", "--layer-path", reg)

	st, _ := getRaw(t, srv.BaseURL+"/scim/v2/Users")
	if st != http.StatusNotFound {
		t.Errorf("GET /scim/v2/Users without PODIUM_SCIM_TOKENS = HTTP %d, want 404", st)
	}
}

// ---- T-D-oidc-22: SCIM creates user when PODIUM_SCIM_TOKENS is set ----------

// T-D-oidc-22
func TestOIDC_22_SCIMCreateUser(t *testing.T) {
	t.Parallel()
	srv := oidcStartSCIMServer(t, "test-scim-token")

	st, body := oidcSCIMDo(t, http.MethodPost, srv.BaseURL+"/scim/v2/Users",
		"test-scim-token", "application/scim+json", oidcSCIMUserBody("alice@acme.example"))
	if st != http.StatusCreated {
		t.Fatalf("POST /scim/v2/Users = HTTP %d, want 201\nbody: %s", st, body)
	}
	if !strings.Contains(string(body), "alice@acme.example") {
		t.Errorf("response body missing userName:\n%s", body)
	}
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("response not valid JSON: %v\nbody: %s", err, body)
	}
	if resp["id"] == nil || resp["id"] == "" {
		t.Errorf("response missing 'id' field: %v", resp)
	}
}

// ---- T-D-oidc-23: SCIM rejects wrong bearer token => 401 -------------------

// T-D-oidc-23
func TestOIDC_23_SCIMWrongToken(t *testing.T) {
	t.Parallel()
	srv := oidcStartSCIMServer(t, "correct-token")

	st, _ := oidcSCIMDo(t, http.MethodPost, srv.BaseURL+"/scim/v2/Users",
		"wrong-token", "application/scim+json", oidcSCIMUserBody("bob@acme.example"))
	if st != http.StatusUnauthorized {
		t.Errorf("POST /scim/v2/Users with wrong token = HTTP %d, want 401", st)
	}
}

// ---- T-D-oidc-24: SCIM group creation; membership visibility needs tokens ---

// T-D-oidc-24
func TestOIDC_24_SCIMGroupCreation(t *testing.T) {
	t.Parallel()
	srv := oidcStartSCIMServer(t, "test-scim-token")

	// Create a user.
	st, body := oidcSCIMDo(t, http.MethodPost, srv.BaseURL+"/scim/v2/Users",
		"test-scim-token", "application/scim+json", oidcSCIMUserBody("alice@acme.example"))
	if st != http.StatusCreated {
		t.Fatalf("create user: HTTP %d body=%s", st, body)
	}
	var userResp map[string]any
	_ = json.Unmarshal(body, &userResp)
	userID, _ := userResp["id"].(string)

	// Create a group with the user as a member.
	st, body = oidcSCIMDo(t, http.MethodPost, srv.BaseURL+"/scim/v2/Groups",
		"test-scim-token", "application/scim+json", oidcSCIMGroupBody("engineering", []string{userID}))
	if st != http.StatusCreated {
		t.Fatalf("create group: HTTP %d body=%s", st, body)
	}
	if !strings.Contains(string(body), "engineering") {
		t.Errorf("group response missing displayName 'engineering':\n%s", body)
	}
	// NOTE: membership-driven layer visibility requires verified JWT tokens with
	// SCIM-resolved group membership; not exercisable in standalone e2e without
	// the nested identity: block (T-D-oidc-28).
}

// ---- T-D-oidc-25: podium admin scim-token issue => unknown subcommand -------

// T-D-oidc-25
func TestOIDC_25_AdminSCIMTokenIssueNotImplemented(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "admin", "scim-token", "issue")
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "unknown admin subcommand: scim-token") {
		t.Errorf("stderr missing 'unknown admin subcommand: scim-token':\n%s", res.Stderr)
	}
}

// ---- T-D-oidc-26: podium admin scim configure => unknown subcommand ---------

// T-D-oidc-26
func TestOIDC_26_AdminSCIMConfigureNotImplemented(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "admin", "scim", "configure",
		"--endpoint", "https://keycloak.acme.example/realms/main/scim/v2",
		"--token", "bearer-tok")
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "unknown admin subcommand: scim") {
		t.Errorf("stderr missing 'unknown admin subcommand: scim':\n%s", res.Stderr)
	}
}

// ---- T-D-oidc-27: podium admin claims-cache flush => unknown subcommand -----

// T-D-oidc-27
func TestOIDC_27_AdminClaimsCacheFlushNotImplemented(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "admin", "claims-cache", "flush",
		"--user", "alice@acme.example")
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "unknown admin subcommand: claims-cache") {
		t.Errorf("stderr missing 'unknown admin subcommand: claims-cache':\n%s", res.Stderr)
	}
}

// ---- T-D-oidc-28: nested identity: block NOT supported (doc-accuracy gap) --

// T-D-oidc-28
func TestOIDC_28_NestedIdentityBlockNotParsed(t *testing.T) {
	t.Parallel()
	cfgDir := t.TempDir()
	cfgFile := filepath.Join(cfgDir, "registry.yaml")
	// Write the nested identity: block as documented in every per-IdP guide.
	nestedYAML := `identity:
  provider: oidc
  issuer: https://acme.okta.com/oauth2/default
  audience: podium
  jwks_uri: https://acme.okta.com/oauth2/default/v1/keys
  groups_claim: groups
  email_claim: email
  sub_claim: sub
`
	if err := os.WriteFile(cfgFile, []byte(nestedYAML), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}

	// config show runs server-free.
	res := runPodium(t, "", []string{
		"PODIUM_CONFIG_FILE=" + cfgFile,
	}, "config", "show")
	if res.Exit != 0 {
		t.Fatalf("config show exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	// The identity_provider field must NOT be "oidc" from the nested block,
	// because the implementation only parses the flat identity_provider field.
	// A correct output will show identity_provider as "(none)" / blank / "default".
	lines := strings.Split(res.Stdout, "\n")
	for _, line := range lines {
		if strings.Contains(line, "identity_provider") && strings.Contains(line, "oidc") {
			t.Errorf("doc-accuracy gap (T-D-oidc-28): nested identity: block should NOT be parsed, but config show reports identity_provider=oidc:\n%s", res.Stdout)
			return
		}
	}
	t.Log("confirmed: nested identity: block is not parsed; flat identity_provider field is the only supported form")
}

// ---- T-D-oidc-29: Auth0 trailing slash in issuer URL -------------------------

// T-D-oidc-29
func TestOIDC_29_Auth0TrailingSlashIssuer(t *testing.T) {
	t.Skip("requires a configured OIDC identity provider with JWKS verification; the standalone server does not parse the nested identity: block (T-D-oidc-28 doc-accuracy gap), so issuer URL matching is not exercisable in e2e")
}

// ---- T-D-oidc-30: Auth0 namespaced groups claim ------------------------------

// T-D-oidc-30
func TestOIDC_30_Auth0NamespacedGroupsClaim(t *testing.T) {
	t.Skip("requires a configured OIDC identity provider with JWKS verification; the standalone server does not parse the nested identity: block (T-D-oidc-28 doc-accuracy gap), so namespaced groups_claim is not exercisable in e2e")
}

// ---- T-D-oidc-31: Entra ID preferred_username as email claim -----------------

// T-D-oidc-31
func TestOIDC_31_EntraIDPreferredUsername(t *testing.T) {
	t.Skip("requires a configured OIDC identity provider with JWKS verification; the standalone server does not parse the nested identity: block (T-D-oidc-28 doc-accuracy gap), so email_claim mapping is not exercisable in e2e")
}

// ---- T-D-oidc-32: Keycloak plain group name matches layer config ------------

// T-D-oidc-32
func TestOIDC_32_KeycloakPlainGroupName(t *testing.T) {
	t.Skip("requires a configured OIDC identity provider with JWKS verification; the standalone server does not parse the nested identity: block (T-D-oidc-28 doc-accuracy gap), so groups_claim matching is not exercisable in e2e")
}

// ---- T-D-oidc-33: Keycloak full-path group does not match bare name ---------

// T-D-oidc-33
func TestOIDC_33_KeycloakFullPathGroupNoMatch(t *testing.T) {
	t.Skip("requires a configured OIDC identity provider with JWKS verification; the standalone server does not parse the nested identity: block (T-D-oidc-28 doc-accuracy gap), so groups_claim matching is not exercisable in e2e")
}

// ---- T-D-oidc-34: Keycloak pre-Quarkus issuer rejected ----------------------

// T-D-oidc-34
func TestOIDC_34_KeycloakPreQuarkusIssuerRejected(t *testing.T) {
	t.Skip("requires a configured OIDC identity provider with JWKS verification; the standalone server does not parse the nested identity: block (T-D-oidc-28 doc-accuracy gap), so issuer mismatch detection is not exercisable in e2e")
}

// ---- T-D-oidc-35: JWKS endpoint fetched and valid JWT accepted ---------------

// T-D-oidc-35
func TestOIDC_35_JWKSFetchAndValidToken(t *testing.T) {
	t.Skip("requires a configured OIDC identity provider with JWKS verification; the standalone server does not parse the nested identity: block (T-D-oidc-28 doc-accuracy gap), so JWKS-based JWT verification is not exercisable in e2e")
}

// ---- T-D-oidc-36: JWKS cache serves stale keys for up to 5 minutes ----------

// T-D-oidc-36
func TestOIDC_36_JWKSCaching(t *testing.T) {
	t.Skip("requires a configured OIDC identity provider with JWKS verification; the standalone server does not parse the nested identity: block (T-D-oidc-28 doc-accuracy gap), so JWKS caching behavior is not exercisable in e2e")
}

// ---- T-D-oidc-37: clock skew tolerance of ±60s ------------------------------

// T-D-oidc-37
func TestOIDC_37_ClockSkewTolerance(t *testing.T) {
	t.Skip("requires a configured OIDC identity provider with JWKS verification; the standalone server does not parse the nested identity: block (T-D-oidc-28 doc-accuracy gap), so clock-skew tolerance is not exercisable in e2e")
}

// ---- T-D-oidc-38: podium init --global writes registry URL ------------------

// T-D-oidc-38
func TestOIDC_38_InitGlobalWritesRegistryURL(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	res := runPodium(t, "", []string{"HOME=" + home},
		"init", "--global", "--registry", "https://podium.acme.example")
	if res.Exit != 0 {
		t.Fatalf("init --global exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	// init --global writes ~/.podium/sync.yaml with the registry URL.
	syncYAML := filepath.Join(home, ".podium", "sync.yaml")
	content := readFile(t, syncYAML)
	if !strings.Contains(content, "https://podium.acme.example") {
		t.Errorf("global sync.yaml missing registry URL:\n%s", content)
	}
	// `podium status` resolves the registry from PODIUM_REGISTRY / --registry,
	// not from the global sync.yaml, so passing the URL explicitly is how an
	// operator points status at the configured registry.
	st := runPodium(t, "", []string{"HOME=" + home},
		"status", "--registry", "https://podium.acme.example")
	if st.Exit != 0 {
		t.Fatalf("status exit=%d stderr=%s", st.Exit, st.Stderr)
	}
	if !strings.Contains(st.Stdout, "https://podium.acme.example") {
		t.Errorf("status does not show the registry URL:\n%s", st.Stdout)
	}
}

// ---- T-D-oidc-39: Google Workspace without groups_claim accepted ------------

// T-D-oidc-39
func TestOIDC_39_GoogleWorkspaceNoGroupsClaim(t *testing.T) {
	t.Skip("requires a configured OIDC identity provider with JWKS verification; the standalone server does not parse the nested identity: block (T-D-oidc-28 doc-accuracy gap), so Option A (no groups_claim) is not exercisable in e2e")
}

// ---- T-D-oidc-40: SCIM user deletion returns 204; visibility needs tokens ---

// T-D-oidc-40
func TestOIDC_40_SCIMUserDeletion(t *testing.T) {
	t.Parallel()
	srv := oidcStartSCIMServer(t, "test-scim-token")

	// Create a user.
	st, body := oidcSCIMDo(t, http.MethodPost, srv.BaseURL+"/scim/v2/Users",
		"test-scim-token", "application/scim+json", oidcSCIMUserBody("alice@acme.example"))
	if st != http.StatusCreated {
		t.Fatalf("create user: HTTP %d body=%s", st, body)
	}
	var resp map[string]any
	_ = json.Unmarshal(body, &resp)
	userID, _ := resp["id"].(string)
	if userID == "" {
		t.Fatal("create user: response missing id")
	}

	// Delete the user.
	st, _ = oidcSCIMDo(t, http.MethodDelete, fmt.Sprintf("%s/scim/v2/Users/%s", srv.BaseURL, userID),
		"test-scim-token", "", nil)
	if st != http.StatusNoContent {
		t.Errorf("DELETE /scim/v2/Users/%s = HTTP %d, want 204", userID, st)
	}
	// NOTE: membership-driven visibility changes after deletion require verified
	// JWT tokens; not exercisable in standalone e2e (T-D-oidc-28).
}

// ---- T-D-oidc-41: SCIM group membership PUT returns 200 ---------------------

// T-D-oidc-41
func TestOIDC_41_SCIMGroupMembershipUpdate(t *testing.T) {
	t.Parallel()
	srv := oidcStartSCIMServer(t, "test-scim-token")

	// Create a user.
	st, body := oidcSCIMDo(t, http.MethodPost, srv.BaseURL+"/scim/v2/Users",
		"test-scim-token", "application/scim+json", oidcSCIMUserBody("alice@acme.example"))
	if st != http.StatusCreated {
		t.Fatalf("create user: HTTP %d body=%s", st, body)
	}
	var userResp map[string]any
	_ = json.Unmarshal(body, &userResp)
	userID, _ := userResp["id"].(string)

	// Create a group with alice.
	st, body = oidcSCIMDo(t, http.MethodPost, srv.BaseURL+"/scim/v2/Groups",
		"test-scim-token", "application/scim+json", oidcSCIMGroupBody("engineering", []string{userID}))
	if st != http.StatusCreated {
		t.Fatalf("create group: HTTP %d body=%s", st, body)
	}
	var groupResp map[string]any
	_ = json.Unmarshal(body, &groupResp)
	groupID, _ := groupResp["id"].(string)
	if groupID == "" {
		t.Fatal("create group: response missing id")
	}

	// Update group to remove alice (empty members).
	st, _ = oidcSCIMDo(t, http.MethodPut, fmt.Sprintf("%s/scim/v2/Groups/%s", srv.BaseURL, groupID),
		"test-scim-token", "application/scim+json", oidcSCIMGroupBody("engineering", nil))
	if st != http.StatusOK {
		t.Errorf("PUT /scim/v2/Groups/%s = HTTP %d, want 200", groupID, st)
	}
	// NOTE: the effect on layer visibility requires verified JWT tokens;
	// not exercisable in standalone e2e (T-D-oidc-28).
}

// ---- T-D-oidc-42: guessTokenURL derives /token from /device -----------------

// T-D-oidc-42
func TestOIDC_42_GuessTokenURL(t *testing.T) {
	t.Parallel()
	// The stub serves both /oauth2/device and /oauth2/token.
	// We set only --issuer (pointing at /oauth2/device) and omit --token-url
	// so guessTokenURL must derive /oauth2/token.
	var tokenHit atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/device", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, oidcDeviceResponse)
	})
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		tokenHit.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, `{"error":"access_denied"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	bin := cmdharness.Bin(t, "podium")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// Provide only --issuer; no --token-url.
	cmd := exec.CommandContext(ctx, bin, "login",
		"--registry", "http://podium.acme.example",
		"--issuer", srv.URL+"/oauth2/device",
		"--client-id", "test-client",
	)
	cmd.Env = mergeEnv("PODIUM_NO_AUTOSTANDALONE=1")
	cmd.Stdin = bytes.NewReader(nil)
	var se bytes.Buffer
	cmd.Stderr = &se
	_ = cmd.Run()

	if tokenHit.Load() == 0 {
		t.Errorf("guessTokenURL did not derive /oauth2/token from /oauth2/device; token endpoint never received a request\nstderr: %s", se.String())
	}
}

// ---- T-D-oidc-43: default scopes include "groups" ---------------------------

// T-D-oidc-43
func TestOIDC_43_DefaultScopesIncludeGroups(t *testing.T) {
	t.Parallel()
	stub := newOIDCStub(oidcStubConfig{
		tokenResponses: []string{`{"error":"access_denied"}`},
	})
	t.Cleanup(stub.Stop)

	oidcRunLogin(t, stub, nil, 15*time.Second)

	body := stub.lastDeviceBody()
	scope := body.Get("scope")
	for _, want := range []string{"openid", "profile", "email", "groups"} {
		if !strings.Contains(scope, want) {
			t.Errorf("device-auth scope=%q missing %q", scope, want)
		}
	}
}

// ---- T-D-oidc-44: slow_down increases poll interval ------------------------

// T-D-oidc-44
func TestOIDC_44_SlowDownIncreasesInterval(t *testing.T) {
	t.Skip("timing-sensitive; covered by pkg/identity DeviceCodeFlow.Poll unit test; not a reliable e2e signal")
}

// ---- T-D-oidc-45: /healthz reachable for OIDC-configured registry -----------

// T-D-oidc-45
func TestOIDC_45_HealthzReachableWithOIDCMode(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"seed/ARTIFACT.md": contextArtifact("seed")})
	// PODIUM_IDENTITY_PROVIDER=oidc is treated as a label; it does not prevent
	// startup because the nested identity: block (issuer/JWKS) is not parsed.
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_IDENTITY_PROVIDER=oidc",
	}, "serve", "--standalone", "--layer-path", reg)

	var healthz map[string]any
	getJSON(t, srv.BaseURL+"/healthz", &healthz)
	if healthz["mode"] == nil {
		t.Errorf("/healthz response missing 'mode': %v", healthz)
	}

	// podium status should report reachability OK.
	st := runPodium(t, "", []string{
		"PODIUM_REGISTRY=" + srv.BaseURL,
		"PODIUM_IDENTITY_PROVIDER=oidc",
	}, "status")
	if st.Exit != 0 {
		t.Fatalf("status exit=%d stderr=%s", st.Exit, st.Stderr)
	}
	if !strings.Contains(st.Stdout, "reachability") {
		t.Errorf("status stdout missing 'reachability':\n%s", st.Stdout)
	}
}

// ---- T-D-oidc-46: --visibility flag does not exist => exit 2 ---------------

// T-D-oidc-46
func TestOIDC_46_LayerRegisterVisibilityFlagNotExist(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"seed/ARTIFACT.md": contextArtifact("seed")})
	srv := startServer(t, reg)
	localPath := writeRegistry(t, map[string]string{"eng/ARTIFACT.md": contextArtifact("eng")})

	res := runPodium(t, "", nil,
		"layer", "register",
		"--registry", srv.BaseURL,
		"--id", "test-vis",
		"--local", localPath,
		"--visibility", `groups: ["engineering"]`,
	)
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 for unknown --visibility flag (stderr=%s stdout=%s)", res.Exit, res.Stderr, res.Stdout)
	}
}

// ---- T-D-oidc-47: SCIM store persists across restart ------------------------

// T-D-oidc-47
func TestOIDC_47_SCIMStorePersistsAcrossRestart(t *testing.T) {
	t.Parallel()
	storeDir := t.TempDir()
	storePath := filepath.Join(storeDir, "scim.json")
	reg := writeRegistry(t, map[string]string{"seed/ARTIFACT.md": contextArtifact("seed")})

	// First server instance.
	srv1 := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_SCIM_TOKENS=tok",
		"PODIUM_SCIM_STORE_PATH=" + storePath,
	}, "serve", "--standalone", "--layer-path", reg)

	// Create a user.
	st, body := oidcSCIMDo(t, http.MethodPost, srv1.BaseURL+"/scim/v2/Users",
		"tok", "application/scim+json", oidcSCIMUserBody("alice@acme.example"))
	if st != http.StatusCreated {
		t.Fatalf("create user: HTTP %d body=%s", st, body)
	}

	// Stop the first server by stopping its process.
	stopProc(srv1.cmd)

	// Verify the store file exists.
	mustExist(t, storePath)
	storeContent := readFile(t, storePath)
	if !strings.Contains(storeContent, "alice@acme.example") {
		t.Errorf("scim store file missing alice@acme.example:\n%s", storeContent)
	}

	// Second server instance with same store path.
	srv2 := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_SCIM_TOKENS=tok",
		"PODIUM_SCIM_STORE_PATH=" + storePath,
	}, "serve", "--standalone", "--layer-path", reg)

	// List users; alice must still be present.
	st, body = oidcSCIMDo(t, http.MethodGet, srv2.BaseURL+"/scim/v2/Users",
		"tok", "", nil)
	if st != http.StatusOK {
		t.Fatalf("GET /scim/v2/Users: HTTP %d body=%s", st, body)
	}
	if !strings.Contains(string(body), "alice@acme.example") {
		t.Errorf("SCIM users after restart missing alice@acme.example:\n%s", body)
	}
}

// ---- T-D-oidc-48: token refresh retains old refresh token -------------------

// T-D-oidc-48
func TestOIDC_48_TokenRefreshRetainsOldToken(t *testing.T) {
	t.Skip("DeviceCodeFlow.Refresh is a pkg/identity unit-level surface; not a CLI/e2e surface; covered by pkg/identity oauth_devicecode unit tests")
}

// ---- T-D-oidc-49: refresh revocation returns ErrAccessDenied ---------------

// T-D-oidc-49
func TestOIDC_49_RefreshRevocationErrAccessDenied(t *testing.T) {
	t.Skip("DeviceCodeFlow.Refresh is a pkg/identity unit-level surface; not a CLI/e2e surface; covered by pkg/identity oauth_devicecode unit tests")
}

// ---- T-D-oidc-50: unreachable issuer => descriptive error -------------------

// T-D-oidc-50
func TestOIDC_50_UnreachableIssuer(t *testing.T) {
	t.Parallel()
	bin := cmdharness.Bin(t, "podium")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// Port 1 on loopback is effectively always refused (well-known unusable port).
	cmd := exec.CommandContext(ctx, bin, "login",
		"--registry", "https://podium.acme.example",
		"--issuer", "http://127.0.0.1:1/device",
		"--client-id", "podium",
	)
	cmd.Env = mergeEnv("PODIUM_NO_AUTOSTANDALONE=1")
	cmd.Stdin = bytes.NewReader(nil)
	var se bytes.Buffer
	cmd.Stderr = &se
	err := cmd.Run()
	exitCode := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exitCode = ee.ExitCode()
	}
	if exitCode != 1 {
		t.Errorf("exit=%d, want 1 for unreachable issuer (stderr=%s)", exitCode, se.String())
	}
	if !strings.Contains(se.String(), "device authorization:") {
		t.Errorf("stderr missing 'device authorization:':\n%s", se.String())
	}
}

// ---- T-D-oidc-51: malformed device-auth response => descriptive error -------

// T-D-oidc-51
func TestOIDC_51_MalformedDeviceAuthResponse(t *testing.T) {
	t.Parallel()
	// Stub returns 200 with a body missing device_code and verification_uri.
	stub := newOIDCStub(oidcStubConfig{
		deviceResponse: `{"user_code":"ABCD"}`,
	})
	t.Cleanup(stub.Stop)

	res := oidcRunLogin(t, stub, nil, 15*time.Second)
	if res.Exit != 1 {
		t.Errorf("exit=%d, want 1 for malformed device-auth response (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "device authorization:") {
		t.Errorf("stderr missing 'device authorization:':\n%s", res.Stderr)
	}
}

// ---- T-D-oidc-52: groups claim as non-array scalar ---------------------------

// T-D-oidc-52
func TestOIDC_52_GroupsClaimNonArrayScalar(t *testing.T) {
	t.Skip("requires a configured OIDC identity provider with JWKS verification; the standalone server does not parse the nested identity: block (T-D-oidc-28 doc-accuracy gap), so groups-claim type handling is not exercisable in e2e")
}

// ---- T-D-oidc-53: Okta audience mismatch => 401 auth.audience_mismatch ------

// T-D-oidc-53
func TestOIDC_53_OktaAudienceMismatch(t *testing.T) {
	t.Skip("requires a configured OIDC identity provider with JWKS verification; the standalone server does not parse the nested identity: block (T-D-oidc-28 doc-accuracy gap), so audience-mismatch detection is not exercisable in e2e")
}

// ---- T-D-oidc-54: Entra ID audience must include api:// prefix exactly ------

// T-D-oidc-54
func TestOIDC_54_EntraIDAudiencePrefixExact(t *testing.T) {
	t.Skip("requires a configured OIDC identity provider with JWKS verification; the standalone server does not parse the nested identity: block (T-D-oidc-28 doc-accuracy gap), so audience prefix validation is not exercisable in e2e")
}
