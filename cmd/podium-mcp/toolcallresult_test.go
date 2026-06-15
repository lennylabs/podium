package main

import (
	"encoding/json"
	"testing"
)

// TestToolCallResult_Success proves a normal (non-error) domain result is
// wrapped in a valid MCP CallToolResult: a content[] text block carrying the
// JSON (so hosts render it), structuredContent carrying the typed object, and
// NO isError flag.
func TestToolCallResult_Success(t *testing.T) {
	// search_artifacts-shaped domain result (the exact shape that rendered
	// empty in Claude Code before the fix).
	domain := map[string]any{
		"query":         "what went wrong in this output",
		"results":       []any{map[string]any{"id": "log-triage", "score": 0.91}},
		"total_matched": 1,
	}

	out, ok := toolCallResult(domain).(map[string]any)
	if !ok {
		t.Fatalf("toolCallResult did not return a map; got %T", toolCallResult(domain))
	}

	// content MUST exist and be a non-empty array of {type:text,text:...}
	content, ok := out["content"].([]map[string]any)
	if !ok || len(content) == 0 {
		t.Fatalf("missing/empty content block: %#v", out["content"])
	}
	if content[0]["type"] != "text" {
		t.Fatalf("content[0].type = %v, want text", content[0]["type"])
	}
	text, _ := content[0]["text"].(string)
	if text == "" {
		t.Fatal("content[0].text is empty — host would render nothing")
	}
	// The text block must be the JSON of the domain object (round-trips).
	var back map[string]any
	if err := json.Unmarshal([]byte(text), &back); err != nil {
		t.Fatalf("content text is not valid JSON: %v", err)
	}
	if back["query"] != "what went wrong in this output" {
		t.Fatalf("content text lost data: %v", back["query"])
	}

	// structuredContent must be the original domain object.
	if out["structuredContent"] == nil {
		t.Fatal("structuredContent missing")
	}

	// Non-breaking contract: the domain object's own fields stay at the result
	// top level so existing consumers that read result.<field> keep working.
	if out["query"] != "what went wrong in this output" {
		t.Fatalf("top-level domain field not preserved: query=%v", out["query"])
	}
	if out["total_matched"] == nil {
		t.Fatal("top-level domain field total_matched not preserved")
	}

	// A successful result must NOT be flagged isError.
	if _, present := out["isError"]; present {
		t.Fatalf("isError should be absent for a success result; got %v", out["isError"])
	}
}

// TestToolCallResult_Error proves a §6.10 error envelope ({"error": ...}) is
// still wrapped with content AND flagged isError:true so the host marks it.
func TestToolCallResult_Error(t *testing.T) {
	domain := map[string]any{"error": "registry unauthorized"}

	out := toolCallResult(domain).(map[string]any)

	content, ok := out["content"].([]map[string]any)
	if !ok || len(content) == 0 || content[0]["text"] == "" {
		t.Fatalf("error result missing content block: %#v", out)
	}
	isErr, present := out["isError"]
	if !present || isErr != true {
		t.Fatalf("isError must be true for an error envelope; got present=%v val=%v", present, isErr)
	}
	// The error key stays at the top level (consumers and the integration
	// suite read result["error"] directly).
	if out["error"] != "registry unauthorized" {
		t.Fatalf("top-level error not preserved: %v", out["error"])
	}
}

// TestToolCallResult_IsValidMCPWire serializes the wrapped result exactly as
// the server writes it to stdout and asserts the on-the-wire JSON-RPC result
// has the fields an MCP host requires (content[].type/text).
func TestToolCallResult_IsValidMCPWire(t *testing.T) {
	domain := map[string]any{"results": []any{}, "total_matched": 0}
	wire, err := json.Marshal(toolCallResult(domain))
	if err != nil {
		t.Fatalf("wrapped result is not JSON-serializable: %v", err)
	}
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent map[string]any `json:"structuredContent"`
	}
	if err := json.Unmarshal(wire, &parsed); err != nil {
		t.Fatalf("wire JSON does not parse: %v", err)
	}
	if len(parsed.Content) == 0 || parsed.Content[0].Type != "text" || parsed.Content[0].Text == "" {
		t.Fatalf("wire result lacks a renderable content text block: %s", wire)
	}
	if parsed.StructuredContent == nil {
		t.Fatalf("wire result lacks structuredContent: %s", wire)
	}
}
