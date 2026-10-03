package testharness

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/lennylabs/podium/pkg/version"
)

// Spec: §4.7.10 — SealDelivery frames the served fields, decodes base64
// inline resources, takes each large resource's digest from its link, and
// writes and frames the epoch revision for a stub that names none.
func TestSealDelivery_FramesTheServedRecord(t *testing.T) {
	t.Parallel()
	blob := []byte{0x00, 0xff}
	sum := sha256.Sum256(blob)
	got := SealDelivery(map[string]any{
		"id": "team/x", "version": "1.0.0", "type": "context", "content_hash": "sha256:c",
		"sensitivity": "low", "frontmatter": "fm", "manifest_body": "body",
		"resources":        map[string]string{"bin": base64.StdEncoding.EncodeToString(blob)},
		"resources_base64": true,
		"large_resources":  map[string]any{"big": map[string]any{"content_hash": "sha256:big"}},
	})
	const epoch = "1970-01-01T00:00:00.000000Z"
	if got["artifact_revision"] != epoch {
		t.Errorf("artifact_revision = %v, want %s", got["artifact_revision"], epoch)
	}
	want := version.DeliveryHash(version.DeliveryRecord{
		ID: "team/x", Version: "1.0.0", Type: "context", ContentHash: "sha256:c",
		Sensitivity: "low", ArtifactRevision: epoch, Frontmatter: "fm", ManifestBody: "body",
		Resources: map[string]string{"bin": "sha256:" + hex.EncodeToString(sum[:]), "big": "sha256:big"},
	})
	if got["delivery_hash"] != want {
		t.Errorf("delivery_hash = %v, want %s", got["delivery_hash"], want)
	}
}

// Spec: §4.7.10 — a stub that names its artifact_revision keeps it, and the
// sealed hash frames that value.
func TestSealDelivery_FramesAnExplicitRevision(t *testing.T) {
	t.Parallel()
	const rev = "2026-01-02T03:04:05.000006Z"
	got := SealDelivery(map[string]any{
		"id": "team/x", "version": "1.0.0", "type": "context", "content_hash": "sha256:c",
		"artifact_revision": rev, "frontmatter": "fm", "manifest_body": "body",
	})
	if got["artifact_revision"] != rev {
		t.Errorf("artifact_revision = %v, want %s", got["artifact_revision"], rev)
	}
	want := version.DeliveryHash(version.DeliveryRecord{
		ID: "team/x", Version: "1.0.0", Type: "context", ContentHash: "sha256:c",
		ArtifactRevision: rev, Frontmatter: "fm", ManifestBody: "body",
		Resources: map[string]string{},
	})
	if got["delivery_hash"] != want {
		t.Errorf("delivery_hash = %v, want %s", got["delivery_hash"], want)
	}
}
