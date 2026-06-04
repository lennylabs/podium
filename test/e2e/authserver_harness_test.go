package e2e

// Shared authenticated, visibility-capable server harness (G-INFRA-5).
//
// The standalone e2e harness (startServer / startServerArgs with
// --standalone) wires no identity provider and materializes every layer
// public, so any per-caller or per-layer-visibility assertion is
// inexpressible against it. The capability to do better already existed in
// scattered form: test/integration/injected_session_token_test.go boots a
// server behind the §6.3.2 verifier and mints JWTs, the parity helpers in
// injected_token_helpers_test.go generate a runtime key pair and register it,
// and auth_admin_rbac_test.go / admin_visibility_override_test.go /
// auth_scim_visibility_test.go each hand-roll a registry.yaml with per-layer
// visibility. This file packages those into one reusable primitive.
//
// startAuthServer boots `serve --standalone` in injected-session-token mode
// from a declarative layer set with explicit visibility (public, org,
// groups:<g>, users:<u>, or private), registers the runtime signing key, and
// optionally seeds SCIM users and groups so the §4.6 groups: filter resolves
// through the registry directory. The returned authServer mints a caller
// token for a given identity, org, and group set, and carries request
// helpers (get, do, searchIDs, loadStatus) so visibility, hidden-parent,
// group-filter, and admin-RBAC tests express a caller's view in a few lines.
//
// Spec: §4.6 (visibility evaluator), §6.3.1 (scopes, SCIM membership),
// §6.3.2 (injected-session-token verification), §4.7.2 (admin RBAC).

