package layer

// InvalidArtifactCode is the §6.10 code ingest returns for an artifact it
// cannot accept as authored (including the §7.3.1 registry-assigned-version
// rule), and the code both modes report for an ARTIFACT.md the §4.6
// no-inheritance rule refuses.
//
// Spec: §4.6, §7.3.1, §13.11.3
const InvalidArtifactCode = "ingest.invalid_artifact"

// ExtendsRemovedConstraint is the details.constraint value of a §4.6
// no-inheritance rejection.
//
// Spec: §4.6, §13.11.3
const ExtendsRemovedConstraint = "extends_removed"

// ExtendsRemovedReason is the reason both modes give an ARTIFACT.md that
// declares extends:. It names no layer.
//
// Spec: §4.6, §13.11.3
const ExtendsRemovedReason = "extends: is not supported; remove the extends: key and declare every field this artifact needs in its own frontmatter"

// ExtendsRemovedDrop returns the drop a filesystem-source composition
// reports for an ARTIFACT.md the §4.6 no-inheritance rule refuses.
//
// Spec: §4.6, §13.11.3
func ExtendsRemovedDrop(id string) Drop {
	return Drop{ArtifactID: id, Code: InvalidArtifactCode, Reason: ExtendsRemovedReason}
}
