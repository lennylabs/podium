package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// toolsByName drives a tools/list request through handle and indexes the
// returned tool objects by name.
func toolsByName(t *testing.T) map[string]map[string]any {
	t.Helper()
	srv := &mcpServer{cfg: &config{}}
	resp := srv.handle(rpcRequest{JSONRPC: "2.0", Method: "tools/list"})
	m, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T", resp.Result)
	}
	tools, ok := m["tools"].([]map[string]any)
	if !ok {
		t.Fatalf("tools type = %T", m["tools"])
	}
	out := map[string]map[string]any{}
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		out[name] = tool
	}
	return out
}

// spec: §5.1 — the strings under each tool heading "are the canonical tool
// descriptions exposed to the LLM via MCP"; hosts "SHOULD use them verbatim."
// The bridge must emit the full multi-sentence canonical string, not a
// first-sentence truncation.
func TestToolsList_DescriptionsAreCanonicalVerbatim(t *testing.T) {
	t.Parallel()
	tools := toolsByName(t)
	want := map[string]string{
		"load_domain":      descLoadDomain,
		"search_domains":   descSearchDomains,
		"search_artifacts": descSearchArtifacts,
		"load_artifact":    descLoadArtifact,
	}
	for name, canonical := range want {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("tool %q missing from tools/list", name)
		}
		got, _ := tool["description"].(string)
		if got != canonical {
			t.Errorf("%s description not verbatim §5.1:\n got: %q\nwant: %q", name, got, canonical)
		}
		// Guard against re-truncation to the first sentence: each canonical
		// string carries cross-tool navigation guidance after the first
		// period that a first-sentence truncation would drop.
		if !strings.Contains(got, "`") && name != "load_artifact" {
			t.Errorf("%s description looks truncated (no backticked guidance): %q", name, got)
		}
	}
	// The pre-fix truncations are gone.
	if d := tools["load_domain"]["description"].(string); !strings.Contains(d, "call `load_artifact`") {
		t.Errorf("load_domain dropped the cross-reference guidance: %q", d)
	}
	if d := tools["search_domains"]["description"].(string); !strings.Contains(d, "search_artifacts") {
		t.Errorf("search_domains dropped the cross-reference guidance: %q", d)
	}
}

// spec: §5.1 / §5 — under MCP protocol 2024-11-05 each tools/list entry
// carries an inputSchema describing its parameters. Every meta-tool must
// declare the documented arguments.
func TestToolsList_DeclaresInputSchemaForEveryMetaTool(t *testing.T) {
	t.Parallel()
	tools := toolsByName(t)
	wantProps := map[string][]string{
		"load_domain":      {"path", "depth", "session_id"},
		"search_domains":   {"query", "scope", "top_k", "session_id"},
		"search_artifacts": {"query", "type", "tags", "scope", "top_k", "session_id"},
		"load_artifact":    {"id", "version", "harness", "session_id", "destination"},
	}
	for name, props := range wantProps {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("tool %q missing", name)
		}
		schema, ok := tool["inputSchema"].(map[string]any)
		if !ok {
			t.Fatalf("%s has no inputSchema (type %T)", name, tool["inputSchema"])
		}
		if schema["type"] != "object" {
			t.Errorf("%s inputSchema type = %v, want object", name, schema["type"])
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s inputSchema has no properties object", name)
		}
		for _, p := range props {
			if _, has := properties[p]; !has {
				t.Errorf("%s inputSchema missing documented property %q", name, p)
			}
		}
	}
	// spec: §5.1 — load_artifact's only required argument is `id`.
	la := tools["load_artifact"]["inputSchema"].(map[string]any)
	req, _ := la["required"].([]string)
	if len(req) != 1 || req[0] != "id" {
		t.Errorf("load_artifact required = %v, want [id]", req)
	}
}

// The whole tools/list payload must round-trip through JSON without losing
// the inputSchema (a strict MCP client decodes it as JSON, not Go maps).
func TestToolsList_JSONRoundTripsInputSchema(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}}
	resp := srv.handle(rpcRequest{JSONRPC: "2.0", Method: "tools/list"})
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, tool := range decoded.Tools {
		if len(tool.InputSchema) == 0 || string(tool.InputSchema) == "null" {
			t.Errorf("tool %q serialized with no inputSchema", tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("tool %q serialized with empty description", tool.Name)
		}
	}
}

// spec: §5.1 — the example system-prompt fragment must be obtainable by a
// host programmatically. The bridge surfaces it through the MCP initialize
// result's `instructions` field.
func TestInitialize_SurfacesSystemPromptFragment(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}}
	params, _ := json.Marshal(map[string]string{"protocolVersion": protocolVersion})
	resp := srv.handle(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "initialize", Params: params})
	m, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T", resp.Result)
	}
	got, _ := m["instructions"].(string)
	if got != systemPromptFragment {
		t.Errorf("initialize instructions != §5.1 fragment:\n got: %q\nwant: %q", got, systemPromptFragment)
	}
	// Anchor on canonical phrases so a future reword of the fragment that
	// drifts from §5.1 is caught.
	for _, want := range []string{
		"You have access to a catalog of authored skills and agents through the Podium meta-tools",
		"Sessions start empty",
		"load_artifact only when",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("fragment missing canonical phrase %q", want)
		}
	}
}
