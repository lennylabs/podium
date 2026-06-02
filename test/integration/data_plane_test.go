package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §7.2 (F-7.2.1, F-7.2.2, F-7.2.3) — the full data plane over the
// SQLite metadata store and a filesystem object store: ingest uploads
// bundled resources keyed by content hash and persists refs that survive
// the SQL column round-trip; load_artifact returns the small resource
// inline and the large one as a presigned URL the consumer fetches from
// the /objects route. This mirrors the standalone deployment (§13.10) in
// process, without the shipping binary.
func TestDataPlane_IngestToLoadArtifactRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	objStore, err := objectstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("objectstore.Open: %v", err)
	}

	small := "print('inline')\n"
	large := strings.Repeat("Z", objectstore.InlineCutoff+4096)
	files := fstest.MapFS{
		"finance/run/ARTIFACT.md":    &fstest.MapFile{Data: []byte("---\ntype: skill\nversion: 1.0.0\nsensitivity: low\n---\n\n<!-- body in SKILL.md -->\n")},
		"finance/run/SKILL.md":       &fstest.MapFile{Data: []byte("---\nname: run\ndescription: Run the analysis when closing the books.\n---\n\nbody\n")},
		"finance/run/scripts/run.py": &fstest.MapFile{Data: []byte(small)},
		"finance/run/data/big.bin":   &fstest.MapFile{Data: []byte(large)},
	}
	if _, err := ingest.Ingest(ctx, st, ingest.Request{
		TenantID:    "default",
		LayerID:     "L",
		Files:       files,
		Linter:      lint.NewIngestLinter(true),
		ResourcePut: objStore.Put,
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	srv := server.New(reg, server.WithObjectStore(objStore, "placeholder", time.Hour))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	objStore.BaseURL = ts.URL

	resp, err := http.Get(ts.URL + "/v1/load_artifact?id=finance/run")
	if err != nil {
		t.Fatalf("GET load_artifact: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var parsed server.LoadArtifactResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	if parsed.Resources["scripts/run.py"] != small {
		t.Errorf("small resource not inline after SQLite round-trip: %v", parsed.Resources)
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

	// The presigned URL resolves to the data-plane route and streams the bytes.
	objResp, err := http.Get(link.URL)
	if err != nil {
		t.Fatalf("GET presigned: %v", err)
	}
	objBody, _ := io.ReadAll(objResp.Body)
	objResp.Body.Close()
	if objResp.StatusCode != http.StatusOK {
		t.Fatalf("presigned fetch = HTTP %d", objResp.StatusCode)
	}
	if string(objBody) != large {
		t.Errorf("fetched %d bytes, want %d", len(objBody), len(large))
	}
}

// Spec: §7.2/§7.6.2 (F-4.1.1, F-7.2.1, F-7.6.4) — the standalone-without-storage
// deployment (§13.11). Ingest with no ResourcePut keeps every resource inline
// regardless of size, so the SQLite store round-trips the bytes and both the
// single-load and batch endpoints serve them: a binary resource base64-encoded
// (resources_base64 / inline_base64) so JSON does not corrupt it, and a large
// resource inline rather than as a presigned URL that no object store can sign.
func TestDataPlane_NoObjectStoreServesResourcesInline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	text := []byte("print('inline')\n")
	binary := []byte{0xff, 0xfe, 0x00, 0x01, 0x02, 0xfd}
	largeBinary := make([]byte, objectstore.InlineCutoff+4096)
	for i := range largeBinary {
		largeBinary[i] = byte(i % 256)
	}
	largeBinary[0], largeBinary[1] = 0xff, 0xfe
	files := fstest.MapFS{
		"finance/run/ARTIFACT.md":    &fstest.MapFile{Data: []byte("---\ntype: skill\nversion: 1.0.0\nsensitivity: low\n---\n\n<!-- body in SKILL.md -->\n")},
		"finance/run/SKILL.md":       &fstest.MapFile{Data: []byte("---\nname: run\ndescription: Run the analysis when closing the books.\n---\n\nbody\n")},
		"finance/run/scripts/run.py": &fstest.MapFile{Data: text},
		"finance/run/data/blob.bin":  &fstest.MapFile{Data: binary},
		"finance/run/data/big.bin":   &fstest.MapFile{Data: largeBinary},
	}
	// ResourcePut omitted: no object store, the standalone-without-storage mode.
	if _, err := ingest.Ingest(ctx, st, ingest.Request{
		TenantID: "default",
		LayerID:  "L",
		Files:    files,
		Linter:   lint.NewIngestLinter(true),
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	srv := server.New(reg) // no WithObjectStore
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Single-load: large resource served inline (not presigned), binary set
	// base64-encoded and decodes back to the original bytes.
	resp, err := http.Get(ts.URL + "/v1/load_artifact?id=finance/run")
	if err != nil {
		t.Fatalf("GET load_artifact: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("load_artifact status = %d, want 200: %s", resp.StatusCode, raw)
	}
	var parsed server.LoadArtifactResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	if len(parsed.LargeResources) != 0 {
		t.Errorf("no object store: nothing should presign, got %+v", parsed.LargeResources)
	}
	if !parsed.ResourcesB64 {
		t.Fatalf("binary inline set should be base64 (resources_base64):\n%s", raw)
	}
	for path, want := range map[string][]byte{
		"scripts/run.py": text, "data/blob.bin": binary, "data/big.bin": largeBinary,
	} {
		dec, derr := base64.StdEncoding.DecodeString(parsed.Resources[path])
		if derr != nil {
			t.Errorf("%s: decode: %v", path, derr)
			continue
		}
		if !bytes.Equal(dec, want) {
			t.Errorf("%s: corrupted (%d vs %d bytes)", path, len(dec), len(want))
		}
	}

	// Batch-load: the same resources are delivered inline, none dropped.
	reqBody, _ := json.Marshal(map[string]any{"ids": []string{"finance/run"}})
	bresp, err := http.Post(ts.URL+"/v1/artifacts:batchLoad", "application/json", strings.NewReader(string(reqBody)))
	if err != nil {
		t.Fatalf("POST batchLoad: %v", err)
	}
	braw, _ := io.ReadAll(bresp.Body)
	bresp.Body.Close()
	var envs []server.BatchLoadEnvelope
	if err := json.Unmarshal(braw, &envs); err != nil {
		t.Fatalf("unmarshal batch: %v\n%s", err, braw)
	}
	if len(envs) != 1 || envs[0].Status != "ok" {
		t.Fatalf("batch envelopes = %+v, want one ok item", envs)
	}
	byPath := map[string]server.BatchResource{}
	for _, r := range envs[0].Resources {
		byPath[r.Path] = r
	}
	if len(byPath) != 3 {
		t.Fatalf("batch resources = %d, want 3 (none dropped): %+v", len(byPath), envs[0].Resources)
	}
	for path, want := range map[string][]byte{
		"scripts/run.py": text, "data/blob.bin": binary, "data/big.bin": largeBinary,
	} {
		r := byPath[path]
		if r.PresignedURL != "" {
			t.Errorf("%s: no object store, presigned_url should be empty", path)
		}
		got := []byte(r.Inline)
		if r.InlineBase64 {
			got, _ = base64.StdEncoding.DecodeString(r.Inline)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: batch inline corrupted (%d vs %d bytes)", path, len(got), len(want))
		}
	}
}
