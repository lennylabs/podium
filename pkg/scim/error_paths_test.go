package scim_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// POST /Users with malformed JSON returns 400 invalidValue.
func TestHandler_CreateUser_MalformedBody(t *testing.T) {
	t.Parallel()
	_, ts := bootSCIM(t, "tok")
	resp := authedRequest(t, "tok", http.MethodPost, ts.URL+"/scim/v2/Users",
		[]byte("not json"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// POST /Users twice with same externalId returns 409 uniqueness.
func TestHandler_CreateUser_Conflict(t *testing.T) {
	t.Parallel()
	_, ts := bootSCIM(t, "tok")
	body := []byte(`{
		"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],
		"userName":"alice@example.com",
		"externalId":"ext-1"
	}`)
	r1 := authedRequest(t, "tok", http.MethodPost, ts.URL+"/scim/v2/Users", body)
	r1.Body.Close()
	r2 := authedRequest(t, "tok", http.MethodPost, ts.URL+"/scim/v2/Users", body)
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusConflict {
		buf, _ := io.ReadAll(r2.Body)
		t.Errorf("status = %d, want 409: %s", r2.StatusCode, buf)
	}
}

// POST /Groups with malformed body returns 400.
func TestHandler_CreateGroup_MalformedBody(t *testing.T) {
	t.Parallel()
	_, ts := bootSCIM(t, "tok")
	resp := authedRequest(t, "tok", http.MethodPost, ts.URL+"/scim/v2/Groups",
		[]byte("not json"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// POST /Groups twice with same displayName returns 409.
func TestHandler_CreateGroup_Conflict(t *testing.T) {
	t.Parallel()
	_, ts := bootSCIM(t, "tok")
	body := []byte(`{
		"schemas":["urn:ietf:params:scim:schemas:core:2.0:Group"],
		"displayName":"engineering"
	}`)
	r1 := authedRequest(t, "tok", http.MethodPost, ts.URL+"/scim/v2/Groups", body)
	r1.Body.Close()
	r2 := authedRequest(t, "tok", http.MethodPost, ts.URL+"/scim/v2/Groups", body)
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", r2.StatusCode)
	}
}

// DELETE /Users/{id} returns 204 on success.
func TestHandler_DeleteUser_NoContent(t *testing.T) {
	t.Parallel()
	_, ts := bootSCIM(t, "tok")
	create := authedRequest(t, "tok", http.MethodPost, ts.URL+"/scim/v2/Users",
		[]byte(`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"x"}`))
	if create.StatusCode != http.StatusCreated {
		create.Body.Close()
		t.Fatalf("create status = %d", create.StatusCode)
	}
	var got map[string]any
	_ = json.NewDecoder(create.Body).Decode(&got)
	create.Body.Close()
	id, _ := got["id"].(string)
	if id == "" {
		t.Fatal("no id in create response")
	}
	del := authedRequest(t, "tok", http.MethodDelete, ts.URL+"/scim/v2/Users/"+id, nil)
	defer del.Body.Close()
	if del.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", del.StatusCode)
	}
}

// /scim/v2/ServiceProviderConfig returns 501.
func TestHandler_ServiceProviderConfigNotImplemented(t *testing.T) {
	t.Parallel()
	_, ts := bootSCIM(t, "tok")
	resp := authedRequest(t, "tok", http.MethodGet, ts.URL+"/scim/v2/ServiceProviderConfig", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", resp.StatusCode)
	}
}

// /scim/v2/Unknown returns 404.
func TestHandler_UnknownPathReturns404(t *testing.T) {
	t.Parallel()
	_, ts := bootSCIM(t, "tok")
	resp := authedRequest(t, "tok", http.MethodGet, ts.URL+"/scim/v2/Unknown", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// Auth failure path: empty bearer / unknown token.
func TestHandler_MissingOrInvalidToken(t *testing.T) {
	t.Parallel()
	_, ts := bootSCIM(t, "valid-tok")
	// No Authorization header.
	resp, err := http.Get(ts.URL + "/scim/v2/Users")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status (no auth) = %d, want 401", resp.StatusCode)
	}

	// Invalid token.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/scim/v2/Users", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("status (bad token) = %d, want 401", resp2.StatusCode)
	}
}

// Method-not-allowed: POST /Users/{id}.
func TestHandler_MethodNotAllowedOnSubresource(t *testing.T) {
	t.Parallel()
	_, ts := bootSCIM(t, "tok")
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/scim/v2/Users/123", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

var _ io.Reader // keep io import in use
