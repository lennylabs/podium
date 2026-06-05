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
func TestPodiumMCP_InitializeReturnsCapabilities(t *testing.T) {
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
func TestPodiumMCP_ToolsListReturnsMetaTools(t *testing.T) {
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

// Spec: §6.1 / §7.5.2 — the MCP server speaks HTTP and requires a
// server-source registry. A filesystem PODIUM_REGISTRY (no http:// or
// https:// prefix) makes the real binary refuse to start with a non-zero
// exit and the config.filesystem_registry_unsupported envelope on stderr,
// rather than serving and failing opaquely on the first tool call.
func TestPodiumMCP_RejectsFilesystemRegistry(t *testing.T) {
	t.Parallel()
	bin := buildMCP(t)
	dir := t.TempDir()
	cmd := exec.Command(bin)
	// An absolute filesystem path is a §7.5.2 filesystem source.
	cmd.Env = append(cmd.Env, "PODIUM_REGISTRY="+dir)
	// No stdin is consumed: the bridge must reject the config before serving.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("filesystem registry: binary exited 0, want non-zero")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run error %v is not an ExitError", err)
	}
	if got := exitErr.ExitCode(); got != 2 {
		t.Errorf("exit code = %d, want 2 (config error)", got)
	}
	if !strings.Contains(stderr.String(), "config.filesystem_registry_unsupported") {
		t.Errorf("stderr %q lacks config.filesystem_registry_unsupported", stderr.String())
	}
}

// Spec: §6.9 "Unknown PODIUM_HARNESS value" — the real binary refuses to
// start and lists the available adapter values, with a non-zero exit and the
// config.unknown_harness envelope on stderr, rather than detecting the bad
// harness lazily on the first load_artifact call.
func TestPodiumMCP_RejectsUnknownHarness(t *testing.T) {
	t.Parallel()
	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env,
		"PODIUM_REGISTRY=http://127.0.0.1:1", // server source so only the harness is at fault
		"PODIUM_HARNESS=claude-codex-typo",
	)
	// No stdin is consumed: the bridge must reject the config before serving.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("unknown harness: binary exited 0, want non-zero")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run error %v is not an ExitError", err)
	}
	if got := exitErr.ExitCode(); got != 2 {
		t.Errorf("exit code = %d, want 2 (config error)", got)
	}
	if !strings.Contains(stderr.String(), "config.unknown_harness") {
		t.Errorf("stderr %q lacks config.unknown_harness", stderr.String())
	}
	// The diagnostic lists the registered adapters so the operator can fix it.
	if !strings.Contains(stderr.String(), "none") {
		t.Errorf("stderr %q does not list available adapters", stderr.String())
	}
}

// Spec: §5.1 — tools/list returns the canonical multi-sentence
// descriptions verbatim and an inputSchema for every meta-tool
// ; initialize surfaces the example system-prompt fragment via
// the MCP `instructions` field. Driven through the real bridge
// subprocess.
func TestPodiumMCP_ToolsListDescriptionsSchemasAndInstructions(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t)
	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env, "PODIUM_REGISTRY="+h.URL)
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "initialize", ID: 1, Params: map[string]any{"protocolVersion": "2024-11-05"}},
		{Method: "tools/list", ID: 2},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\n%s", err, stdout.String())
	}

	dec := json.NewDecoder(&stdout)
	var initResp struct {
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	if err := dec.Decode(&initResp); err != nil {
		t.Fatalf("decode initialize: %v", err)
	}
	// the §5.1 example fragment is exposed programmatically.
	for _, want := range []string{
		"You have access to a catalog of authored skills and agents through the Podium meta-tools",
		"Sessions start empty",
	} {
		if !strings.Contains(initResp.Result.Instructions, want) {
			t.Errorf("initialize instructions missing %q: %q", want, initResp.Result.Instructions)
		}
	}

	var listResp struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := dec.Decode(&listResp); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	byName := map[string]struct {
		desc   string
		schema json.RawMessage
	}{}
	for _, tool := range listResp.Result.Tools {
		byName[tool.Name] = struct {
			desc   string
			schema json.RawMessage
		}{tool.Description, tool.InputSchema}
	}
	// every meta-tool advertises an inputSchema.
	for _, name := range []string{"load_domain", "search_domains", "search_artifacts", "load_artifact"} {
		tool, ok := byName[name]
		if !ok {
			t.Fatalf("tools/list missing %q", name)
		}
		if len(tool.schema) == 0 || string(tool.schema) == "null" {
			t.Errorf("%s missing inputSchema", name)
		}
	}
	// descriptions are the full canonical strings, not the
	// first-sentence truncation. The cross-tool guidance lives past the
	// first period.
	if d := byName["load_domain"].desc; !strings.Contains(d, "call `load_artifact`") {
		t.Errorf("load_domain description truncated: %q", d)
	}
	if d := byName["search_artifacts"].desc; !strings.Contains(d, "total_matched") {
		t.Errorf("search_artifacts description truncated: %q", d)
	}
}

// Spec: §5 — tools/call for search_artifacts forwards to the registry's
// HTTP API and returns the decoded response.
func TestPodiumMCP_ToolsCallProxiesSearchArtifacts(t *testing.T) {
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

// Spec: §5.0 — the bridge advertises a `resources` capability and serves
// the read-only mirror of load_artifact: resources/list enumerates the
// effective view and resources/read returns an artifact body without
// materializing anything. Driven through the real subprocess.
func TestPodiumMCP_ResourcesMirror(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		testharness.WriteTreeOption{
			Path:    "finance/run/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: Variance analysis\n---\n\nthe body text\n",
		},
	)
	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env, "PODIUM_REGISTRY="+h.URL, "PODIUM_CACHE_DIR="+t.TempDir())
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "initialize", ID: 1, Params: map[string]any{"protocolVersion": "2024-11-05"}},
		{Method: "resources/list", ID: 2},
		{Method: "resources/read", ID: 3, Params: map[string]any{"uri": "podium://artifact/finance/run"}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\n%s", err, stdout.String())
	}

	dec := json.NewDecoder(&stdout)
	var initResp struct {
		Result struct {
			Capabilities map[string]any `json:"capabilities"`
		} `json:"result"`
	}
	if err := dec.Decode(&initResp); err != nil {
		t.Fatalf("decode initialize: %v", err)
	}
	if _, ok := initResp.Result.Capabilities["resources"]; !ok {
		t.Errorf("initialize did not advertise resources capability: %+v", initResp.Result.Capabilities)
	}

	var listResp struct {
		Result struct {
			Resources []struct {
				URI  string `json:"uri"`
				Name string `json:"name"`
			} `json:"resources"`
		} `json:"result"`
	}
	if err := dec.Decode(&listResp); err != nil {
		t.Fatalf("decode resources/list: %v", err)
	}
	found := false
	for _, r := range listResp.Result.Resources {
		if r.URI == "podium://artifact/finance/run" {
			found = true
		}
	}
	if !found {
		t.Errorf("resources/list missing finance/run: %+v", listResp.Result.Resources)
	}

	var readResp struct {
		Result struct {
			Contents []struct {
				Text string `json:"text"`
			} `json:"contents"`
		} `json:"result"`
	}
	if err := dec.Decode(&readResp); err != nil {
		t.Fatalf("decode resources/read: %v", err)
	}
	if len(readResp.Result.Contents) != 1 || !strings.Contains(readResp.Result.Contents[0].Text, "the body text") {
		t.Errorf("resources/read did not return the artifact body: %+v", readResp.Result.Contents)
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
		if _, err := exec.Command("test", "-f", d+"/go.mod").CombinedOutput(); err == nil {
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
