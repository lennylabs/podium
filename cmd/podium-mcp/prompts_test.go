package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// promptsFixture spins up a fake registry that answers
// /v1/search_artifacts and /v1/load_artifact with the supplied
// shapes so the prompts paths can be exercised offline.
func promptsFixture(t *testing.T, results []map[string]any, manifests map[string]map[string]any) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/search_artifacts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total_matched": len(results),
				"results":       results,
			})
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

// Spec: §5.2 — prompts/list returns one entry per `type: command`
// artifact whose frontmatter declares `expose_as_mcp_prompt: true`.
func TestPrompts_ListFiltersToOptIns(t *testing.T) {
	t.Parallel()
	ts := promptsFixture(t,
		[]map[string]any{
			{"id": "ops/restart", "description": "restart"},
			{"id": "ops/audit", "description": "audit"},
		},
		map[string]map[string]any{
			"ops/restart": {
				"id":            "ops/restart",
				"type":          "command",
				"manifest_body": "Restart the service.",
				"frontmatter":   "---\ntype: command\nname: restart\ndescription: restart\nversion: 1.0.0\nexpose_as_mcp_prompt: true\n---\n",
			},
			"ops/audit": {
				"id":            "ops/audit",
				"type":          "command",
				"manifest_body": "Run audit.",
				"frontmatter":   "---\ntype: command\nname: audit\ndescription: audit\nversion: 1.0.0\n---\n",
			},
		},
	)
	srv := &mcpServer{cfg: &config{registry: ts.URL}, http: &http.Client{}}
	out, ok := srv.handlePromptsList().(promptsListResult)
	if !ok {
		t.Fatalf("handlePromptsList returned %T", srv.handlePromptsList())
	}
	if len(out.Prompts) != 1 {
		t.Fatalf("len = %d, want 1", len(out.Prompts))
	}
	if out.Prompts[0].Name != "ops/restart" {
		t.Errorf("Name = %q, want ops/restart", out.Prompts[0].Name)
	}
}

// Spec: §5.2 — prompts/get returns the manifest body as the
// user-message text for an opted-in command.
func TestPrompts_GetReturnsBodyAsUserMessage(t *testing.T) {
	t.Parallel()
	ts := promptsFixture(t, nil,
		map[string]map[string]any{
			"ops/restart": {
				"id":            "ops/restart",
				"type":          "command",
				"manifest_body": "Restart the service safely.",
				"frontmatter":   "---\ntype: command\nname: restart\ndescription: restart\nversion: 1.0.0\nexpose_as_mcp_prompt: true\n---\n",
			},
		},
	)
	srv := &mcpServer{cfg: &config{registry: ts.URL}, http: &http.Client{}}
	out := srv.handlePromptsGet(json.RawMessage(`{"name":"ops/restart"}`))
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map", out)
	}
	if m["description"] != "restart" {
		t.Errorf("description = %v, want restart", m["description"])
	}
	// The prompts/get reply uses []map[string]any; tests run
	// without a JSON round-trip so the type stays concrete.
	messages, ok := m["messages"].([]map[string]any)
	if !ok {
		t.Fatalf("messages = %T, want []map[string]any", m["messages"])
	}
	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(messages))
	}
	content, _ := messages[0]["content"].(map[string]any)
	if got, _ := content["text"].(string); got != "Restart the service safely." {
		t.Errorf("text = %q", got)
	}
}

// Spec: §5.2 — a command artifact without `expose_as_mcp_prompt`
// is not addressable through prompts/get.
func TestPrompts_GetRefusesUnopted(t *testing.T) {
	t.Parallel()
	ts := promptsFixture(t, nil,
		map[string]map[string]any{
			"ops/audit": {
				"id":            "ops/audit",
				"type":          "command",
				"manifest_body": "audit body",
				"frontmatter":   "---\ntype: command\nname: audit\ndescription: audit\nversion: 1.0.0\n---\n",
			},
		},
	)
	srv := &mcpServer{cfg: &config{registry: ts.URL}, http: &http.Client{}}
	out := srv.handlePromptsGet(json.RawMessage(`{"name":"ops/audit"}`))
	body := errorMessageText(out)
	if !strings.Contains(body, "prompts.not_exposed") {
		t.Errorf("error = %q, want prompts.not_exposed", body)
	}
}

// Spec: §5.2 — a non-command artifact is rejected.
func TestPrompts_GetRefusesNonCommand(t *testing.T) {
	t.Parallel()
	ts := promptsFixture(t, nil,
		map[string]map[string]any{
			"docs/glossary": {
				"id":            "docs/glossary",
				"type":          "context",
				"manifest_body": "glossary",
				"frontmatter":   "---\ntype: context\nname: glossary\ndescription: glossary\nversion: 1.0.0\nexpose_as_mcp_prompt: true\n---\n",
			},
		},
	)
	srv := &mcpServer{cfg: &config{registry: ts.URL}, http: &http.Client{}}
	out := srv.handlePromptsGet(json.RawMessage(`{"name":"docs/glossary"}`))
	body := errorMessageText(out)
	if !strings.Contains(body, "prompts.not_a_command") {
		t.Errorf("error = %q, want prompts.not_a_command", body)
	}
}

// Spec: §5.2 — missing artifact returns prompts.not_found.
func TestPrompts_GetMissing(t *testing.T) {
	t.Parallel()
	ts := promptsFixture(t, nil, map[string]map[string]any{})
	srv := &mcpServer{cfg: &config{registry: ts.URL}, http: &http.Client{}}
	out := srv.handlePromptsGet(json.RawMessage(`{"name":"absent"}`))
	body := errorMessageText(out)
	if body == "" {
		t.Errorf("expected error envelope, got empty")
	}
	// 404 on /v1/load_artifact is acceptable; the message just
	// has to flag that the artifact wasn't found.
	if !strings.Contains(body, "404") && !strings.Contains(body, "not_found") {
		t.Errorf("error = %q, want a not-found-style message", body)
	}
}
