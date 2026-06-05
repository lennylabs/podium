package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/registryharness"
)

// Spec: §2.1 / §6.4 step 2 — the MCP bridge binary asks a roots-capable host
// for its workspace via roots/list and defaults the overlay to
// <workspace>/.podium/overlay/, so a later load_artifact serves the draft
// from the overlay without contacting the registry.
func TestPodiumMCP_RootsListResolvesWorkspaceOverlay(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t)
	ws := t.TempDir()
	testharness.WriteTree(t, ws, testharness.WriteTreeOption{
		Path:    ".podium/overlay/draft/ARTIFACT.md",
		Content: "---\ntype: context\nversion: 0.1.0\n---\n\noverlay draft body\n",
	})

	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"PODIUM_REGISTRY="+h.URL,
		"PODIUM_CACHE_DIR="+t.TempDir(),
		"PODIUM_OVERLAY_PATH=", // ensure no inherited explicit path short-circuits roots
	)

	// Three stdin lines: initialize (advertising the roots capability), the
	// host's reply to the bridge's server-initiated roots/list request, then
	// load_artifact for the draft that lives in the workspace overlay.
	initLine, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"roots": map[string]any{"listChanged": true}},
		},
	})
	rootsLine, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": "podium-roots-1",
		"result": map[string]any{"roots": []map[string]any{{"uri": "file://" + ws}}},
	})
	loadLine, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "load_artifact", "arguments": map[string]any{"id": "draft"}},
	})
	cmd.Stdin = bytes.NewReader(bytes.Join([][]byte{initLine, rootsLine, loadLine}, []byte("\n")))

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\nstdout: %s", err, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, `"roots/list"`) {
		t.Errorf("bridge did not emit a roots/list request:\n%s", out)
	}
	if !strings.Contains(out, "overlay") {
		t.Errorf("load_artifact did not serve the draft from the workspace overlay:\n%s", out)
	}
}
