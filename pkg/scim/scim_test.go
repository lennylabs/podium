package scim_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/scim"
)

func bootSCIM(t *testing.T, token string) (*scim.Memory, *httptest.Server) {
	t.Helper()
	store := scim.NewMemory()
	h := &scim.Handler{Store: store}
	if token != "" {
		h.Tokens = map[string]bool{token: true}
	}
	mux := http.NewServeMux()
	mux.Handle("/scim/v2/", http.StripPrefix("", h))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return store, ts
}

func authedRequest(t *testing.T, token, method, url string, body []byte) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, _ := http.NewRequest(method, url, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/scim+json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

// Spec: §6.3.1 — POST /scim/v2/Users creates a user with the
// returned id; subsequent GET returns the same record.
// Phase: 7
func TestSCIM_UserCRUD(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	_, ts := bootSCIM(t, "tok")
	body := []byte(`{
		"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],
		"userName":"alice@example.com",
		"externalId":"ext-1",
		"active":true,
		"emails":[{"value":"alice@example.com","primary":true}]
	}`)
	resp := authedRequest(t, "tok", http.MethodPost, ts.URL+"/scim/v2/Users", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d: %s", resp.StatusCode, buf)
	}
	var created map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&created)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("created user has no id: %v", created)
	}

	getResp := authedRequest(t, "tok", http.MethodGet, ts.URL+"/scim/v2/Users/"+id, nil)
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d", getResp.StatusCode)
	}

	delResp := authedRequest(t, "tok", http.MethodDelete, ts.URL+"/scim/v2/Users/"+id, nil)
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", delResp.StatusCode)
	}

	notFound := authedRequest(t, "tok", http.MethodGet, ts.URL+"/scim/v2/Users/"+id, nil)
	notFound.Body.Close()
	if notFound.StatusCode != http.StatusNotFound {
		t.Fatalf("post-delete get status = %d, want 404", notFound.StatusCode)
	}
}

// Spec: §6.3.1 — bearer-token auth: requests without a valid token
// fail with 401; the SCIM error envelope carries the schema URN.
// Phase: 7
func TestSCIM_RequiresBearerToken(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	_, ts := bootSCIM(t, "expected")
	resp := authedRequest(t, "" /* no token */, http.MethodGet, ts.URL+"/scim/v2/Users", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("urn:ietf:params:scim:api:messages:2.0:Error")) {
		t.Errorf("missing SCIM error schema: %s", body)
	}

	bad := authedRequest(t, "wrong", http.MethodGet, ts.URL+"/scim/v2/Users", nil)
	bad.Body.Close()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad-token status = %d, want 401", bad.StatusCode)
	}
}

// Spec: §6.3.1 — userName uniqueness is enforced; the second POST
// returns 409 Conflict with scimType=uniqueness.
// Phase: 7
func TestSCIM_UserNameUniqueness(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	_, ts := bootSCIM(t, "tok")
	body := []byte(`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"u@x","active":true}`)
	first := authedRequest(t, "tok", http.MethodPost, ts.URL+"/scim/v2/Users", body)
	first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first status = %d", first.StatusCode)
	}
	second := authedRequest(t, "tok", http.MethodPost, ts.URL+"/scim/v2/Users", body)
	defer second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Errorf("second status = %d, want 409", second.StatusCode)
	}
	buf, _ := io.ReadAll(second.Body)
	if !bytes.Contains(buf, []byte(`"scimType":"uniqueness"`)) {
		t.Errorf("missing scimType=uniqueness: %s", buf)
	}
}

// Spec: §6.3.1 — Group CRUD plus membership lookup. Storing
// userName values via Group members lets the visibility evaluator
// resolve a `groups:[<displayName>]` filter via MembersOf.
// Phase: 7
func TestSCIM_GroupAndMembership(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	store, ts := bootSCIM(t, "tok")

	// Create two users and a group containing both.
	for _, un := range []string{"alice@x", "bob@x"} {
		body := []byte(`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"` + un + `","active":true}`)
		resp := authedRequest(t, "tok", http.MethodPost, ts.URL+"/scim/v2/Users", body)
		resp.Body.Close()
	}
	users, _ := store.ListUsers(context.Background(), scim.Filter{})
	memberIDs := make([]string, len(users))
	for i, u := range users {
		memberIDs[i] = u.ID
	}
	gbody, _ := json.Marshal(map[string]any{
		"schemas":     []string{"urn:ietf:params:scim:schemas:core:2.0:Group"},
		"displayName": "engineering",
		"members": []map[string]string{
			{"value": memberIDs[0], "type": "User"},
			{"value": memberIDs[1], "type": "User"},
		},
	})
	resp := authedRequest(t, "tok", http.MethodPost, ts.URL+"/scim/v2/Groups", gbody)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("create group status = %d: %s", resp.StatusCode, buf)
	}

	got, err := store.MembersOf(context.Background(), "engineering")
	if err != nil {
		t.Fatalf("MembersOf: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d members, want 2", len(got))
	}
	want := map[string]bool{"alice@x": false, "bob@x": false}
	for _, name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("unexpected member %q", name)
		}
		want[name] = true
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("missing member %q", name)
		}
	}
}

