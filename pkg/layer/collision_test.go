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

// Spec: §4.6, §13.11.3 — a collision reports as a drop with the collision
// code and the same withheld reason.
func TestCollision_Drop(t *testing.T) {
	t.Parallel()
	c := Collision{ArtifactID: "finance/close", Layer: "team-finance", ExistingLayer: "org-defaults"}
	want := Drop{ArtifactID: "finance/close", Code: "ingest.collision", Reason: c.Reason()}
	if got := c.Drop(); got != want {
		t.Errorf("Drop() = %+v, want %+v", got, want)
	}
}
