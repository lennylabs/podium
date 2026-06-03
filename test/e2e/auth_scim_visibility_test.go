package e2e

// End-to-end coverage for §6.3.1 SCIM-resolved group membership driving layer
// visibility. The registry resolves group membership registry-side via SCIM
// 2.0 push and maintains a (user_id -> groups) directory; the §4.6 visibility
// evaluator expands a layer's `groups:` filter by calling MembersOf on that
// directory. This test pushes a user and a group over the SCIM endpoint, then
// proves a verified caller whose token does NOT itself carry the group claim
// still sees a groups-restricted layer because SCIM membership grants it. A
// removal case (the member is taken out of the SCIM group) revokes that
// visibility.
//
// This fills the gap left by the existing SCIM endpoint tests, which assert
// CRUD status codes but annotate that membership-driven visibility "requires
// verified JWT tokens; not exercisable". The injected-session-token path makes
// it exercisable.
//
// Helpers/identifiers here are prefixed scimvis* so they do not collide with
// sibling auth tests in package e2e.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

const scimvisToken = "scimvis-bearer"

// scimvisStartServer boots a standalone registry in injected-session-token
// mode with SCIM mounted (PODIUM_SCIM_TOKENS) and a single layer whose
// visibility is restricted to the named group. Setting PODIUM_SCIM_TOKENS is
// what wires WithGroupResolver(scimStore.MembersOf), so the group filter is
// resolved through the SCIM directory.
func scimvisStartServer(t *testing.T, home, groupName string) (*serverProc, string) {
	t.Helper()
	layerRoot := writeRegistry(t, map[string]string{
		"engineering/runbook/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: engineering runbook\n---\n\nbody\n",
	})
	cfgPath := filepath.Join(home, "registry.yaml")
	cfg := "" +
		"registry:\n" +
		"  layers:\n" +
		"    - id: eng\n" +
		"      source:\n" +
		"        local:\n" +
		"          path: " + layerRoot + "\n" +
		"      visibility:\n" +
		"        groups: [" + groupName + "]\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}
	srv := startServerArgs(t, []string{
		"HOME=" + home,
		"PODIUM_CONFIG_FILE=" + cfgPath,
		"PODIUM_INGEST_OFFLINE=true",
		"PODIUM_IDENTITY_PROVIDER=injected-session-token",
		"PODIUM_OAUTH_AUDIENCE=" + injAudience,
		"PODIUM_SCIM_TOKENS=" + scimvisToken,
	}, "serve", "--standalone")
	return srv, cfgPath
}

// scimvisPushUser creates a SCIM user with the given userName and returns its
// server-assigned id. MembersOf returns userNames, and the §4.6 evaluator
// matches each against the token sub or email, so userName must equal the sub
// (or email) of the token the caller later mints.
func scimvisPushUser(t *testing.T, srv *serverProc, userName string) string {
	t.Helper()
	st, body := oidcSCIMDo(t, http.MethodPost, srv.BaseURL+"/scim/v2/Users",
		scimvisToken, "application/scim+json", oidcSCIMUserBody(userName))
	if st != http.StatusCreated {
		t.Fatalf("SCIM create user %q: HTTP %d body=%s", userName, st, body)
	}
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode SCIM user: %v", err)
	}
	id, _ := resp["id"].(string)
	if id == "" {
		t.Fatalf("SCIM create user %q: response missing id (body=%s)", userName, body)
	}
	return id
}

