package layer_test

import (
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
)

// Spec: §4.6 + §6.3.1 — when a GroupResolver is supplied,
// VisibleWith expands a layer's `groups:` filter via the resolver
// before falling back to JWT-supplied groups. SCIM-pushed
// memberships become first-class in the visibility evaluator.
// Phase: 7
func TestVisibleWith_GroupResolverExpandsSCIMMembership(t *testing.T) {
	testharness.RequirePhase(t, 7)
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
// Phase: 7
func TestVisibleWith_JWTGroupsTakePrecedence(t *testing.T) {
	testharness.RequirePhase(t, 7)
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
// Phase: 7
func TestVisible_BackCompatNoResolver(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	l := layer.Layer{Visibility: layer.Visibility{Public: true}}
	if !layer.Visible(l, layer.Identity{IsAuthenticated: true}) {
		t.Errorf("public layer should be visible without a resolver")
	}
}

// Spec: §6.3.1 — resolver matches against Identity.Email when
// Sub doesn't match (some IdPs put userName in email vs sub).
// Phase: 7
func TestVisibleWith_ResolverMatchesEmail(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	resolve := func(string) []string { return []string{"alice@example.com"} }
	l := layer.Layer{Visibility: layer.Visibility{Groups: []string{"eng"}}}
	id := layer.Identity{Sub: "auth0|abc123", Email: "alice@example.com", IsAuthenticated: true}
	if !layer.VisibleWith(l, id, resolve) {
		t.Errorf("should match by email when Sub doesn't match")
	}
}
