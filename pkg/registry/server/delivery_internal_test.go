package server

import (
	"testing"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/version"
)

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

// Spec: §4.7.10 — the delivery record frames the load result's artifact
// revision, so the served delivery_hash covers the served artifact_revision.
func TestDeliveryRecordOf_FramesTheArtifactRevision(t *testing.T) {
	t.Parallel()
	res := &core.LoadArtifactResult{ID: "team/x", ArtifactRevision: "2026-01-02T03:04:05.000006Z"}
	if got := deliveryRecordOf(res).ArtifactRevision; got != res.ArtifactRevision {
		t.Errorf("ArtifactRevision = %q, want %q", got, res.ArtifactRevision)
	}
	other := *res
	other.ArtifactRevision = "2026-01-02T03:04:05.000007Z"
	if version.DeliveryHash(deliveryRecordOf(res)) == version.DeliveryHash(deliveryRecordOf(&other)) {
		t.Error("two revisions produced one delivery hash")
	}
}
