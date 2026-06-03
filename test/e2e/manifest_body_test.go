package e2e

// End-to-end tests for the §6.6 presigned manifest-body channel, driving the
// real podium-mcp binary. The §4.1 manifest token cap (lint.manifest_size)
// rejects an above-cutoff manifest at real ingest, so the channel cannot be
// produced by `podium serve`; these tests point the real bridge at a registry
// stub that emits manifest_body_url, exercising the bridge's fetch,
// content-hash verification, body reconstitution, and materialization through
// the shipping binary.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/podium/pkg/version"
)

// mbStubRegistry serves /v1/load_artifact emitting a presigned
// manifest_body_url for the given ARTIFACT.md, and an /objects route that
// streams the document. bodyHits counts how many times the body URL was
// fetched. status is the HTTP status the /objects route returns (200 to
// serve, anything else to simulate a failure).
func mbStubRegistry(t *testing.T, id, artifactMD string, status int) (*httptest.Server, *int32) {
	t.Helper()
	sum := sha256.Sum256([]byte(artifactMD))
	key := hex.EncodeToString(sum[:])
	contentHash := "sha256:" + version.ContentHash([]byte(artifactMD))
	var bodyHits int32

	mux := http.NewServeMux()
	mux.HandleFunc("/objects/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&bodyHits, 1)
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte(artifactMD))
	})
	mux.HandleFunc("/v1/load_artifact", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"id": id, "type": "context", "version": "1.0.0",
			"content_hash":  contentHash,
			"manifest_body": "", "frontmatter": "",
			"manifest_body_url": map[string]any{
				"presigned_url": "http://" + r.Host + "/objects/" + key,
				"content_hash":  "sha256:" + key,
				"size":          len(artifactMD),
				"content_type":  "text/markdown",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &bodyHits
}

// Spec: §6.6 — the bridge fetches a presigned manifest body, verifies its
// content hash, reconstitutes the ARTIFACT.md, and materializes the full body
// to disk. The content-hash check (step 2) passing proves the reconstituted
// frontmatter binds to the served content_hash.
func TestManifestBody_BridgeFetchesReconstitutesMaterializes(t *testing.T) {
	t.Parallel()
	id := "finance/glossary"
	body := strings.Repeat("Glossary entry line.\n", 20000) // ~420 KB, above the 256 KB cutoff
	artifactMD := "---\ntype: context\nversion: 1.0.0\ndescription: Big glossary.\n---\n\n" + body
	ts, bodyHits := mbStubRegistry(t, id, artifactMD, http.StatusOK)

	mat := t.TempDir()
	env := []string{
		"PODIUM_REGISTRY=" + ts.URL, "PODIUM_CACHE_DIR=" + t.TempDir(),
		"HOME=" + t.TempDir(), "PODIUM_HARNESS=none", "PODIUM_MATERIALIZE_ROOT=" + mat,
	}
	res := mcpExec(t, env, toolCall(1, "load_artifact", map[string]any{"id": id}))
	result := rpcResult(t, res.Stdout, 1)
	if e, ok := result["error"]; ok && e != nil {
		t.Fatalf("presigned-body load should succeed, got error: %v\nstderr=%s", e, res.Stderr)
	}
	if atomic.LoadInt32(bodyHits) == 0 {
		t.Error("bridge never fetched the presigned manifest-body URL")
	}
	// The reconstituted body is returned to the host as the tool result.
	if mb, _ := result["manifest_body"].(string); !strings.Contains(mb, "Glossary entry line.") {
		t.Errorf("manifest_body not reconstituted in the result (len=%d)", len(mb))
	}
	// The full ARTIFACT.md (frontmatter + reconstituted body) materialized.
	var found string
	for _, v := range readTreeAll(t, mat) {
		if strings.Contains(v, "Glossary entry line.") {
			found = v
			break
		}
	}
	if found == "" {
		t.Fatalf("no materialized file carried the reconstituted body; tree=%v", keysOf(readTreeAll(t, mat)))
	}
	if found != artifactMD {
		t.Errorf("materialized document != served ARTIFACT.md (%d vs %d bytes)", len(found), len(artifactMD))
	}
}

// Spec: §6.6 step 1 — when the presigned body cannot be fetched, the load
// fails with a structured error and nothing is written to disk, rather than
// materializing a body-less manifest.
func TestManifestBody_BridgeFetchFailureAborts(t *testing.T) {
	t.Parallel()
	id := "finance/glossary"
	body := strings.Repeat("Glossary entry line.\n", 20000)
	artifactMD := "---\ntype: context\nversion: 1.0.0\ndescription: Big glossary.\n---\n\n" + body
	ts, _ := mbStubRegistry(t, id, artifactMD, http.StatusNotFound)

	mat := t.TempDir()
	env := []string{
		"PODIUM_REGISTRY=" + ts.URL, "PODIUM_CACHE_DIR=" + t.TempDir(),
		"HOME=" + t.TempDir(), "PODIUM_HARNESS=none", "PODIUM_MATERIALIZE_ROOT=" + mat,
	}
	res := mcpExec(t, env, toolCall(1, "load_artifact", map[string]any{"id": id}))
	result := rpcResult(t, res.Stdout, 1)
	errStr, _ := result["error"].(string)
	if !strings.Contains(errStr, "materialize.fetch_failed") {
		t.Errorf("expected materialize.fetch_failed, got result=%v", result)
	}
	if files := readTreeAll(t, mat); len(files) != 0 {
		t.Errorf("a failed body fetch must not materialize anything, wrote: %v", keysOf(files))
	}
}
