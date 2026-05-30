package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// envelope decodes the §6.10 structured error response.
type envelope struct {
	Code            string         `json:"code"`
	Message         string         `json:"message"`
	Details         map[string]any `json:"details"`
	Retryable       bool           `json:"retryable"`
	SuggestedAction string         `json:"suggested_action"`
}

// spec: SS 6.10 (F-6.10.4, F-6.10.3) — an over-rate search returns HTTP
// 429 with quota.search_qps_exceeded marked retryable and carrying an
// operator remediation hint. Before the fix the 429 envelope reported
// retryable=false with no suggested_action.
func TestServer_SearchQPSEnvelope_RetryableWithHint(t *testing.T) {
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

	// Burst capacity is 1, so the first call drains the bucket and the
	// second is rate-limited.
	resp1, err := http.Get(ts.URL + "/v1/search_artifacts?query=x")
	if err != nil {
		t.Fatalf("GET 1: %v", err)
	}
	resp1.Body.Close()
	resp2, err := http.Get(ts.URL + "/v1/search_artifacts?query=x")
	if err != nil {
		t.Fatalf("GET 2: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second call status = %d, want 429", resp2.StatusCode)
	}
	var env envelope
	if err := json.NewDecoder(resp2.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Code != "quota.search_qps_exceeded" {
		t.Errorf("code = %q, want quota.search_qps_exceeded", env.Code)
	}
	if !env.Retryable {
		t.Errorf("retryable = false, want true for a rate-limit code")
	}
	if env.SuggestedAction == "" {
		t.Errorf("suggested_action empty, want a remediation hint")
	}
}

// spec: SS 6.10 (F-6.10.1) — the layer-count quota envelope carries the
// machine-readable cap and current count in details. Before the fix the
// envelope had no details object.
func TestServer_LayerCountEnvelope_Details(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithMaxUserLayers(1)
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	if status, code := registerUserLayer(t, ts.URL, "a", "alice"); status != http.StatusCreated {
		t.Fatalf("first layer: status %d code %q, want 201", status, code)
	}
	resp, body := mustPost(t, ts.URL, "/v1/layers", map[string]any{
		"id": "b", "source_type": "local", "local_path": "/tmp/b",
		"user_defined": true, "owner": "alice",
	})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second layer: status %d, want 429; body=%s", resp.StatusCode, body)
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Code != "quota.layer_count_exceeded" {
		t.Fatalf("code = %q, want quota.layer_count_exceeded", env.Code)
	}
	if env.Details == nil {
		t.Fatalf("details absent; want machine-readable limit/current")
	}
	if env.Details["limit"] != float64(1) {
		t.Errorf("details.limit = %v, want 1", env.Details["limit"])
	}
	if _, ok := env.Details["current"]; !ok {
		t.Errorf("details.current absent: %v", env.Details)
	}
	if env.SuggestedAction == "" {
		t.Errorf("suggested_action empty, want a remediation hint")
	}
	if env.Retryable {
		t.Errorf("retryable = true, want false for a hard cap")
	}
}
