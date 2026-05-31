package e2e

// End-to-end coverage for the §5.0 resource mirror (F-5.0.1) and the
// §5.2 conditional `prompts` capability (F-5.2.2). The resource mirror
// exposes artifact bodies through MCP's resources/list + resources/read
// as a read-only mirror of load_artifact. The prompts capability is
// advertised in initialize only when at least one command artifact opted
// into projection. Tests drive the podium-mcp bridge against a
// standalone server.

import (
	"strings"
	"testing"
)

// spec: §5.0 — resources/list mirrors the effective view and
// resources/read returns the artifact manifest body. F-5.0.1.
func TestMCP_ResourcesMirrorListAndRead(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"reference/glossary/ARTIFACT.md": contextArtifact("glossary"),
	}))
	env := mcpServerEnv(t, srv.BaseURL)

	listRes := mcpExec(t, env, rpcReq{ID: 1, Method: "resources/list", Params: map[string]any{}})
	listBody := mustJSON(rpcResult(t, listRes.Stdout, 1))
	if !strings.Contains(listBody, "podium://artifact/reference/glossary") {
		t.Errorf("resources/list missing the artifact URI:\n%s", listBody)
	}

	readRes := mcpExec(t, env, rpcReq{ID: 1, Method: "resources/read", Params: map[string]any{"uri": "podium://artifact/reference/glossary"}})
	result := rpcResult(t, readRes.Stdout, 1)
	contents, _ := result["contents"].([]any)
	if len(contents) == 0 {
		t.Fatalf("resources/read returned no contents: %v", result)
	}
	first, _ := contents[0].(map[string]any)
	if text, _ := first["text"].(string); !strings.Contains(text, "type: context") {
		t.Errorf("resources/read content missing manifest frontmatter: %q", text)
	}
	if mt, _ := first["mimeType"].(string); mt != "text/markdown" {
		t.Errorf("mimeType = %q, want text/markdown", mt)
	}
}

// spec: §5.0 — a URI that does not carry the podium artifact scheme is
// rejected with resources.invalid_argument.
func TestMCP_ResourcesReadRejectsBadURI(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"reference/glossary/ARTIFACT.md": contextArtifact("glossary"),
	}))
	res := mcpExec(t, mcpServerEnv(t, srv.BaseURL), rpcReq{ID: 1, Method: "resources/read", Params: map[string]any{"uri": "file:///etc/passwd"}})
	result := rpcResult(t, res.Stdout, 1)
	if e, _ := result["error"].(string); !strings.Contains(e, "resources.invalid_argument") {
		t.Errorf("error=%q, want resources.invalid_argument", e)
	}
}

// spec: §5.2 — initialize advertises the `prompts` capability only when
// at least one command artifact opted into projection. F-5.2.2.
func TestMCP_PromptsCapabilityConditional(t *testing.T) {
	t.Parallel()

	// With an opted-in command, prompts must be advertised.
	exposed := startServer(t, writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact}))
	res := mcpExec(t, mcpServerEnv(t, exposed.BaseURL),
		rpcReq{ID: 1, Method: "initialize", Params: map[string]any{"protocolVersion": "2024-11-05"}})
	caps, _ := rpcResult(t, res.Stdout, 1)["capabilities"].(map[string]any)
	if _, ok := caps["prompts"]; !ok {
		t.Errorf("initialize omitted prompts despite an opted-in command: %+v", caps)
	}

	// With only a non-command (or non-exposed) artifact, prompts is absent
	// while tools and resources remain present.
	none := startServer(t, writeRegistry(t, map[string]string{
		"reference/glossary/ARTIFACT.md": contextArtifact("glossary"),
	}))
	res2 := mcpExec(t, mcpServerEnv(t, none.BaseURL),
		rpcReq{ID: 1, Method: "initialize", Params: map[string]any{"protocolVersion": "2024-11-05"}})
	caps2, _ := rpcResult(t, res2.Stdout, 1)["capabilities"].(map[string]any)
	if _, ok := caps2["prompts"]; ok {
		t.Errorf("initialize advertised prompts with no opt-in: %+v", caps2)
	}
	if _, ok := caps2["tools"]; !ok {
		t.Errorf("initialize dropped tools capability: %+v", caps2)
	}
	if _, ok := caps2["resources"]; !ok {
		t.Errorf("initialize dropped resources capability: %+v", caps2)
	}
}
