package e2e

// Proof that the G-INFRA-5 authenticated, visibility-capable server harness
// works: one server declares a layer in every §4.6 visibility mode (public,
// organization, groups, users, and private), and tokens minted for distinct
// identities resolve to the expected view. This is the primitive's own test;
// the visibility, hidden-parent, group-filter, and admin-RBAC journeys build
// on it. Spec: §4.6, §6.3.1, §6.3.2, §4.7.2.

import (
	"net/http"
	"testing"
)

// TestAuthServerHarness_VisibilityMatrix boots one server with five layers,
// one per visibility mode, and asserts each caller's load and search view
// matches the declared visibility. The matrix proves the declarative spec, the
// token minting, the SCIM group resolution, and the admin override all flow
// through the single harness.
func TestAuthServerHarness_VisibilityMatrix(t *testing.T) {
	t.Parallel()

	const scimToken = "harness-proof-scim"
	srv := startAuthServer(t, authServerSpec{
		BootstrapAdmins: []string{"admin@acme.com"},
		SCIMToken:       scimToken,
		// bob is the sole member of the engineering group, resolved via SCIM.
		SCIMUsers: map[string][]string{
			"bob@acme.com":   {"engineering"},
			"alice@acme.com": nil,
		},
		Layers: []authLayer{
			{
				ID:         "pub",
				Files:      map[string]string{"pub/note/ARTIFACT.md": authContext("public note")},
				Visibility: authVisibility{Public: true},
			},
			{
				ID:         "org",
				Files:      map[string]string{"org/handbook/ARTIFACT.md": authContext("org handbook")},
				Visibility: authVisibility{Org: true},
			},
			{
				ID: "eng",
				// A skill-typed artifact exercises authSkillFiles (ARTIFACT.md plus
				// a name-matching SKILL.md) through the harness ingest path.
				Files:      authSkillFiles("eng/runbook", "runbook", "Engineering runbook. Use when operating the service."),
				Visibility: authVisibility{Groups: []string{"engineering"}},
			},
			{
				ID:         "carolonly",
				Files:      map[string]string{"carolonly/secret/ARTIFACT.md": authContext("carol secret")},
				Visibility: authVisibility{Users: []string{"carol@acme.com"}},
			},
			{
				ID:         "vault",
				Files:      map[string]string{"vault/locked/ARTIFACT.md": authContext("locked vault")},
				Visibility: authVisibility{}, // private: no one but an admin override
			},
		},
	})

	// Identities. alice is a provisioned non-member; bob is the engineering
	// group member (via SCIM, not a JWT group claim); carol is named directly
	// in the users: list; admin is a bootstrap admin.
	alice := srv.token(authIdentity{Sub: "alice@acme.com", Email: "alice@acme.com"})
	bob := srv.token(authIdentity{Sub: "bob@acme.com", Email: "bob@acme.com"})
	carol := srv.token(authIdentity{Sub: "carol@acme.com", Email: "carol@acme.com"})
	admin := srv.adminToken("admin@acme.com")

	const (
		pub       = "pub/note"
		org       = "org/handbook"
		eng       = "eng/runbook"
		carolOnly = "carolonly/secret"
		vault     = "vault/locked"
	)

	// matrix rows: for an artifact, which tokens should load it (200) and which
	// should be denied (404). Every token not listed under "see" is asserted
	// denied, so the test fails on a leak as well as on a missing grant.
	all := map[string]string{"alice": alice, "bob": bob, "carol": carol, "admin": admin}
	rows := []struct {
		id  string
		see map[string]bool
	}{
		// Public: visible to every authenticated caller.
		{pub, map[string]bool{"alice": true, "bob": true, "carol": true, "admin": true}},
		// Organization: visible to any authenticated caller.
		{org, map[string]bool{"alice": true, "bob": true, "carol": true, "admin": true}},
		// Group engineering: bob via SCIM membership; nobody else.
		{eng, map[string]bool{"bob": true}},
		// Users list: carol only.
		{carolOnly, map[string]bool{"carol": true}},
		// Private: no normal caller, including the admin's own view.
		{vault, map[string]bool{}},
	}

	for _, row := range rows {
		for name, token := range all {
			got := srv.loadStatus(row.id, token)
			want := http.StatusNotFound
			if row.see[name] {
				want = http.StatusOK
			}
			if got != want {
				t.Errorf("load %s as %s = %d, want %d\nserver log:\n%s", row.id, name, got, want, srv.log())
			}
		}
	}

	// An unauthenticated caller cannot load even a public artifact: the verifier
	// rejects the missing token before visibility is consulted (§6.3.2).
	if st := srv.loadStatus(pub, ""); st == http.StatusOK {
		t.Errorf("unauthenticated load of public artifact = 200, want a rejection")
	}

	// Search reflects the same per-caller surface. alice (authenticated
	// non-member) sees public and org but not the group, users, or private
	// layers.
	aliceIDs := srv.searchIDs(alice)
	assertHas(t, aliceIDs, pub, "alice search includes public")
	assertHas(t, aliceIDs, org, "alice search includes org")
	assertMissing(t, aliceIDs, eng, "alice search omits the group layer")
	assertMissing(t, aliceIDs, carolOnly, "alice search omits the users layer")
	assertMissing(t, aliceIDs, vault, "alice search omits the private layer")

	// bob additionally sees the engineering layer through SCIM membership.
	bobIDs := srv.searchIDs(bob)
	assertHas(t, bobIDs, eng, "bob search includes the engineering layer")
	assertMissing(t, bobIDs, carolOnly, "bob search omits the users layer")
	assertMissing(t, bobIDs, vault, "bob search omits the private layer")

	// The admin override surfaces the private artifact for the admin and is
	// refused for a non-admin (§4.7.2). The normal admin view above already
	// confirmed the private layer is invisible without the override.
	if st, _ := srv.get("/v1/load_artifact?id="+vault+"&as_admin=1", admin); st != http.StatusOK {
		t.Errorf("admin override load of private artifact = %d, want 200", st)
	}
	if st, _ := srv.get("/v1/load_artifact?id="+vault+"&as_admin=1", alice); st != http.StatusForbidden {
		t.Errorf("non-admin override load = %d, want 403", st)
	}
}

