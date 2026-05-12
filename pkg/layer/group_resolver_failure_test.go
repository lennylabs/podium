package layer_test

import (
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
)

// Spec: §4.6 / §6.3.1 — when a layer's visibility is gated on a
// `groups:` filter and SCIM is the source of group membership, a
// SCIM outage that causes GroupResolver to return nil must
// fail-closed: callers without an explicit JWT-claim group match
// see the layer as invisible. The alternative (over-permit on
// resolver error) would leak the layer to anyone during an outage.
//
// This test pins fail-closed by configuring a resolver that
// returns nil (the same shape a SCIM-store error path produces in
// internal/serverboot) and asserting the user is denied.
func TestVisibility_GroupResolverFailureFailsClosed(t *testing.T) {
	t.Parallel()
	l := layer.Layer{
		ID:         "team-shared",
		Visibility: layer.Visibility{Groups: []string{"engineering"}},
	}
	// Identity has NO direct group claim. Membership must come from
	// the SCIM resolver, but the resolver returns nil (simulating
	// SCIM unreachable).
	id := layer.Identity{
		Sub:             "alice",
		IsAuthenticated: true,
	}
	failingResolver := func(string) []string { return nil }
	if layer.VisibleWith(l, id, failingResolver) {
		t.Errorf("VisibleWith returned true with a nil-returning resolver — visibility " +
			"over-permits during a SCIM outage. This would leak group-restricted layers " +
			"to anonymous-authenticated users when the resolver errors.")
	}
}

// Spec: §4.6 — when a resolver returns a non-nil but empty member
// list (the group exists but has no members), visibility falls
// through to JWT claims. A user without the JWT-claim group sees
// nothing.
func TestVisibility_GroupResolverEmptyMembership(t *testing.T) {
	t.Parallel()
	l := layer.Layer{
		ID:         "team-shared",
		Visibility: layer.Visibility{Groups: []string{"engineering"}},
	}
	id := layer.Identity{Sub: "alice", IsAuthenticated: true}
	emptyResolver := func(string) []string { return []string{} }
	if layer.VisibleWith(l, id, emptyResolver) {
		t.Errorf("empty membership should not grant visibility")
	}
}

// Spec: §4.6 — JWT-claim groups remain a valid path even when the
// resolver is failing. A user whose JWT carries `engineering`
// retains visibility regardless of resolver state.
func TestVisibility_JWTClaimsBypassResolverFailure(t *testing.T) {
	t.Parallel()
	l := layer.Layer{
		ID:         "team-shared",
		Visibility: layer.Visibility{Groups: []string{"engineering"}},
	}
	id := layer.Identity{
		Sub:             "alice",
		Groups:          []string{"engineering"},
		IsAuthenticated: true,
	}
	failingResolver := func(string) []string { return nil }
	if !layer.VisibleWith(l, id, failingResolver) {
		t.Errorf("JWT-claim match should grant visibility even when resolver fails")
	}
}

// Spec: §4.6 — when the resolver IS available and returns the
// user's sub among the members, visibility is granted. Pins the
// happy path so a regression in the JWT-vs-resolver branch logic
// would show up here.
func TestVisibility_ResolverMatchesBySub(t *testing.T) {
	t.Parallel()
	l := layer.Layer{
		ID:         "team-shared",
		Visibility: layer.Visibility{Groups: []string{"engineering"}},
	}
	id := layer.Identity{Sub: "alice", IsAuthenticated: true}
	resolver := func(g string) []string {
		if g == "engineering" {
			return []string{"alice", "bob"}
		}
		return nil
	}
	if !layer.VisibleWith(l, id, resolver) {
		t.Errorf("expected resolver match by Sub to grant visibility")
	}
}

// Spec: §4.6 — resolver returning email (the §6.3.1 alternative
// membership identifier) matches against id.Email.
func TestVisibility_ResolverMatchesByEmail(t *testing.T) {
	t.Parallel()
	l := layer.Layer{
		ID:         "team-shared",
		Visibility: layer.Visibility{Groups: []string{"engineering"}},
	}
	id := layer.Identity{
		Sub:             "u-1234",
		Email:           "alice@example.com",
		IsAuthenticated: true,
	}
	resolver := func(string) []string { return []string{"alice@example.com"} }
	if !layer.VisibleWith(l, id, resolver) {
		t.Errorf("expected resolver match by Email to grant visibility")
	}
}

// Spec: §4.6 — the resolver IS consulted only for groups listed in
// the layer's `groups:` filter; it must not be asked about
// arbitrary group names that would let it over-permit.
func TestVisibility_ResolverOnlyConsultedForListedGroups(t *testing.T) {
	t.Parallel()
	l := layer.Layer{
		ID:         "team-shared",
		Visibility: layer.Visibility{Groups: []string{"engineering"}},
	}
	id := layer.Identity{Sub: "alice", IsAuthenticated: true}
	calls := []string{}
	resolver := func(g string) []string {
		calls = append(calls, g)
		return nil
	}
	_ = layer.VisibleWith(l, id, resolver)
	if len(calls) != 1 || calls[0] != "engineering" {
		t.Errorf("resolver called with %v, want only [engineering]", calls)
	}
}