// Spec: §6.3.1 — group membership is resolved registry-side via SCIM 2.0 push;
// the §4.6 visibility evaluator expands the layer `groups:` filter through that
// directory. A verified caller whose token carries no group claim still sees a
// groups-restricted layer when SCIM places the user in the group. Removing the
// user from the group revokes the visibility.
func TestAuthSCIMVisibility_MembershipDrivesVisibility(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	priv, pemPath := injKeyPair(t)

	const groupName = "engineering"
	srv, _ := scimvisStartServer(t, home, groupName)
	injRegisterRuntime(t, srv, pemPath)

	// alice is provisioned over SCIM; bob is not. The token sub equals the
	// SCIM userName so MembersOf -> userName matches the verified identity.
	aliceID := scimvisPushUser(t, srv, "alice@acme.com")
	_ = scimvisPushUser(t, srv, "bob@acme.com")

	// A group with alice as its only member. displayName must equal the
	// layer's visibility.groups entry because MembersOf matches on DisplayName.
	st, body := oidcSCIMDo(t, http.MethodPost, srv.BaseURL+"/scim/v2/Groups",
		scimvisToken, "application/scim+json", oidcSCIMGroupBody(groupName, []string{aliceID}))
	if st != http.StatusCreated {
		t.Fatalf("SCIM create group: HTTP %d body=%s", st, body)
	}
	var groupResp map[string]any
	if err := json.Unmarshal(body, &groupResp); err != nil {
		t.Fatalf("decode SCIM group: %v", err)
	}
	groupID, _ := groupResp["id"].(string)
	if groupID == "" {
		t.Fatalf("SCIM create group: response missing id (body=%s)", body)
	}

	const artifactURL = "/v1/load_artifact?id=engineering/runbook"

	// alice's token deliberately carries NO groups claim. Visibility is granted
	// only because SCIM membership resolves engineering -> [alice@acme.com],
	// which the evaluator matches against the verified sub. This isolates the
	// SCIM directory path from the direct JWT-group-claim path.
	aliceClaims := injClaims("alice@acme.com")
	if _, present := aliceClaims["groups"]; present {
		t.Fatal("injClaims unexpectedly carries a groups claim")
	}
	aliceToken := injSignJWT(t, priv, aliceClaims)
	if status, b := injGet(t, srv.BaseURL+artifactURL, aliceToken); status != http.StatusOK {
		t.Fatalf("SCIM-member alice load = %d, want 200 (SCIM membership should grant visibility)\nbody: %s\nlog:\n%s", status, b, srv.log())
	}

	// bob is a provisioned user but not in the group; the layer is invisible.
	bobToken := injSignJWT(t, priv, injClaims("bob@acme.com"))
	if status, _ := injGet(t, srv.BaseURL+artifactURL, bobToken); status != http.StatusNotFound {
		t.Errorf("non-member bob load = %d, want 404 (no leak)", status)
	}

	// Removal case: take alice out of the SCIM group (PUT with empty members).
	// MembersOf then resolves engineering -> [], so alice loses visibility even
	// though her verified identity is unchanged.
	st, body = oidcSCIMDo(t, http.MethodPut, fmt.Sprintf("%s/scim/v2/Groups/%s", srv.BaseURL, groupID),
		scimvisToken, "application/scim+json", oidcSCIMGroupBody(groupName, nil))
	if st != http.StatusOK {
		t.Fatalf("SCIM group membership removal: HTTP %d body=%s", st, body)
	}
	if status, b := injGet(t, srv.BaseURL+artifactURL, aliceToken); status != http.StatusNotFound {
		t.Errorf("removed-member alice load = %d, want 404 (membership revoked)\nbody: %s", status, b)
	}
}

// Spec: §6.3.1 — deleting a SCIM user scrubs the user from every group's
// membership, so the registry directory no longer resolves that user into the
// group and a groups-restricted layer becomes invisible to it. This is the
// deactivation/removal counterpart driven through user lifecycle rather than
// group edits.
func TestAuthSCIMVisibility_UserDeletionRevokesVisibility(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	priv, pemPath := injKeyPair(t)

	const groupName = "engineering"
	srv, _ := scimvisStartServer(t, home, groupName)
	injRegisterRuntime(t, srv, pemPath)

	aliceID := scimvisPushUser(t, srv, "alice@acme.com")
	st, body := oidcSCIMDo(t, http.MethodPost, srv.BaseURL+"/scim/v2/Groups",
		scimvisToken, "application/scim+json", oidcSCIMGroupBody(groupName, []string{aliceID}))
	if st != http.StatusCreated {
		t.Fatalf("SCIM create group: HTTP %d body=%s", st, body)
	}

	const artifactURL = "/v1/load_artifact?id=engineering/runbook"
	aliceToken := injSignJWT(t, priv, injClaims("alice@acme.com"))

	// Baseline: SCIM membership grants visibility.
	if status, b := injGet(t, srv.BaseURL+artifactURL, aliceToken); status != http.StatusOK {
		t.Fatalf("pre-deletion alice load = %d, want 200\nbody: %s\nlog:\n%s", status, b, srv.log())
	}

	// Deprovision alice over SCIM. DeleteUser removes the user and scrubs it
	// from every group, so MembersOf can no longer resolve her into the group.
	st, _ = oidcSCIMDo(t, http.MethodDelete, fmt.Sprintf("%s/scim/v2/Users/%s", srv.BaseURL, aliceID),
		scimvisToken, "", nil)
	if st != http.StatusNoContent {
		t.Fatalf("SCIM delete user: HTTP %d, want 204", st)
	}

	if status, b := injGet(t, srv.BaseURL+artifactURL, aliceToken); status != http.StatusNotFound {
		t.Errorf("post-deletion alice load = %d, want 404 (deprovisioned)\nbody: %s", status, b)
	}
}
