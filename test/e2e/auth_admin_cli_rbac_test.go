package e2e

// Admin RBAC through the CLI against an authenticated server (gap G-AUTH-14).
//
// admin grant, revoke, show-effective, and the admin-defined layer distinction
// were exercised only against the standalone server, which resolves callers to
// system:public and so cannot tell an admin from a non-admin. The HTTP-level
// gate is covered by auth_admin_rbac_test.go; this drives the same §4.7.2
// operations through the real podium CLI binary against the authenticated
// harness (G-INFRA-5), so the CLI's credential attachment (PODIUM_SESSION_TOKEN
// -> Authorization: Bearer) and the server's AdminAuthorize check are exercised
// end to end.
//
// A bootstrap admin (alice) runs `podium admin grant` to promote bob, then
// `show-effective` confirms the per-layer view computes, then `revoke` removes
// the grant and a follow-up admin operation as bob is refused. A non-admin
// (carol) is refused at the gate with HTTP 403 on grant, revoke, and
// show-effective. The admin also registers an admin-defined layer through the
// CLI to confirm the admin path the no-op standalone authorizer could not
// distinguish.
//
// The CLI authenticates with PODIUM_SESSION_TOKEN, the injected-session-token
// credential the harness mints; readCLIToken reads it (cmd/podium/main.go), and
// doJSON attaches it as a bearer on the admin endpoints. PODIUM_TOKEN_KEYCHAIN_NAME
// and a fresh HOME isolate the CLI from any developer keychain.
//
// Spec: §4.7.2 (the admin role gates grant, revoke, show-effective, and
// admin-defined layer mutation; the check runs through AdminAuthorize over the
// verified identity), §7.6 / §7.6.1 (the CLI attaches the caller credential to
// authenticated registry endpoints). Gap G-AUTH-14.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// acliEnv returns the CLI environment for a call as token against srv: the
// registry URL, the session token the admin endpoints authenticate with, an
// isolated keychain name, and a fresh HOME so no developer credential leaks in.
func acliEnv(t *testing.T, srv *authServer, token string) []string {
	t.Helper()
	return []string{
		"PODIUM_REGISTRY=" + srv.BaseURL,
		"PODIUM_SESSION_TOKEN=" + token,
		"PODIUM_TOKEN_KEYCHAIN_NAME=podium-acli-test",
		"HOME=" + t.TempDir(),
	}
}

// TestAuthAdminCLIRBAC_GrantRevokeShowEffective drives podium admin grant,
// revoke, and show-effective through the CLI as a bootstrap admin against the
// authenticated harness, asserts the grant takes effect (the promoted user can
// run an admin operation) and is removed by revoke, and asserts a non-admin is
// refused with 403 on every admin operation.
func TestAuthAdminCLIRBAC_GrantRevokeShowEffective(t *testing.T) {
	t.Parallel()

	// A restricted layer gives show-effective an observable per-target view:
	// bob is in the layer's user list, carol is not.
	srv := startAuthServer(t, authServerSpec{
		BootstrapAdmins: []string{"alice@acme.com"},
		Layers: []authLayer{{
			ID:         "restricted",
			Files:      map[string]string{"finance/ledger/ARTIFACT.md": authContext("restricted ledger")},
			Visibility: authVisibility{Users: []string{"bob@acme.com"}},
		}},
	})
	adminToken := srv.adminToken("alice@acme.com")
	carolToken := srv.token(authIdentity{Sub: "carol@acme.com", Email: "carol@acme.com"})

	// ---- A verified non-admin is refused at the gate on every admin command --
	for _, tc := range []struct {
		what string
		args []string
	}{
		{"grant", []string{"admin", "grant", "dave@acme.com"}},
		{"revoke", []string{"admin", "revoke", "dave@acme.com"}},
		{"show-effective", []string{"admin", "show-effective", "bob@acme.com"}},
	} {
		res := runPodium(t, "", acliEnv(t, srv, carolToken), tc.args...)
		cliWantNonZero(t, res, "non-admin "+tc.what)
		if !strings.Contains(res.Stderr, "403") {
			t.Errorf("non-admin %s: stderr missing 403 (auth.forbidden)\nstderr: %s", tc.what, res.Stderr)
		}
	}

	// ---- The admin grants bob through the CLI --------------------------------
	grant := runPodium(t, "", acliEnv(t, srv, adminToken), "admin", "grant", "bob@acme.com")
	cliWantExit(t, grant, 0, "admin grant bob")

	// The freshly-granted bob can now run an admin-only command, proving the
	// grant took effect rather than merely returning success. His token holds no
	// bootstrap admin; the grant is the only thing that admits him.
	bobToken := srv.token(authIdentity{Sub: "bob@acme.com", Email: "bob@acme.com"})
	bobShow := runPodium(t, "", acliEnv(t, srv, bobToken), "admin", "show-effective", "carol@acme.com")
	cliWantExit(t, bobShow, 0, "granted bob show-effective")

	// ---- The admin's show-effective computes the per-target view -------------
	show := runPodium(t, "", acliEnv(t, srv, adminToken), "admin", "show-effective", "bob@acme.com")
	cliWantExit(t, show, 0, "admin show-effective bob")
	acliAssertLayerVisible(t, show.Stdout, "restricted", true, "bob is in the restricted layer's user list")

	showCarol := runPodium(t, "", acliEnv(t, srv, adminToken), "admin", "show-effective", "carol@acme.com")
	cliWantExit(t, showCarol, 0, "admin show-effective carol")
	acliAssertLayerVisible(t, showCarol.Stdout, "restricted", false, "carol is not in the restricted layer's user list")

	// ---- The admin revokes bob; bob is then refused --------------------------
	revoke := runPodium(t, "", acliEnv(t, srv, adminToken), "admin", "revoke", "bob@acme.com")
	cliWantExit(t, revoke, 0, "admin revoke bob")

	bobAfter := runPodium(t, "", acliEnv(t, srv, bobToken), "admin", "show-effective", "carol@acme.com")
	cliWantNonZero(t, bobAfter, "revoked bob show-effective")
	if !strings.Contains(bobAfter.Stderr, "403") {
		t.Errorf("revoked bob show-effective: stderr missing 403\nstderr: %s", bobAfter.Stderr)
	}
}

