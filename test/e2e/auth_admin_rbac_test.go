package e2e

// End-to-end coverage for the §4.7.2 admin role enforced through the verified
// injected-session-token path. A bootstrap grant makes alice an admin; every
// admin operation (grant, revoke, show-effective, and the diagnostic load
// override) is gated by core.AdminAuthorize, which compares the verified token
// sub against the admin-grant table for the tenant. A verified non-admin and
// an unauthenticated caller are both rejected with auth.forbidden (HTTP 403).
//
// This closes the gap where admin RBAC was asserted only for the load/search
// override; the grant, revoke, and show-effective endpoints had no e2e proving
// the gate holds for a minted admin token and fails closed for everyone else.
//
// Helpers here are prefixed rbac* so they do not collide with sibling auth
// tests in package e2e.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// rbacBearer performs an HTTP request to the running server with an optional
// Bearer token and returns the status and body. Unlike injGet it accepts any
// method and an optional JSON body, which the admin grant/revoke endpoints
// require (POST and DELETE).
func rbacBearer(t *testing.T, method, url, token string, body []byte) (int, []byte) {
	t.Helper()
	contentType := ""
	if body != nil {
		contentType = "application/json"
	}
	return oidcSCIMDo(t, method, url, token, contentType, body)
}

// Spec: §4.7.2 — the admin role gates grant, revoke, and the per-layer
// show-effective diagnostic. The check runs through core.AdminAuthorize over
// the verified identity, so a minted admin token is accepted and a verified
// non-admin is rejected with auth.forbidden.
func TestAuthAdminRBAC_GrantRevokeShowEffectiveGated(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	priv, pemPath := injKeyPair(t)

	// A restricted layer (visible only to bob) gives show-effective a layer
	// whose visibility differs per target, so the response is observable.
	layerRoot := writeRegistry(t, map[string]string{
		"finance/ledger/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: restricted ledger\n---\n\nbody\n",
	})
	cfgPath := filepath.Join(home, "registry.yaml")
	cfg := "" +
		"registry:\n" +
		"  layers:\n" +
		"    - id: restricted\n" +
		"      source:\n" +
		"        local:\n" +
		"          path: " + layerRoot + "\n" +
		"      visibility:\n" +
		"        users: [bob@acme.com]\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}

	srv := startServerArgs(t, []string{
		"HOME=" + home,
		"PODIUM_CONFIG_FILE=" + cfgPath,
		"PODIUM_INGEST_OFFLINE=true",
		"PODIUM_IDENTITY_PROVIDER=injected-session-token",
		"PODIUM_OAUTH_AUDIENCE=" + injAudience,
		"PODIUM_BOOTSTRAP_ADMINS=alice@acme.com",
	}, "serve", "--standalone")
	injRegisterRuntime(t, srv, pemPath)

	adminToken := injSignJWT(t, priv, injClaims("alice@acme.com")) // bootstrap admin
	carolToken := injSignJWT(t, priv, injClaims("carol@acme.com")) // verified non-admin

	grantsURL := srv.BaseURL + "/v1/admin/grants"

	// A verified non-admin cannot grant: auth.forbidden (403).
	if st, body := rbacBearer(t, http.MethodPost, grantsURL, carolToken, []byte(`{"user_id":"dave@acme.com"}`)); st != http.StatusForbidden {
		t.Errorf("non-admin grant = %d, want 403 (body=%s)", st, body)
	} else {
		var env struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(body, &env)
		if env.Code != "auth.forbidden" {
			t.Errorf("non-admin grant code = %q, want auth.forbidden", env.Code)
		}
	}

	// An unauthenticated grant is also rejected (never 201).
	if st, _ := rbacBearer(t, http.MethodPost, grantsURL, "", []byte(`{"user_id":"dave@acme.com"}`)); st == http.StatusCreated {
		t.Errorf("unauthenticated grant = 201, want a rejection")
	}

	// The admin grants bob: 201.
	if st, body := rbacBearer(t, http.MethodPost, grantsURL, adminToken, []byte(`{"user_id":"bob@acme.com"}`)); st != http.StatusCreated {
		t.Fatalf("admin grant bob = %d, want 201 (body=%s)", st, body)
	}

	// The freshly-granted bob can now exercise an admin-only endpoint, proving
	// the grant took effect (not merely returned 201). show-effective is
	// admin-gated, so a 200 here means bob holds the admin role.
	bobToken := injSignJWT(t, priv, injClaims("bob@acme.com"))
	if st, body := rbacBearer(t, http.MethodGet, srv.BaseURL+"/v1/admin/show-effective?user_id=carol@acme.com", bobToken, nil); st != http.StatusOK {
		t.Errorf("granted bob show-effective = %d, want 200 (body=%s)", st, body)
	}

	// The admin revokes bob: 204.
	if st, body := rbacBearer(t, http.MethodDelete, grantsURL+"?user_id=bob@acme.com", adminToken, nil); st != http.StatusNoContent {
		t.Fatalf("admin revoke bob = %d, want 204 (body=%s)", st, body)
	}

	// After revocation bob is no longer an admin: show-effective is 403.
	if st, _ := rbacBearer(t, http.MethodGet, srv.BaseURL+"/v1/admin/show-effective?user_id=carol@acme.com", bobToken, nil); st != http.StatusForbidden {
		t.Errorf("revoked bob show-effective = %d, want 403", st)
	}

	// The admin's own show-effective returns the per-layer view for a target.
	// bob is in the restricted layer's user list, so the restricted layer is
	// visible to target bob and the response reflects that.
	st, body := rbacBearer(t, http.MethodGet, srv.BaseURL+"/v1/admin/show-effective?user_id=bob@acme.com", adminToken, nil)
	if st != http.StatusOK {
		t.Fatalf("admin show-effective = %d, want 200 (body=%s)", st, body)
	}
	var eff struct {
		UserID string `json:"user_id"`
		Layers []struct {
			LayerID string `json:"LayerID"`
			Visible bool   `json:"Visible"`
			Reason  string `json:"Reason"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(body, &eff); err != nil {
		t.Fatalf("decode show-effective: %v (body=%s)", err, body)
	}
	if eff.UserID != "bob@acme.com" {
		t.Errorf("show-effective user_id = %q, want bob@acme.com", eff.UserID)
	}
	var restrictedVisible, sawRestricted bool
	for _, l := range eff.Layers {
		if l.LayerID == "restricted" {
			sawRestricted = true
			restrictedVisible = l.Visible
		}
	}
	if !sawRestricted {
		t.Fatalf("show-effective missing the restricted layer: %+v", eff.Layers)
	}
	if !restrictedVisible {
		t.Errorf("restricted layer not visible to its listed user bob: %+v", eff.Layers)
	}

	// The same diagnostic for a target who is NOT in the layer's user list
	// reports the restricted layer as not visible, confirming the response is
	// computed per target rather than constant.
	st, body = rbacBearer(t, http.MethodGet, srv.BaseURL+"/v1/admin/show-effective?user_id=carol@acme.com", adminToken, nil)
	if st != http.StatusOK {
		t.Fatalf("admin show-effective (carol) = %d, want 200 (body=%s)", st, body)
	}
	eff.Layers = nil
	if err := json.Unmarshal(body, &eff); err != nil {
		t.Fatalf("decode show-effective (carol): %v", err)
	}
	for _, l := range eff.Layers {
		if l.LayerID == "restricted" && l.Visible {
			t.Errorf("restricted layer visible to non-listed carol: %+v", eff.Layers)
		}
	}
}

// Spec: §4.7.2 — "View any layer's contents for diagnostic purposes (override
// visibility ...)". The override is admin-only: a verified admin sees an
// otherwise-invisible artifact with as_admin=1, a verified non-admin is 403,
// and an unauthenticated override never succeeds. This complements the
// grant/revoke gate above by proving the read-path override consults the same
// AdminAuthorize check over the verified token.
func TestAuthAdminRBAC_LoadOverrideGated(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	priv, pemPath := injKeyPair(t)

	layerRoot := writeRegistry(t, map[string]string{
		"hr/secret/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: restricted hr record\n---\n\nbody\n",
	})
	cfgPath := filepath.Join(home, "registry.yaml")
	cfg := "" +
		"registry:\n" +
		"  layers:\n" +
		"    - id: hr-restricted\n" +
		"      source:\n" +
		"        local:\n" +
		"          path: " + layerRoot + "\n" +
		"      visibility:\n" +
		"        users: [bob@acme.com]\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}

	srv := startServerArgs(t, []string{
		"HOME=" + home,
		"PODIUM_CONFIG_FILE=" + cfgPath,
		"PODIUM_INGEST_OFFLINE=true",
		"PODIUM_IDENTITY_PROVIDER=injected-session-token",
		"PODIUM_OAUTH_AUDIENCE=" + injAudience,
		"PODIUM_BOOTSTRAP_ADMINS=alice@acme.com",
	}, "serve", "--standalone")
	injRegisterRuntime(t, srv, pemPath)

	adminToken := injSignJWT(t, priv, injClaims("alice@acme.com"))
	carolToken := injSignJWT(t, priv, injClaims("carol@acme.com"))

	const id = "hr/secret"
	loadURL := srv.BaseURL + "/v1/load_artifact?id=" + id

	// alice is an admin but not in the restricted layer; a normal load is 404.
	if st, body := injGet(t, loadURL, adminToken); st != http.StatusNotFound {
		t.Errorf("admin normal load = %d, want 404 (body=%s)", st, body)
	}
	// With the override the admin sees it.
	if st, body := injGet(t, loadURL+"&as_admin=1", adminToken); st != http.StatusOK {
		t.Errorf("admin override load = %d, want 200 (body=%s)", st, body)
	}
	// A verified non-admin is rejected at the gate.
	if st, _ := injGet(t, loadURL+"&as_admin=1", carolToken); st != http.StatusForbidden {
		t.Errorf("non-admin override load = %d, want 403", st)
	}
	// An unauthenticated override never succeeds.
	if st, _ := injGet(t, loadURL+"&as_admin=1", ""); st == http.StatusOK {
		t.Errorf("unauthenticated override load = 200, want a rejection")
	}
}
