package server_test

import (
	"bytes"
	"context"
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

// Spec: §13.4 — NewFromFilesystem hands its object store to the stored-row
// admission check, so it serves the object-held body its own ingest wrote and
// refuses the load once that object's bytes change.
func TestNewFromFilesystem_AdmitsObjectHeldBody(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	large := makeBigPayload(objectstore.InlineCutoff + 1024)
	testharness.WriteTree(t, dir,
		testharness.WriteTreeOption{Path: "finance/run/ARTIFACT.md", Content: "---\ntype: context\nversion: 1.0.0\ndescription: run\n---\n\nbody\n"},
		testharness.WriteTreeOption{Path: "finance/run/data/big.bin", Content: string(large)},
	)
	objects, err := objectstore.Open(filepath.Join(t.TempDir(), "objects"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	srv, err := server.NewFromFilesystem(dir, server.WithObjectStore(objects, "https://placeholder", time.Hour))
	if err != nil {
		t.Fatalf("NewFromFilesystem: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	objects.BaseURL = ts.URL

	status, body := getBody(t, ts.URL+"/v1/load_artifact?id=finance/run")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", status, body)
	}
	var parsed server.LoadArtifactResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	link, ok := parsed.LargeResources["data/big.bin"]
	if !ok {
		t.Fatalf("large resource missing from large_resources: %s", body)
	}

	key := strings.TrimPrefix(link.ContentHash, "sha256:")
	if err := objects.Delete(context.Background(), key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := objects.Put(context.Background(), key, bytes.ToLower(large), ""); err != nil {
		t.Fatalf("Put: %v", err)
	}
	status, body = getBody(t, ts.URL+"/v1/load_artifact?id=finance/run")
	if status != http.StatusInternalServerError || !strings.Contains(string(body), `"materialize.content_hash_mismatch"`) {
		t.Fatalf("altered object: status %d body %s, want 500 materialize.content_hash_mismatch", status, body)
	}
}

func getBody(t *testing.T, url string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}
