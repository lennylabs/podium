package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §13.2.1 — every response (read or admin) carries
// X-Podium-Read-Only: true plus an X-Podium-Read-Only-Lag-Seconds
// header when the registry is in read-only mode. Clients that need
// strict freshness inspect these headers.
// Phase: 0
func TestServer_ReadOnlyHeadersOnReadEndpoints(t *testing.T) {
	testharness.RequirePhase(t, 0)
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	mode := server.NewModeTracker()
	srv := server.New(reg, server.WithMode(mode))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Healthy mode: no read-only headers expected.
	resp, err := http.Get(ts.URL + "/v1/search_artifacts?query=x")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("X-Podium-Read-Only"); got != "" {
		t.Errorf("ready mode: X-Podium-Read-Only = %q, want empty", got)
	}

	// Flip read-only: header should appear on the next response.
	mode.Set(server.ModeReadOnly)
	resp, err = http.Get(ts.URL + "/v1/search_artifacts?query=x")
	if err != nil {
		t.Fatalf("GET (read-only): %v", err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("X-Podium-Read-Only"); got != "true" {
		t.Errorf("read-only mode: X-Podium-Read-Only = %q, want true", got)
	}
	if resp.Header.Get("X-Podium-Read-Only-Lag-Seconds") == "" {
		t.Errorf("read-only mode: missing X-Podium-Read-Only-Lag-Seconds header")
	}
}

// Spec: §13.2.1 — load_artifact also carries the header so the
// MCP server can surface staleness to the agent.
// Phase: 0
func TestServer_ReadOnlyHeadersOnLoadArtifact(t *testing.T) {
	testharness.RequirePhase(t, 0)
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "default"})
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	mode := server.NewModeTracker()
	mode.Set(server.ModeReadOnly)
	srv := server.New(reg, server.WithMode(mode))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/v1/load_artifact?id=missing")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("X-Podium-Read-Only"); got != "true" {
		t.Errorf("X-Podium-Read-Only = %q, want true", got)
	}
}
