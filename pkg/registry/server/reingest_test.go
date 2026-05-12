package server_test

import (
	"net/http"
	"testing"
)

// Spec: §7.3.1 — POST /v1/layers/reingest?id=ID enqueues a reingest
// for the named layer. Missing id surfaces a 400 with
// registry.invalid_argument; GET is method-not-allowed.
func TestLayerEndpoint_ReingestHappyPath(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id":          "team-finance",
		"source_type": "local",
		"local_path":  "/tmp/finance",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d: %s", resp.StatusCode, body)
	}

	resp, body = mustPost(t, base, "/v1/layers/reingest?id=team-finance", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reingest status = %d: %s", resp.StatusCode, body)
	}
}

func TestLayerEndpoint_ReingestUnknownReturns404(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	resp, _ := mustPost(t, base, "/v1/layers/reingest?id=ghost", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestLayerEndpoint_ReingestMissingIDReturns400(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	resp, _ := mustPost(t, base, "/v1/layers/reingest", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestLayerEndpoint_ReingestGetReturns405(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	resp, err := http.Get(base + "/v1/layers/reingest?id=x")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}
