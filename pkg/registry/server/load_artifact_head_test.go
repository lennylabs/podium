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

// spec: §6.5 (F-6.5.2) — HEAD /v1/load_artifact revalidates a cached
// resolution. It returns the resolved content hash (and version) in headers
// and no body, so the MCP cache can confirm an unchanged artifact without
// downloading the manifest.
func TestLoadArtifact_HeadReturnsContentHash(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	putVersion(t, st, "team/a", "1.0.0", "sha256:v1", time.Now().UTC())

	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	ts := httptest.NewServer(server.New(reg).Handler())
	t.Cleanup(ts.Close)

	req, err := http.NewRequest(http.MethodHead, ts.URL+"/v1/load_artifact?id=team/a", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Podium-Content-Hash"); got != "sha256:v1" {
		t.Errorf("X-Podium-Content-Hash = %q, want sha256:v1", got)
	}
	if got := resp.Header.Get("X-Podium-Version"); got != "1.0.0" {
		t.Errorf("X-Podium-Version = %q, want 1.0.0", got)
	}
	if resp.ContentLength > 0 {
		t.Errorf("HEAD returned a body of %d bytes; want none", resp.ContentLength)
	}
}

// spec: §6.5 — a HEAD for an unknown artifact reports the not-found status
// without a body so the MCP cache falls through to a full fetch.
func TestLoadArtifact_HeadUnknownArtifact(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	ts := httptest.NewServer(server.New(reg).Handler())
	t.Cleanup(ts.Close)

	req, _ := http.NewRequest(http.MethodHead, ts.URL+"/v1/load_artifact?id=team/missing", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Errorf("status = 200 for unknown artifact; want an error status")
	}
}
