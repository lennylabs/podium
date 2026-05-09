package layer

import (
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/manifest"
)

// Spec: §4.6 Visibility — public layer is visible to everyone, including
// unauthenticated callers.
// Phase: 7
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
