package server_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/server"
)

// makeBigPayload builds a deterministic body of n bytes so tests
// can assert byte-equality after a presigned-URL fetch round-trip.
func makeBigPayload(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte('A' + (i % 26))
	}
	return out
}

// largeResourceSetup writes a single artifact with both an inline
// resource (small) and a large resource (above the §4.1 cutoff),
// boots a server with a Filesystem objectstore configured behind
// it, and returns (server URL, large body, large content hash).
func largeResourceSetup(t *testing.T) (string, []byte, string) {
	t.Helper()
	dir := t.TempDir()
	smallBody := "print('inline')\n"
	largeBody := makeBigPayload(objectstore.InlineCutoff + 1024)
	testharness.WriteTree(t, dir,
		testharness.WriteTreeOption{
			Path: "finance/run/ARTIFACT.md",
			Content: "---\ntype: skill\nversion: 1.0.0\ndescription: r\n---\n\nbody\n",
		},
		testharness.WriteTreeOption{
			Path:    "finance/run/SKILL.md",
			Content: "---\nname: run\ndescription: run\n---\n\nbody\n",
		},
		testharness.WriteTreeOption{
			Path:    "finance/run/scripts/run.py",
			Content: smallBody,
		},
		testharness.WriteTreeOption{
			Path:    "finance/run/data/big.bin",
			Content: string(largeBody),
		},
	)
	objDir := filepath.Join(t.TempDir(), "objects")
	store, err := objectstore.Open(objDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// We don't know the eventual httptest.Server URL until after we
	// boot it; install a placeholder and patch BaseURL once the
	// real URL is known.
	srv, err := server.NewFromFilesystem(dir,
		server.WithObjectStore(store, "https://placeholder", time.Hour),
	)
	if err != nil {
		t.Fatalf("NewFromFilesystem: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	store.BaseURL = ts.URL

	h := sha256.Sum256(largeBody)
	return ts.URL, largeBody, "sha256:" + hex.EncodeToString(h[:])
}

// Spec: §4.1 — load_artifact splits resources by the inline cutoff:
// small ones return inline, large ones return as URLs in the
// large_resources field. The content hash and size are present so
// the consumer can verify the bytes after fetch.
// Phase: 2
func TestLoadArtifact_LargeResourceReturnedAsURL(t *testing.T) {
	testharness.RequirePhase(t, 2)
	t.Parallel()
	srvURL, largeBody, contentHash := largeResourceSetup(t)
	resp, err := http.Get(srvURL + "/v1/load_artifact?id=finance/run")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var parsed server.LoadArtifactResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Resources["scripts/run.py"] == "" {
		t.Errorf("small resource missing from inline Resources: %+v", parsed.Resources)
	}
	if _, ok := parsed.Resources["data/big.bin"]; ok {
		t.Errorf("large resource should not appear inline")
	}
	link, ok := parsed.LargeResources["data/big.bin"]
	if !ok {
		t.Fatalf("large resource missing from LargeResources: %+v", parsed.LargeResources)
	}
	if link.URL == "" {
		t.Error("LargeResources URL is empty")
	}
	if link.ContentHash != contentHash {
		t.Errorf("ContentHash = %q, want %q", link.ContentHash, contentHash)
	}
	if link.Size != int64(len(largeBody)) {
		t.Errorf("Size = %d, want %d", link.Size, len(largeBody))
	}
	if link.ContentType != "application/octet-stream" {
		t.Errorf("ContentType = %q, want application/octet-stream", link.ContentType)
	}
}

// Spec: §13.10 — the filesystem backend's /objects/{key} route
// returns the bytes for an authorized caller, and the consumer can
// verify sha256(bytes) == ContentHash after fetch.
// Phase: 2
func TestObjectsRoute_FetchAndVerifyHash(t *testing.T) {
	testharness.RequirePhase(t, 2)
	t.Parallel()
	srvURL, largeBody, contentHash := largeResourceSetup(t)
	loadResp, err := http.Get(srvURL + "/v1/load_artifact?id=finance/run")
	if err != nil {
		t.Fatalf("GET load_artifact: %v", err)
	}
	body, _ := io.ReadAll(loadResp.Body)
	loadResp.Body.Close()
	var parsed server.LoadArtifactResponse
	_ = json.Unmarshal(body, &parsed)
	link := parsed.LargeResources["data/big.bin"]

	resp, err := http.Get(link.URL)
	if err != nil {
		t.Fatalf("GET object: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("object GET status = %d", resp.StatusCode)
	}
	gotBytes, _ := io.ReadAll(resp.Body)
	if string(gotBytes) != string(largeBody) {
		t.Errorf("fetched bytes mismatch: got %d bytes, want %d", len(gotBytes), len(largeBody))
	}
	h := sha256.Sum256(gotBytes)
	if "sha256:"+hex.EncodeToString(h[:]) != contentHash {
		t.Errorf("hash mismatch after fetch")
	}
	if got := resp.Header.Get("X-Content-Hash"); got != contentHash {
		t.Errorf("X-Content-Hash = %q, want %q", got, contentHash)
	}
}

// Spec: §13.10 — /objects/{key} for an unknown key returns 404, not
// 200. Tests confirm an attacker cannot probe for the existence of
// content hashes they don't already know.
// Phase: 2
func TestObjectsRoute_UnknownKeyReturnsNotFound(t *testing.T) {
	testharness.RequirePhase(t, 2)
	t.Parallel()
	srvURL, _, _ := largeResourceSetup(t)
	resp, err := http.Get(srvURL + "/objects/" + strings.Repeat("0", 64))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// Spec: §13.10 — /objects/{key} rejects path-traversal attempts.
// Phase: 2
func TestObjectsRoute_RejectsPathTraversal(t *testing.T) {
	testharness.RequirePhase(t, 2)
	t.Parallel()
	srvURL, _, _ := largeResourceSetup(t)
	resp, err := http.Get(srvURL + "/objects/..%2Fescape")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 400 or 404", resp.StatusCode)
	}
}

// Spec: §13.10 — the /objects route only registers when an
// objectstore is configured. Without it, the route returns 404.
// Phase: 2
func TestObjectsRoute_NotRegisteredWithoutObjectStore(t *testing.T) {
	testharness.RequirePhase(t, 2)
	t.Parallel()
	dir := t.TempDir()
	testharness.WriteTree(t, dir,
		testharness.WriteTreeOption{
			Path:    "x/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: low\n---\n\nbody\n",
		},
	)
	srv, _ := server.NewFromFilesystem(dir)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	resp, err := http.Get(ts.URL + "/objects/anything")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	// The default mux returns 404 for an unmatched route.
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
