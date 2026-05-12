package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// domain search happy path with scope + top-k.
func TestDomainSearch_HappyPath(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/search_domains" {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"total_matched":0,"domains":[]}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	captureStdout(t, func() {
		withStderr(t, func() {
			if rc := domainSearch([]string{"--scope", "finance", "--top-k", "5", "alpha"}); rc != 0 {
				t.Errorf("rc = %d", rc)
			}
		})
	})
	if !strings.Contains(gotQuery, "query=alpha") {
		t.Errorf("query missing alpha: %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "scope=finance") {
		t.Errorf("query missing scope: %q", gotQuery)
	}
}

// domain show happy path.
func TestDomainShow_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/load_domain" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"path":"finance","subdomains":[]}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	captureStdout(t, func() {
		withStderr(t, func() {
			if rc := domainShow([]string{"finance"}); rc != 0 {
				t.Errorf("rc = %d", rc)
			}
		})
	})
}

// domain show with --json flag.
func TestDomainShow_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"path":"","subdomains":[]}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	captureStdout(t, func() {
		withStderr(t, func() {
			if rc := domainShow([]string{"--json"}); rc != 0 {
				t.Errorf("rc = %d", rc)
			}
		})
	})
}

// artifactShow happy path.
func TestArtifactShow_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/load_artifact" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("id") != "team/x" {
			t.Errorf("query id = %q", r.URL.Query().Get("id"))
		}
		_, _ = w.Write([]byte(`{"id":"team/x","manifest_body":"body"}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	captureStdout(t, func() {
		withStderr(t, func() {
			if rc := artifactShow([]string{"team/x"}); rc != 0 {
				t.Errorf("rc = %d", rc)
			}
		})
	})
}

// search happy path with all flags.
func TestSearchCmd_AllFlags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("type") != "context" || q.Get("scope") != "finance" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"total_matched":1,"results":[{"id":"a","type":"context","description":"x"}]}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	out := captureStdout(t, func() {
		withStderr(t, func() {
			if rc := searchCmd([]string{
				"--type", "context", "--scope", "finance", "--top-k", "5",
				"alpha-query",
			}); rc != 0 {
				t.Errorf("rc = %d", rc)
			}
		})
	})
	if !strings.Contains(out, "a") {
		t.Errorf("output missing artifact: %s", out)
	}
}
