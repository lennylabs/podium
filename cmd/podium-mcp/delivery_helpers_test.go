package main

import (
	"encoding/base64"
	"testing"

	"github.com/lennylabs/podium/pkg/version"
)

// sealDelivery returns resp with the §4.7.10 delivery hash the registry would
// serve for it. The record is composed with version.DeliveryHash over resp as
// the consumer holds it after reconstitution: inline resources decoded when
// resources_base64 is set, and each large resource contributing its link's
// content hash. A test seals the untampered record and then alters one field,
// so the load fails on the alteration the test describes.
func sealDelivery(resp loadArtifactResponse) loadArtifactResponse {
	resp.DeliveryHash = deliveryHashOf(resp)
	return resp
}

// deliveryHashOf composes the delivery hash of resp without modifying it.
func deliveryHashOf(resp loadArtifactResponse) string {
	rec := version.DeliveryRecord{
		ID:           resp.ID,
		Version:      resp.Version,
		Type:         resp.Type,
		ContentHash:  resp.ContentHash,
		Sensitivity:  resp.Sensitivity,
		Frontmatter:  resp.Frontmatter,
		ManifestBody: resp.ManifestBody,
		SkillRaw:     resp.SkillRaw,
		Resources:    map[string]string{},
	}
	for path, body := range resp.Resources {
		raw := []byte(body)
		if resp.ResourcesB64 {
			if dec, err := base64.StdEncoding.DecodeString(body); err == nil {
				raw = dec
			}
		}
		rec.Resources[path] = sha256Hex(raw)
	}
	for path, link := range resp.LargeResources {
		rec.Resources[path] = link.ContentHash
	}
	return version.DeliveryHash(rec)
}

// primeCachedRecord writes the sealed context record cachedRecord builds for
// id into cache through cacheVerifiedRecord, the writer a verified live load
// uses, so a later cache-served load finds its per-ID delivery files. It
// returns the record.
func primeCachedRecord(t *testing.T, cache *contentCache, id, frontmatter, body string) loadArtifactResponse {
	t.Helper()
	rec := cachedRecord(id, frontmatter, body, nil)
	if err := (&mcpServer{cache: cache}).cacheVerifiedRecord(rec); err != nil {
		t.Fatalf("cacheVerifiedRecord: %v", err)
	}
	return rec
}
