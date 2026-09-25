package server

import "testing"

// Spec: §7.2, §13.4 — the load_artifact validator is the content-hash ETag
// when no extends_pin is served, and folds a served pin in otherwise, so two
// different pins yield two different validators and neither equals the
// content-hash ETag.
func TestLoadArtifactETag_FoldsTheServedExtendsPin(t *testing.T) {
	t.Parallel()
	const h = "sha256:abc"
	if got := loadArtifactETag(h, ""); got != contentHashETag(h) {
		t.Errorf("loadArtifactETag(h, \"\") = %q, want %q", got, contentHashETag(h))
	}
	a, b := loadArtifactETag(h, "acme/parent@1.0.0"), loadArtifactETag(h, "acme/parent@1.1.0")
	if a == b || a == contentHashETag(h) || b == contentHashETag(h) {
		t.Errorf("pinned ETags %q and %q must differ from each other and from %q", a, b, contentHashETag(h))
	}
	if got := loadArtifactETag("", "acme/parent@1.0.0"); got != "" {
		t.Errorf("loadArtifactETag with no content hash = %q, want empty", got)
	}
}
