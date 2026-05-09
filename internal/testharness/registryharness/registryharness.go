// Package registryharness wraps a filesystem-source registry behind a
// httptest.Server so integration tests exercise the real HTTP API
// without paying for a TCP socket. Stage 3 ships the filesystem-backed
// flavor; Postgres / S3 backends land alongside Phase 5.
package registryharness

import (
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/registry/server"
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
