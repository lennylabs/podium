package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// spec: §12 — "ETag caching of immutable artifact versions" among the registry
// latency mitigations. GET /v1/load_artifact publishes the content hash as the
// ETag and honors a matching If-None-Match with 304 Not Modified and no body,
// so the MCP client serves its content-addressed cache instead of
// re-downloading.

func newETagServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	hash := putVersion(t, st, "team/a", "1.0.0", time.Now().UTC())
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	ts := httptest.NewServer(server.New(reg).Handler())
	t.Cleanup(ts.Close)
	return ts, `"` + hash + `"`
}

func TestLoadArtifact_GETSetsETag(t *testing.T) {
	t.Parallel()
	ts, etag := newETagServer(t)
	resp, err := http.Get(ts.URL + "/v1/load_artifact?id=team/a")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("ETag"); got != etag {
		t.Errorf("ETag = %q, want %q", got, etag)
	}
}

func TestLoadArtifact_IfNoneMatchReturns304(t *testing.T) {
	t.Parallel()
	ts, etag := newETagServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/load_artifact?id=team/a", nil)
	req.Header.Set("If-None-Match", etag)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", resp.StatusCode)
	}
	if resp.ContentLength > 0 {
		t.Errorf("304 returned a %d-byte body; want none", resp.ContentLength)
	}
	if got := resp.Header.Get("ETag"); got != etag {
		t.Errorf("ETag = %q, want %q", got, etag)
	}
}

// A stale (non-matching) If-None-Match must serve the full 200 body so a
// changed artifact is delivered rather than spuriously revalidated.
func TestLoadArtifact_IfNoneMatchStaleReturns200(t *testing.T) {
	t.Parallel()
	ts, etag := newETagServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/load_artifact?id=team/a", nil)
	req.Header.Set("If-None-Match", `"sha256:old"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("ETag"); got != etag {
		t.Errorf("ETag = %q, want %q", got, etag)
	}
}

// The wildcard If-None-Match: * matches any existing representation.
func TestLoadArtifact_IfNoneMatchWildcardReturns304(t *testing.T) {
	t.Parallel()
	ts, _ := newETagServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/load_artifact?id=team/a", nil)
	req.Header.Set("If-None-Match", "*")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", resp.StatusCode)
	}
}
