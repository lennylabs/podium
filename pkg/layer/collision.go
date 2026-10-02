package layer

import (
	"fmt"

	"github.com/lennylabs/podium/pkg/version"
)

// CollisionCode is the §7.3.1 error code for a cross-layer same-ID collision
// that no extends: declaration sanctions. Ingest returns it on the rejected
// artifact, and a filesystem-source composition reports it for the artifact
// it drops, so both paths name the failure identically.
//
// Spec: §4.6
const CollisionCode = "ingest.collision"

// Collision describes one unsanctioned cross-layer collision on a canonical
// artifact ID.
//
// Spec: §4.6
type Collision struct {
	// ArtifactID is the canonical ID two registry-side layers contribute.
	ArtifactID string
}

// Reason returns the human-readable rejection text. It names the artifact
// and the extends: remedy and omits the layer that already contributes the
// ID, because that layer can be one the caller is not entitled to see.
//
// Spec: §4.6
func (c Collision) Reason() string {
	return fmt.Sprintf("cross-layer collision: %q is already contributed by another layer; declare extends: %s to overlay it",
		c.ArtifactID, c.ArtifactID)
}

// ExtendsOverlays reports whether an extends: reference sanctions a same-ID
// overlay of id: the reference, with any §4.7.6 pin stripped, names id
// itself. An empty reference sanctions nothing.
//
// Spec: §4.6
func ExtendsOverlays(extendsRef, id string) bool {
	return extendsRef != "" && version.StripPin(extendsRef) == id
}
