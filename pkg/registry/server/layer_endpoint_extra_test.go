package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// list works against an empty registry.
func TestLayerEndpoint_ListEmpty(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	resp, err := http.Get(base + "/v1/layers")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// unregister missing id returns 400.
func TestLayerEndpoint_UnregisterMissingIDReturns400(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	req, _ := http.NewRequest(http.MethodDelete, base+"/v1/layers", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// unregister missing layer returns 404.
func TestLayerEndpoint_UnregisterMissingLayerReturns404(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	req, _ := http.NewRequest(http.MethodDelete, base+"/v1/layers?id=ghost", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// unregister of a user-defined layer succeeds without admin auth.
func TestLayerEndpoint_UnregisterUserDefinedSucceeds(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	// Register as user-defined.
	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id":           "personal",
		"source_type":  "local",
		"local_path":   "/tmp/personal",
		"user_defined": true,
		"owner":        "alice",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d: %s", resp.StatusCode, body)
	}
	// Now unregister.
	req, _ := http.NewRequest(http.MethodDelete, base+"/v1/layers?id=personal", nil)
	delResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", delResp.StatusCode)
	}
}

// reorder GET returns 405.
func TestLayerEndpoint_ReorderWrongMethodReturns405(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	resp, err := http.Get(base + "/v1/layers/reorder")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

// reorder bad JSON returns 400.
func TestLayerEndpoint_ReorderBadJSONReturns400(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	resp, err := http.Post(base+"/v1/layers/reorder", "application/json",
		bytes.NewReader([]byte("not json")))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// reorder happy path.
func TestLayerEndpoint_ReorderHappyPath(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	for _, id := range []string{"a", "b", "c"} {
		r, body := mustPost(t, base, "/v1/layers", map[string]any{
			"id": id, "source_type": "local", "local_path": "/tmp/" + id,
		})
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("register %s: %d: %s", id, r.StatusCode, body)
		}
	}
	resp, body := mustPost(t, base, "/v1/layers/reorder", map[string]any{
		"order": []string{"c", "b", "a"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
}

// register with missing required fields returns 400.
func TestLayerEndpoint_RegisterMissingFieldsReturns400(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	resp, _ := mustPost(t, base, "/v1/layers", map[string]any{}) // no id/source_type
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// register with bad JSON returns 400.
func TestLayerEndpoint_RegisterBadJSONReturns400(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	resp, err := http.Post(base+"/v1/layers", "application/json",
		bytes.NewReader([]byte("not json")))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// register user-defined layer with visibility flags persists them.
func TestLayerEndpoint_RegisterUserDefinedWithVisibility(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "personal",
		"source_type":  "local",
		"local_path":   "/tmp/personal",
		"user_defined": true,
		"owner":        "alice",
		"public":       false,
		"organization": false,
		"groups":       []string{"team-a"},
		"users":        []string{"alice"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	var got struct {
		Layer struct {
			Groups []string `json:"Groups"`
			Users  []string `json:"Users"`
		} `json:"layer"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Layer.Groups) != 1 || got.Layer.Users[0] != "alice" {
		t.Errorf("got %+v", got)
	}
}
