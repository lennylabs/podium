package layer

import "testing"

// Spec: §4.6, §13.11.3 — a filesystem-source composition reports an
// ARTIFACT.md that declares extends: as an ingest.invalid_artifact drop
// with the extends_removed constraint and the remove-the-key remedy.
func TestExtendsRemovedDrop(t *testing.T) {
	t.Parallel()
	want := Drop{ArtifactID: "a/b", Code: "ingest.invalid_artifact", Reason: ExtendsRemovedReason}
	if got := ExtendsRemovedDrop("a/b"); got != want {
		t.Errorf("ExtendsRemovedDrop(a/b) = %+v, want %+v", got, want)
	}
	const wantReason = "extends: is not supported; remove the extends: key and declare every field this artifact needs in its own frontmatter"
	if ExtendsRemovedReason != wantReason {
		t.Errorf("ExtendsRemovedReason = %q, want %q", ExtendsRemovedReason, wantReason)
	}
	if ExtendsRemovedConstraint != "extends_removed" {
		t.Errorf("ExtendsRemovedConstraint = %q, want extends_removed", ExtendsRemovedConstraint)
	}
	if InvalidArtifactCode != "ingest.invalid_artifact" {
		t.Errorf("InvalidArtifactCode = %q, want ingest.invalid_artifact", InvalidArtifactCode)
	}
}
