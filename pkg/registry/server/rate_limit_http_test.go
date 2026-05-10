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

// Spec: §4.7.8 — over-rate search calls return HTTP 429 with the
// quota.search_qps_exceeded code in the §6.10 error envelope.
// Phase: 12
func TestServer_SearchQPSRateLimited(t *testing.T) {
	testharness.RequirePhase(t, 12)
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	srv := server.New(reg,
		server.WithQuotaLimiter(server.NewQuotaLimiter(server.QuotaLimits{SearchQPS: 1})),
	)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp1, err := http.Get(ts.URL + "/v1/search_artifacts?query=x")
	if err != nil {
		t.Fatalf("GET 1: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first call status = %d, want 200", resp1.StatusCode)
	}
	resp2, err := http.Get(ts.URL + "/v1/search_artifacts?query=x")
	if err != nil {
		t.Fatalf("GET 2: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Errorf("second call status = %d, want 429", resp2.StatusCode)
	}
}

// Spec: §4.7.8 — over-rate load_artifact calls return HTTP 429
// with quota.materialize_rate_exceeded.
// Phase: 12
func TestServer_LoadArtifactRateLimited(t *testing.T) {
	testharness.RequirePhase(t, 12)
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "default"})
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	srv := server.New(reg,
		server.WithQuotaLimiter(server.NewQuotaLimiter(server.QuotaLimits{MaterializeRate: 1})),
	)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	for i := 0; i < 1; i++ {
		resp, err := http.Get(ts.URL + "/v1/load_artifact?id=x")
		if err != nil {
			t.Fatalf("GET %d: %v", i, err)
		}
		resp.Body.Close()
	}
	resp, err := http.Get(ts.URL + "/v1/load_artifact?id=x")
	if err != nil {
		t.Fatalf("GET (rate-limited): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", resp.StatusCode)
	}
}
