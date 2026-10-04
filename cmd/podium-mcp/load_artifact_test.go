package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// loadArtifactJSON builds a /v1/load_artifact response body whose content_hash
// is the §4.7.6 hash of frontmatter plus resources and whose delivery_hash is
// the §4.7.10 hash of the record, so the §6.6 step 2 consumer-side check
// (sign.DeliveryCheck.Verify) accepts it, and whose artifact_revision defaults to
// epochRevision so the §6.5 freshness check admits a `latest` answer. A field
// the caller sets is kept. Compute it
// in the test goroutine and write the returned string from the stub handler.
func loadArtifactJSON(t *testing.T, fields map[string]any) string {
	t.Helper()
	fm, _ := fields["frontmatter"].(string)
	var resources map[string][]byte
	if res, ok := fields["resources"].(map[string]string); ok {
		resources = make(map[string][]byte, len(res))
		for k, v := range res {
			resources[k] = []byte(v)
		}
	}
	if _, set := fields["content_hash"]; !set {
		fields["content_hash"] = "sha256:" + version.CanonicalContentHash([]byte(fm), nil, resources)
	}
	withEpochRevision(fields)
	if _, set := fields["delivery_hash"]; !set {
		b, err := json.Marshal(fields)
		if err != nil {
			t.Fatalf("marshal stub response: %v", err)
		}
		var resp loadArtifactResponse
		if err := json.Unmarshal(b, &resp); err != nil {
			t.Fatalf("decode stub response: %v", err)
		}
		fields["delivery_hash"] = deliveryHashOf(resp)
	}
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal stub response: %v", err)
	}
	return string(b)
}

func newTestServer(t *testing.T, cfg *config) *mcpServer {
	t.Helper()
	cache, err := newContentCache(t.TempDir())
	if err != nil {
		t.Fatalf("newContentCache: %v", err)
	}
	s := &mcpServer{
		cfg:         cfg,
		http:        &http.Client{},
		cache:       cache,
		resolutions: newResolutionCache(t.TempDir()),
		adapters:    adapter.DefaultRegistry(),
	}
	return s
}

// loadArtifact succeeds against a stub registry response, caches the
// resolution, and returns a result map.
func TestLoadArtifact_HappyPath(t *testing.T) {
	t.Parallel()
	respBody := loadArtifactJSON(t, map[string]any{
		"id": "x", "type": "context", "version": "1.0.0",
		"manifest_body": "body", "frontmatter": "---\ntype: context\n---\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/load_artifact" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(respBody))
	}))
	defer srv.Close()
	s := newTestServer(t, &config{
		registry:     srv.URL,
		harness:      "none",
		verifyPolicy: sign.PolicyNever,
	})
	got := s.loadArtifact(map[string]any{"id": "x"})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if m["id"] != "x" {
		t.Errorf("id = %v", m["id"])
	}
}

// always-revalidate mode falls back to cache when the registry is unreachable.
func TestLoadArtifact_AlwaysRevalidate_NetworkFailure(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &config{
		registry:     "http://127.0.0.1:1",
		cacheMode:    "always-revalidate",
		harness:      "none",
		verifyPolicy: sign.PolicyNever,
	})
	got := s.loadArtifact(map[string]any{"id": "x"})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if _, has := m["error"]; !has {
		t.Errorf("expected error, got %v", m)
	}
}

// offline-only with no cache surfaces a cache.offline_miss error.
func TestLoadArtifact_OfflineOnlyCacheMiss(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &config{
		registry:     "http://127.0.0.1:1",
		cacheMode:    "offline-only",
		harness:      "none",
		verifyPolicy: sign.PolicyNever,
	})
	got := s.loadArtifact(map[string]any{"id": "x"})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if errStr, _ := m["error"].(string); !strings.Contains(errStr, "offline") {
		t.Errorf("error = %q", errStr)
	}
}

// Spec: §4.7.10 — a registry response that is not a JSON text fails step 1
// and is refused with materialize.content_hash_mismatch.
func TestLoadArtifact_MalformedResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	s := newTestServer(t, &config{
		registry:     srv.URL,
		harness:      "none",
		verifyPolicy: sign.PolicyNever,
	})
	got := s.loadArtifact(map[string]any{"id": "x"})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if errStr, _ := m["error"].(string); !strings.HasPrefix(errStr, "materialize.content_hash_mismatch") {
		t.Errorf("error = %q, want materialize.content_hash_mismatch", errStr)
	}
}

// argsIDAndVersion extracts id + version from the args map.
func TestArgsIDAndVersion(t *testing.T) {
	t.Parallel()
	id, ver := argsIDAndVersion(map[string]any{"id": "x", "version": "1.0.0"})
	if id != "x" || ver != "1.0.0" {
		t.Errorf("got id=%q ver=%q", id, ver)
	}
	id, ver = argsIDAndVersion(map[string]any{})
	if id != "" || ver != "" {
		t.Errorf("empty args: id=%q ver=%q", id, ver)
	}
	id, ver = argsIDAndVersion(map[string]any{"id": "x"})
	if id != "x" || ver != "" {
		t.Errorf("no version: id=%q ver=%q", id, ver)
	}
}

