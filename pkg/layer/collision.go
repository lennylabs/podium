package layer

import "fmt"

// CollisionCode is the §7.3.1 error code for a cross-layer same-ID
// collision. Ingest returns it on the rejected
// artifact, and a filesystem-source composition reports it for the artifact
// it drops, so both paths name the failure identically.
//
// Spec: §4.6
const CollisionCode = "ingest.collision"

// Collision describes one cross-layer collision on a canonical
// artifact ID.
//
// Spec: §4.6
type Collision struct {
	// ArtifactID is the canonical ID two registry-side layers contribute.
	ArtifactID string
	// Layer is the layer whose contribution was dropped. Reason omits it.
	Layer string
	// ExistingLayer is the layer whose contribution was kept. Reason omits
	// it, because it can name a layer the caller is not entitled to see.
	ExistingLayer string
}

// Reason returns the human-readable rejection text. It names the artifact
// and the remedy and omits the layer that already contributes the ID,
// because that layer can be one the caller is not entitled to see.
//
// Spec: §4.6, §13.11.3
func (c Collision) Reason() string {
	return fmt.Sprintf("cross-layer collision: %q is already contributed by another layer; rename this artifact or remove the other copy first",
		c.ArtifactID)
}

// Drop is one artifact a filesystem-source composition dropped, with the
// §6.10 code and reason it reports. A collision and a §4.6 no-inheritance
// refusal both report through it, so a caller prints every drop the same way.
//
// Spec: §4.6, §13.11.3
type Drop struct {
	// ArtifactID is the canonical ID of the dropped artifact.
	ArtifactID string
	// Code is the §6.10 error code the drop reports.
	Code string
	// Reason is the human-readable text the drop reports.
	Reason string
}

// Drop returns the drop record for c: its artifact ID, CollisionCode, and
// its Reason.
//
// Spec: §4.6
func (c Collision) Drop() Drop {
	return Drop{ArtifactID: c.ArtifactID, Code: CollisionCode, Reason: c.Reason()}
}
