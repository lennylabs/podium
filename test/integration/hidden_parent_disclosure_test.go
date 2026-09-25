package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/version"
)

// The hidden-parent fixture holds its ancestors in layers only alice can see
// and the requested artifacts in a layer alice and bob can see. Every request
// names its identity in the hpUserHeader header, and every request below is
// made as bob.
const hpUserHeader = "X-Test-User"

// hpRow is one fixture row. Its bytes declare the extends: reference its pin
// names, so stored-row admission passes it and the chain walk reaches its
// parent.
type hpRow struct{ layer, id, ver, pin string }

var hpRows = []hpRow{
	// team/leaf extends base/mid extends base/grand; the root layer is
	// deleted before any request, so the walk fails while it stands at the
	// hidden base/mid.
	{"root", "base/grand", "3.1.4", ""},
	{"hidden", "base/mid", "2.7.1", "base/grand@3.1.4"},
	{"team", "team/leaf", "1.0.0", "base/mid@2.7.1"},
	// team/cyc enters a cycle whose repeating member is the ancestor base/x.
	{"hidden", "base/x", "5.0.0", "base/y@6.0.0"},
	{"hidden", "base/y", "6.0.0", "base/x@5.0.0"},
	{"team", "team/cyc", "1.0.0", "base/x@5.0.0"},
	// team/child merges a hidden parent, and team/plain extends nothing.
	{"hidden", "base/parent", "1.0.0", ""},
	{"team", "team/child", "1.0.0", "base/parent@1.0.0"},
	{"team", "team/plain", "1.0.0", ""},
}

// hpWithheld is every ancestor ID and version the fixture's requests must not
// surface to bob.
var hpWithheld = []string{"base/grand", "3.1.4", "base/mid", "2.7.1", "base/x", "5.0.0", "base/y", "6.0.0"}

func hpManifest(r hpRow) []byte {
	extra := ""
	if r.pin != "" {
		extra = "extends: " + r.pin + "\n"
	}
	return []byte("---\ntype: context\nversion: " + r.ver + "\ndescription: " + r.id + " description.\n" +
		"sensitivity: low\ntags: [" + strings.ReplaceAll(r.id, "/", "-") + "]\n" + extra + "---\n\n" + r.id + " body\n")
}

