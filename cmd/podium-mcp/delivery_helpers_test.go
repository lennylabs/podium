package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// checkDeliveryHash runs the shared §4.7.10 delivery check under never on the
// record resp frames, so a test asserts the hash recomputation alone.
func checkDeliveryHash(resp loadArtifactResponse) error {
	check := sign.DeliveryCheck{Policy: sign.PolicyNever}
	return check.Verify(context.Background(), deliveryRecordOf(resp), resp.DeliveryHash, resp.DeliverySignature)
}

// epochRevision is the artifact_revision a registry serves for a record with
// no ingest time (§4.7.10). Stubs that do not exercise the §6.5 freshness
// check serve it, so a `latest` answer carries a canonical revision.
var epochRevision = version.FormatArtifactRevision(time.Time{})

// sealDelivery returns resp with the §4.7.10 delivery hash the registry would
// serve for it. The record is composed with version.DeliveryHash over resp as
// the consumer holds it after reconstitution: each inline resource
// contributing the digest of its decoded body, and each large resource
// contributing its link's content hash. A test seals the untampered record and then alters one field,
// so the load fails on the alteration the test describes. An empty
// ArtifactRevision defaults to epochRevision before sealing.
func sealDelivery(resp loadArtifactResponse) loadArtifactResponse {
	resp.ArtifactRevision = revisionOrEpoch(resp.ArtifactRevision)
	resp.DeliveryHash = deliveryHashOf(resp)
	return resp
}

// revisionOrEpoch returns rev, or epochRevision when rev is empty.
func revisionOrEpoch(rev string) string {
	if rev == "" {
		return epochRevision
	}
	return rev
}

// withEpochRevision sets artifact_revision to epochRevision on a stub
// response map that serves none, matching the default deliveryHashOf frames.
func withEpochRevision(resp map[string]any) map[string]any {
	if _, set := resp["artifact_revision"]; !set {
		resp["artifact_revision"] = epochRevision
	}
	return resp
}

// deliveryHashOf composes the delivery hash of resp without modifying it. An
// empty ArtifactRevision frames epochRevision, the value a stub serves by
// default.
func deliveryHashOf(resp loadArtifactResponse) string {
	resp.ArtifactRevision = revisionOrEpoch(resp.ArtifactRevision)
	return servedDeliveryHashOf(resp)
}

// servedDeliveryHashOf composes the delivery hash of resp with its
// ArtifactRevision framed as served, an empty value included, so a test can
// seal a record whose revision a registry would never write.
func servedDeliveryHashOf(resp loadArtifactResponse) string {
	rec := version.DeliveryRecord{
		ID:               resp.ID,
		Version:          resp.Version,
		Type:             resp.Type,
		ContentHash:      resp.ContentHash,
		Sensitivity:      resp.Sensitivity,
		ArtifactRevision: resp.ArtifactRevision,
		Frontmatter:      resp.Frontmatter,
		ManifestBody:     resp.ManifestBody,
		SkillRaw:         resp.SkillRaw,
		Resources:        map[string]string{},
	}
	for path, body := range resp.Resources {
		rec.Resources[path] = sha256Hex([]byte(body))
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

// servedJSON returns the /v1/load_artifact body a registry serves for resp,
// with each override member set to its value, or removed when the value is
// nil. A test seals resp first, so an override alters the served bytes after
// the delivery hash was fixed.
func servedJSON(t *testing.T, resp loadArtifactResponse, overrides map[string]any) string {
	t.Helper()
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for k, v := range overrides {
		if v == nil {
			delete(m, k)
			continue
		}
		m[k] = v
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return string(out)
}

// base64Body returns a team/x body that serves the bin/blob resource as
// encoded under resources_base64 and seals the delivery hash over decoded,
// the bytes the registry framed. links, when non-nil, is served as
// large_resources and framed by each link's content hash.
func base64Body(t *testing.T, decoded []byte, encoded string, links map[string]largeResourceLink) string {
	t.Helper()
	fm := "---\ntype: context\n---\nbody"
	resp := sealDelivery(loadArtifactResponse{
		ID: "team/x", Type: "context", Version: "1.0.0", Frontmatter: fm,
		Resources:      map[string]string{"bin/blob": string(decoded)},
		LargeResources: links,
		ContentHash:    "sha256:" + version.CanonicalContentHash([]byte(fm), nil, map[string][]byte{"bin/blob": decoded}),
	})
	return servedJSON(t, resp, map[string]any{
		"resources":        map[string]string{"bin/blob": encoded},
		"resources_base64": true,
	})
}

// objectStub is a stub object store that serves bodies by request path and
// counts every request it receives, per path.
type objectStub struct {
	ts     *httptest.Server
	bodies map[string][]byte
	mu     sync.Mutex
	hits   map[string]int
}

// newObjectStub starts an object store that serves bodies, keyed by URL path.
func newObjectStub(t *testing.T, bodies map[string][]byte) *objectStub {
	t.Helper()
	o := &objectStub{bodies: bodies, hits: map[string]int{}}
	o.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		o.hits[r.URL.Path]++
		o.mu.Unlock()
		b, ok := o.bodies[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(o.ts.Close)
	return o
}

// requests returns how many requests the stub received for path, or for
// every path when path is empty.
func (o *objectStub) requests(path string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	if path != "" {
		return o.hits[path]
	}
	n := 0
	for _, c := range o.hits {
		n += c
	}
	return n
}

// rawRegistry starts a stub registry that answers /v1/load_artifact with body
// verbatim and returns its URL.
func rawRegistry(t *testing.T, body string) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/load_artifact" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}