// TestAuthAdminCLIRBAC_AdminDefinedLayerRegistration registers an admin-defined
// layer through the CLI as the bootstrap admin and confirms the server accepts
// it as admin-defined (UserDefined=false). The standalone server's no-op admin
// authorizer could not make this distinction; the authenticated harness can.
func TestAuthAdminCLIRBAC_AdminDefinedLayerRegistration(t *testing.T) {
	t.Parallel()

	srv := startAuthServer(t, authServerSpec{
		BootstrapAdmins: []string{"alice@acme.com"},
		Layers: []authLayer{{
			ID:         "seed",
			Files:      map[string]string{"seed/note/ARTIFACT.md": authContext("seed note")},
			Visibility: authVisibility{Public: true},
		}},
	})
	adminToken := srv.adminToken("alice@acme.com")

	// A local source the registry can read. No --user-defined flag: this is an
	// admin-defined registration, which the server gates on AdminAuthorize.
	layerRoot := writeRegistry(t, map[string]string{
		"ops/runbook/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: an admin-defined operations runbook\n---\n\nbody\n",
	})
	reg := runPodium(t, "", acliEnv(t, srv, adminToken),
		"layer", "register", "--id", "ops", "--local", layerRoot, "--organization")
	cliWantExit(t, reg, 0, "admin layer register")

	// The server recorded the layer as admin-defined (not coerced to a personal
	// user-defined layer). Read it back over the authenticated list endpoint.
	st, body := srv.get("/v1/layers", adminToken)
	if st != http.StatusOK {
		t.Fatalf("list layers = %d\nbody: %s", st, body)
	}
	var listed struct {
		Layers []struct {
			ID          string `json:"id"`
			UserDefined bool   `json:"user_defined"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decode layer list: %v\nbody: %s", err, body)
	}
	var found, userDefined bool
	for _, l := range listed.Layers {
		if l.ID == "ops" {
			found = true
			userDefined = l.UserDefined
		}
	}
	if !found {
		t.Fatalf("registered layer 'ops' not found in list: %s", body)
	}
	if userDefined {
		t.Errorf("admin-registered layer 'ops' recorded as user_defined=true; an admin registration must be admin-defined")
	}
}

// acliAssertLayerVisible decodes a show-effective JSON response and asserts the
// named layer's Visible flag matches want, so the test confirms the per-target
// computation rather than only the exit code.
func acliAssertLayerVisible(t *testing.T, stdout, layerID string, want bool, why string) {
	t.Helper()
	var eff struct {
		Layers []struct {
			LayerID string `json:"LayerID"`
			Visible bool   `json:"Visible"`
		} `json:"layers"`
	}
	if err := json.Unmarshal([]byte(stdout), &eff); err != nil {
		t.Fatalf("decode show-effective: %v\nstdout: %s", err, stdout)
	}
	var saw bool
	for _, l := range eff.Layers {
		if l.LayerID == layerID {
			saw = true
			if l.Visible != want {
				t.Errorf("show-effective layer %q Visible=%v, want %v (%s)", layerID, l.Visible, want, why)
			}
		}
	}
	if !saw {
		t.Fatalf("show-effective response missing layer %q: %s", layerID, stdout)
	}
}