import (
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// authVisibility is the declarative §4.6 visibility for one layer. The zero
// value (no field set) is a private layer, visible only to an admin override.
// Setting Public makes the layer visible to everyone; Org makes it visible to
// any authenticated caller; Groups and Users restrict it to the named groups
// or user identifiers.
type authVisibility struct {
	Public bool
	Org    bool
	Groups []string
	Users  []string
}

// authYAML renders the visibility as the registry.yaml `visibility:` block
// body (already indented under the layer entry). A private layer renders an
// explicit empty block so the layer is parsed but reachable only via the
// admin override.
func (v authVisibility) authYAML(indent string) string {
	var b strings.Builder
	b.WriteString(indent + "visibility:\n")
	if v.Public {
		b.WriteString(indent + "  public: true\n")
	}
	if v.Org {
		b.WriteString(indent + "  organization: true\n")
	}
	if len(v.Groups) > 0 {
		b.WriteString(indent + "  groups: [" + strings.Join(v.Groups, ", ") + "]\n")
	}
	if len(v.Users) > 0 {
		b.WriteString(indent + "  users: [" + strings.Join(v.Users, ", ") + "]\n")
	}
	return b.String()
}

// authLayer is one declarative layer in the harness: an id, the artifact
// files placed in its local source root (path -> file contents, e.g.
// "finance/ledger/ARTIFACT.md" -> a frontmatter document), and the
// visibility that gates it.
type authLayer struct {
	ID         string
	Files      map[string]string
	Visibility authVisibility
}

// authServerSpec declares an authenticated, visibility-capable server. Layers
// are listed lowest-precedence first (registry.yaml order is the §4.6
// precedence). BootstrapAdmins seeds the §4.7.2 admin-grant table so a token
// minted for one of those identities passes core.AdminAuthorize. SCIMToken,
// when set, mounts the SCIM endpoint and wires the group resolver so a
// layer's groups: filter resolves through SCIM-pushed membership; the
// SCIMUsers entries are then provisioned at boot.
type authServerSpec struct {
	Layers          []authLayer
	BootstrapAdmins []string
	// SCIMToken mounts /scim/v2 and wires WithGroupResolver when non-empty.
	SCIMToken string
	// SCIMUsers maps a userName (matched against the token sub or email by the
	// §4.6 evaluator) to the SCIM groups it belongs to. Provisioned at boot.
	SCIMUsers map[string][]string
	// ExtraEnv is appended to the server environment (last write wins), for
	// tests that need an additional knob without a bespoke boot.
	ExtraEnv []string
}

// authServer is a running authenticated harness. It exposes the base URL, the
// runtime signing key (for minting tokens), and request helpers. Its zero
// value is not usable; construct it with startAuthServer.
type authServer struct {
	t    *testing.T
	proc *serverProc
	// priv signs minted tokens. The verifier trusts the matching public key
	// under the package-wide injIssuer / injAudience.
	priv *rsa.PrivateKey
	// BaseURL is the running server's HTTP base, e.g. http://127.0.0.1:PORT.
	BaseURL string
}

// authIdentity is the declarative caller identity a token is minted for. Sub
// is the OIDC subject (required); Email and Org map to the email / org_id
// claims; Groups carries the JWT groups claim; Scopes carries §6.3.1
// "podium:*" OAuth scope grants that narrow the caller's surface.
type authIdentity struct {
	Sub    string
	Email  string
	Org    string
	Groups []string
	Scopes []string
}

// startAuthServer boots a standalone registry behind the injected-session-token
// verifier from spec, registers the runtime signing key so minted tokens
// verify immediately, and seeds any declared SCIM users and groups. It returns
// a handle whose token() mints caller tokens and whose request helpers drive
// the server as a chosen identity.
func startAuthServer(t *testing.T, spec authServerSpec) *authServer {
	t.Helper()
	home := t.TempDir()
	priv, pemPath := injKeyPair(t)

	cfgPath := filepath.Join(home, "registry.yaml")
	if err := os.WriteFile(cfgPath, []byte(authRenderConfig(t, home, spec)), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}

	env := []string{
		"HOME=" + home,
		"PODIUM_CONFIG_FILE=" + cfgPath,
		"PODIUM_INGEST_OFFLINE=true",
		"PODIUM_IDENTITY_PROVIDER=injected-session-token",
		"PODIUM_OAUTH_AUDIENCE=" + injAudience,
	}
	if len(spec.BootstrapAdmins) > 0 {
		env = append(env, "PODIUM_BOOTSTRAP_ADMINS="+strings.Join(spec.BootstrapAdmins, ","))
	}
	if spec.SCIMToken != "" {
		env = append(env, "PODIUM_SCIM_TOKENS="+spec.SCIMToken)
	}
	env = append(env, spec.ExtraEnv...)

	proc := startServerArgs(t, env, "serve", "--standalone")
	injRegisterRuntime(t, proc, pemPath)

	as := &authServer{
		t:       t,
		proc:    proc,
		priv:    priv,
		BaseURL: proc.BaseURL,
	}
	if spec.SCIMToken != "" {
		as.seedSCIM(spec)
	}
	return as
}

// authRenderConfig renders the registry.yaml for spec. Each layer's files are
// written under a per-layer local source root inside home, and the layer entry
// names that root plus the declared visibility.
func authRenderConfig(t *testing.T, home string, spec authServerSpec) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("registry:\n")
	b.WriteString("  layers:\n")
	for _, l := range spec.Layers {
		root := writeAuthLayerRoot(t, home, l)
		b.WriteString("    - id: " + l.ID + "\n")
		b.WriteString("      source:\n")
		b.WriteString("        local:\n")
		b.WriteString("          path: " + root + "\n")
		b.WriteString(l.Visibility.authYAML("      "))
	}
	return b.String()
}

// writeAuthLayerRoot writes a layer's declared files under
// home/<layerID>-src/ and returns that root. A layer with no explicit files
// gets a single placeholder artifact so the source is non-empty and ingests.
func writeAuthLayerRoot(t *testing.T, home string, l authLayer) string {
	t.Helper()
	root := filepath.Join(home, l.ID+"-src")
	files := l.Files
	if len(files) == 0 {
		files = map[string]string{
			l.ID + "/placeholder/ARTIFACT.md": authContext(l.ID + " placeholder"),
		}
	}
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return root
}

