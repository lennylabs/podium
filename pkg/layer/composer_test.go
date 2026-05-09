package layer

import (
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/manifest"
)

// Spec: §4.6 Visibility — public layer is visible to everyone, including
// unauthenticated callers.
// Phase: 7
// Matrix: §4.6 (public)
func TestVisible_PublicLayerEveryone(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	layer := Layer{ID: "x", Visibility: Visibility{Public: true}}
	if !Visible(layer, Identity{}) {
		t.Errorf("unauthenticated should see public layer")
	}
	if !Visible(layer, Identity{Sub: "joan", IsAuthenticated: true}) {
		t.Errorf("authenticated should see public layer")
	}
}

// Spec: §4.6 — organization: true requires an authenticated org member.
// Phase: 7
// Matrix: §4.6 (organization)
func TestVisible_OrgRequiresAuth(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	layer := Layer{ID: "x", Visibility: Visibility{Organization: true}}
	if Visible(layer, Identity{}) {
		t.Errorf("unauthenticated should not see org layer")
	}
	if !Visible(layer, Identity{Sub: "joan", IsAuthenticated: true}) {
		t.Errorf("authenticated should see org layer")
	}
}

// Spec: §4.6 — groups: matches OIDC group claims.
// Phase: 7
// Matrix: §4.6 (groups)
func TestVisible_GroupsMatch(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	layer := Layer{ID: "x", Visibility: Visibility{Groups: []string{"finance"}}}
	if !Visible(layer, Identity{Sub: "joan", IsAuthenticated: true, Groups: []string{"finance", "ops"}}) {
		t.Errorf("group match should grant visibility")
	}
	if Visible(layer, Identity{Sub: "joan", IsAuthenticated: true, Groups: []string{"sales"}}) {
		t.Errorf("non-matching group should not grant visibility")
	}
}

// Spec: §4.6 — multiple visibility fields combine as a union.
// Phase: 7
// Matrix: §4.6 (groups_users)
func TestVisible_FieldUnion(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	layer := Layer{ID: "x", Visibility: Visibility{
		Groups: []string{"finance"},
		Users:  []string{"explicit-user"},
	}}
	if !Visible(layer, Identity{Sub: "explicit-user", IsAuthenticated: true}) {
		t.Errorf("user match should grant visibility")
	}
	if !Visible(layer, Identity{Sub: "other", IsAuthenticated: true, Groups: []string{"finance"}}) {
		t.Errorf("group match should grant visibility")
	}
}

// Spec: §13.10 — public-mode bypasses the visibility evaluator.
// Phase: 7
func TestVisible_PublicModeBypass(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	layer := Layer{ID: "x", Visibility: Visibility{Users: []string{"someone-else"}}}
	if !Visible(layer, Identity{IsPublic: true}) {
		t.Errorf("public-mode should bypass visibility")
	}
}

// Spec: §4.6 Visibility — every subset of {public, organization,
// groups, users} composes as a union: a caller sees the layer if any
// component matches, and never if none match.
// Phase: 7
// Matrix: §4.6 (users)
// Matrix: §4.6 (public_organization)
// Matrix: §4.6 (public_groups)
// Matrix: §4.6 (public_users)
// Matrix: §4.6 (organization_groups)
// Matrix: §4.6 (organization_users)
// Matrix: §4.6 (public_organization_groups)
// Matrix: §4.6 (public_organization_users)
// Matrix: §4.6 (public_groups_users)
// Matrix: §4.6 (organization_groups_users)
// Matrix: §4.6 (public_organization_groups_users)
func TestVisible_AllUnions(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()

	// Identities: matchPublic is the only one that should see a
	// public-only layer; matchOrg sees org-true layers; matchGroup
	// sees layers listing "finance" in groups; matchUser sees layers
	// listing "explicit-user" in users; nobody matches none.
	matchOrg := Identity{Sub: "org-member", IsAuthenticated: true}
	matchGroup := Identity{Sub: "g-only", IsAuthenticated: true, Groups: []string{"finance"}}
	matchUser := Identity{Sub: "explicit-user", IsAuthenticated: true}
	noMatch := Identity{Sub: "outsider", IsAuthenticated: true}
	unauth := Identity{}

	type subset struct {
		name string
		vis  Visibility
	}
	subsets := []subset{
		{"users", Visibility{Users: []string{"explicit-user"}}},
		{"public_organization", Visibility{Public: true, Organization: true}},
		{"public_groups", Visibility{Public: true, Groups: []string{"finance"}}},
		{"public_users", Visibility{Public: true, Users: []string{"explicit-user"}}},
		{"organization_groups", Visibility{Organization: true, Groups: []string{"finance"}}},
		{"organization_users", Visibility{Organization: true, Users: []string{"explicit-user"}}},
		{"public_organization_groups", Visibility{Public: true, Organization: true, Groups: []string{"finance"}}},
		{"public_organization_users", Visibility{Public: true, Organization: true, Users: []string{"explicit-user"}}},
		{"public_groups_users", Visibility{Public: true, Groups: []string{"finance"}, Users: []string{"explicit-user"}}},
		{"organization_groups_users", Visibility{Organization: true, Groups: []string{"finance"}, Users: []string{"explicit-user"}}},
		{"public_organization_groups_users", Visibility{Public: true, Organization: true, Groups: []string{"finance"}, Users: []string{"explicit-user"}}},
	}

	for _, s := range subsets {
		layer := Layer{ID: s.name, Visibility: s.vis}

		// users-component matches matchUser when present.
		hasUsers := len(s.vis.Users) > 0
		if hasUsers && !Visible(layer, matchUser) {
			t.Errorf("%s: users component should match explicit-user", s.name)
		}

		// groups-component matches matchGroup when present.
		hasGroups := len(s.vis.Groups) > 0
		if hasGroups && !Visible(layer, matchGroup) {
			t.Errorf("%s: groups component should match finance", s.name)
		}

		// organization matches authenticated callers.
		if s.vis.Organization && !Visible(layer, matchOrg) {
			t.Errorf("%s: organization should match authenticated", s.name)
		}

		// public matches everyone.
		if s.vis.Public {
			if !Visible(layer, unauth) {
				t.Errorf("%s: public should grant unauthenticated", s.name)
			}
			if !Visible(layer, noMatch) {
				t.Errorf("%s: public should grant non-matching authenticated", s.name)
			}
		}

		// Unions where neither org nor public is set must reject
		// non-matching authenticated users.
		if !s.vis.Public && !s.vis.Organization {
			if Visible(layer, noMatch) {
				t.Errorf("%s: outsider should not be visible", s.name)
			}
		}
	}
}

