package server_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// dataPlaneFixture boots a server.New()-constructed registry (the path
// both shipping binaries use, distinct from NewFromFilesystem) whose
// store already holds a manifest with one small inline resource and one
// large object-store resource. It proves the data plane serves from the
// core load result rather than a construction-time cache (F-7.2.3).
func dataPlaneFixture(t *testing.T) (*httptest.Server, []byte, string) {
	t.Helper()
	small := []byte("print('inline')\n")
	large := make([]byte, objectstore.InlineCutoff+2048)
	for i := range large {
		large[i] = byte('A' + i%26)
	}
	keyOf := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	hashOf := func(b []byte) string { return "sha256:" + keyOf(b) }

	objDir := t.TempDir()
	objStore, err := objectstore.Open(objDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Both blobs live in the object store (as ingest uploads them); the
	// small one also keeps its bytes inline on the record.
	for _, b := range [][]byte{small, large} {
		if err := objStore.Put(context.Background(), keyOf(b), b, "application/octet-stream"); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "default", ArtifactID: "finance/run", Version: "1.0.0",
		ContentHash: "sha256:c", Type: "skill", Layer: "L",
		Resources: []store.ResourceRef{
			{Path: "scripts/run.py", ContentHash: hashOf(small), Size: int64(len(small)), ContentType: "application/octet-stream", Inline: small},
			{Path: "data/big.bin", ContentHash: hashOf(large), Size: int64(len(large)), ContentType: "application/octet-stream"},
		},
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	srv := server.New(reg, server.WithObjectStore(objStore, "placeholder", time.Hour))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	objStore.BaseURL = ts.URL
	return ts, large, keyOf(large)
}

// Spec: §7.2 (F-7.2.1/F-7.2.3) — a server.New registry serves bundled
// resources from the core load result: small ones inline, large ones as
// a presigned URL. The wire field is presigned_url (F-7.2.5, §7.6.2).
func TestDataPlane_LoadArtifactServesResourcesFromCore(t *testing.T) {
	t.Parallel()
	ts, large, _ := dataPlaneFixture(t)
	resp, err := http.Get(ts.URL + "/v1/load_artifact?id=finance/run")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// F-7.2.5: the large-resource reference is named presigned_url.
	if !strings.Contains(string(raw), "\"presigned_url\"") {
		t.Errorf("load_artifact large_resources should use presigned_url, got:\n%s", raw)
	}
	if strings.Contains(string(raw), "\"url\"") {
		t.Errorf("large-resource link must not use the legacy url field:\n%s", raw)
	}

	var parsed server.LoadArtifactResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Resources["scripts/run.py"] != "print('inline')\n" {
		t.Errorf("small resource not inline: %v", parsed.Resources)
	}
	if _, inline := parsed.Resources["data/big.bin"]; inline {
		t.Error("large resource must not be inline")
	}
	link, ok := parsed.LargeResources["data/big.bin"]
	if !ok || link.URL == "" {
		t.Fatalf("large resource missing presigned link: %+v", parsed.LargeResources)
	}
	if link.Size != int64(len(large)) {
		t.Errorf("link.Size = %d, want %d", link.Size, len(large))
	}
}

// Spec: §7.6.2 — the batch endpoint returns bundled resources as a
// presigned array {path, presigned_url, content_hash} so the response
// stays small (F-7.2.3 previously returned no resources at all).
func TestDataPlane_BatchLoadReturnsPresignedResources(t *testing.T) {
	t.Parallel()
	ts, _, _ := dataPlaneFixture(t)
	reqBody, _ := json.Marshal(map[string]any{"ids": []string{"finance/run"}})
	resp, err := http.Post(ts.URL+"/v1/artifacts:batchLoad", "application/json", strings.NewReader(string(reqBody)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var envelopes []server.BatchLoadEnvelope
	if err := json.Unmarshal(raw, &envelopes); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	if len(envelopes) != 1 {
		t.Fatalf("envelopes len = %d, want 1", len(envelopes))
	}
	got := map[string]server.BatchResource{}
	for _, r := range envelopes[0].Resources {
		got[r.Path] = r
	}
	if len(got) != 2 {
		t.Fatalf("batch resources = %d, want 2: %+v", len(got), envelopes[0].Resources)
	}
	for _, path := range []string{"scripts/run.py", "data/big.bin"} {
		if got[path].PresignedURL == "" || got[path].ContentHash == "" {
			t.Errorf("batch resource %q missing presigned_url/content_hash: %+v", path, got[path])
		}
	}
	if !strings.Contains(string(raw), "\"presigned_url\"") {
		t.Errorf("batch resources should use presigned_url:\n%s", raw)
	}
}

// Spec: §7.2 (F-7.2.4) — the /objects HEAD path reports the size via
// Content-Length without returning a body; GET streams the bytes.
func TestDataPlane_ObjectsHeadReportsSizeWithoutBody(t *testing.T) {
	t.Parallel()
	ts, large, key := dataPlaneFixture(t)

	headReq, _ := http.NewRequest(http.MethodHead, ts.URL+"/objects/"+key, nil)
	headResp, err := http.DefaultClient.Do(headReq)
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	headBody, _ := io.ReadAll(headResp.Body)
	headResp.Body.Close()
	if headResp.StatusCode != http.StatusOK {
		t.Fatalf("HEAD status = %d", headResp.StatusCode)
	}
	if got := headResp.Header.Get("Content-Length"); got != strconv.Itoa(len(large)) {
		t.Errorf("HEAD Content-Length = %q, want %d", got, len(large))
	}
	if len(headBody) != 0 {
		t.Errorf("HEAD returned a body of %d bytes, want 0", len(headBody))
	}

	getResp, err := http.Get(ts.URL + "/objects/" + key)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	getBody, _ := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	if string(getBody) != string(large) {
		t.Errorf("GET streamed %d bytes, want %d", len(getBody), len(large))
	}
}
