package e2e

// Token-bound object route refuses a blob after the caller loses layer
// visibility.
//
// data_plane_test.go and http_api_test.go assert the happy-path /objects fetch
// of an above-cutoff resource, and large_resource_data_plane_test.go asserts a
// caller who never had access is refused, but no test fetches the route
// successfully as an authorized member, revokes that member's access at
// runtime, and asserts the same previously-issued object URL is then refused
// while a still-authorized member keeps fetching. The §13.11 contract is that
// the filesystem /objects route re-evaluates visibility on every fetch (the URL
// is bound to the caller's session, not a clock), so a membership change takes
// effect immediately on the next fetch of the same URL.
//
// Two finance-group members back the test. Both resolve into the group through
// SCIM with no group claim on their tokens, so revoking the SCIM membership of
// one removes that member's visibility without touching the other. The member
// fetches the bytes (200), the SCIM group is rebuilt without that member,
// MembersOf then resolves the group without them, and the same object URL is
// refused (the route's ResolveResourceOwner re-check finds no visible owning
// artifact). A still-authorized member keeps fetching the same URL throughout,
// proving the refusal is per-caller and not a server-wide change to the bytes.
//
// The route reports the refusal as 404 registry.not_found rather than 403: the
// §6.9 / §13.11 contract withholds the layer's existence from a caller who can
// no longer see it, the same no-leak convention the single load_artifact path
// uses. The gap's intent (the route refuses the blob after visibility is lost)
// is the refusal; the no-leak status is the product behavior.
//
// Spec: §4.6 (the visibility evaluator; groups: resolves through the §6.3.1
// SCIM directory), §6.9 (visibility denial without leaking the layer's
// existence), §13.10 / §13.11 (the token-bound /objects route re-checks
// visibility per fetch; the URL is bound to the session, not a clock).

import (
	"strings"
	"testing"
)

// TestAuthObjectsRevocation_RefusesAfterMembershipLoss ingests an above-cutoff
// resource into a groups:finance layer, fetches its token-bound /objects URL as
// a SCIM-resolved member (200), revokes that member from the SCIM group, and
// asserts the same URL is then refused while a second still-member keeps
// fetching it.
func TestAuthObjectsRevocation_RefusesAfterMembershipLoss(t *testing.T) {
	t.Parallel()

	const scimToken = "objects-revoke-scim"
	above := vdAbove()
	const id = "finance/close/variance-bundle"
	// alice and bob both start in finance, resolved through SCIM (no group claim
	// on either token). bob is the still-authorized control; alice is revoked.
	srv := startAuthServer(t, authServerSpec{
		SCIMToken: scimToken,
		SCIMUsers: map[string][]string{
			"alice@acme.com": {"finance"},
			"bob@acme.com":   {"finance"},
		},
		Layers: []authLayer{{
			ID: "finance",
			Files: map[string]string{
				id + "/ARTIFACT.md":  authContext("variance bundle restricted to finance"),
				id + "/data/big.bin": above,
			},
			Visibility: authVisibility{Groups: []string{"finance"}},
		}},
	})

	// Tokens carry NO group claim: visibility depends solely on the SCIM
	// directory, so a SCIM revocation actually removes it. A JWT group claim
	// would keep the layer visible past the SCIM change (§4.6 checks the claim
	// directly), which would defeat the revocation under test.
	alice := srv.token(authIdentity{Sub: "alice@acme.com", Email: "alice@acme.com"})
	bob := srv.token(authIdentity{Sub: "bob@acme.com", Email: "bob@acme.com"})

	// Resolve the token-bound object URL as alice while she is still a member.
	objURL := vdObjectURL(t, srv, id, alice, above)
	objPath := strings.TrimPrefix(objURL, srv.BaseURL)

	// Baseline: alice fetches the bytes with her session token.
	if st, body := srv.get(objPath, alice); st != 200 {
		t.Fatalf("member alice /objects fetch = %d, want 200\nbody: %s\nlog:\n%s", st, body, srv.log())
	} else if string(body) != above {
		t.Fatalf("member alice fetched %d object bytes, want %d", len(body), len(above))
	}
	// bob (the control) fetches the same URL successfully too.
	if st, _ := srv.get(objPath, bob); st != 200 {
		t.Fatalf("member bob /objects fetch (baseline) = %d, want 200", st)
	}

	// ---- Revoke alice: rebuild the finance SCIM group with bob only ----------
	groupID := srv.scimGroupID(scimToken, "finance")
	bobID := srv.scimUserID(scimToken, "bob@acme.com")
	srv.scimSetGroupMembers(scimToken, groupID, "finance", []string{bobID})

	// alice can no longer load the owning artifact, so the route's per-fetch
	// re-check (ResolveResourceOwner) finds no visible owner for the content hash
	// and refuses the same URL. The refusal is 404 (no existence leak), never a
	// 200.
	if st, body := srv.get(objPath, alice); st == 200 {
		t.Errorf("revoked alice still fetched the /objects bytes (route did not re-check visibility): %d\n%s", st, body)
	} else if st != 404 {
		t.Errorf("revoked alice /objects fetch = %d, want 404 (refused without leaking existence)", st)
	}
	// Confirm the revocation also closed alice's load_artifact path (the route
	// re-check rides on the same visibility evaluation).
	if st := srv.loadStatus(id, alice); st == 200 {
		t.Errorf("revoked alice still loaded the artifact = 200; SCIM revocation did not remove visibility")
	}

	// ---- The still-authorized member keeps fetching the same URL -------------
	// bob's membership is unchanged, so the same previously-issued URL still
	// serves him the bytes. This proves the refusal is per-caller: the bytes and
	// the URL are intact; only alice's authorization changed.
	if st, body := srv.get(objPath, bob); st != 200 {
		t.Errorf("still-member bob /objects fetch after alice's revocation = %d, want 200\nbody: %s", st, body)
	} else if string(body) != above {
		t.Errorf("still-member bob fetched %d object bytes, want %d", len(body), len(above))
	}
}
