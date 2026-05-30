package integration

import (
	"bytes"
	"encoding/json"
	"net"
	"os/exec"
	"testing"

	"github.com/lennylabs/podium/internal/testharness/registryharness"
)

// healthToolResult mirrors the §13.9 health tool payload on the wire.
type healthToolResult struct {
	Registry           string `json:"registry"`
	Connected          bool   `json:"connected"`
	Mode               string `json:"mode"`
	CacheSize          int    `json:"cache_size"`
	LastSuccessfulCall string `json:"last_successful_call"`
}

// runHealthTool drives the real podium-mcp binary through a single
// tools/call health request against the given registry and returns the
// decoded result. The binary exits on stdin EOF, so the call is bounded.
func runHealthTool(t *testing.T, registry string) healthToolResult {
	t.Helper()
	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env, "PODIUM_REGISTRY="+registry)
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "tools/call", ID: 1, Params: map[string]any{"name": "health"}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\n%s", err, stdout.String())
	}
	var resp struct {
		Result healthToolResult `json:"result"`
	}
	if err := json.NewDecoder(&stdout).Decode(&resp); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout.String())
	}
	return resp.Result
}

// Spec: §13.9 (F-13.9.1) — the health tool reports the registry as
// connected and in ready mode when it answers /readyz, and stamps the
// last successful call timestamp.
func TestPodiumMCP_HealthToolReportsReady(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t)
	res := runHealthTool(t, h.URL)
	if res.Mode != "ready" {
		t.Errorf("mode = %q, want ready", res.Mode)
	}
	if !res.Connected {
		t.Errorf("connected = false, want true")
	}
	if res.Registry != h.URL {
		t.Errorf("registry = %q, want %q", res.Registry, h.URL)
	}
	if res.LastSuccessfulCall == "" {
		t.Errorf("last_successful_call empty, want a timestamp after a successful probe")
	}
}

// Spec: §13.9 (F-13.9.1) — the health tool reports mode unreachable and
// connected false when nothing answers at the registry address.
func TestPodiumMCP_HealthToolReportsUnreachable(t *testing.T) {
	t.Parallel()
	res := runHealthTool(t, deadRegistryURL(t))
	if res.Mode != "unreachable" {
		t.Errorf("mode = %q, want unreachable", res.Mode)
	}
	if res.Connected {
		t.Errorf("connected = true, want false (no listener)")
	}
}

// deadRegistryURL returns an http URL whose port has no listener: it
// binds an ephemeral port, captures the address, and closes the
// listener so a later dial is refused.
func deadRegistryURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return "http://" + addr
}
