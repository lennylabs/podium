package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// spec: §7.6.1 — podium search forwards the --tags flag to the registry as
// the tags query parameter (F-7.6.4).
func TestSearchCmd_ForwardsTags(t *testing.T) {
	var gotTags string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTags = r.URL.Query().Get("tags")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query": "q", "total_matched": 0, "results": []any{},
		})
	}))
	defer ts.Close()
	if rc := searchCmd([]string{"--registry", ts.URL, "--tags", "finance,close", "q"}); rc != 0 {
		t.Fatalf("searchCmd rc = %d, want 0", rc)
	}
	if gotTags != "finance,close" {
		t.Errorf("tags param = %q, want finance,close", gotTags)
	}
}

// spec: §7.6.1 — podium artifact show forwards --version and --session-id to
// the registry's load_artifact endpoint (F-7.6.5, F-7.6.6).
func TestArtifactShow_ForwardsVersionAndSessionID(t *testing.T) {
	var gotVersion, gotSession string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotVersion = r.URL.Query().Get("version")
		gotSession = r.URL.Query().Get("session_id")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "x", "version": "1.0.0"})
	}))
	defer ts.Close()
	rc := artifactShow([]string{"--registry", ts.URL, "--version", "1.2.3", "--session-id", "sess-9", "x"})
	if rc != 0 {
		t.Fatalf("artifactShow rc = %d, want 0", rc)
	}
	if gotVersion != "1.2.3" {
		t.Errorf("version param = %q, want 1.2.3", gotVersion)
	}
	if gotSession != "sess-9" {
		t.Errorf("session_id param = %q, want sess-9", gotSession)
	}
}
