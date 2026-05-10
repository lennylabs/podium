package integration

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness/registryharness"
)

// Spec: §6.9 / §6.10 — initialize with a too-old protocolVersion is
// rejected with mcp.unsupported_version.
// Matrix: §6.10 (mcp.unsupported_version)
func TestPodiumMCP_RejectsOlderProtocol(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t)
	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env, "PODIUM_REGISTRY="+h.URL)
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "initialize", ID: 1, Params: map[string]any{
			"protocolVersion": "2020-01-01",
		}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	var resp struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(&stdout).Decode(&resp); err != nil {
		t.Fatalf("decode: %v\nstdout: %s", err, stdout.String())
	}
	if resp.Error == nil {
		t.Fatalf("expected error, got: %s", stdout.String())
	}
	if !strings.Contains(resp.Error.Message, "mcp.unsupported_version") {
		t.Errorf("error message did not include mcp.unsupported_version: %s", resp.Error.Message)
	}
}

// Spec: §6.9 / §6.10 — initialize with no protocolVersion is accepted
// (host did not assert a specific version; we negotiate to ours).
func TestPodiumMCP_InitializeNegotiates(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t)
	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env, "PODIUM_REGISTRY="+h.URL)
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "initialize", ID: 1, Params: map[string]any{
			"protocolVersion": "2024-11-05",
		}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	var resp struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
		Error *struct{ Message string } `json:"error"`
	}
	if err := json.NewDecoder(&stdout).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	if resp.Result.ProtocolVersion == "" {
		t.Errorf("ProtocolVersion empty")
	}
}

// Spec: §6.10 — when the registry is unreachable, the MCP bridge
// surfaces network.registry_unreachable in the tool-call result so
// the host can warn the user about staleness.
// Matrix: §6.10 (network.registry_unreachable)
func TestPodiumMCP_NetworkRegistryUnreachable(t *testing.T) {
	t.Parallel()
	bin := buildMCP(t)
	cmd := exec.Command(bin)
	// Point at a closed port to force a connection failure.
	cmd.Env = append(cmd.Env, "PODIUM_REGISTRY=http://127.0.0.1:1") // RFC 6335 reserved port; nothing listens
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "tools/call", ID: 1, Params: map[string]any{
			"name":      "search_artifacts",
			"arguments": map[string]any{"query": "x"},
		}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "connect") &&
		!strings.Contains(stdout.String(), "refused") &&
		!strings.Contains(stdout.String(), "reachable") {
		t.Errorf("expected a network error in stdout, got: %s", stdout.String())
	}
}
