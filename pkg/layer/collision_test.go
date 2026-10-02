package layer

import (
	"strings"
	"testing"
)

// Spec: §4.6 — the collision reason names the artifact and the extends:
// remedy and never the layer that already contributes the ID.
func TestCollision_Reason(t *testing.T) {
	t.Parallel()
	c := Collision{ArtifactID: "finance/close", Layer: "team-finance", ExistingLayer: "org-defaults"}
	got := c.Reason()
	want := `cross-layer collision: "finance/close" is already contributed by another layer; declare extends: finance/close to overlay it`
	if got != want {
		t.Errorf("Reason() = %q, want %q", got, want)
	}
	for _, sub := range []string{c.ArtifactID, "declare extends: " + c.ArtifactID} {
		if !strings.Contains(got, sub) {
			t.Errorf("Reason() = %q, want it to contain %q", got, sub)
		}
	}
	// The existing layer can be one the caller is not entitled to see, so
	// neither layer value reaches the reason text.
	for _, layerID := range []string{c.Layer, c.ExistingLayer} {
		if strings.Contains(got, layerID) {
			t.Errorf("Reason() = %q, must not name layer %q", got, layerID)
		}
	}
	if CollisionCode != "ingest.collision" {
		t.Errorf("CollisionCode = %q, want ingest.collision", CollisionCode)
	}
}

// Spec: §4.6 — only an extends: reference that names the colliding ID,
// pinned or bare, sanctions the overlay.
func TestExtendsOverlays(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, ref, id string
		want          bool
	}{
		{"empty reference", "", "a/b", false},
		{"empty reference and empty id", "", "", false},
		{"bare same id", "a/b", "a/b", true},
		{"semver pin", "a/b@1.0.0", "a/b", true},
		{"minor pin", "a/b@1.2.x", "a/b", true},
		{"content-hash pin", "a/b@sha256:abc", "a/b", true},
		{"other id", "a/c", "a/b", false},
		{"other id pinned", "a/c@1.0.0", "a/b", false},
	}
	for _, c := range cases {
		if got := ExtendsOverlays(c.ref, c.id); got != c.want {
			t.Errorf("%s: ExtendsOverlays(%q, %q) = %v, want %v", c.name, c.ref, c.id, got, c.want)
		}
	}
}
