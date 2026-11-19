package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

// Spec: §7.3.1, §13.12 — the CLI does not follow a redirect that leaves the
// host it was given. A 307 to another host would otherwise resend the
// operator token header, which Go forwards to any host, and the JSON body,
// which can carry a repository token. A redirect to another path on the same
// host is followed with the token.
func TestDoJSON_RefusesRedirectToAnotherHost(t *testing.T) {
	type hit struct{ path, token, body string }
	var other []hit
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		other = append(other, hit{r.URL.Path, r.Header.Get(server.OperatorTokenHeader), string(b)})
		_, _ = w.Write([]byte(`{}`))
	}))
	defer elsewhere.Close()
	var sameHost []hit
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/layers/away":
			http.Redirect(w, r, elsewhere.URL+"/v1/layers", http.StatusTemporaryRedirect)
		case "/v1/layers/moved":
			http.Redirect(w, r, "/v1/layers", http.StatusPermanentRedirect)
		case "/v1/layers/loop":
			http.Redirect(w, r, "/v1/layers/loop", http.StatusTemporaryRedirect)
		default:
			b, _ := io.ReadAll(r.Body)
			sameHost = append(sameHost, hit{r.URL.Path, r.Header.Get(server.OperatorTokenHeader), string(b)})
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer registry.Close()

	t.Setenv("PODIUM_OPERATOR_TOKEN", "operator-secret")
	body := map[string]string{"repo_token": "repo-secret"}
	out, status := doJSON(registry.URL+"/v1/layers/away", http.MethodPost, body)
	if len(other) != 0 {
		t.Errorf("the other host received %+v", other)
	}
	if status == http.StatusOK || !strings.Contains(string(out), "redirect") {
		t.Errorf("cross-host redirect: status=%d body=%s, want a refusal naming the redirect", status, out)
	}

	out, status = doJSON(registry.URL+"/v1/layers/moved", http.MethodPost, body)
	if status != http.StatusOK {
		t.Fatalf("same-host redirect: status=%d body=%s", status, out)
	}
	if len(sameHost) != 1 || sameHost[0].token != "operator-secret" || !strings.Contains(sameHost[0].body, "repo-secret") {
		t.Errorf("same-host redirect target saw %+v, want the token and the body", sameHost)
	}

	if out, _ := doJSON(registry.URL+"/v1/layers/loop", http.MethodGet, nil); !strings.Contains(string(out), "stopped after 10 redirects") {
		t.Errorf("redirect loop: %s, want the hop limit", out)
	}
}

// Spec: §13.12 — a redirect from https to http on the same host is refused,
// because it would send the token in clear text; a redirect within one
// scheme and host, and one from http to https, is followed.
func TestSameHostRedirect(t *testing.T) {
	u := func(s string) *url.URL {
		v, err := url.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tc := range []struct {
		from, to string
		ok       bool
	}{
		{"https://reg.acme.com/v1/layers", "https://reg.acme.com/v1/x", true},
		{"https://reg.acme.com/v1/layers", "https://REG.acme.com/v1/x", true},
		{"http://reg.acme.com/v1/layers", "https://reg.acme.com/v1/x", true},
		{"https://reg.acme.com/v1/layers", "http://reg.acme.com/v1/x", false},
		{"https://reg.acme.com/v1/layers", "https://evil.example/v1/x", false},
		{"https://reg.acme.com/v1/layers", "https://reg.acme.com:8443/v1/x", false},
		{"https://reg.acme.com/v1/layers", "https://api.reg.acme.com/v1/x", false},
	} {
		if got := sameHostRedirect(u(tc.from), u(tc.to)); got != tc.ok {
			t.Errorf("%s -> %s: %v, want %v", tc.from, tc.to, got, tc.ok)
		}
	}
}
