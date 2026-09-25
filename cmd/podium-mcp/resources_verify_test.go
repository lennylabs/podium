package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// mirrorRegistry is a stub registry for the §5.0 mirror's verification
// cases. It serves one load_artifact response built from the server's own
// base URL, so the response can link blobs the same server serves.
type mirrorRegistry struct {
	ts    *httptest.Server
	resp  map[string]any
	blobs map[string][]byte
}

// newMirrorRegistry starts the stub. build receives the server's base URL
// and returns the load_artifact response and the blobs served under
// /blob/<name>.
func newMirrorRegistry(t *testing.T, build func(base string) (map[string]any, map[string][]byte)) *mirrorRegistry {
	t.Helper()
	m := &mirrorRegistry{}
	m.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if name, ok := strings.CutPrefix(r.URL.Path, "/blob/"); ok {
			b, found := m.blobs[name]
			if !found {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(b)
			return
		}
		if r.URL.Path == "/v1/load_artifact" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(m.resp)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(m.ts.Close)
	m.resp, m.blobs = build(m.ts.URL)
	return m
}

// readMirror issues resources/read for id against a bridge configured with
// policy and returns the text and the error message; exactly one is set.
func readMirror(t *testing.T, registry string, policy sign.VerificationPolicy, id string) (text, errMsg string) {
	t.Helper()
	s := &mcpServer{cfg: &config{registry: registry, verifyPolicy: policy, signatureProvider: "registry-managed"}, http: &http.Client{}}
	out := s.handleResourcesRead(json.RawMessage(`{"uri":"podium://artifact/` + id + `"}`))
	if msg := errorMessageText(out); msg != "" {
		if m, ok := out.(map[string]any); ok && m["contents"] != nil {
			t.Errorf("refused read also carried contents: %v", m["contents"])
		}
		return "", msg
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("resources/read returned %T: %v", out, out)
	}
	contents, ok := m["contents"].([]map[string]any)
	if !ok || len(contents) != 1 {
		t.Fatalf("contents = %v", m["contents"])
	}
	text, _ = contents[0]["text"].(string)
	return text, ""
}

// Spec: §4.7.9 — under always, the mirror refuses an artifact the registry
// served with a valid delivery hash and no delivery signature, with the
// missing-signature code and no text.
func TestResources_ReadRefusesAnUnsignedArtifactUnderAlways(t *testing.T) {
	t.Parallel()
	fm := "---\ntype: context\nversion: 1.0.0\n---\n"
	rec := sealDelivery(loadArtifactResponse{
		ID: "docs/x", Type: "context", Frontmatter: fm, ManifestBody: "secret",
		ContentHash: "sha256:" + version.CanonicalContentHash([]byte(fm), nil, nil),
	})
	reg := newMirrorRegistry(t, func(string) (map[string]any, map[string][]byte) {
		return map[string]any{
			"id": rec.ID, "type": rec.Type, "frontmatter": rec.Frontmatter, "manifest_body": rec.ManifestBody,
			"content_hash": rec.ContentHash, "delivery_hash": rec.DeliveryHash,
		}, nil
	})
	text, errMsg := readMirror(t, reg.ts.URL, sign.PolicyAlways, "docs/x")
	if !strings.HasPrefix(errMsg, "materialize.signature_missing") {
		t.Errorf("error = %q (text %q), want materialize.signature_missing", errMsg, text)
	}
}

// Spec: §6.6 — the mirror refuses a served record whose bytes do not
// reproduce its delivery_hash, with the verification's code and no text.
func TestResources_ReadRefusesAFailedVerification(t *testing.T) {
	t.Parallel()
	reg := newMirrorRegistry(t, func(string) (map[string]any, map[string][]byte) {
		return map[string]any{
			"id": "docs/x", "type": "context", "frontmatter": "---\ntype: context\n---\n",
			"manifest_body": "forged", "content_hash": "sha256:" + strings.Repeat("0", 64),
		}, nil
	})
	text, errMsg := readMirror(t, reg.ts.URL, sign.PolicyNever, "docs/x")
	if !strings.HasPrefix(errMsg, "materialize.content_hash_mismatch") {
		t.Errorf("error = %q (text %q), want materialize.content_hash_mismatch", errMsg, text)
	}
}

// Spec: §5.0 — the mirror reconstitutes a manifest body the registry
// delivered through manifest_body_url and returns the fetched body text.
func TestResources_ReadReconstitutesAPresignedBody(t *testing.T) {
	t.Parallel()
	doc := []byte("---\ntype: context\nversion: 1.0.0\n---\nPresigned body text.\n")
	reg := newMirrorRegistry(t, func(base string) (map[string]any, map[string][]byte) {
		return map[string]any{
				"id": "docs/big", "type": "context",
				"content_hash": "sha256:" + version.CanonicalContentHash(doc, nil, nil),
				// The delivery hash frames the document and the body the fetch
				// reconstitutes, not the cleared inline fields.
				"delivery_hash": deliveryHashOf(loadArtifactResponse{
					ID: "docs/big", Type: "context", Frontmatter: string(doc),
					ManifestBody: "Presigned body text.\n",
					ContentHash:  "sha256:" + version.CanonicalContentHash(doc, nil, nil),
				}),
				"manifest_body_url": map[string]any{
					"presigned_url": base + "/blob/body", "content_hash": sha256Hex(doc),
				},
			}, map[string][]byte{
				"body": doc,
			}
	})
	text, errMsg := readMirror(t, reg.ts.URL, sign.PolicyNever, "docs/big")
	if errMsg != "" {
		t.Fatalf("resources/read refused: %s", errMsg)
	}
	if !strings.Contains(text, "Presigned body text.") || !strings.Contains(text, "type: context") {
		t.Errorf("text = %q, want the reconstituted manifest", text)
	}
}

// Spec: §5.0, §6.6 — the mirror decodes inline resources and fetches large
// resources before it recomputes the delivery hash, because the record frames
// every bundled resource. A linked body whose bytes were altered is refused
// with materialize.fetch_failed.
func TestResources_ReadVerifiesOverBundledResources(t *testing.T) {
	t.Parallel()
	fm := []byte("---\ntype: context\nversion: 1.0.0\n---\nbody\n")
	inline := []byte("inline resource bytes")
	large := []byte("large resource bytes")
	hash := "sha256:" + version.CanonicalContentHash(fm, nil, map[string][]byte{
		"inline.txt": inline, "data/big.bin": large,
	})
	delivery := deliveryHashOf(loadArtifactResponse{
		ID: "docs/res", Type: "context", Frontmatter: string(fm), ManifestBody: "body\n",
		ContentHash: hash, Resources: map[string]string{"inline.txt": string(inline)},
		LargeResources: map[string]largeResourceLink{"data/big.bin": {ContentHash: sha256Hex(large)}},
	})
	build := func(served []byte) func(string) (map[string]any, map[string][]byte) {
		return func(base string) (map[string]any, map[string][]byte) {
			return map[string]any{
					"id": "docs/res", "type": "context", "frontmatter": string(fm), "manifest_body": "body\n",
					"content_hash":     hash,
					"delivery_hash":    delivery,
					"resources":        map[string]string{"inline.txt": base64.StdEncoding.EncodeToString(inline)},
					"resources_base64": true,
					"large_resources": map[string]any{
						"data/big.bin": map[string]any{"presigned_url": base + "/blob/big", "content_hash": sha256Hex(large)},
					},
				}, map[string][]byte{
					"big": served,
				}
		}
	}

	reg := newMirrorRegistry(t, build(large))
	text, errMsg := readMirror(t, reg.ts.URL, sign.PolicyNever, "docs/res")
	if errMsg != "" {
		t.Fatalf("resources/read refused a record that verifies over its resources: %s", errMsg)
	}
	if !strings.Contains(text, "type: context") {
		t.Errorf("text = %q, want the manifest", text)
	}

	tampered := newMirrorRegistry(t, build([]byte("altered resource bytes")))
	text, errMsg = readMirror(t, tampered.ts.URL, sign.PolicyNever, "docs/res")
	if !strings.HasPrefix(errMsg, "materialize.fetch_failed") {
		t.Errorf("error = %q (text %q), want materialize.fetch_failed", errMsg, text)
	}
}

// Spec: §5.0, §4.7.10 — the mirror returns the served frontmatter and manifest
// body, and the delivery record covers both. A stub that serves a valid record
// and then changes only the top-level manifest_body is refused with
// materialize.content_hash_mismatch and returns no text.
func TestResources_ReadRefusesAForgedManifestBody(t *testing.T) {
	t.Parallel()
	fm := "---\ntype: context\nversion: 1.0.0\n---\nreal body\n"
	rec := sealDelivery(loadArtifactResponse{
		ID: "docs/x", Type: "context", Version: "1.0.0", Frontmatter: fm, ManifestBody: "real body\n",
		ContentHash: "sha256:" + version.CanonicalContentHash([]byte(fm), nil, nil),
	})
	serve := func(body string) *mirrorRegistry {
		return newMirrorRegistry(t, func(string) (map[string]any, map[string][]byte) {
			return map[string]any{
				"id": rec.ID, "type": rec.Type, "version": rec.Version, "frontmatter": rec.Frontmatter,
				"manifest_body": body, "content_hash": rec.ContentHash, "delivery_hash": rec.DeliveryHash,
			}, nil
		})
	}
	text, errMsg := readMirror(t, serve(rec.ManifestBody).ts.URL, sign.PolicyNever, "docs/x")
	if errMsg != "" || !strings.Contains(text, "real body") {
		t.Fatalf("untampered read = %q / %q, want the served text", text, errMsg)
	}
	text, errMsg = readMirror(t, serve("forged instructions\n").ts.URL, sign.PolicyNever, "docs/x")
	if !strings.HasPrefix(errMsg, "materialize.content_hash_mismatch") || text != "" {
		t.Errorf("forged read = %q / %q, want materialize.content_hash_mismatch and no text", text, errMsg)
	}
}
