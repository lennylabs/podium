package e2e

// End-to-end coverage for the §5.0 resource mirror (F-5.0.1) and the
// MCP server's initialize capability set. The resource mirror exposes
// artifact bodies through MCP's resources/list + resources/read as a
// read-only mirror of load_artifact. Command artifacts are delivered
// through harness-native materialization (§5.2, §6.7), not an MCP prompt
// projection, so initialize advertises no `prompts` capability. Tests
// drive the podium-mcp bridge against a standalone server.

import (
	"strings"
	"testing"
)

// spec: §5.0 — resources/list mirrors the effective view and
// resources/read returns the artifact manifest body. F-5.0.1.
func TestMCPResources_MirrorListAndRead(t *testing.T) {
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
func TestMCPResources_ReadRejectsBadURI(t *testing.T) {
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

// Covers: spec §5.2 (Command Materialization), §5.0 — MCP prompt projection of
// commands was removed; commands materialize through the harness adapters (§6.7)
// instead. initialize advertises {tools, resources, sessionCorrelation} and no
// `prompts` capability, even when a command artifact is present.
func TestMCPInitialize_AdvertisesNoPromptsCapability(t *testing.T) {
	t.Parallel()

	// A command artifact in the view does not add a prompts capability.
	withCommand := startServer(t, writeRegistry(t, map[string]string{standupID + "/ARTIFACT.md": standupArtifact}))
	res := mcpExec(t, mcpServerEnv(t, withCommand.BaseURL),
		rpcReq{ID: 1, Method: "initialize", Params: map[string]any{"protocolVersion": "2024-11-05"}})
	caps, _ := rpcResult(t, res.Stdout, 1)["capabilities"].(map[string]any)
	if _, ok := caps["prompts"]; ok {
		t.Errorf("initialize advertised a prompts capability (projection was removed): %+v", caps)
	}

	// The advertised set is tools + resources + sessionCorrelation, both with
	// and without a command artifact.
	plain := startServer(t, writeRegistry(t, map[string]string{
		"reference/glossary/ARTIFACT.md": contextArtifact("glossary"),
	}))
	res2 := mcpExec(t, mcpServerEnv(t, plain.BaseURL),
		rpcReq{ID: 1, Method: "initialize", Params: map[string]any{"protocolVersion": "2024-11-05"}})
	caps2, _ := rpcResult(t, res2.Stdout, 1)["capabilities"].(map[string]any)
	if _, ok := caps2["prompts"]; ok {
		t.Errorf("initialize advertised prompts: %+v", caps2)
	}
	for _, want := range []string{"tools", "resources", "sessionCorrelation"} {
		if _, ok := caps2[want]; !ok {
			t.Errorf("initialize missing %q capability: %+v", want, caps2)
		}
	}
}
