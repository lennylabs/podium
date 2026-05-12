package server_test

import (
	"bytes"
	"net/http"
	"testing"
)

func TestLayerEndpoint_UpdateMissingID(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	req, _ := http.NewRequest(http.MethodPut, base+"/v1/layers/update", bytes.NewReader([]byte("{}")))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestLayerEndpoint_UpdateUnknownLayer(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	req, _ := http.NewRequest(http.MethodPut, base+"/v1/layers/update?id=ghost",
		bytes.NewReader([]byte(`{"ref":"main"}`)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestLayerEndpoint_UpdateMalformedBody(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	// First register a layer to update.
	resp, _ := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "team", "source_type": "local", "local_path": "/tmp/team",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPut, base+"/v1/layers/update?id=team",
		bytes.NewReader([]byte("not json")))
	bad, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", bad.StatusCode)
	}
}

func TestLayerEndpoint_UpdateWrongMethod(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	resp, err := http.Get(base + "/v1/layers/update?id=team")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestLayerEndpoint_UpdateMultiFieldPatch(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	// Register a git layer first.
	resp, _ := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "shared", "source_type": "git",
		"repo": "git@example/shared.git", "ref": "main",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d", resp.StatusCode)
	}
	// Patch multiple fields at once.
	req, _ := http.NewRequest(http.MethodPut, base+"/v1/layers/update?id=shared",
		bytes.NewReader([]byte(`{
			"ref":"release",
			"root":"artifacts/",
			"owner":"alice",
			"organization":true,
			"groups":["engineering"],
			"users":["alice"]
		}`)))
	put, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer put.Body.Close()
	if put.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", put.StatusCode)
	}
}
