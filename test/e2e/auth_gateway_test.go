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
	"encoding/json"
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
// identity headers. proxySecret, when non-empty, sets PODIUM_TRUSTED_PROXY_SECRET,
// and scimToken, when non-empty, sets PODIUM_SCIM_TOKENS so the same fixture
// mounts the §6.3.1 SCIM receiver.
func gwTrustedHeadersServer(t *testing.T, proxySecret, scimToken string) *serverProc {
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
	if scimToken != "" {
		env = append(env, "PODIUM_SCIM_TOKENS="+scimToken)
	}
	return startServerArgs(t, env, "serve", "--standalone")
}

// Spec: §6.3.3 — trusted-headers resolves the caller from gateway-injected
// X-Podium-User-* headers and applies §4.6 per-layer visibility. An engineering
// caller sees the engineering layer; a non-member and an anonymous caller see
// the public layer only.
func TestGateway_TrustedHeadersVisibility(t *testing.T) {
	t.Parallel()
	srv := gwTrustedHeadersServer(t, "", "")

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
	srv := gwTrustedHeadersServer(t, "s3cr3t", "")

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

// Spec: §6.3.3 / §13.12 — oidc-jwt requires PODIUM_OAUTH_AUDIENCE to name at
// least one audience. An unset variable, and a comma-separated list whose
// every entry is blank, both resolve to no entry and fail startup with
// config.oidc_jwt_audience_unset. The blank-list arm is the one a resolution
// that dropped the trim would boot, leaving the verifier holding an empty
// entry that matches a token carrying aud: ["", "x"].
func TestGateway_OIDCJWTMissingAudienceRefused(t *testing.T) {
	for _, audience := range []string{"", " , "} {
		gwExpectStartupFailure(t, "config.oidc_jwt_audience_unset",
			"PODIUM_IDENTITY_PROVIDER=oidc-jwt",
			"PODIUM_OAUTH_ISSUER=https://acme.okta.example/oauth2/default",
			"PODIUM_OAUTH_AUDIENCE="+audience,
		)
	}
}

// Spec: §6.3.1 / §13.12 — a non-empty PODIUM_IDP_GROUP_MAPPING that does not
// resolve to a claim=group table fails startup with
// config.invalid_idp_group_mapping, under every identity provider and so with
// no identity-provider variable set at all. The whitespace-only value is
// refused alongside the separators-only one: both are non-empty settings that
// configure no table, and both are the same operator mistake.
// Matrix: §6.10 (config.invalid_idp_group_mapping)
func TestGateway_IdpGroupMappingMalformedRefused(t *testing.T) {
	for _, spec := range []string{"finance", "ok=fine,broken", " ", " , "} {
		gwExpectStartupFailure(t, "config.invalid_idp_group_mapping",
			"PODIUM_IDP_GROUP_MAPPING="+spec,
		)
	}
}

// Spec: §13.12 — the refusal is a startup guard. `podium config show --server`
// reads the same setting without validating it, so the diagnostic command that
// an operator reaches for after the refusal still exits 0 and still names
// PODIUM_IDP_GROUP_MAPPING as the source of the idp_group_mapping row.
// Matrix: §6.10 (config.invalid_idp_group_mapping)
func TestGateway_IdpGroupMappingConfigShowStillRuns(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", []string{
		"PODIUM_CONFIG_FILE=" + filepath.Join(t.TempDir(), "absent.yaml"),
		"PODIUM_IDP_GROUP_MAPPING=finance",
	}, "config", "show", "--server")
	if res.Exit != 0 {
		t.Fatalf("config show --server exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "PODIUM_IDP_GROUP_MAPPING") {
		t.Errorf("config show missing the idp_group_mapping source column:\n%s", res.Stdout)
	}
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

// gwSCIMToken is the bearer token the trusted-headers SCIM fixture mounts its
// §6.3.1 receiver with.
const gwSCIMToken = "gw-scim-bearer"

// gwPushSCIMMember pushes a SCIM user whose userName is userName and a SCIM
// group named group holding it, so the pushed directory names the caller as a
// member of the layer's `groups:` entry.
func gwPushSCIMMember(t *testing.T, srv *serverProc, userName, group string) {
	t.Helper()
	st, body := oidcSCIMDo(t, http.MethodPost, srv.BaseURL+"/scim/v2/Users",
		gwSCIMToken, "application/scim+json", oidcSCIMUserBody(userName))
	if st != http.StatusCreated {
		t.Fatalf("SCIM create user %q = %d, want 201\nbody: %s\nlog:\n%s", userName, st, body, srv.log())
	}
	var user struct{ ID string }
	if err := json.Unmarshal(body, &user); err != nil {
		t.Fatalf("decode SCIM user: %v (body=%s)", err, body)
	}
	if user.ID == "" {
		t.Fatalf("SCIM create user %q: response carries no id (body=%s)", userName, body)
	}
	st, body = oidcSCIMDo(t, http.MethodPost, srv.BaseURL+"/scim/v2/Groups",
		gwSCIMToken, "application/scim+json", oidcSCIMGroupBody(group, []string{user.ID}))
	if st != http.StatusCreated {
		t.Fatalf("SCIM create group %q = %d, want 201\nbody: %s", group, st, body)
	}
}

// gwLayerIDs reads the §7.3.1 layer list with the given request headers and
// reports the layer IDs it names. The read is narrowed to the caller's §4.6
// view, and it reaches the evaluator through readableBy rather than through
// the composed catalog, so it is the second consumer of the boot path's group
// resolver.
func gwLayerIDs(t *testing.T, srv *serverProc, headers map[string]string) []string {
	t.Helper()
	st, body := gwHeaderGet(t, srv.BaseURL+"/v1/layers", headers)
	if st != http.StatusOK {
		t.Fatalf("layer read = %d, want 200\nbody: %s\nlog:\n%s", st, body, srv.log())
	}
	var resp struct {
		Layers []struct{ ID string }
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode layer list: %v (body=%s)", err, body)
	}
	ids := make([]string, 0, len(resp.Layers))
	for _, l := range resp.Layers {
		ids = append(ids, l.ID)
	}
	return ids
}

// gwListsLayer reports whether ids holds the named layer.
func gwListsLayer(ids []string, id string) bool {
	for _, got := range ids {
		if got == id {
			return true
		}
	}
	return false
}

// Spec: §4.6, §6.3.1, §6.3.3, §7.3.1 — under trusted-headers the gateway's
// X-Podium-User-Groups is the whole of the caller's group membership, and the
// §6.3.1 pushed directory is not consulted, because there is no token to read
// and the gateway is the source of truth. A registry that mounts the SCIM
// receiver keeps the endpoint and the persistence; what the directory loses is
// its effect on a `groups:` filter.
//
// The two authenticated arms and the negative control read both consumers: the
// composed catalog through load_artifact and the layer read through
// GET /v1/layers, which reach the evaluator by different routes. The caller
// holds no §4.7.2 admin grant, which the negative arms establish by listing
// the public layer alone rather than the whole tenant list.
//
// The anonymous arms are asserted on the data plane alone, because the layer
// read answers an unauthenticated caller the empty list before the evaluator
// runs (§7.3.1), so the public layer is absent there as well.
func TestGateway_TrustedHeadersIgnoresSCIMDirectory(t *testing.T) {
	t.Parallel()
	srv := gwTrustedHeadersServer(t, "s3cr3t", gwSCIMToken)
	gwPushSCIMMember(t, srv, "alice@acme.com", "engineering")

	const (
		publicArtifact = "/v1/load_artifact?id=welcome"
		engArtifact    = "/v1/load_artifact?id=secret"
	)

	// The directory names alice in engineering. The gateway sends no groups
	// header, so neither consumer admits her to the engineering layer. The
	// pre-fix registry admits her on both, matching the pushed userName
	// against her asserted sub.
	bySub := map[string]string{
		"X-Podium-User-Sub":     "alice@acme.com",
		"X-Podium-Proxy-Secret": "s3cr3t",
	}
	// The same directory entry reached through the email header, which the
	// expander compares as well, so both routes to the grant are withdrawn.
	byEmail := map[string]string{
		"X-Podium-User-Sub":     "opaque-123",
		"X-Podium-User-Email":   "alice@acme.com",
		"X-Podium-Proxy-Secret": "s3cr3t",
	}
	for name, hdr := range map[string]map[string]string{"by sub": bySub, "by email": byEmail} {
		if st, _ := gwHeaderGet(t, srv.BaseURL+engArtifact, hdr); st != 404 {
			t.Errorf("%s: load engineering secret = %d, want 404\nlog:\n%s", name, st, srv.log())
		}
		ids := gwLayerIDs(t, srv, hdr)
		if gwListsLayer(ids, "eng-layer") {
			t.Errorf("%s: layer read lists eng-layer (%v), want the public layer alone", name, ids)
		}
		if !gwListsLayer(ids, "public-layer") {
			t.Errorf("%s: layer read = %v, want it to name public-layer", name, ids)
		}
	}

	// The negative control: the header-derived grant is intact, so the
	// correction withdrew the directory arm alone.
	byHeader := map[string]string{
		"X-Podium-User-Sub":     "alice@acme.com",
		"X-Podium-User-Groups":  "engineering",
		"X-Podium-Proxy-Secret": "s3cr3t",
	}
	if st, body := gwHeaderGet(t, srv.BaseURL+engArtifact, byHeader); st != 200 {
		t.Errorf("groups header: load engineering secret = %d, want 200\nbody: %s\nlog:\n%s", st, body, srv.log())
	}
	if ids := gwLayerIDs(t, srv, byHeader); !gwListsLayer(ids, "eng-layer") {
		t.Errorf("groups header: layer read = %v, want it to name eng-layer", ids)
	}

	// An anonymous caller reaches the public layer and not the engineering
	// one, and a request carrying the identity headers without the proxy
	// secret is anonymous on the same terms.
	noSecret := map[string]string{
		"X-Podium-User-Sub":   "alice@acme.com",
		"X-Podium-User-Email": "alice@acme.com",
	}
	for name, hdr := range map[string]map[string]string{"anonymous": nil, "no proxy secret": noSecret} {
		if st, body := gwHeaderGet(t, srv.BaseURL+publicArtifact, hdr); st != 200 {
			t.Errorf("%s: load public welcome = %d, want 200\nbody: %s", name, st, body)
		}
		if st, _ := gwHeaderGet(t, srv.BaseURL+engArtifact, hdr); st != 404 {
			t.Errorf("%s: load engineering secret = %d, want 404", name, st)
		}
	}

	// The startup line reports the expansion withheld on a registry that
	// mounts the receiver under trusted-headers.
	if got := srv.log(); !strings.Contains(got, "SCIM group expansion in layer visibility: false") {
		t.Errorf("boot log does not report the expansion withheld:\n%s", got)
	}
}
