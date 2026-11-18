package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
)

// Spec: §13.10 — the CLI sends PODIUM_OPERATOR_TOKEN in the operator token
// header on a re-embed or overlap rebuild request and on no other request,
// and sends nothing when the variable is unset.
func TestDoJSON_SendsOperatorTokenOnlyToThePasses(t *testing.T) {
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = r.Header.Get(server.OperatorTokenHeader)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	t.Setenv("PODIUM_OPERATOR_TOKEN", "operator-secret")
	for _, path := range []string{"/v1/admin/reembed", "/v1/admin/overlap/rebuild", "/v1/layers"} {
		doJSON(srv.URL+path+"?only_missing=true", http.MethodPost, nil)
	}
	for path, want := range map[string]string{
		"/v1/admin/reembed":         "operator-secret",
		"/v1/admin/overlap/rebuild": "operator-secret",
		"/v1/layers":                "",
	} {
		if seen[path] != want {
			t.Errorf("%s carried %q, want %q", path, seen[path], want)
		}
	}

	t.Setenv("PODIUM_OPERATOR_TOKEN", "")
	doJSON(srv.URL+"/v1/admin/reembed", http.MethodPost, nil)
	if got := seen["/v1/admin/reembed"]; got != "" {
		t.Errorf("unset variable: /v1/admin/reembed carried %q, want none", got)
	}
	if operatorPass("://not a url") {
		t.Error("an unparseable URL counted as an operator pass")
	}
}
