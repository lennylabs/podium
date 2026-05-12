package scim_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/scim"
)

func TestMemory_ReplaceUser(t *testing.T) {
	t.Parallel()
	store := scim.NewMemory()
	created, err := store.CreateUser(context.Background(), scim.User{
		ID: "u-1", UserName: "alice", Email: "alice@example",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	updated, err := store.ReplaceUser(context.Background(), "u-1", scim.User{
		UserName: "alice2", Email: "alice2@example",
	})
	if err != nil {
		t.Fatalf("ReplaceUser: %v", err)
	}
	if updated.UserName != "alice2" || updated.ID != "u-1" {
		t.Errorf("updated = %+v", updated)
	}
	if !updated.UpdatedAt.After(created.CreatedAt) && !updated.UpdatedAt.Equal(created.CreatedAt) {
		// UpdatedAt should be at least as recent as CreatedAt.
		t.Errorf("UpdatedAt %v not >= CreatedAt %v", updated.UpdatedAt, created.CreatedAt)
	}
}

func TestMemory_ReplaceUser_NotFound(t *testing.T) {
	t.Parallel()
	store := scim.NewMemory()
	if _, err := store.ReplaceUser(context.Background(), "ghost", scim.User{UserName: "x"}); err == nil {
		t.Errorf("expected ErrNotFound, got nil")
	}
}

func TestMemory_GroupListReplaceDelete(t *testing.T) {
	t.Parallel()
	store := scim.NewMemory()
	_, _ = store.CreateGroup(context.Background(), scim.Group{ID: "g-1", DisplayName: "engineering"})
	_, _ = store.CreateGroup(context.Background(), scim.Group{ID: "g-2", DisplayName: "sales"})

	groups, err := store.ListGroups(context.Background(), scim.Filter{})
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(groups) != 2 {
		t.Errorf("ListGroups returned %d, want 2", len(groups))
	}

	updated, err := store.ReplaceGroup(context.Background(), "g-1",
		scim.Group{DisplayName: "engineering-rebrand", MemberIDs: []string{"u-1"}})
	if err != nil {
		t.Fatalf("ReplaceGroup: %v", err)
	}
	if updated.DisplayName != "engineering-rebrand" || updated.ID != "g-1" {
		t.Errorf("updated = %+v", updated)
	}

	if err := store.DeleteGroup(context.Background(), "g-2"); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	if _, err := store.GetGroup(context.Background(), "g-2"); err == nil {
		t.Errorf("group still present after delete")
	}
}

func TestMemory_ReplaceGroup_NotFound(t *testing.T) {
	t.Parallel()
	store := scim.NewMemory()
	if _, err := store.ReplaceGroup(context.Background(), "ghost",
		scim.Group{DisplayName: "x"}); err == nil {
		t.Errorf("expected ErrNotFound, got nil")
	}
}

// --- FileStore --------------------------------------------------------------

func TestFileStore_UserListAndReplace(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "scim.json")
	s, err := scim.LoadFileStore(path)
	if err != nil {
		t.Fatalf("LoadFileStore: %v", err)
	}
	_, _ = s.CreateUser(context.Background(), scim.User{ID: "u-1", UserName: "alice"})
	_, _ = s.CreateUser(context.Background(), scim.User{ID: "u-2", UserName: "bob"})
	users, err := s.ListUsers(context.Background(), scim.Filter{})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("ListUsers returned %d, want 2", len(users))
	}
	if _, err := s.ReplaceUser(context.Background(), "u-1",
		scim.User{UserName: "alice2"}); err != nil {
		t.Fatalf("ReplaceUser: %v", err)
	}
	// Reopen to confirm the rename persisted.
	s2, _ := scim.LoadFileStore(path)
	got, _ := s2.GetUser(context.Background(), "u-1")
	if got.UserName != "alice2" {
		t.Errorf("after reopen, UserName = %q, want alice2", got.UserName)
	}
}

func TestFileStore_GroupListReplaceDelete(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "scim.json")
	s, _ := scim.LoadFileStore(path)
	_, _ = s.CreateGroup(context.Background(), scim.Group{ID: "g-1", DisplayName: "team-a"})
	_, _ = s.CreateGroup(context.Background(), scim.Group{ID: "g-2", DisplayName: "team-b"})
	groups, err := s.ListGroups(context.Background(), scim.Filter{})
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(groups) != 2 {
		t.Errorf("ListGroups returned %d, want 2", len(groups))
	}
	if _, err := s.ReplaceGroup(context.Background(), "g-1",
		scim.Group{DisplayName: "team-a2"}); err != nil {
		t.Fatalf("ReplaceGroup: %v", err)
	}
	if err := s.DeleteGroup(context.Background(), "g-2"); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	s2, _ := scim.LoadFileStore(path)
	all, _ := s2.ListGroups(context.Background(), scim.Filter{})
	if len(all) != 1 {
		t.Errorf("after reopen, len = %d, want 1", len(all))
	}
}

// --- HTTP handler routes ----------------------------------------------------

