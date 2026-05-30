package integration

import (
	"context"
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
