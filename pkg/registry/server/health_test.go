package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// healthBody is the on-the-wire shape of /healthz; tested locally
// to keep the assertion symmetric with the response struct.
type healthBody struct {
	Mode  string `json:"mode"`
	Ready bool   `json:"ready"`
}

func newHealthFixture(t *testing.T, opts ...server.Option) (*httptest.Server, *server.ModeTracker) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	reg := core.New(st, "default", []layer.Layer{})
	mode := server.NewModeTracker()
	allOpts := append([]server.Option{server.WithMode(mode)}, opts...)
	srv := server.New(reg, allOpts...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, mode
}

// Spec: §13.9 — /healthz reports mode and is always 200 in
// liveness terms; the mode field surfaces "ready" by default.
// Phase: 0
func TestHealth_DefaultIsReady(t *testing.T) {
	testharness.RequirePhase(t, 0)
	t.Parallel()
	ts, _ := newHealthFixture(t)
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	var body healthBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Mode != "ready" {
		t.Errorf("Mode = %q, want ready", body.Mode)
	}
}

// Spec: §13.2.1 / §13.9 — when the mode tracker is flipped to
// read_only, /healthz reflects it.
// Phase: 0
func TestHealth_SurfacesReadOnly(t *testing.T) {
	testharness.RequirePhase(t, 0)
	t.Parallel()
	ts, mode := newHealthFixture(t)
	mode.Set(server.ModeReadOnly)
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	var body healthBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Mode != "read_only" {
		t.Errorf("Mode = %q, want read_only", body.Mode)
	}
}

// Spec: §13.10 — public mode is reported through /healthz so
// load balancers and clients can detect the deployment shape.
// Phase: 0
func TestHealth_SurfacesPublicMode(t *testing.T) {
	testharness.RequirePhase(t, 0)
	t.Parallel()
	ts, _ := newHealthFixture(t, server.WithPublicMode())
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	var body healthBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Mode != "public" {
		t.Errorf("Mode = %q, want public", body.Mode)
	}
}

// Spec: §13.9 — /readyz answers 200 in ready / read_only modes
// (the registry stays in load-balancer rotation while flipped
// read-only) and reports the canonical mode string.
// Phase: 0
func TestReadyz_ReadyAndReadOnlyAreOK(t *testing.T) {
	testharness.RequirePhase(t, 0)
	t.Parallel()
	ts, mode := newHealthFixture(t)

	resp, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("ready /readyz status = %d, want 200", resp.StatusCode)
	}

	mode.Set(server.ModeReadOnly)
	resp, err = http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("read_only /readyz status = %d, want 200", resp.StatusCode)
	}
	var body server.ReadyResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Mode != "read_only" {
		t.Errorf("ReadyResponse.Mode = %q, want read_only", body.Mode)
	}
}
