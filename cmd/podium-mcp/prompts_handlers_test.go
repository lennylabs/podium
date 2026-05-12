package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
)

// handlePromptsList queries search_artifacts with type=command and
// returns the filtered prompt descriptors.
func TestHandlePromptsList_FiltersByOptIn(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/search_artifacts":
			_, _ = w.Write([]byte(`{"results":[
				{"id":"cmd-opted-in","description":"opted in"},
				{"id":"cmd-not-opted","description":"not"}
			]}`))
		case "/v1/load_artifact":
			id := r.URL.Query().Get("id")
			if id == "cmd-opted-in" {
				_, _ = w.Write([]byte(`{
					"id":"cmd-opted-in","type":"command","version":"1.0.0",
					"content_hash":"sha256:abc","manifest_body":"cmd body",
					"frontmatter":"---\ntype: command\nversion: 1.0.0\nexpose_as_mcp_prompt: true\nname: opted\n---\n"
				}`))
			} else {
				_, _ = w.Write([]byte(`{
					"id":"cmd-not-opted","type":"command","version":"1.0.0",
					"content_hash":"sha256:def","manifest_body":"body2",
					"frontmatter":"---\ntype: command\nversion: 1.0.0\nname: not\n---\n"
				}`))
			}
		}
	}))
	defer srv.Close()
	s := newTestServer(t, &config{registry: srv.URL, verifyPolicy: sign.PolicyNever})
	got := s.handlePromptsList()
	result, ok := got.(promptsListResult)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if len(result.Prompts) != 1 || result.Prompts[0].Name != "cmd-opted-in" {
		t.Errorf("prompts = %+v", result.Prompts)
	}
}

// handlePromptsList propagates fetch errors.
func TestHandlePromptsList_FetchErrorReturnsErrorResult(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &config{registry: "http://127.0.0.1:1", verifyPolicy: sign.PolicyNever})
	got := s.handlePromptsList()
	if m, ok := got.(map[string]any); !ok || m["error"] == nil {
		t.Errorf("expected error result, got %v", got)
	}
}

// handlePromptsGet returns the prompt's body as a user message.
func TestHandlePromptsGet_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"id":"cmd-x","type":"command","version":"1.0.0",
			"content_hash":"sha256:abc","manifest_body":"hello prompt",
			"frontmatter":"---\ntype: command\nversion: 1.0.0\nname: cmd-x\ndescription: a command\nexpose_as_mcp_prompt: true\n---\n"
		}`))
	}))
	defer srv.Close()
	s := newTestServer(t, &config{registry: srv.URL, verifyPolicy: sign.PolicyNever})
	got := s.handlePromptsGet([]byte(`{"name":"cmd-x"}`))
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if m["description"] != "a command" {
		t.Errorf("description = %v", m["description"])
	}
	msgs, _ := m["messages"].([]map[string]any)
	if len(msgs) != 1 || msgs[0]["role"] != "user" {
		t.Errorf("messages = %v", m["messages"])
	}
}

func TestHandlePromptsGet_MalformedRaw(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &config{verifyPolicy: sign.PolicyNever})
	got := s.handlePromptsGet([]byte("not json"))
	if m, ok := got.(map[string]any); !ok || m["error"] == nil {
		t.Errorf("expected error result, got %v", got)
	}
}

func TestHandlePromptsGet_MissingName(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &config{verifyPolicy: sign.PolicyNever})
	got := s.handlePromptsGet([]byte(`{}`))
	m, _ := got.(map[string]any)
	if !strings.Contains(m["error"].(string), "name") {
		t.Errorf("error = %v", m["error"])
	}
}

func TestHandlePromptsGet_NotFound(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`)) // empty id field
	}))
	defer srv.Close()
	s := newTestServer(t, &config{registry: srv.URL, verifyPolicy: sign.PolicyNever})
	got := s.handlePromptsGet([]byte(`{"name":"ghost"}`))
	m, _ := got.(map[string]any)
	if !strings.Contains(m["error"].(string), "not_found") {
		t.Errorf("error = %v", m["error"])
	}
}

func TestHandlePromptsGet_NotCommandType(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"id":"skill","type":"skill","version":"1.0.0","content_hash":"sha256:x",
			"frontmatter":"---\ntype: skill\nversion: 1.0.0\nname: x\n---\n"
		}`))
	}))
	defer srv.Close()
	s := newTestServer(t, &config{registry: srv.URL, verifyPolicy: sign.PolicyNever})
	got := s.handlePromptsGet([]byte(`{"name":"skill"}`))
	m, _ := got.(map[string]any)
	if !strings.Contains(m["error"].(string), "not_a_command") {
		t.Errorf("error = %v", m["error"])
	}
}

func TestHandlePromptsGet_NotExposed(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"id":"cmd","type":"command","version":"1.0.0","content_hash":"sha256:x",
			"frontmatter":"---\ntype: command\nversion: 1.0.0\nname: cmd\n---\n"
		}`))
	}))
	defer srv.Close()
	s := newTestServer(t, &config{registry: srv.URL, verifyPolicy: sign.PolicyNever})
	got := s.handlePromptsGet([]byte(`{"name":"cmd"}`))
	m, _ := got.(map[string]any)
	if !strings.Contains(m["error"].(string), "not_exposed") {
		t.Errorf("error = %v", m["error"])
	}
}

// handle dispatches prompts/list and prompts/get to the right handlers.
func TestHandle_PromptsListAndGet(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()
	s := newTestServer(t, &config{registry: srv.URL, verifyPolicy: sign.PolicyNever})

	respList := s.handle(rpcRequest{JSONRPC: "2.0", Method: "prompts/list"})
	if respList.Error != nil {
		t.Errorf("prompts/list err = %v", respList.Error)
	}
	paramsGet, _ := json.Marshal(map[string]string{"name": ""})
	respGet := s.handle(rpcRequest{JSONRPC: "2.0", Method: "prompts/get", Params: paramsGet})
	// Get returned an errorResult inside the result; the response
	// itself doesn't fail. Just verify it ran.
	if respGet.Result == nil {
		t.Errorf("prompts/get result nil")
	}
}
