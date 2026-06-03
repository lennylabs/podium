package server_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// mbLargeContextDoc is an ARTIFACT.md whose bytes exceed the §4.2 inline
// cutoff so the §6.6 manifest-body channel presigns it.
func mbLargeContextDoc() []byte {
	body := strings.Repeat("glossary line\n", objectstore.InlineCutoff/14+200)
	return []byte("---\ntype: context\nversion: 1.0.0\ndescription: Big glossary.\n---\n\n" + body)
}

func mbHashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// manifestBodyServer boots a server.New registry whose store holds rec under
// a public layer. withStore configures the filesystem object store (and wires
// its BaseURL to the test server) so the §6.6 presigned channel engages;
// without it, the standalone-without-storage path keeps bodies inline.
func manifestBodyServer(t *testing.T, rec store.ManifestRecord, withStore bool) *httptest.Server {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(t.Context(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	rec.TenantID = "default"
	rec.Layer = "L"
	if err := st.PutManifest(t.Context(), rec); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	var opts []server.Option
	var objStore *objectstore.Filesystem
	if withStore {
		os, err := objectstore.Open(t.TempDir())
		if err != nil {
			t.Fatalf("objectstore.Open: %v", err)
		}
		objStore = os
		opts = append(opts, server.WithObjectStore(objStore, "placeholder", time.Hour))
	}
	srv := server.New(reg, opts...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	if objStore != nil {
		objStore.BaseURL = ts.URL
	}
	return ts
}

func mbGetLoadArtifact(t *testing.T, ts *httptest.Server, id string) server.LoadArtifactResponse {
	t.Helper()
	resp, err := http.Get(ts.URL + "/v1/load_artifact?id=" + id)
	if err != nil {
		t.Fatalf("GET load_artifact: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var parsed server.LoadArtifactResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	return parsed
}

// Spec: §6.6/§7.2 — a context artifact whose ARTIFACT.md body exceeds the
// inline cutoff is delivered via a presigned manifest_body_url rather than
// inline. The inline manifest_body and frontmatter are cleared so the body
// does not also travel inline, and the URL serves the verbatim ARTIFACT.md.
func TestManifestBody_ContextLargeBodyPresigned(t *testing.T) {
	t.Parallel()
	doc := mbLargeContextDoc()
	ts := manifestBodyServer(t, store.ManifestRecord{
		ArtifactID: "finance/glossary", Version: "1.0.0", ContentHash: "sha256:c",
		Type: "context", Frontmatter: doc, Body: []byte("glossary line\n"),
	}, true)

	parsed := mbGetLoadArtifact(t, ts, "finance/glossary")
	if parsed.ManifestBodyURL == nil {
		t.Fatalf("a body above the cutoff must presign: %+v", parsed)
	}
	if parsed.ManifestBody != "" {
		t.Errorf("manifest_body must be empty when presigned, got %d bytes", len(parsed.ManifestBody))
	}
	if parsed.Frontmatter != "" {
		t.Errorf("frontmatter must be empty when presigned, got %d bytes", len(parsed.Frontmatter))
	}
	if want := "sha256:" + mbHashOf(doc); parsed.ManifestBodyURL.ContentHash != want {
		t.Errorf("content_hash = %q, want %q", parsed.ManifestBodyURL.ContentHash, want)
	}
	if parsed.ManifestBodyURL.Size != int64(len(doc)) {
		t.Errorf("size = %d, want %d", parsed.ManifestBodyURL.Size, len(doc))
	}

	resp, err := http.Get(parsed.ManifestBodyURL.URL)
	if err != nil {
		t.Fatalf("GET presigned body: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("presigned body fetch = HTTP %d", resp.StatusCode)
	}
	if string(got) != string(doc) {
		t.Errorf("presigned body served %d bytes, want %d", len(got), len(doc))
	}
	// Keying invariant: object key == sha256(bytes), surfaced as X-Content-Hash.
	if hdr := resp.Header.Get("X-Content-Hash"); hdr != "sha256:"+mbHashOf(doc) {
		t.Errorf("X-Content-Hash = %q, want %q", hdr, "sha256:"+mbHashOf(doc))
	}
}

// Spec: §6.6/§4.3.4 — for a skill the canonical document is its verbatim
// SKILL.md, so a large SKILL.md presigns while the small ARTIFACT.md
// frontmatter stays inline.
func TestManifestBody_SkillLargeSkillRawPresigned(t *testing.T) {
	t.Parallel()
	front := "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- body in SKILL.md -->\n"
	skill := []byte("---\nname: run\ndescription: Big skill.\n---\n\n" + strings.Repeat("step line\n", objectstore.InlineCutoff/10+200))
	ts := manifestBodyServer(t, store.ManifestRecord{
		ArtifactID: "finance/run", Version: "1.0.0", ContentHash: "sha256:c",
		Type: "skill", Frontmatter: []byte(front), SkillRaw: skill, Body: []byte("step line\n"),
	}, true)

	parsed := mbGetLoadArtifact(t, ts, "finance/run")
	if parsed.ManifestBodyURL == nil {
		t.Fatalf("a SKILL.md above the cutoff must presign: %+v", parsed)
	}
	if parsed.SkillRaw != "" {
		t.Errorf("skill_raw must be empty when presigned, got %d bytes", len(parsed.SkillRaw))
	}
	if parsed.Frontmatter != front {
		t.Errorf("a small ARTIFACT.md frontmatter must stay inline, got %q", parsed.Frontmatter)
	}
	if want := "sha256:" + mbHashOf(skill); parsed.ManifestBodyURL.ContentHash != want {
		t.Errorf("content_hash = %q, want %q", parsed.ManifestBodyURL.ContentHash, want)
	}
	resp, err := http.Get(parsed.ManifestBodyURL.URL)
	if err != nil {
		t.Fatalf("GET presigned body: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != string(skill) {
		t.Errorf("presigned SKILL.md served %d bytes, want %d", len(got), len(skill))
	}
}

// Spec: §6.6 — a body at or below the inline cutoff is returned inline with
// no manifest_body_url.
func TestManifestBody_BelowCutoffInline(t *testing.T) {
	t.Parallel()
	doc := []byte("---\ntype: context\nversion: 1.0.0\ndescription: Small.\n---\n\nshort body\n")
	ts := manifestBodyServer(t, store.ManifestRecord{
		ArtifactID: "finance/small", Version: "1.0.0", ContentHash: "sha256:c",
		Type: "context", Frontmatter: doc, Body: []byte("short body\n"),
	}, true)

	parsed := mbGetLoadArtifact(t, ts, "finance/small")
	if parsed.ManifestBodyURL != nil {
		t.Errorf("a sub-cutoff body must not presign: %+v", parsed.ManifestBodyURL)
	}
	if parsed.Frontmatter != string(doc) {
		t.Errorf("frontmatter should be inline: %q", parsed.Frontmatter)
	}
	if parsed.ManifestBody != "short body\n" {
		t.Errorf("manifest_body should be inline, got %q", parsed.ManifestBody)
	}
}

// Spec: §6.6/§13.11 — without an object store (standalone-without-storage),
// even a large body is returned inline because there is nowhere to presign it.
func TestManifestBody_NoObjectStoreInline(t *testing.T) {
	t.Parallel()
	doc := mbLargeContextDoc()
	ts := manifestBodyServer(t, store.ManifestRecord{
		ArtifactID: "finance/glossary", Version: "1.0.0", ContentHash: "sha256:c",
		Type: "context", Frontmatter: doc, Body: []byte("glossary line\n"),
	}, false)

	parsed := mbGetLoadArtifact(t, ts, "finance/glossary")
	if parsed.ManifestBodyURL != nil {
		t.Errorf("no object store: nothing should presign: %+v", parsed.ManifestBodyURL)
	}
	if parsed.Frontmatter != string(doc) {
		t.Errorf("frontmatter must stay inline without an object store (%d bytes)", len(parsed.Frontmatter))
	}
}
