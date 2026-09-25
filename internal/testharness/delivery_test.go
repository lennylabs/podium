package testharness

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/lennylabs/podium/pkg/version"
)

// Spec: n/a — internal harness primitive. SealDelivery frames the served
// fields, decodes base64 inline resources, and takes each large resource's
// digest from its link.
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
	want := version.DeliveryHash(version.DeliveryRecord{
		ID: "team/x", Version: "1.0.0", Type: "context", ContentHash: "sha256:c",
		Sensitivity: "low", Frontmatter: "fm", ManifestBody: "body",
		Resources: map[string]string{"bin": "sha256:" + hex.EncodeToString(sum[:]), "big": "sha256:big"},
	})
	if got["delivery_hash"] != want {
		t.Errorf("delivery_hash = %v, want %s", got["delivery_hash"], want)
	}
}
