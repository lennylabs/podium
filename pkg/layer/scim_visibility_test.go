package layer_test

import (
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
)

// Spec: §4.6 + §6.3.1 — when a GroupResolver is supplied,
// VisibleWith expands a layer's `groups:` filter via the resolver
// before falling back to JWT-supplied groups. SCIM-pushed
// memberships become first-class in the visibility evaluator.
func TestVisibleWith_GroupResolverExpandsSCIMMembership(t *testing.T) {
	t.Parallel()
	groups := map[string][]string{
		"engineering": {"alice@x", "bob@x"},
	}
	resolve := func(group string) []string { return groups[group] }
	l := layer.Layer{
		ID:         "engineering-internal",
		Visibility: layer.Visibility{Groups: []string{"engineering"}},
	}
	id := layer.Identity{Sub: "alice@x", IsAuthenticated: true}
	if !layer.VisibleWith(l, id, resolve) {
		t.Errorf("alice@x should be visible via SCIM-resolved engineering group")
	}
	other := layer.Identity{Sub: "carol@x", IsAuthenticated: true}
	if layer.VisibleWith(l, other, resolve) {
		t.Errorf("carol@x should NOT be visible (not in SCIM group)")
	}
}

// Spec: §4.6 + §6.3.1 — JWT-supplied groups still work. The
// resolver only fires when the JWT path doesn't already match.
func TestVisibleWith_JWTGroupsTakePrecedence(t *testing.T) {
	t.Parallel()
	calls := 0
	resolve := func(string) []string { calls++; return nil }
	l := layer.Layer{
		Visibility: layer.Visibility{Groups: []string{"engineering"}},
	}
	id := layer.Identity{Sub: "x", Groups: []string{"engineering"}, IsAuthenticated: true}
	if !layer.VisibleWith(l, id, resolve) {
		t.Errorf("JWT-supplied group should match without resolver")
	}
	if calls != 0 {
		t.Errorf("resolver was called %d times; should not fire when JWT carries the group", calls)
	}
}

// Spec: §4.6 — Visible (no resolver) still works as before.
func TestVisible_BackCompatNoResolver(t *testing.T) {
	t.Parallel()
	l := layer.Layer{Visibility: layer.Visibility{Public: true}}
	if !layer.Visible(l, layer.Identity{IsAuthenticated: true}) {
		t.Errorf("public layer should be visible without a resolver")
	}
}

// Spec: §6.3.1 — resolver matches against Identity.Email when
// Sub doesn't match (some IdPs put userName in email vs sub).
func TestVisibleWith_ResolverMatchesEmail(t *testing.T) {
	t.Parallel()
	resolve := func(string) []string { return []string{"alice@example.com"} }
	l := layer.Layer{Visibility: layer.Visibility{Groups: []string{"eng"}}}
	id := layer.Identity{Sub: "auth0|abc123", Email: "alice@example.com", IsAuthenticated: true}
	if !layer.VisibleWith(l, id, resolve) {
		t.Errorf("should match by email when Sub doesn't match")
	}
}

// Spec: §4.6 — the direct `users:` filter lists OIDC subjects, but the
// visibility table's config example uses email-style identifiers
// (users: [alice@acme.com]). A caller whose Sub is opaque and whose Email
// matches the listed identifier sees the layer, matching the group path.
func TestVisible_UsersMatchesEmail(t *testing.T) {
	t.Parallel()
	l := layer.Layer{Visibility: layer.Visibility{Users: []string{"alice@acme.com"}}}
	id := layer.Identity{Sub: "auth0|abc123", Email: "alice@acme.com", IsAuthenticated: true}
	if !layer.Visible(l, id) {
		t.Errorf("opaque-sub caller with matching email should see a users:[alice@acme.com] layer")
	}
}

// Spec: §4.6 — the direct `users:` filter still matches the OIDC subject.
func TestVisible_UsersMatchesSub(t *testing.T) {
	t.Parallel()
	l := layer.Layer{Visibility: layer.Visibility{Users: []string{"auth0|abc123"}}}
	id := layer.Identity{Sub: "auth0|abc123", Email: "alice@acme.com", IsAuthenticated: true}
	if !layer.Visible(l, id) {
		t.Errorf("subject match should grant visibility")
	}
}

// Spec: §4.6 — a non-matching caller is denied. An empty Email must not
// match a layer whose `users:` list happens to carry an empty entry's
// neighbour; only an exact Sub or non-empty Email match grants access.
func TestVisible_UsersNoMatchDenied(t *testing.T) {
	t.Parallel()
	l := layer.Layer{Visibility: layer.Visibility{Users: []string{"alice@acme.com"}}}
	// Opaque sub, different email: denied.
	if layer.Visible(l, layer.Identity{Sub: "auth0|zzz", Email: "bob@acme.com", IsAuthenticated: true}) {
		t.Errorf("non-matching caller should be denied")
	}
	// Caller with no email and a non-matching sub: denied, and an empty
	// Email must never match a (hypothetical) empty list entry.
	deny := layer.Layer{Visibility: layer.Visibility{Users: []string{""}}}
	if layer.Visible(deny, layer.Identity{Sub: "auth0|zzz", IsAuthenticated: true}) {
		t.Errorf("empty Email must not match an empty users entry")
	}
}