// callTool dispatches to the proper handler.
func TestCallTool_DispatchToLoadArtifact(t *testing.T) {
	t.Parallel()
	respBody := loadArtifactJSON(t, map[string]any{
		"id": "x", "type": "context", "version": "1.0.0",
		"frontmatter": "---\ntype: context\n---\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(respBody))
	}))
	defer srv.Close()
	s := newTestServer(t, &config{
		registry:     srv.URL,
		harness:      "none",
		verifyPolicy: sign.PolicyNever,
	})
	got := s.callTool([]byte(`{"name":"load_artifact","arguments":{"id":"x"}}`))
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if m["id"] != "x" {
		t.Errorf("id = %v", m["id"])
	}
}

// callTool dispatches to search_artifacts via the registry.
func TestCallTool_DispatchToSearchArtifacts(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"total_matched":0,"results":[]}`))
	}))
	defer srv.Close()
	s := newTestServer(t, &config{
		registry:     srv.URL,
		harness:      "none",
		verifyPolicy: sign.PolicyNever,
	})
	got := s.callTool([]byte(`{"name":"search_artifacts","arguments":{"query":"x"}}`))
	if got == nil {
		t.Errorf("nil result")
	}
}

// callTool dispatches to load_domain via proxyGet.
func TestCallTool_DispatchToLoadDomain(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/load_domain" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"path":"","subdomains":[]}`))
	}))
	defer srv.Close()
	s := newTestServer(t, &config{
		registry:     srv.URL,
		harness:      "none",
		verifyPolicy: sign.PolicyNever,
	})
	got := s.callTool([]byte(`{"name":"load_domain","arguments":{"path":""}}`))
	if got == nil {
		t.Errorf("nil result")
	}
}

// freshLoadFixture is a sealed team/x record with one inline resource and one
// large resource the object stub serves, and a layer member outside the
// record. body returns its served form with overrides applied.
type freshLoadFixture struct {
	obj      *objectStub
	resp     loadArtifactResponse
	linkHash string
}

// newFreshLoadFixture builds the fixture against a fresh object stub.
func newFreshLoadFixture(t *testing.T) *freshLoadFixture {
	t.Helper()
	big := []byte("large resource bytes")
	obj := newObjectStub(t, map[string][]byte{"/big.bin": big, "/extra.bin": []byte("extra")})
	fm := "---\ntype: context\nversion: 1.0.0\n---\nbody\n"
	resp := sealDelivery(loadArtifactResponse{
		ID: "team/x", Type: "context", Version: "1.0.0", Sensitivity: "internal",
		Frontmatter: fm, ManifestBody: "body\n", Layer: "team-layer",
		Resources: map[string]string{"a.txt": "inline bytes"},
		LargeResources: map[string]largeResourceLink{
			"big.bin": {URL: obj.ts.URL + "/big.bin", ContentHash: sha256Hex(big), Size: int64(len(big))},
		},
		ContentHash: "sha256:" + version.CanonicalContentHash([]byte(fm), nil, map[string][]byte{
			"a.txt": []byte("inline bytes"), "big.bin": big,
		}),
	})
	return &freshLoadFixture{obj: obj, resp: resp, linkHash: sha256Hex(big)}
}

// body returns the fixture's served body with overrides applied.
func (f *freshLoadFixture) body(t *testing.T, overrides map[string]any) string {
	t.Helper()
	return servedJSON(t, f.resp, overrides)
}

// prependMember inserts member as the first member of the object body.
func prependMember(body, member string) string {
	return "{" + member + "," + body[1:]
}

