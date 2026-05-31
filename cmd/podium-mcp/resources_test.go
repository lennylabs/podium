package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// resourcesFixture spins up a fake registry answering /v1/sync/manifest
// (the effective-view enumeration the resource mirror walks) and
// /v1/load_artifact, so the §5.0 resource handlers can be exercised
// offline.
func resourcesFixture(t *testing.T, manifests map[string]map[string]any) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/sync/manifest":
			arts := []map[string]any{}
			for id, m := range manifests {
				arts = append(arts, map[string]any{"id": id, "type": m["type"]})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"artifacts": arts})
		case "/v1/load_artifact":
			id := r.URL.Query().Get("id")
			if m, ok := manifests[id]; ok {
				_ = json.NewEncoder(w).Encode(m)
				return
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

// spec: §5.0 — resources/list mirrors the effective view, one resource
// per artifact, and filters out mcp-server artifacts as the bridge's
// tool results do (F-5.0.1).
func TestResources_ListMirrorsEffectiveView(t *testing.T) {
	t.Parallel()
	ts := resourcesFixture(t, map[string]map[string]any{
		"finance/close/checklist": {
			"id": "finance/close/checklist", "type": "context",
		},
		"ops/deploy-mcp": {
			"id": "ops/deploy-mcp", "type": "mcp-server",
		},
	})
	s := &mcpServer{cfg: &config{registry: ts.URL}, http: &http.Client{}}
	out, ok := s.handleResourcesList().(resourcesListResult)
	if !ok {
		t.Fatalf("handleResourcesList returned %T", s.handleResourcesList())
	}
	if len(out.Resources) != 1 {
		t.Fatalf("len = %d, want 1 (mcp-server filtered out): %+v", len(out.Resources), out.Resources)
	}
	got := out.Resources[0]
	if got.URI != "podium://artifact/finance/close/checklist" {
		t.Errorf("URI = %q", got.URI)
	}
	if got.Name != "finance/close/checklist" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.MimeType != resourceMimeType {
		t.Errorf("MimeType = %q, want %q", got.MimeType, resourceMimeType)
	}
}

// spec: §5.0 — resources/read returns the manifest (frontmatter + body)
// as the resource content, the read-only mirror of load_artifact.
func TestResources_ReadReturnsManifestBody(t *testing.T) {
	t.Parallel()
	fm := "---\ntype: context\nname: glossary\nversion: 1.0.0\n---\n"
	ts := resourcesFixture(t, map[string]map[string]any{
		"docs/glossary": {
			"id":            "docs/glossary",
			"type":          "context",
			"frontmatter":   fm,
			"manifest_body": "Term definitions.",
		},
	})
	s := &mcpServer{cfg: &config{registry: ts.URL}, http: &http.Client{}}
	out := s.handleResourcesRead(json.RawMessage(`{"uri":"podium://artifact/docs/glossary"}`))
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map", out)
	}
	contents, ok := m["contents"].([]map[string]any)
	if !ok {
		t.Fatalf("contents = %T", m["contents"])
	}
	if len(contents) != 1 {
		t.Fatalf("len(contents) = %d, want 1", len(contents))
	}
	text, _ := contents[0]["text"].(string)
	if !strings.Contains(text, "type: context") || !strings.Contains(text, "Term definitions.") {
		t.Errorf("text missing manifest content: %q", text)
	}
	if contents[0]["uri"] != "podium://artifact/docs/glossary" {
		t.Errorf("uri = %v", contents[0]["uri"])
	}
}

// spec: §5.0 — a URI that does not carry the podium artifact scheme is
// rejected without a registry round-trip.
func TestResources_ReadRejectsBadURI(t *testing.T) {
	t.Parallel()
	s := &mcpServer{cfg: &config{registry: "http://127.0.0.1:0"}, http: &http.Client{}}
	for _, uri := range []string{"", "file:///etc/passwd", "podium://artifact/"} {
		out := s.handleResourcesRead(json.RawMessage(`{"uri":"` + uri + `"}`))
		if !strings.Contains(errorMessageText(out), "resources.invalid_argument") {
			t.Errorf("uri %q: got %v, want resources.invalid_argument", uri, out)
		}
	}
}

// spec: §5.0 — a syntactically valid URI for an absent artifact returns
// resources.not_found when the registry yields an empty result.
func TestResources_ReadNotFound(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 200 with an empty id (not a 404) so the not_found synth path runs.
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)
	s := &mcpServer{cfg: &config{registry: ts.URL}, http: &http.Client{}}
	out := s.handleResourcesRead(json.RawMessage(`{"uri":"podium://artifact/absent"}`))
	if !strings.Contains(errorMessageText(out), "resources.not_found") {
		t.Errorf("got %v, want resources.not_found", out)
	}
}

func TestArtifactIDFromResourceURI(t *testing.T) {
	t.Parallel()
	cases := []struct {
		uri string
		id  string
		ok  bool
	}{
		{"podium://artifact/finance/close/checklist", "finance/close/checklist", true},
		{"podium://artifact/x", "x", true},
		{"podium://artifact/", "", false},
		{"podium://other/x", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		id, ok := artifactIDFromResourceURI(c.uri)
		if id != c.id || ok != c.ok {
			t.Errorf("%q: got (%q,%v), want (%q,%v)", c.uri, id, ok, c.id, c.ok)
		}
	}
}
