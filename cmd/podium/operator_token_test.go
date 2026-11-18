package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
)

// Spec: §7.3.1, §13.10 — the CLI sends PODIUM_OPERATOR_TOKEN in the operator
// token header on a re-embed, overlap rebuild, erasure, or layer request and
// on no other request, and sends nothing when the variable is unset.
func TestDoJSON_SendsOperatorTokenOnlyToGatedEndpoints(t *testing.T) {
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = r.Header.Get(server.OperatorTokenHeader)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	t.Setenv("PODIUM_OPERATOR_TOKEN", "operator-secret")
	want := map[string]string{
		"/v1/admin/reembed":         "operator-secret",
		"/v1/admin/overlap/rebuild": "operator-secret",
		"/v1/admin/erase":           "operator-secret",
		"/v1/layers":                "operator-secret",
		"/v1/layers/reingest":       "operator-secret",
		"/v1/layers/update":         "operator-secret",
		"/v1/usage":                 "",
		"/v1/layerset":              "",
	}
	for path := range want {
		doJSON(srv.URL+path+"?id=x", http.MethodPost, nil)
	}
	for path, token := range want {
		if seen[path] != token {
			t.Errorf("%s carried %q, want %q", path, seen[path], token)
		}
	}

	t.Setenv("PODIUM_OPERATOR_TOKEN", "")
	doJSON(srv.URL+"/v1/layers", http.MethodPost, nil)
	if got := seen["/v1/layers"]; got != "" {
		t.Errorf("unset variable: /v1/layers carried %q, want none", got)
	}
	if sendsOperatorToken("://not a url") {
		t.Error("an unparseable URL counted as a gated endpoint")
	}
}