// loadFresh serves body from a stub registry and loads team/x through
// loadArtifact, which hands the 2xx body to deliverFreshLoad.
func loadFresh(t *testing.T, body, dest string) any {
	t.Helper()
	s := newTestServer(t, &config{registry: rawRegistry(t, body), harness: "none", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
	return s.loadArtifact(map[string]any{"id": "team/x", "destination": dest})
}

// Spec: §4.7.10 — deliverFreshLoad decodes every 2xx body through
// version.ParseLoadResponse and refuses a body that fails a decoding step with
// materialize.content_hash_mismatch before the object store records a
// request, writing nothing: a path served both inline and as a link,
// non-canonical base64 (invalid characters, an escaped line break, and a
// non-zero pad bit), a leading byte order mark, a mistyped record member, an
// id-less body, and a mistyped layer, link size, or link content_type, which
// the consumer reads beyond the record.
func TestDeliverFreshLoad_RefusesBodiesTheProcedureRefuses(t *testing.T) {
	t.Parallel()
	raw := []byte{0x00, 0x01, 0x02, 0xff}
	cases := map[string]struct {
		body func(f *freshLoadFixture) string
		want string
	}{
		"path both inline and linked": {body: func(f *freshLoadFixture) string {
			return f.body(t, map[string]any{"resources": map[string]string{"a.txt": "inline bytes", "big.bin": "x"}})
		}},
		"base64 with invalid characters": {body: func(f *freshLoadFixture) string {
			return base64Body(t, raw, "not!base64!", f.resp.LargeResources)
		}, want: "base64"},
		"base64 with an escaped line break": {body: func(f *freshLoadFixture) string {
			return base64Body(t, raw, "AAEC\r\n/w==", f.resp.LargeResources)
		}, want: "base64"},
		"base64 with a non-zero pad bit": {body: func(f *freshLoadFixture) string {
			return base64Body(t, raw, "AAEC/x==", f.resp.LargeResources)
		}, want: "base64"},
		"leading byte order mark": {body: func(f *freshLoadFixture) string {
			return "\ufeff" + f.body(t, nil)
		}},
		"numeric sensitivity": {body: func(f *freshLoadFixture) string {
			return f.body(t, map[string]any{"sensitivity": 5})
		}},
		"id-less body": {body: func(f *freshLoadFixture) string {
			return f.body(t, map[string]any{"id": nil})
		}},
		"numeric layer": {body: func(f *freshLoadFixture) string {
			return f.body(t, map[string]any{"layer": 5})
		}, want: "layer"},
		"string link size": {body: func(f *freshLoadFixture) string {
			return strings.Replace(f.body(t, nil), `"size":20`, `"size":"x"`, 1)
		}, want: "size"},
		"numeric manifest_body_url content_type": {body: func(f *freshLoadFixture) string {
			return f.body(t, map[string]any{"manifest_body_url": map[string]any{
				"presigned_url": f.obj.ts.URL + "/doc.md", "content_hash": f.linkHash, "content_type": 5,
			}})
		}, want: "content_type"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFreshLoadFixture(t)
			dest := t.TempDir()
			got := errorMessageText(loadFresh(t, tc.body(f), dest))
			if !strings.HasPrefix(got, "materialize.content_hash_mismatch") || !strings.Contains(got, tc.want) {
				t.Errorf("error = %q, want materialize.content_hash_mismatch naming %q", got, tc.want)
			}
			if name != "id-less body" {
				if n := f.obj.requests(""); n != 0 {
					t.Errorf("object store requests = %d, want 0 before the decoding refusal", n)
				}
			}
			if entries, _ := os.ReadDir(dest); len(entries) != 0 {
				t.Errorf("a refused body wrote to the destination: %v", entries)
			}
		})
	}
}

// Spec: §4.7.10 — members are read by exact name and no struct decode of the
// body runs. A case-variant or earlier repeated member with the wrong type
// has no effect, and a LARGE_RESOURCES member is ignored as unknown: the body
// verifies, materializes only the exact member's paths, and the object store
// sees no request for the variant's path.
func TestDeliverFreshLoad_ReadsMembersByExactName(t *testing.T) {
	t.Parallel()
	cases := map[string]func(f *freshLoadFixture) string{
		"mistyped earlier sensitivity": func(f *freshLoadFixture) string {
			return prependMember(f.body(t, nil), `"sensitivity":5`)
		},
		"Sensitivity beside sensitivity": func(f *freshLoadFixture) string {
			return prependMember(f.body(t, nil), `"Sensitivity":5`)
		},
		"LAYER beside layer": func(f *freshLoadFixture) string {
			return prependMember(f.body(t, nil), `"LAYER":5`)
		},
		"Content_Hash inside a link": func(f *freshLoadFixture) string {
			b := f.body(t, nil)
			return strings.Replace(b, `"content_hash":"`+f.linkHash+`"`, `"Content_Hash":7,"content_hash":"`+f.linkHash+`"`, 1)
		},
		"LARGE_RESOURCES beside large_resources": func(f *freshLoadFixture) string {
			return prependMember(f.body(t, nil), `"LARGE_RESOURCES":{"extra.bin":{"presigned_url":"`+
				f.obj.ts.URL+`/extra.bin","content_hash":"`+sha256Hex([]byte("extra"))+`"}}`)
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFreshLoadFixture(t)
			dest := t.TempDir()
			body := build(f)
			if body == f.body(t, nil) {
				t.Fatalf("the case did not alter the served body")
			}
			wantServed(t, loadFresh(t, body, dest), "body\n")
			for _, p := range []string{"a.txt", "big.bin"} {
				if _, err := os.Stat(filepath.Join(dest, "team/x", p)); err != nil {
					t.Errorf("resource %s not materialized: %v", p, err)
				}
			}
			if _, err := os.Stat(filepath.Join(dest, "team/x", "extra.bin")); err == nil {
				t.Errorf("the variant member's path was materialized")
			}
			if n := f.obj.requests("/extra.bin"); n != 0 {
				t.Errorf("object store requests for the variant path = %d, want 0", n)
			}
		})
	}
}
