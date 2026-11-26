package scim_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/scim"
)

// scimDo sends a SCIM request with the test bearer token and returns the
// status and the decoded body.
func scimDo(t *testing.T, method, url string, body any) (int, map[string]any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/scim+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return resp.StatusCode, out
}

// Spec: §6.3.1 — RFC 7643 requires a user's userName and a group's
// displayName, so the receiver refuses an empty one on replacement exactly as
// on creation: HTTP 400 with the invalidValue error type, and the stored
// resource is unchanged. Before the fix a PUT with an empty userName was
// stored, and the group expansion then returned "" as a member name. The case
// runs against the in-memory directory and the file-backed one that
// PODIUM_SCIM_STORE_PATH selects, which delegates to it.
func TestHandler_ReplaceRefusesEmptyRequiredAttribute(t *testing.T) {
	t.Parallel()
	stores := map[string]func(t *testing.T) scim.Store{
		"memory": func(*testing.T) scim.Store { return scim.NewMemory() },
		"file": func(t *testing.T) scim.Store {
			fs, err := scim.LoadFileStore(filepath.Join(t.TempDir(), "scim.json"))
			if err != nil {
				t.Fatalf("LoadFileStore: %v", err)
			}
			return fs
		},
	}
	for name, open := range stores {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			st := open(t)
			ts := httptest.NewServer(&scim.Handler{Store: st, Tokens: map[string]bool{"tok": true}})
			t.Cleanup(ts.Close)
			userURL, groupURL := ts.URL+"/scim/v2/Users", ts.URL+"/scim/v2/Groups"

			_, user := scimDo(t, http.MethodPost, userURL, map[string]any{"userName": "alice@acme.com", "active": true})
			uid, _ := user["id"].(string)
			_, group := scimDo(t, http.MethodPost, groupURL, map[string]any{
				"displayName": "acme-finance", "members": []map[string]any{{"value": uid}},
			})
			gid, _ := group["id"].(string)

			// Creation and replacement answer an empty attribute alike.
			var got []string
			for _, req := range []struct{ method, url, attr string }{
				{http.MethodPost, userURL, "userName"},
				{http.MethodPut, userURL + "/" + uid, "userName"},
				{http.MethodPost, groupURL, "displayName"},
				{http.MethodPut, groupURL + "/" + gid, "displayName"},
			} {
				status, env := scimDo(t, req.method, req.url, map[string]any{req.attr: "", "active": true})
				got = append(got, fmt.Sprintf("%s %d %v %v", req.method, status, env["scimType"], env["detail"]))
			}
			want := []string{
				"POST 400 invalidValue scim: userName is required",
				"PUT 400 invalidValue scim: userName is required",
				"POST 400 invalidValue scim: displayName is required",
				"PUT 400 invalidValue scim: displayName is required",
			}
			members, err := st.MembersOf(context.Background(), "acme-finance")
			if err != nil {
				t.Fatalf("MembersOf: %v", err)
			}
			if fmt.Sprint(got) != fmt.Sprint(want) || fmt.Sprint(members) != "[alice@acme.com]" {
				t.Fatalf("responses:\n%q\nwant:\n%q\nMembersOf(acme-finance) = %q, want [alice@acme.com]", got, want, members)
			}
		})
	}
}