// authContext returns a minimal §4.4 context artifact with the given
// description, suitable as a layer's placeholder or as an inline test fixture.
func authContext(description string) string {
	return "---\ntype: context\nversion: 1.0.0\ndescription: " + description + "\n---\n\nbody\n"
}

// authSkillFiles returns the two files a §4.3.4 skill artifact requires: an
// ARTIFACT.md naming the type and version, and a sibling SKILL.md whose
// top-level name matches the leaf directory (the ingest linter rejects a
// mismatch). dir is the artifact path without the trailing file, for example
// "finance/run-close"; name is the leaf directory ("run-close"). The returned
// map is merged into an authLayer's Files.
func authSkillFiles(dir, name, description string) map[string]string {
	return map[string]string{
		dir + "/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- Skill body lives in SKILL.md. -->\n",
		dir + "/SKILL.md":    "---\nname: " + name + "\ndescription: " + description + "\n---\n\n" + name + " body.\n",
	}
}

// token mints a signed injected-session-token for id. Sub is required; Email,
// Org, Groups, and Scopes are attached when set. The token verifies against
// the registered runtime immediately, so it is accepted on the next request.
func (as *authServer) token(id authIdentity) string {
	as.t.Helper()
	if id.Sub == "" {
		as.t.Fatal("authServer.token: identity Sub is required")
	}
	claims := injClaims(id.Sub)
	if id.Email != "" {
		claims["email"] = id.Email
	}
	if id.Org != "" {
		claims["org_id"] = id.Org
	}
	if len(id.Groups) > 0 {
		claims["groups"] = toAnySlice(id.Groups)
	}
	if len(id.Scopes) > 0 {
		claims["scope"] = strings.Join(id.Scopes, " ")
	}
	return injSignJWT(as.t, as.priv, claims)
}

// adminToken mints a token for a bootstrap-admin identity. It is token() with
// a clearer name at the call site; the identity must be one of the spec's
// BootstrapAdmins (or a granted admin) for admin endpoints to accept it.
func (as *authServer) adminToken(sub string) string {
	return as.token(authIdentity{Sub: sub})
}

// get issues an authenticated GET against a path under the server base URL
// (path begins with "/") and returns the status and body. An empty token
// sends no Authorization header, exercising the unauthenticated case.
func (as *authServer) get(path, token string) (int, []byte) {
	as.t.Helper()
	return injGet(as.t, as.BaseURL+path, token)
}

// do issues an arbitrary authenticated request with an optional JSON body and
// returns the status and body. It covers the admin grant/revoke endpoints
// (POST/DELETE) the read-only get cannot express.
func (as *authServer) do(method, path, token string, body []byte) (int, []byte) {
	as.t.Helper()
	ct := ""
	if body != nil {
		ct = "application/json"
	}
	return oidcSCIMDo(as.t, method, as.BaseURL+path, token, ct, body)
}

// loadStatus returns the HTTP status of load_artifact for id as token. It is
// the common visibility assertion: 200 when the caller can see the artifact,
// 404 (visibility.denied is reported as not-found on the read path) when not.
func (as *authServer) loadStatus(id, token string) int {
	as.t.Helper()
	st, _ := as.get("/v1/load_artifact?id="+id, token)
	return st
}

// loadCode returns the HTTP status and the registry error code (if any) for a
// load_artifact call, so a test can assert visibility.denied explicitly where
// the surface reports it on the object route or batch path.
func (as *authServer) loadCode(id, token string) (int, string) {
	as.t.Helper()
	st, body := as.get("/v1/load_artifact?id="+id, token)
	return st, authErrCode(body)
}

// searchIDs returns the sorted artifact ids search_artifacts surfaces for
// token, so a test asserts a restricted artifact is omitted from an
// unauthorized caller's discovery surface and present for an authorized one.
func (as *authServer) searchIDs(token string) []string {
	as.t.Helper()
	st, body := as.get("/v1/search_artifacts?query=", token)
	if st != http.StatusOK {
		as.t.Fatalf("search_artifacts = %d, want 200 (body=%s)", st, body)
	}
	var resp struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		as.t.Fatalf("decode search: %v (body=%s)", err, body)
	}
	ids := make([]string, 0, len(resp.Results))
	for _, r := range resp.Results {
		ids = append(ids, r.ID)
	}
	sort.Strings(ids)
	return ids
}