// TestAuthServerHarness_ScopeNarrowsSurface proves a §6.3.1 read scope minted
// through the harness narrows the caller's discovery surface to the granted
// subtree while every layer is otherwise visible. This exercises the Scopes
// field of authIdentity end to end.
func TestAuthServerHarness_ScopeNarrowsSurface(t *testing.T) {
	t.Parallel()
	srv := startAuthServer(t, authServerSpec{
		Layers: []authLayer{{
			ID: "shared",
			Files: map[string]string{
				"finance/ap/pay-invoice/ARTIFACT.md": authContext("pay invoice"),
				"hr/policies/ARTIFACT.md":            authContext("hr policies"),
			},
			Visibility: authVisibility{Public: true},
		}},
	})

	// A read scope for the finance subtree narrows discovery to finance; the hr
	// artifact is filtered even though the layer is public.
	scoped := srv.token(authIdentity{
		Sub:    "alice@acme.com",
		Email:  "alice@acme.com",
		Scopes: []string{"podium:read:finance/*"},
	})
	ids := srv.searchIDs(scoped)
	assertHas(t, ids, "finance/ap/pay-invoice", "scoped search includes finance")
	assertMissing(t, ids, "hr/policies", "scoped search omits hr (out of scope)")

	// An unscoped token for the same identity sees both artifacts.
	unscoped := srv.token(authIdentity{Sub: "alice@acme.com", Email: "alice@acme.com"})
	all := srv.searchIDs(unscoped)
	assertHas(t, all, "finance/ap/pay-invoice", "unscoped search includes finance")
	assertHas(t, all, "hr/policies", "unscoped search includes hr")
}

func assertHas(t *testing.T, ids []string, want, msg string) {
	t.Helper()
	for _, id := range ids {
		if id == want {
			return
		}
	}
	t.Errorf("%s: %q not in %v", msg, want, ids)
}

func assertMissing(t *testing.T, ids []string, unwanted, msg string) {
	t.Helper()
	for _, id := range ids {
		if id == unwanted {
			t.Errorf("%s: %q leaked into %v", msg, unwanted, ids)
			return
		}
	}
}
