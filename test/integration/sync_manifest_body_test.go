package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/registryharness"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/sync"
)

// writeLargeManifestRegistry lays out a two-layer filesystem registry whose
// merged child and skill carry manifest documents above the inline cutoff.
func writeLargeManifestRegistry(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\nlayer_order:\n  - base\n  - top\n"},
		testharness.WriteTreeOption{Path: "base/.layer-config", Content: "visibility:\n  public: true\n"},
		testharness.WriteTreeOption{Path: "top/.layer-config", Content: "visibility:\n  public: true\n"},
		testharness.WriteTreeOption{Path: "base/shared/parent/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: parent\nsensitivity: low\nx_owner: platform\n---\n\nparent prose\n"},
		testharness.WriteTreeOption{Path: "top/team/big/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: big child\nextends: shared/parent@1.x\n---\n\n" + daBigBody},
		testharness.WriteTreeOption{Path: "top/team/bigskill/ARTIFACT.md",
			Content: "---\ntype: skill\nversion: 1.0.0\ndescription: big skill\n---\n"},
		testharness.WriteTreeOption{Path: "top/team/bigskill/SKILL.md",
			Content: "---\nname: bigskill\ndescription: big skill\n---\n\n" + daBigBody},
		testharness.WriteTreeOption{Path: "top/team/small/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: small\n---\n\nsmall\n"},
	)
	return root
}

// largeManifestServer serves root's layers through registryharness.NewLayered,
// which stores an above-cutoff document so it takes manifest_body_url.
func largeManifestServer(t *testing.T, root string) *httptest.Server {
	t.Helper()
	return registryharness.NewLayered(t, root, "base", "top").Server
}

// Spec: §2.2, §6.6 — server-source `podium sync` follows manifest_body_url, so
// an above-cutoff merged ARTIFACT.md and an above-cutoff SKILL.md materialize
// byte-identical to a filesystem-source sync of the same layers.
func TestServerSync_LargeMergedManifestMatchesFilesystemSync(t *testing.T) {
	t.Parallel()
	root := writeLargeManifestRegistry(t)
	ts := largeManifestServer(t, root)
	for _, id := range []string{"team/big", "team/bigskill"} {
		if registryManifestBodyURL(t, ts.URL, id) == nil {
			t.Fatalf("%s was served inline; the fixture must exceed the cutoff", id)
		}
	}

	fsTarget, srvTarget := t.TempDir(), t.TempDir()
	if _, err := sync.Run(sync.Options{RegistryPath: root, Target: fsTarget, AdapterID: "none"}); err != nil {
		t.Fatalf("filesystem sync.Run: %v", err)
	}
	if _, err := sync.Run(sync.Options{RegistryPath: ts.URL, Target: srvTarget, AdapterID: "none"}); err != nil {
		t.Fatalf("server sync.Run: %v", err)
	}
	fsTree, srvTree := materializedTree(t, fsTarget), materializedTree(t, srvTarget)
	for _, path := range []string{"team/big/ARTIFACT.md", "team/bigskill/SKILL.md", "team/bigskill/ARTIFACT.md"} {
		if fsTree[path] == "" || fsTree[path] != srvTree[path] {
			t.Errorf("%s: filesystem %d bytes, server %d bytes; want equal and non-empty", path, len(fsTree[path]), len(srvTree[path]))
		}
	}
	if !strings.Contains(srvTree["team/big/ARTIFACT.md"], "x_owner: platform") {
		t.Error("the server-source ARTIFACT.md is not the merged document")
	}
}

// registryManifestBodyURL returns the manifest_body_url a load_artifact of id
// carries, or nil.
func registryManifestBodyURL(t *testing.T, base, id string) *server.LargeResourceLink {
	t.Helper()
	resp, err := http.Get(base + "/v1/load_artifact?id=" + url.QueryEscape(id))
	if err != nil {
		t.Fatalf("load %s: %v", id, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("load %s = %d: %s", id, resp.StatusCode, b)
	}
	var out server.LoadArtifactResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", id, err)
	}
	return out.ManifestBodyURL
}

// Spec: §2.2, §6.6 — a manifest body whose bytes do not hash to the link's
// content hash aborts the whole server-source sync: sync.Run returns the
// mismatch for that artifact, and no file and no lock reach the target.
func TestServerSync_ManifestBodyDigestMismatchAbortsTheSync(t *testing.T) {
	t.Parallel()
	ts := largeManifestServer(t, writeLargeManifestRegistry(t))
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, _ := http.NewRequestWithContext(r.Context(), r.Method, ts.URL+r.URL.RequestURI(), r.Body)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if r.URL.Path == "/v1/load_artifact" && r.URL.Query().Get("id") == "team/big" {
			body = bytes.ReplaceAll(body, []byte(`"content_hash": "sha256:`), []byte(`"content_hash": "sha256:00`))
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(body)
	}))
	t.Cleanup(proxy.Close)

	target := t.TempDir()
	_, err := sync.Run(sync.Options{RegistryPath: proxy.URL, Target: target, AdapterID: "none"})
	if err == nil || !strings.Contains(err.Error(), "load_artifact team/big: manifest body content hash mismatch") {
		t.Fatalf("sync.Run err = %v, want the team/big manifest body mismatch", err)
	}
	if files := testharness.ReadTree(t, target); len(files) != 0 {
		t.Errorf("an aborted sync wrote %v", keysOf(files))
	}
}

// Spec: §4.7.6, §11 — the §6.4 overlay leg of the content-hash agreement for
// a package that declares extends:: the overlay resolves no chain and hashes
// the child's authored bytes, so a server-source sync whose overlay holds the
// reference fixture's extends: child byte for byte records the digest ingest
// stored for it.
func TestSyncServerSource_OverlayExtendsChildAgreesWithIngest(t *testing.T) {
	t.Parallel()
	dir := referenceRegistryPath(t)
	srv, err := server.NewFromFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFromFilesystem: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	const id = "notes/glossary-notes"
	ingested := registryContentHash(t, &http.Client{Timeout: 30 * time.Second}, ts.URL, id)

	authored, err := os.ReadFile(filepath.Join(dir, "personal", id, "ARTIFACT.md"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	overlayDir := t.TempDir()
	testharness.WriteTree(t, overlayDir, testharness.WriteTreeOption{Path: id + "/ARTIFACT.md", Content: string(authored)})

	target := t.TempDir()
	if _, err := sync.Run(sync.Options{RegistryPath: ts.URL, Target: target, AdapterID: "none", OverlayPath: overlayDir}); err != nil {
		t.Fatalf("server sync.Run with overlay: %v", err)
	}
	lock, err := sync.ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock: %v %v", lock, err)
	}
	for _, a := range lock.Artifacts {
		if a.ID == id {
			if a.ContentHash != ingested {
				t.Errorf("overlay lock content_hash = %s, want the ingested %s", a.ContentHash, ingested)
			}
			return
		}
	}
	t.Fatalf("lock has no entry for %s", id)
}
