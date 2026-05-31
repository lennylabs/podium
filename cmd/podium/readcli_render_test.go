package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Spec: §7.6.1 Output formats — the default for `domain show` is a
// human-readable nested-bullet rendering; --json is the structured envelope
// (F-7.6.10).
func TestReadCLI_DomainShowHumanAndJSON(t *testing.T) {
	ts := newRegistryStub(t, map[string]any{
		"/v1/load_domain": map[string]any{
			"path":        "finance",
			"description": "Finance hub",
			"subdomains": []map[string]any{
				{"path": "finance/close", "name": "close", "description": "Month-end close"},
			},
			"notable": []map[string]any{
				{"id": "finance/run-variance", "type": "skill", "summary": "Variance analysis"},
			},
		},
	})
	human := captureStdout(t, func() { _ = domainShow([]string{"--registry", ts.URL, "finance"}) })
	for _, want := range []string{"finance", "Finance hub", "close", "Month-end close", "finance/run-variance", "Variance analysis"} {
		if !strings.Contains(human, want) {
			t.Errorf("human output missing %q\n%s", want, human)
		}
	}
	// The default rendering is not raw JSON.
	if strings.Contains(human, "\"path\":") {
		t.Errorf("default output should be human-readable, got JSON:\n%s", human)
	}
	js := captureStdout(t, func() { _ = domainShow([]string{"--registry", ts.URL, "--json", "finance"}) })
	if !strings.Contains(js, "\"path\"") {
		t.Errorf("--json output should be the raw envelope:\n%s", js)
	}
}

// Spec: §7.6.1 — `domain search` gains a --json flag; the default is a
// human-readable ranked list (F-7.6.10).
func TestReadCLI_DomainSearchHumanAndJSON(t *testing.T) {
	ts := newRegistryStub(t, map[string]any{
		"/v1/search_domains": map[string]any{
			"query":         "finance",
			"total_matched": 1,
			"domains": []map[string]any{
				{"path": "finance/close", "name": "close", "description": "Month-end close", "score": 0.9},
			},
		},
	})
	human := captureStdout(t, func() { _ = domainSearch([]string{"--registry", ts.URL, "finance"}) })
	if !strings.Contains(human, "Showing 1 of 1") || !strings.Contains(human, "close") {
		t.Errorf("human ranked list missing expected content:\n%s", human)
	}
	js := captureStdout(t, func() { _ = domainSearch([]string{"--registry", ts.URL, "--json", "finance"}) })
	if !strings.Contains(js, "\"total_matched\"") {
		t.Errorf("--json output should be the raw envelope:\n%s", js)
	}
}

// Spec: §7.6.1 — `artifact show` default prints the markdown body with
// frontmatter at the top; --json emits the structured envelope (F-7.6.10).
func TestReadCLI_ArtifactShowHumanAndJSON(t *testing.T) {
	ts := newRegistryStub(t, map[string]any{
		"/v1/load_artifact": map[string]any{
			"id":            "finance/run",
			"type":          "skill",
			"version":       "1.0.0",
			"frontmatter":   "---\ntype: skill\nname: run\n---\n",
			"manifest_body": "Run the report.",
		},
	})
	human := captureStdout(t, func() { _ = artifactShow([]string{"--registry", ts.URL, "finance/run"}) })
	if !strings.Contains(human, "name: run") || !strings.Contains(human, "Run the report.") {
		t.Errorf("human output missing frontmatter or body:\n%s", human)
	}
	// Default rendering does not wrap the body in a JSON object.
	if strings.Contains(human, "\"manifest_body\"") {
		t.Errorf("default output should be human-readable, got JSON:\n%s", human)
	}
	js := captureStdout(t, func() { _ = artifactShow([]string{"--registry", ts.URL, "--json", "finance/run"}) })
	if !strings.Contains(js, "\"manifest_body\"") {
		t.Errorf("--json output should be the raw envelope:\n%s", js)
	}
}

// Spec: §7.6 / §7.6.1 — the read CLI reaches the registry with the caller's
// identity: a configured session token is attached as the Bearer credential
// (F-7.6.13).
func TestReadCLI_AttachesSessionToken(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"query":"q","total_matched":0,"results":[]}`))
	}))
	t.Cleanup(ts.Close)
	t.Setenv("PODIUM_SESSION_TOKEN", "test-token")
	if rc := searchCmd([]string{"--registry", ts.URL, "q"}); rc != 0 {
		t.Fatalf("searchCmd rc = %d, want 0", rc)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-token")
	}
}

// Spec: §7.6.1 — with no credential configured the read CLI reaches the
// registry anonymously (no Authorization header), matching the prior behavior.
func TestReadCLI_NoTokenSendsNoAuthHeader(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"query":"q","total_matched":0,"results":[]}`))
	}))
	t.Cleanup(ts.Close)
	t.Setenv("PODIUM_SESSION_TOKEN", "")
	t.Setenv("PODIUM_SESSION_TOKEN_FILE", "")
	t.Setenv("PODIUM_TOKEN_KEYCHAIN_NAME", "podium-nonexistent-test-service")
	if rc := searchCmd([]string{"--registry", ts.URL, "q"}); rc != 0 {
		t.Fatalf("searchCmd rc = %d, want 0", rc)
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q, want empty", gotAuth)
	}
}