func TestHandler_ReplaceUserHTTP(t *testing.T) {
	t.Parallel()
	_, ts := bootSCIM(t, "tok")
	created := authedRequest(t, "tok", http.MethodPost, ts.URL+"/scim/v2/Users", []byte(`{
		"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],
		"userName":"alice@example.com",
		"active":true
	}`))
	defer created.Body.Close()
	if created.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(created.Body)
		t.Fatalf("create status = %d: %s", created.StatusCode, buf)
	}
	var m map[string]any
	_ = json.NewDecoder(created.Body).Decode(&m)
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("no id in %+v", m)
	}

	put := authedRequest(t, "tok", http.MethodPut, ts.URL+"/scim/v2/Users/"+id, []byte(`{
		"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],
		"userName":"alice2@example.com",
		"active":true
	}`))
	defer put.Body.Close()
	if put.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(put.Body)
		t.Fatalf("PUT status = %d: %s", put.StatusCode, buf)
	}
	var got map[string]any
	_ = json.NewDecoder(put.Body).Decode(&got)
	if got["userName"] != "alice2@example.com" {
		t.Errorf("updated userName = %v", got["userName"])
	}

	// PUT against an unknown ID = 404.
	notFound := authedRequest(t, "tok", http.MethodPut, ts.URL+"/scim/v2/Users/ghost", []byte(`{
		"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],
		"userName":"x@example.com"
	}`))
	defer notFound.Body.Close()
	if notFound.StatusCode != http.StatusNotFound {
		t.Errorf("PUT ghost status = %d, want 404", notFound.StatusCode)
	}

	// Malformed PUT body = 400.
	bad := authedRequest(t, "tok", http.MethodPut, ts.URL+"/scim/v2/Users/"+id, []byte(`not json`))
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Errorf("PUT bad json status = %d, want 400", bad.StatusCode)
	}
}

func TestHandler_GroupCRUDHTTP(t *testing.T) {
	t.Parallel()
	_, ts := bootSCIM(t, "tok")
	body := []byte(`{
		"schemas":["urn:ietf:params:scim:schemas:core:2.0:Group"],
		"displayName":"engineering",
		"members":[]
	}`)
	created := authedRequest(t, "tok", http.MethodPost, ts.URL+"/scim/v2/Groups", body)
	defer created.Body.Close()
	if created.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(created.Body)
		t.Fatalf("POST Groups status = %d: %s", created.StatusCode, buf)
	}
	var m map[string]any
	_ = json.NewDecoder(created.Body).Decode(&m)
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("no id in %+v", m)
	}

	// GET /Groups/{id}.
	got := authedRequest(t, "tok", http.MethodGet, ts.URL+"/scim/v2/Groups/"+id, nil)
	defer got.Body.Close()
	if got.StatusCode != http.StatusOK {
		t.Errorf("GET Group status = %d", got.StatusCode)
	}

	// GET /Groups (list).
	list := authedRequest(t, "tok", http.MethodGet, ts.URL+"/scim/v2/Groups", nil)
	defer list.Body.Close()
	if list.StatusCode != http.StatusOK {
		t.Errorf("LIST Group status = %d", list.StatusCode)
	}
	var listResp struct {
		Resources []map[string]any `json:"Resources"`
	}
	_ = json.NewDecoder(list.Body).Decode(&listResp)
	if len(listResp.Resources) == 0 {
		t.Errorf("ListGroups Resources empty")
	}

	// PUT /Groups/{id}.
	put := authedRequest(t, "tok", http.MethodPut, ts.URL+"/scim/v2/Groups/"+id, []byte(`{
		"schemas":["urn:ietf:params:scim:schemas:core:2.0:Group"],
		"displayName":"engineering-rebrand",
		"members":[]
	}`))
	defer put.Body.Close()
	if put.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(put.Body)
		t.Errorf("PUT status = %d: %s", put.StatusCode, buf)
	}

	// PUT missing group = 404.
	missingPut := authedRequest(t, "tok", http.MethodPut, ts.URL+"/scim/v2/Groups/ghost", []byte(`{
		"schemas":["urn:ietf:params:scim:schemas:core:2.0:Group"],
		"displayName":"x"
	}`))
	defer missingPut.Body.Close()
	if missingPut.StatusCode != http.StatusNotFound {
		t.Errorf("PUT ghost status = %d, want 404", missingPut.StatusCode)
	}

	// Bad JSON PUT = 400.
	bad := authedRequest(t, "tok", http.MethodPut, ts.URL+"/scim/v2/Groups/"+id, []byte(`not json`))
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Errorf("PUT bad json status = %d, want 400", bad.StatusCode)
	}

	// DELETE /Groups/{id} = 204.
	del := authedRequest(t, "tok", http.MethodDelete, ts.URL+"/scim/v2/Groups/"+id, nil)
	defer del.Body.Close()
	if del.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE Group status = %d, want 204", del.StatusCode)
	}

	// GET after DELETE = 404.
	gone := authedRequest(t, "tok", http.MethodGet, ts.URL+"/scim/v2/Groups/"+id, nil)
	defer gone.Body.Close()
	if gone.StatusCode != http.StatusNotFound {
		t.Errorf("GET deleted Group status = %d, want 404", gone.StatusCode)
	}
}

func TestHandler_GroupFilter(t *testing.T) {
	t.Parallel()
	_, ts := bootSCIM(t, "tok")
	for _, name := range []string{"eng", "sales", "ops"} {
		body := []byte(`{
			"schemas":["urn:ietf:params:scim:schemas:core:2.0:Group"],
			"displayName":"` + name + `"
		}`)
		r := authedRequest(t, "tok", http.MethodPost, ts.URL+"/scim/v2/Groups", body)
		r.Body.Close()
	}
	resp := authedRequest(t, "tok", http.MethodGet,
		ts.URL+`/scim/v2/Groups?filter=displayName+eq+"sales"`, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("filter LIST status = %d", resp.StatusCode)
	}
	var got struct {
		Resources []struct {
			DisplayName string `json:"displayName"`
		} `json:"Resources"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if len(got.Resources) != 1 || got.Resources[0].DisplayName != "sales" {
		t.Errorf("filter result = %+v, want one [sales]", got.Resources)
	}
}

// Compile-time check that bytes is referenced; the import is shared
// indirectly through authedRequest above.
var _ = bytes.NewReader
