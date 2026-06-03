package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// scopePreviewStub returns an httptest server whose /v1/scope/preview answers
// with the given status and body; any other path 404s.
func scopePreviewStub(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/scope/preview" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// Spec: §3.5 (F-3.5.3) — the MCP bridge exposes the scope preview as a
// transparency tool. tools/list advertises scope_preview and tools/call
// proxies GET /v1/scope/preview, returning the aggregate counts verbatim.
func TestScopePreviewTool_ListedAndProxies(t *testing.T) {
	t.Parallel()
	ts := scopePreviewStub(t, http.StatusOK,
		`{"layers":["alice-personal"],"artifact_count":9,"by_type":{"skill":9},"by_sensitivity":{"low":9}}`)
	s := newHealthServer(ts.URL)

	listed := s.handle(rpcRequest{Method: "tools/list"})
	listBody, _ := json.Marshal(listed.Result)
	if !strings.Contains(string(listBody), `"scope_preview"`) {
		t.Errorf("tools/list missing scope_preview: %s", listBody)
	}

	params, _ := json.Marshal(toolCallParams{Name: "scope_preview"})
	out := s.callTool(params)
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("callTool(scope_preview) = %T, want map", out)
	}
	if m["artifact_count"].(float64) != 9 {
		t.Errorf("artifact_count = %v, want 9", m["artifact_count"])
	}
	if _, isErr := m["error"]; isErr {
		t.Errorf("unexpected error envelope: %v", m)
	}
}

// Spec: §3.5 (F-3.5.1/F-3.5.3) — when the tenant gate is off the endpoint
// answers 403 config.scope_preview_disabled, and the MCP tool surfaces that
// structured error rather than masking it as offline.
func TestScopePreviewTool_DisabledSurfaces403(t *testing.T) {
	t.Parallel()
	ts := scopePreviewStub(t, http.StatusForbidden,
		`{"code":"config.scope_preview_disabled","message":"scope preview is disabled for this tenant"}`)
	s := newHealthServer(ts.URL)

	params, _ := json.Marshal(toolCallParams{Name: "scope_preview"})
	out := s.callTool(params)
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("callTool(scope_preview) = %T, want map", out)
	}
	if m["code"] != "config.scope_preview_disabled" {
		t.Errorf("code = %v, want config.scope_preview_disabled (full envelope: %v)", m["code"], m)
	}
}