// newHPServer builds the fixture's registry over a memory store behind the
// HTTP server and returns the server.
func newHPServer(t *testing.T) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	rlvLayer(t, st, store.LayerConfig{TenantID: "default", ID: "root", Order: 1, Users: []string{"alice"}})
	rlvLayer(t, st, store.LayerConfig{TenantID: "default", ID: "hidden", Order: 2, Users: []string{"alice"}})
	rlvLayer(t, st, store.LayerConfig{TenantID: "default", ID: "team", Order: 3, Users: []string{"alice", "bob"}})
	for _, r := range hpRows {
		rlvManifest(t, st, store.ManifestRecord{
			TenantID: "default", ArtifactID: r.id, Version: r.ver, Layer: r.layer, Type: "context",
			Frontmatter: hpManifest(r), ExtendsPin: r.pin,
		})
	}
	if err := st.DeleteLayerConfig(ctx, "default", "root"); err != nil {
		t.Fatalf("DeleteLayerConfig: %v", err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	reg := core.New(st, "default", nil).WithAdmission(nil, objectstore.NewMemory(), objectstore.DefaultReadTimeout)
	srv := server.New(reg,
		server.WithDeliverySigner(sign.RegistryManagedKey{PrivateKey: priv, PublicKey: pub}),
		server.WithIdentityResolver(func(r *http.Request) layer.Identity {
			sub := r.Header.Get(hpUserHeader)
			return layer.Identity{Sub: sub, IsAuthenticated: sub != ""}
		}))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// hpDo issues a request as bob and returns the status and the raw body.
func hpDo(t *testing.T, ts *httptest.Server, method, path string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(hpUserHeader, "bob")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp.StatusCode, raw
}

// hpBatchError issues a single-ID batch load and returns the item's error
// envelope and the raw response body.
func hpBatchError(t *testing.T, ts *httptest.Server, id string) (*server.ErrorResponse, []byte) {
	t.Helper()
	req, _ := json.Marshal(map[string]any{"ids": []string{id}})
	status, raw := hpDo(t, ts, http.MethodPost, "/v1/artifacts:batchLoad", req)
	if status != http.StatusOK {
		t.Fatalf("batchLoad %s = %d: %s", id, status, raw)
	}
	var envs []server.BatchLoadEnvelope
	if err := json.Unmarshal(raw, &envs); err != nil || len(envs) != 1 || envs[0].Status != "error" || envs[0].Error == nil {
		t.Fatalf("batchLoad %s: want one error item, got %v %s", id, err, raw)
	}
	return envs[0].Error, raw
}

// hpWantWithheld fails when the raw body carries any withheld ancestor string.
func hpWantWithheld(t *testing.T, raw []byte) {
	t.Helper()
	for _, w := range hpWithheld {
		if bytes.Contains(raw, []byte(w)) {
			t.Errorf("response body names withheld %q: %s", w, raw)
		}
	}
}

// Spec: §4.6 hidden parents (withheld) — a failure to resolve the chain
// reports the artifact the caller requested and not the parent it could not
// reach, on the wire of both load paths.
func TestLoadArtifact_ErrorBodyNamesNoHiddenParent(t *testing.T) {
	t.Parallel()
	ts := newHPServer(t)

	t.Run("single load missing ancestor", func(t *testing.T) {
		status, raw := hpDo(t, ts, http.MethodGet, "/v1/load_artifact?id=team/leaf", nil)
		var e server.ErrorResponse
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatalf("decode %d %s: %v", status, raw, err)
		}
		if status != http.StatusNotFound || e.Code != "registry.not_found" || e.Message != "registry.not_found: team/leaf" {
			t.Errorf("single load = %d %+v, want 404 registry.not_found naming only team/leaf", status, e)
		}
		hpWantWithheld(t, raw)
	})

	t.Run("batch missing ancestor", func(t *testing.T) {
		e, raw := hpBatchError(t, ts, "team/leaf")
		if e.Code != "visibility.denied" || e.Message != "artifact not visible to caller" {
			t.Errorf("batch item error = %+v, want the fixed visibility.denied envelope", e)
		}
		hpWantWithheld(t, raw)
	})

	t.Run("batch cycle", func(t *testing.T) {
		e, raw := hpBatchError(t, ts, "team/cyc")
		if e.Code != "registry.invalid_argument" || e.Message != "registry.invalid_argument: team/cyc" {
			t.Errorf("batch item error = %+v, want registry.invalid_argument naming only team/cyc", e)
		}
		hpWantWithheld(t, raw)
	})
}

// hpServedHash recomputes the §4.7.6 content hash over the bytes a
// load_artifact response serves and returns it with the served content_hash.
// The served frontmatter field is the whole ARTIFACT.md document, the one
// core.CanonicalManifestDoc names, and the fixture bundles no resources.
func hpServedHash(t *testing.T, ts *httptest.Server, id string) (recomputed, served string) {
	t.Helper()
	status, raw := hpDo(t, ts, http.MethodGet, "/v1/load_artifact?id="+id, nil)
	if status != http.StatusOK {
		t.Fatalf("load %s = %d: %s", id, status, raw)
	}
	var resp server.LoadArtifactResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode load %s: %v", id, err)
	}
	return "sha256:" + version.CanonicalContentHash([]byte(resp.Frontmatter), []byte(resp.SkillRaw), nil), resp.ContentHash
}

// Spec: §4.6 hidden parents (observable) — the served content_hash is computed
// over the child's pre-merge package, so the served bytes of a merged child do
// not reproduce it, even for a requester who cannot see the parent. The
// unmerged control shows the recomputation reproduces the hash of an artifact
// that extends nothing, so the mismatch is the merge's signal. This test pins
// the documented limitation; closing it by changing the §4.7.6 hash basis
// fails it.
func TestLoadArtifact_MergedServedBytesDoNotReproduceContentHash(t *testing.T) {
	t.Parallel()
	ts := newHPServer(t)
	if got, served := hpServedHash(t, ts, "team/plain"); got != served {
		t.Fatalf("unmerged control: served bytes hash to %s, content_hash is %s", got, served)
	}
	if got, served := hpServedHash(t, ts, "team/child"); got == served {
		t.Errorf("merged child: served bytes reproduce content_hash %s, want a mismatch", served)
	}
}
