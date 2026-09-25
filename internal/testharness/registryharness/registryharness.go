// Package registryharness wraps a filesystem-source registry
// behind a httptest.Server so integration tests exercise the real
// HTTP API without paying for a TCP socket.
package registryharness

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Harness owns the lifetime of one test registry server.
type Harness struct {
	URL          string
	RegistryPath string
	Server       *httptest.Server
}

// Close shuts down the test server.
func (h *Harness) Close() {
	if h.Server != nil {
		h.Server.Close()
	}
}

// New brings up a registry server backed by a filesystem registry built
// from the given tree of fixture entries. The harness manages cleanup
// via t.Cleanup.
func New(t testing.TB, entries ...testharness.WriteTreeOption) *Harness {
	t.Helper()
	dir := t.TempDir()
	testharness.WriteTree(t, dir, entries...)
	srv, err := server.NewFromFilesystem(dir)
	if err != nil {
		t.Fatalf("server.NewFromFilesystem: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &Harness{URL: ts.URL, RegistryPath: dir, Server: ts}
}

// NewLayered brings up a registry server over the multi-layer filesystem
// registry at root, one public layer per name in layers in ascending
// precedence, with a filesystem object store behind the /objects route. Each
// layer is ingested with no lint rule applied, so a manifest document above
// the §4.1 inline cutoff, which the manifest-size rule rejects at a real
// ingest, is stored and served through the §6.6 manifest_body_url channel.
func NewLayered(t testing.TB, root string, layers ...string) *Harness {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for i, id := range layers {
		if err := st.PutLayerConfig(ctx, store.LayerConfig{TenantID: "default", ID: id, Order: i + 1, Public: true}); err != nil {
			t.Fatalf("PutLayerConfig %s: %v", id, err)
		}
		res, err := ingest.Ingest(ctx, st, ingest.Request{
			TenantID: "default", LayerID: id, Files: os.DirFS(filepath.Join(root, id)),
			Linter: &lint.Linter{Rules: []lint.Rule{noLintRule{}}},
		})
		if err != nil || res.Accepted == 0 || len(res.Rejected)+len(res.LintFailures)+len(res.Conflicts) != 0 {
			t.Fatalf("ingest %s: %v %+v", id, err, res)
		}
	}
	objects, err := objectstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("objectstore.Open: %v", err)
	}
	reg := core.New(st, "default", nil).WithAdmission(nil, objects, objectstore.DefaultReadTimeout)
	ts := httptest.NewServer(server.New(reg, server.WithObjectStore(objects, "placeholder", time.Hour)).Handler())
	t.Cleanup(ts.Close)
	objects.BaseURL = ts.URL
	return &Harness{URL: ts.URL, RegistryPath: root, Server: ts}
}

// noLintRule reports nothing. A Linter with an empty rule set applies the
// defaults, so skipping lint takes one rule that never fires.
type noLintRule struct{}

func (noLintRule) Code() string { return "testharness.none" }

func (noLintRule) Check(context.Context, *filesystem.Registry, []filesystem.ArtifactRecord) []lint.Diagnostic {
	return nil
}
