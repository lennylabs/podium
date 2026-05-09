package integration

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/registryharness"
)

// rpcCall encodes a single JSON-RPC request and returns the decoded
// response envelope.
type rpcCall struct {
	Method string
	Params any
	ID     int
}

func newlineDelimitedRequests(calls []rpcCall) []byte {
	var buf bytes.Buffer
	for _, c := range calls {
		req := map[string]any{
			"jsonrpc": "2.0",
			"id":      c.ID,
			"method":  c.Method,
		}
		if c.Params != nil {
			req["params"] = c.Params
		}
		body, _ := json.Marshal(req)
		buf.Write(body)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// Spec: §6.1 The Bridge — initialize returns the MCP capabilities
// declaration with tools, prompts, and sessionCorrelation.
// Phase: 4
func TestPodiumMCP_InitializeReturnsCapabilities(t *testing.T) {
	testharness.RequirePhase(t, 4)
	t.Parallel()
	h := registryharness.New(t)

	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env, "PODIUM_REGISTRY="+h.URL)
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "initialize", ID: 1},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\n%s", err, stdout.String())
	}

	var resp struct {
		Result struct {
			Capabilities map[string]any `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.NewDecoder(&stdout).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp.Result.Capabilities["tools"]; !ok {
		t.Errorf("capabilities missing tools: %+v", resp.Result.Capabilities)
	}
}

// Spec: §5 — tools/list returns the four meta-tools.
// Phase: 4
func TestPodiumMCP_ToolsListReturnsMetaTools(t *testing.T) {
	testharness.RequirePhase(t, 4)
	t.Parallel()
	h := registryharness.New(t)
	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env, "PODIUM_REGISTRY="+h.URL)
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "tools/list", ID: 1},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\n%s", err, stdout.String())
	}
	body := stdout.String()
	for _, want := range []string{
		"load_domain", "search_domains", "search_artifacts", "load_artifact",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("tools/list missing %q: %s", want, body)
		}
	}
}

// Spec: §5 — tools/call for search_artifacts forwards to the registry's
// HTTP API and returns the decoded response.
// Phase: 4
func TestPodiumMCP_ToolsCallProxiesSearchArtifacts(t *testing.T) {
	testharness.RequirePhase(t, 4)
	t.Parallel()
	h := registryharness.New(t,
		testharness.WriteTreeOption{
			Path:    "finance/run/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: Variance analysis\n---\n\nbody\n",
		},
	)
	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env, "PODIUM_REGISTRY="+h.URL)
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "tools/call", ID: 1, Params: map[string]any{
			"name": "search_artifacts",
			"arguments": map[string]any{
				"query": "variance",
			},
		}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(stdout.String(), "finance/run") {
		t.Errorf("expected finance/run in output, got: %s", stdout.String())
	}
}

func buildMCP(t testing.TB) string {
	t.Helper()
	tmp := t.TempDir()
	bin := tmp + "/podium-mcp"
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/podium-mcp")
	cmd.Dir = repoRoot(t)
	if buf, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build podium-mcp: %v\n%s", err, buf)
	}
	return bin
}

func repoRoot(t testing.TB) string {
	t.Helper()
	dir, _ := exec.Command("pwd").Output()
	d := strings.TrimSpace(string(dir))
	for d != "/" {
		if _, err := exec.Command("test", "-f", d+"/.phase").CombinedOutput(); err == nil {
			return d
		}
		d = parentOf(d)
	}
	t.Fatalf("repo root not found")
	return ""
}

func parentOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			if i == 0 {
				return "/"
			}
			return p[:i]
		}
	}
	return p
}