// log returns the server's captured stdout+stderr for failure diagnostics.
func (as *authServer) log() string { return as.proc.log() }

// seedSCIM provisions the spec's SCIM users and groups so the §4.6 groups:
// filter resolves through the registry directory. A user is created, then
// added to each named group (groups are created on first reference). The
// userName equals the value matched against the token sub or email, so a token
// minted for that identity is resolved into the group.
func (as *authServer) seedSCIM(spec authServerSpec) {
	as.t.Helper()
	// userName -> SCIM id.
	userID := map[string]string{}
	// groupName -> member SCIM ids.
	groupMembers := map[string][]string{}
	// Stable user iteration so group membership is deterministic.
	users := make([]string, 0, len(spec.SCIMUsers))
	for u := range spec.SCIMUsers {
		users = append(users, u)
	}
	sort.Strings(users)
	for _, userName := range users {
		id := as.scimCreateUser(spec.SCIMToken, userName)
		userID[userName] = id
		for _, g := range spec.SCIMUsers[userName] {
			groupMembers[g] = append(groupMembers[g], id)
		}
	}
	groups := make([]string, 0, len(groupMembers))
	for g := range groupMembers {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	for _, g := range groups {
		as.scimCreateGroup(spec.SCIMToken, g, groupMembers[g])
	}
}

// scimCreateUser provisions a SCIM user and returns its server-assigned id.
func (as *authServer) scimCreateUser(scimToken, userName string) string {
	as.t.Helper()
	st, body := as.do2(http.MethodPost, "/scim/v2/Users", scimToken, "application/scim+json", oidcSCIMUserBody(userName))
	if st != http.StatusCreated {
		as.t.Fatalf("SCIM create user %q: HTTP %d body=%s", userName, st, body)
	}
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		as.t.Fatalf("decode SCIM user: %v", err)
	}
	id, _ := resp["id"].(string)
	if id == "" {
		as.t.Fatalf("SCIM create user %q: response missing id (body=%s)", userName, body)
	}
	return id
}

// scimCreateGroup provisions a SCIM group with the given displayName and
// member ids. displayName must equal the layer's visibility.groups entry
// because MembersOf matches on DisplayName.
func (as *authServer) scimCreateGroup(scimToken, displayName string, memberIDs []string) {
	as.t.Helper()
	st, body := as.do2(http.MethodPost, "/scim/v2/Groups", scimToken, "application/scim+json", oidcSCIMGroupBody(displayName, memberIDs))
	if st != http.StatusCreated {
		as.t.Fatalf("SCIM create group %q: HTTP %d body=%s", displayName, st, body)
	}
}

// do2 is do with an explicit content type, used by the SCIM seeding path
// which sends application/scim+json rather than application/json.
func (as *authServer) do2(method, path, token, contentType string, body []byte) (int, []byte) {
	as.t.Helper()
	return oidcSCIMDo(as.t, method, as.BaseURL+path, token, contentType, body)
}

// authErrCode extracts the registry error `code` from a JSON error body, or
// "" when the body is not a coded error envelope.
func authErrCode(body []byte) string {
	var env struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &env)
	return env.Code
}

// toAnySlice widens a []string to []any for a jwt.MapClaims array claim.
func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// authGrantAdmin grants the admin role to userID through the §4.7.2 admin
// grants endpoint as an existing admin, failing the test on a non-201. It lets
// a test promote a second identity to admin and assert the grant took effect.
func (as *authServer) authGrantAdmin(adminToken, userID string) {
	as.t.Helper()
	body, _ := json.Marshal(map[string]any{"user_id": userID})
	if st, b := as.do(http.MethodPost, "/v1/admin/grants", adminToken, body); st != http.StatusCreated {
		as.t.Fatalf("admin grant %s = %d, want 201 (body=%s)", userID, st, b)
	}
}