// Spec: §4.6 — EffectiveLayers returns the subset visible to identity
// in precedence order.
// Phase: 7
func TestEffectiveLayers_FiltersAndOrders(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	layers := []Layer{
		{ID: "high", Visibility: Visibility{Public: true}, Precedence: 30},
		{ID: "mid", Visibility: Visibility{Users: []string{"other"}}, Precedence: 20},
		{ID: "low", Visibility: Visibility{Public: true}, Precedence: 10},
	}
	got := EffectiveLayers(layers, Identity{Sub: "joan", IsAuthenticated: true})
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0].ID != "low" || got[1].ID != "high" {
		t.Errorf("got %+v, want [low, high] in order", got)
	}
}

// Spec: §4.6 — Compose under highest-wins keeps the highest-precedence
// candidate per canonical ID.
// Phase: 7
func TestCompose_HighestPrecedenceWins(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	layers := []Layer{
		{ID: "low", Precedence: 10},
		{ID: "high", Precedence: 30},
	}
	candidates := map[string][]Candidate{
		"x": {
			{LayerID: "low", Artifact: &manifest.Artifact{Description: "low"}},
			{LayerID: "high", Artifact: &manifest.Artifact{Description: "high"}},
		},
	}
	out := Compose(layers, candidates)
	if len(out) != 1 || out[0].LayerID != "high" {
		t.Errorf("got %+v, want winner=high", out)
	}
}

// Spec: §4.6 merge semantics — sensitivity is most-restrictive-wins
// (high > medium > low).
// Phase: 7
func TestMostRestrictiveSensitivity(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	candidates := []Candidate{
		{Artifact: &manifest.Artifact{Sensitivity: manifest.SensitivityLow}},
		{Artifact: &manifest.Artifact{Sensitivity: manifest.SensitivityHigh}},
		{Artifact: &manifest.Artifact{Sensitivity: manifest.SensitivityMedium}},
	}
	if got := MostRestrictiveSensitivity(candidates); got != manifest.SensitivityHigh {
		t.Errorf("got %s, want high", got)
	}
}

// Spec: §4.6 merge semantics — tags append-unique across layers.
// Phase: 7
func TestAppendUniqueTags(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	candidates := []Candidate{
		{Artifact: &manifest.Artifact{Tags: []string{"a", "b"}}},
		{Artifact: &manifest.Artifact{Tags: []string{"b", "c"}}},
	}
	got := AppendUniqueTags(candidates)
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %q want %q", i, got[i], want[i])
		}
	}
}

// Spec: §4.7.6 — SplitArtifactRef parses id@version pinning syntax.
// Phase: 7
func TestSplitArtifactRef(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	id, ver := SplitArtifactRef("finance/ap/pay-invoice@1.2.0")
	if id != "finance/ap/pay-invoice" || ver != "1.2.0" {
		t.Errorf("got %q@%q", id, ver)
	}
	id, ver = SplitArtifactRef("finance/ap/pay-invoice")
	if id != "finance/ap/pay-invoice" || ver != "" {
		t.Errorf("got %q@%q", id, ver)
	}
}
