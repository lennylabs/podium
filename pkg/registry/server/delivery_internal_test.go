package server

import (
	"testing"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/version"
)

// Spec: §7.2, §13.4 — the load_artifact validator is the bare content-hash
// ETag, and it is empty for an empty content hash so the response carries no
// ETag header.
func TestValidatorFor_IsTheContentHashETag(t *testing.T) {
	t.Parallel()
	const h = "sha256:abc"
	if got := validatorFor(&core.LoadArtifactResult{ContentHash: h}); got != contentHashETag(h) || got == "" {
		t.Errorf("validatorFor = %q, want %q", got, contentHashETag(h))
	}
	if got := validatorFor(&core.LoadArtifactResult{}); got != "" {
		t.Errorf("validatorFor with no content hash = %q, want empty", got)
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
