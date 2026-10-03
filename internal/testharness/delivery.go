package testharness

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/lennylabs/podium/pkg/version"
)

// SealDelivery sets delivery_hash on a stub load_artifact response to the
// §4.7.10 digest of the record it serves, and returns fields. The record is
// read from the response as a consumer reconstitutes it: the id, version,
// type, content_hash, sensitivity, artifact_revision, frontmatter,
// manifest_body, and skill_raw fields; each inline resource's digest over its bytes (decoded when
// resources_base64 is set); and each large resource's link content_hash. A
// stub seals the untampered response and then alters the one field its test
// describes. A response that delivers its manifest document by
// manifest_body_url is sealed before the document moves to the link.
//
// A stub that names no artifact_revision is served the epoch, the value the
// registry serves for a version with no recorded ingest time, so every sealed
// stub frames the revision it serves.
func SealDelivery(fields map[string]any) map[string]any {
	if _, ok := fields["artifact_revision"]; !ok {
		fields["artifact_revision"] = version.FormatArtifactRevision(time.Time{})
	}
	fields["delivery_hash"] = version.DeliveryHash(deliveryRecordOf(fields))
	return fields
}

// deliveryRecordOf reads the delivery record from a load_artifact response
// map. It round-trips the map through JSON so a caller may build it from any
// JSON-compatible values.
func deliveryRecordOf(fields map[string]any) version.DeliveryRecord {
	var resp struct {
		ID             string            `json:"id"`
		Version        string            `json:"version"`
		Type           string            `json:"type"`
		ContentHash    string            `json:"content_hash"`
		Sensitivity    string            `json:"sensitivity"`
		Revision       string            `json:"artifact_revision"`
		Frontmatter    string            `json:"frontmatter"`
		ManifestBody   string            `json:"manifest_body"`
		SkillRaw       string            `json:"skill_raw"`
		Resources      map[string]string `json:"resources"`
		ResourcesB64   bool              `json:"resources_base64"`
		LargeResources map[string]struct {
			ContentHash string `json:"content_hash"`
		} `json:"large_resources"`
	}
	b, _ := json.Marshal(fields)
	_ = json.Unmarshal(b, &resp)
	rec := version.DeliveryRecord{
		ID: resp.ID, Version: resp.Version, Type: resp.Type,
		ContentHash: resp.ContentHash, Sensitivity: resp.Sensitivity, ArtifactRevision: resp.Revision,
		Frontmatter: resp.Frontmatter, ManifestBody: resp.ManifestBody, SkillRaw: resp.SkillRaw,
		Resources: map[string]string{},
	}
	for path, body := range resp.Resources {
		raw := []byte(body)
		if dec, err := base64.StdEncoding.DecodeString(body); err == nil && resp.ResourcesB64 {
			raw = dec
		}
		sum := sha256.Sum256(raw)
		rec.Resources[path] = "sha256:" + hex.EncodeToString(sum[:])
	}
	for path, link := range resp.LargeResources {
		rec.Resources[path] = link.ContentHash
	}
	return rec
}