// Spec: §6.3.1 — the filter parser accepts eq, sw, co, pr; an
// unsupported operator returns 400 with scimType=invalidFilter.
// Phase: 7
func TestSCIM_FilterParser(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	cases := []struct {
		input    string
		wantErr  bool
		wantAttr string
		wantOp   string
		wantVal  string
	}{
		{`userName eq "alice"`, false, "userName", "eq", "alice"},
		{`displayName sw "eng"`, false, "displayName", "sw", "eng"},
		{`userName co "@x"`, false, "userName", "co", "@x"},
		{`active pr`, false, "active", "pr", ""},
		{`userName like "alice"`, true, "", "", ""},
	}
	for _, c := range cases {
		_, ts := bootSCIM(t, "tok")
		url := ts.URL + "/scim/v2/Users?filter=" + scimEncodeQuery(c.input)
		resp := authedRequest(t, "tok", http.MethodGet, url, nil)
		resp.Body.Close()
		if c.wantErr {
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("filter %q: status = %d, want 400", c.input, resp.StatusCode)
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("filter %q: status = %d, want 200", c.input, resp.StatusCode)
		}
	}
}

// Spec: §6.3.1 — DeleteUser cascades to scrub group memberships
// so a user removed from the IdP is also removed from every
// group's member list.
// Phase: 7
func TestSCIM_DeleteUserScrubsMembership(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	store := scim.NewMemory()
	u, err := store.CreateUser(context.Background(), scim.User{UserName: "alice@x", Active: true})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := store.CreateGroup(context.Background(), scim.Group{
		DisplayName: "eng", MemberIDs: []string{u.ID},
	}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if err := store.DeleteUser(context.Background(), u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	got, err := store.MembersOf(context.Background(), "eng")
	if err != nil {
		t.Fatalf("MembersOf: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("members = %v, want empty after user delete", got)
	}
}

// Spec: §6.3.1 — Filter.Match returns true on the eq/sw/co cases
// and false on a mismatch; tested directly so other code paths
// can rely on the matcher.
// Phase: 7
func TestSCIM_FilterMatchSemantics(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	cases := []struct {
		f    scim.Filter
		val  string
		want bool
	}{
		{scim.Filter{Attribute: "userName", Operator: "eq", Value: "alice"}, "alice", true},
		{scim.Filter{Attribute: "userName", Operator: "eq", Value: "alice"}, "bob", false},
		{scim.Filter{Attribute: "userName", Operator: "sw", Value: "ali"}, "alice", true},
		{scim.Filter{Attribute: "userName", Operator: "co", Value: "lic"}, "alice", true},
		{scim.Filter{Attribute: "userName", Operator: "co", Value: "x"}, "alice", false},
	}
	for _, c := range cases {
		if got := c.f.Match("userName", c.val); got != c.want {
			t.Errorf("Match(%+v, %q) = %v, want %v", c.f, c.val, got, c.want)
		}
	}
	// Unrelated attribute is a passthrough (other filters apply).
	if !(scim.Filter{Attribute: "userName", Operator: "eq", Value: "x"}).Match("displayName", "y") {
		t.Errorf("non-target attribute should not filter")
	}
}

// scimEncodeQuery percent-encodes the SCIM filter for use in a URL
// query parameter. We don't use net/url here so the test stays
// dependency-light.
func scimEncodeQuery(s string) string {
	out := []byte{}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' {
			out = append(out, '+')
		} else if c == '"' {
			out = append(out, '%', '2', '2')
		} else {
			out = append(out, c)
		}
	}
	return string(out)
}

var _ = errors.New
