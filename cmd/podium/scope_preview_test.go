package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// scopePreviewServer returns an httptest server whose /v1/scope/preview
// answers with the given status and body. Any other path 404s.
func scopePreviewServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/scope/preview" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// Spec: §3.5 — fetchScopePreview GETs the endpoint and
// decodes the aggregate-count envelope for the caller's effective view.
func TestFetchScopePreview_Decodes(t *testing.T) {
	ts := scopePreviewServer(t, http.StatusOK,
		`{"layers":["alice-personal"],"artifact_count":3,"by_type":{"skill":2,"agent":1},"by_sensitivity":{"low":2,"high":1}}`)

	p, err := fetchScopePreview(ts.URL)
	if err != nil {
		t.Fatalf("fetchScopePreview: %v", err)
	}
	if p.ArtifactCount != 3 {
		t.Errorf("artifact_count = %d, want 3", p.ArtifactCount)
	}
	if p.ByType["skill"] != 2 || p.ByType["agent"] != 1 {
		t.Errorf("by_type = %v", p.ByType)
	}
	if p.BySensitivity["high"] != 1 {
		t.Errorf("by_sensitivity = %v", p.BySensitivity)
	}
}

// Spec: §3.5 — a tenant with expose_scope_preview false makes the
// endpoint answer 403, which fetchScopePreview maps to the disabled sentinel.
func TestFetchScopePreview_Disabled(t *testing.T) {
	ts := scopePreviewServer(t, http.StatusForbidden, `{"code":"config.scope_preview_disabled"}`)

	_, err := fetchScopePreview(ts.URL)
	if !isScopePreviewDisabled(err) {
		t.Fatalf("err = %v, want config.scope_preview_disabled sentinel", err)
	}
}

// Spec: §3.5 — printScopePreview output is deterministic: count-map keys are
// sorted so the same preview prints identically across runs (Go map iteration
// order is otherwise random).
func TestPrintScopePreview_Deterministic(t *testing.T) {
	p := &scopePreview{
		Layers:        []string{"low", "mid", "high"},
		ArtifactCount: 5,
		ByType:        map[string]int{"skill": 3, "agent": 2},
		BySensitivity: map[string]int{"low": 4, "high": 1},
	}
	var first string
	for i := 0; i < 20; i++ {
		var sb strings.Builder
		printScopePreview(&sb, p)
		got := sb.String()
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("non-deterministic output:\n%q\nvs\n%q", first, got)
		}
	}
	if !strings.Contains(first, "artifacts:        5") {
		t.Errorf("missing artifact count:\n%s", first)
	}
	// agent must sort before skill under "by type".
	if strings.Index(first, "agent") > strings.Index(first, "skill") {
		t.Errorf("by_type keys not sorted:\n%s", first)
	}
}

// Spec: §3.5 — `podium status` surfaces the scope-preview aggregate
// counts for human inspection alongside the other client diagnostics.
func TestStatus_SurfacesScopePreview(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"mode":"ready","ready":true}`))
		case "/v1/scope/preview":
			_, _ = w.Write([]byte(`{"layers":["alice-personal"],"artifact_count":7,"by_type":{"skill":7},"by_sensitivity":{"low":7}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	t.Setenv("PODIUM_REGISTRY", ts.URL)

	out := captureStdout(t, func() {
		if rc := statusCmd(nil); rc != 0 {
			t.Errorf("rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "scope preview:") {
		t.Errorf("status output missing scope preview section:\n%s", out)
	}
	if !strings.Contains(out, "artifacts:        7") {
		t.Errorf("status output missing artifact count:\n%s", out)
	}
}

// Spec: §3.5 — a tenant gate refusal is surfaced as a disabled
// notice rather than a hard error, so status still exits cleanly.
func TestStatus_ScopePreviewDisabled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"mode":"ready","ready":true}`))
		case "/v1/scope/preview":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"config.scope_preview_disabled"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	t.Setenv("PODIUM_REGISTRY", ts.URL)

	out := captureStdout(t, func() {
		if rc := statusCmd(nil); rc != 0 {
			t.Errorf("rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "disabled by tenant config") {
		t.Errorf("status output missing disabled notice:\n%s", out)
	}
}

// Spec: §3.5 — `podium sync --preview` against a server registry
// prints the aggregate counts and writes nothing.
func TestSyncPreview_ServerRegistry(t *testing.T) {
	ts := scopePreviewServer(t, http.StatusOK,
		`{"layers":["alice-personal"],"artifact_count":4,"by_type":{"skill":4},"by_sensitivity":{"low":4}}`)
	// Isolate from any developer sync.yaml in the working tree.
	t.Setenv("HOME", t.TempDir())

	out := captureStdout(t, func() {
		if rc := runScopePreview(ts.URL, false); rc != 0 {
			t.Errorf("rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "artifacts:        4") {
		t.Errorf("sync --preview output missing counts:\n%s", out)
	}
}

// Spec: §3.5 — the preview is served by GET /v1/scope/preview, so
// --preview against a filesystem-source registry is rejected with exit 2
// rather than silently doing nothing.
func TestSyncPreview_FilesystemRejected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	rc := syncCmd([]string{"--preview", "--registry", t.TempDir()})
	if rc != 2 {
		t.Errorf("rc = %d, want 2 for filesystem source", rc)
	}
}
