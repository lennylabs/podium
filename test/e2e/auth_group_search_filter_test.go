package e2e

// Per-caller visibility filtering of search across a group-restricted layer and
// a public layer.
//
// runtime_layer_visibility_test.go asserts search returns the admin layer plus
// the caller's own user-defined layer, and server_operations_test.go asserts
// the group filter on load_artifact, but no test issues one broad search across
// a groups:-restricted layer and a public layer and asserts the group filter
// applies to the search result set itself. This drives the identical broad
// search_artifacts query for a finance-group member and a non-member and
// asserts the two result sets differ by exactly the finance-layer matches.
//
// The group membership is carried two ways to prove both §4.6 resolution paths
// filter search: the member's JWT carries the finance group claim directly,
// while a second member is resolved into the group registry-side through SCIM
// with no group claim on the token. Both see the finance matches; the
// non-member sees only the public matches. The result-set difference is
// asserted to be exactly the finance set, so the test fails on a leak (a
// finance artifact surfaced to the non-member) and on over-filtering (a public
// artifact withheld from anyone).
//
// Spec: §4.6 (the visibility evaluator filters the discovery surface to the
// caller's effective view; groups: resolves through the JWT group claim and the
// §6.3.1 SCIM directory), §7.6 (search is visibility-filtered server-side).

import (
	"sort"
	"testing"
)

// TestAuthGroupSearchFilter_GroupAndPublicLayers boots a server with a
// groups:finance layer and a public layer both holding query-matching
// artifacts, then runs the identical broad search for a finance member (via a
// JWT group claim), a second finance member (via SCIM membership, no claim),
// and a non-member. The members' result sets exceed the non-member's by exactly
// the finance-layer matches.
func TestAuthGroupSearchFilter_GroupAndPublicLayers(t *testing.T) {
	t.Parallel()

	const scimToken = "group-search-scim"
	// Two finance artifacts and two public artifacts. Empty-query search returns
	// every visible artifact, so the result-set difference is the visibility
	// filter alone rather than a relevance cutoff.
	const (
		finReport = "finance/close/quarterly-report"
		finLedger = "finance/close/general-ledger"
		pubNote   = "shared/notes/release-note"
		pubGuide  = "shared/notes/onboarding-guide"
	)
	srv := startAuthServer(t, authServerSpec{
		SCIMToken: scimToken,
		// carol is resolved into finance through SCIM; alice carries the group on
		// her JWT claim; bob is a provisioned non-member.
		SCIMUsers: map[string][]string{
			"carol@acme.com": {"finance"},
			"bob@acme.com":   nil,
		},
		Layers: []authLayer{
			{
				ID: "finance",
				Files: map[string]string{
					finReport + "/ARTIFACT.md": authContext("quarterly close report for finance"),
					finLedger + "/ARTIFACT.md": authContext("general ledger detail for finance"),
				},
				Visibility: authVisibility{Groups: []string{"finance"}},
			},
			{
				ID: "public",
				Files: map[string]string{
					pubNote + "/ARTIFACT.md":  authContext("release note for everyone"),
					pubGuide + "/ARTIFACT.md": authContext("onboarding guide for everyone"),
				},
				Visibility: authVisibility{Public: true},
			},
		},
	})

	// alice: finance member via a direct JWT group claim.
	alice := srv.token(authIdentity{Sub: "alice@acme.com", Email: "alice@acme.com", Groups: []string{"finance"}})
	// carol: finance member via SCIM membership, NO group claim on the token, so
	// her finance visibility comes only from the registry directory.
	carol := srv.token(authIdentity{Sub: "carol@acme.com", Email: "carol@acme.com"})
	// bob: a verified non-member.
	bob := srv.token(authIdentity{Sub: "bob@acme.com", Email: "bob@acme.com"})

	financeMatches := []string{finLedger, finReport}
	publicMatches := []string{pubGuide, pubNote}

	aliceIDs := srv.searchIDs(alice)
	carolIDs := srv.searchIDs(carol)
	bobIDs := srv.searchIDs(bob)

	// The non-member sees exactly the public matches: every public artifact and
	// no finance artifact. An equality check catches both a leak and
	// over-filtering.
	if got := vdSubsetSorted(bobIDs, finReport, finLedger, pubNote, pubGuide); !equalSorted(got, publicMatches) {
		t.Errorf("non-member bob search = %v, want exactly the public matches %v\nlog:\n%s", got, publicMatches, srv.log())
	}

	// Each member sees the public matches plus the finance matches.
	wantMember := append(append([]string{}, publicMatches...), financeMatches...)
	sort.Strings(wantMember)
	if got := vdSubsetSorted(aliceIDs, finReport, finLedger, pubNote, pubGuide); !equalSorted(got, wantMember) {
		t.Errorf("JWT-claim member alice search = %v, want the public + finance matches %v", got, wantMember)
	}
	if got := vdSubsetSorted(carolIDs, finReport, finLedger, pubNote, pubGuide); !equalSorted(got, wantMember) {
		t.Errorf("SCIM-member carol search = %v, want the public + finance matches %v", got, wantMember)
	}

	// The core assertion: each member's result set exceeds the non-member's by
	// exactly the finance-layer matches. Nothing else differs.
	if diff := setDifference(vdSubsetSorted(aliceIDs, finReport, finLedger, pubNote, pubGuide), bobIDs); !equalSorted(diff, financeMatches) {
		t.Errorf("alice minus bob = %v, want exactly the finance matches %v (the group filter must be the only difference)", diff, financeMatches)
	}
	if diff := setDifference(vdSubsetSorted(carolIDs, finReport, finLedger, pubNote, pubGuide), bobIDs); !equalSorted(diff, financeMatches) {
		t.Errorf("carol minus bob = %v, want exactly the finance matches %v", diff, financeMatches)
	}
}

// vdSubsetSorted returns the sorted subset of ids that are among the named
// wanted ids, so the assertion ignores any incidental seed artifacts the
// harness may surface and compares only the artifacts this test placed.
func vdSubsetSorted(ids []string, wanted ...string) []string {
	want := map[string]bool{}
	for _, w := range wanted {
		want[w] = true
	}
	out := []string{}
	for _, id := range ids {
		if want[id] {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// setDifference returns the sorted elements of a that are not in b.
func setDifference(a, b []string) []string {
	in := map[string]bool{}
	for _, x := range b {
		in[x] = true
	}
	out := []string{}
	for _, x := range a {
		if !in[x] {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

// equalSorted reports whether two already-sorted string slices are equal.
func equalSorted(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
