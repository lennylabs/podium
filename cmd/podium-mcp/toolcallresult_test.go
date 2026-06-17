package main

import (
	"encoding/json"
	"fmt"
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

	// structuredContent must be the original domain object, with its fields
	// intact.
	sc, ok := out["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent missing or not an object: %#v", out["structuredContent"])
	}
	if sc["query"] != "what went wrong in this output" || sc["total_matched"] != 1 {
		t.Fatalf("structuredContent lost data: %#v", sc)
	}

	// The domain fields live under structuredContent, not at the result top
	// level. The top level carries only the MCP envelope keys.
	if _, present := out["query"]; present {
		t.Errorf("domain field query must not appear at the result top level: %#v", out)
	}
	if _, present := out["total_matched"]; present {
		t.Errorf("domain field total_matched must not appear at the result top level: %#v", out)
	}
	assertEnvelopeKeysOnly(t, out, false)

	// A successful result must NOT be flagged isError.
	if _, present := out["isError"]; present {
		t.Fatalf("isError should be absent for a success result; got %v", out["isError"])
	}
}

// TestToolCallResult_Error proves a §6.10 error envelope ({"error": ...}) is
// still wrapped with content AND flagged isError:true so the host marks it.
// The error envelope itself stays under structuredContent.
func TestToolCallResult_Error(t *testing.T) {
	domain := map[string]any{"error": "registry unauthorized", "code": "auth.forbidden"}

	out := toolCallResult(domain).(map[string]any)

	content, ok := out["content"].([]map[string]any)
	if !ok || len(content) == 0 || content[0]["text"] == "" {
		t.Fatalf("error result missing content block: %#v", out)
	}
	isErr, present := out["isError"]
	if !present || isErr != true {
		t.Fatalf("isError must be true for an error envelope; got present=%v val=%v", present, isErr)
	}

	// The error envelope is reachable under structuredContent, not at the top
	// level.
	if _, present := out["error"]; present {
		t.Errorf("error must not appear at the result top level: %#v", out)
	}
	sc, ok := out["structuredContent"].(map[string]any)
	if !ok || sc["error"] != "registry unauthorized" {
		t.Fatalf("structuredContent did not carry the error envelope: %#v", out["structuredContent"])
	}
	assertEnvelopeKeysOnly(t, out, true)
}

// TestToolCallResult_IsValidMCPWire serializes the wrapped result exactly as
// the server writes it to stdout and asserts the on-the-wire JSON-RPC result
// has the fields an MCP host requires (content[].type/text + structuredContent).
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
	if _, present := parsed.StructuredContent["total_matched"]; !present {
		t.Fatalf("structuredContent lost the domain fields on the wire: %s", wire)
	}
}

// TestToolCallResult_MarshalFallback covers the branch where json.MarshalIndent
// fails (a value json cannot marshal). The wrapper falls back to a fmt rendering
// for the content text instead of panicking, and still carries
// structuredContent. No meta-tool produces such a result; the test exercises
// the guard directly.
func TestToolCallResult_MarshalFallback(t *testing.T) {
	domain := map[string]any{"bad": make(chan int)} // channels are not JSON-marshalable

	out, ok := toolCallResult(domain).(map[string]any)
	if !ok {
		t.Fatalf("toolCallResult did not return a map; got %T", toolCallResult(domain))
	}
	content, ok := out["content"].([]map[string]any)
	if !ok || len(content) == 0 {
		t.Fatalf("missing content block on marshal fallback: %#v", out)
	}
	text, _ := content[0]["text"].(string)
	if text != fmt.Sprintf("%v", domain) {
		t.Fatalf("content text = %q, want the fmt fallback rendering", text)
	}
	if out["structuredContent"] == nil {
		t.Fatal("structuredContent missing on marshal fallback")
	}
}

// assertEnvelopeKeysOnly asserts the wrapped result's top level carries only
// the MCP CallToolResult envelope keys (content, structuredContent, and
// isError when wantIsError), so no domain field leaks to the top level.
func assertEnvelopeKeysOnly(t *testing.T, out map[string]any, wantIsError bool) {
	t.Helper()
	allowed := map[string]bool{"content": true, "structuredContent": true}
	if wantIsError {
		allowed["isError"] = true
	}
	for k := range out {
		if !allowed[k] {
			t.Errorf("unexpected top-level key %q on the CallToolResult envelope: %#v", k, out)
		}
	}
}
