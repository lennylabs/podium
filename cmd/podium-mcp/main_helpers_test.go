package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvDefault(t *testing.T) {
	const key = "PODIUM_TEST_MCP_ENVDEFAULT_XYZZY"
	t.Setenv(key, "")
	if got := envDefault(key, "fb"); got != "fb" {
		t.Errorf("unset: got %q", got)
	}
	t.Setenv(key, "from-env")
	if got := envDefault(key, "fb"); got != "from-env" {
		t.Errorf("set: got %q", got)
	}
}

func TestSplitCSV(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"":              {},
		"alone":         {"alone"},
		"a,b,c":         {"a", "b", "c"},
		" a , b ,  c ": {"a", "b", "c"},
		",,a,,b,,":     {"a", "b"},
	}
	for in, want := range cases {
		got := splitCSV(in)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("splitCSV(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSplitCSVMCP(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"":         nil,
		"x":        {"x"},
		"x,y":      {"x", "y"},
		"a,,b,,c":  {"a", "b", "c"},
	}
	for in, want := range cases {
		got := splitCSVMCP(in)
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("splitCSVMCP(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestResourcesAsBytes_And_Strings_RoundTrip(t *testing.T) {
	t.Parallel()
	if got := resourcesAsBytes(nil); got != nil {
		t.Errorf("nil input: got %v", got)
	}
	if got := resourcesAsStrings(nil); got != nil {
		t.Errorf("nil input: got %v", got)
	}
	in := map[string]string{"a.md": "hello", "sub/b.txt": "world"}
	asBytes := resourcesAsBytes(in)
	if len(asBytes) != 2 || string(asBytes["a.md"]) != "hello" {
		t.Errorf("asBytes = %v", asBytes)
	}
	back := resourcesAsStrings(asBytes)
	if back["a.md"] != "hello" || back["sub/b.txt"] != "world" {
		t.Errorf("round-trip = %v", back)
	}
}

func TestSynthesizeSkillMD_ConcatenatesFrontmatterAndBody(t *testing.T) {
	t.Parallel()
	r := loadArtifactResponse{
		Frontmatter:  "---\nname: greet\n---\n",
		ManifestBody: "Hello, world.",
	}
	if got := synthesizeSkillMD(r); got != "---\nname: greet\n---\nHello, world." {
		t.Errorf("got = %q", got)
	}
}

func TestContentCache_HasReportsPresence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c, err := newContentCache(dir)
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}
	hash := "sha256:abcdef"
	if c.has(hash) {
		t.Errorf("empty cache reports has(%q)=true", hash)
	}
	if err := c.put(hash, "fm", "body", map[string]string{"r/x.md": "data"}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if !c.has(hash) {
		t.Errorf("after put, has(%q)=false", hash)
	}
	if c.has("") {
		t.Errorf("empty hash reports has=true")
	}
	// A disabled cache (empty dir) never reports has=true.
	disabled := &contentCache{}
	if disabled.has(hash) {
		t.Errorf("disabled cache reports has=true")
	}
}

func TestErrorResultWithStatus(t *testing.T) {
	t.Parallel()
	// 200 OK: no status suffix.
	got := errorResultWithStatus("bad", http.StatusOK)
	m, ok := got.(map[string]any)
	if !ok || m["error"] != "bad" {
		t.Errorf("OK case = %v", got)
	}
	// 503: includes status in the message.
	got = errorResultWithStatus("upstream down", http.StatusServiceUnavailable)
	m, ok = got.(map[string]any)
	if !ok {
		t.Fatalf("non-map: %T", got)
	}
	if msg, ok := m["error"].(string); !ok || !strings.Contains(msg, "503") {
		t.Errorf("non-OK case = %v", m)
	}
}

func TestPromptsCapabilityActive_AlwaysTrue(t *testing.T) {
	t.Parallel()
	s := &mcpServer{}
	if !s.promptsCapabilityActive() {
		t.Errorf("promptsCapabilityActive() = false, want true")
	}
}

// --- loadConfig --------------------------------------------------------------

func TestLoadConfig_MissingRegistryErrors(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "")
	if _, err := loadConfig(); err == nil {
		t.Errorf("missing PODIUM_REGISTRY: no error")
	}
}

func TestLoadConfig_BadCacheModeErrors(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	t.Setenv("PODIUM_CACHE_MODE", "bogus")
	if _, err := loadConfig(); err == nil {
		t.Errorf("bad cache mode: no error")
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	t.Setenv("PODIUM_CACHE_MODE", "")
	t.Setenv("PODIUM_HARNESS", "")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.harness != "none" {
		t.Errorf("harness default = %q, want none", cfg.harness)
	}
	if cfg.cacheMode != "always-revalidate" {
		t.Errorf("cacheMode default = %q", cfg.cacheMode)
	}
}

func TestLoadConfig_TokenEnvIndirection(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	t.Setenv("PODIUM_SESSION_TOKEN_ENV", "MY_HOST_TOKEN")
	t.Setenv("MY_HOST_TOKEN", "from-named-env")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.sessionToken != "from-named-env" {
		t.Errorf("sessionToken = %q, want from-named-env", cfg.sessionToken)
	}
}

// --- newServer ---------------------------------------------------------------

func TestNewServer_OK(t *testing.T) {
	dir := t.TempDir()
	cfg := &config{registry: "http://127.0.0.1:1", harness: "none", cacheDir: dir, cacheMode: "always-revalidate"}
	srv, err := newServer(cfg)
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	if srv.cache == nil || srv.adapters == nil {
		t.Errorf("server wiring missing: %+v", srv)
	}
}

func TestNewServer_MissingOverlayIsSilent(t *testing.T) {
	dir := t.TempDir()
	cfg := &config{
		registry:    "http://127.0.0.1:1",
		harness:     "none",
		cacheDir:    dir,
		cacheMode:   "always-revalidate",
		overlayPath: filepath.Join(t.TempDir(), "absent"),
	}
	srv, err := newServer(cfg)
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	if len(srv.overlay) != 0 {
		t.Errorf("expected empty overlay, got %d records", len(srv.overlay))
	}
}

// --- handle ------------------------------------------------------------------

func TestHandle_InitializeRejectsOldProtocol(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}}
	params, _ := json.Marshal(map[string]string{"protocolVersion": "2023-01-01"})
	resp := srv.handle(rpcRequest{JSONRPC: "2.0", Method: "initialize", Params: params})
	if resp.Error == nil {
		t.Errorf("expected error for old protocol, got %+v", resp)
	}
}

func TestHandle_InitializeAcceptsCurrentProtocol(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}}
	params, _ := json.Marshal(map[string]string{"protocolVersion": protocolVersion})
	resp := srv.handle(rpcRequest{JSONRPC: "2.0", Method: "initialize", Params: params})
	if resp.Error != nil {
		t.Errorf("error on current protocol: %v", resp.Error)
	}
	m, ok := resp.Result.(map[string]any)
	if !ok || m["protocolVersion"] != protocolVersion {
		t.Errorf("result = %+v", resp.Result)
	}
}

func TestHandle_ToolsListReturnsKnownTools(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}}
	resp := srv.handle(rpcRequest{JSONRPC: "2.0", Method: "tools/list"})
	m, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T", resp.Result)
	}
	tools, ok := m["tools"].([]map[string]any)
	if !ok || len(tools) < 4 {
		t.Fatalf("tools = %+v", m["tools"])
	}
	names := map[string]bool{}
	for _, t := range tools {
		names[t["name"].(string)] = true
	}
	for _, want := range []string{"load_domain", "search_domains", "search_artifacts", "load_artifact"} {
		if !names[want] {
			t.Errorf("missing tool %q in %v", want, names)
		}
	}
}

func TestHandle_UnknownMethodReturnsError(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}}
	resp := srv.handle(rpcRequest{JSONRPC: "2.0", Method: "definitely/not/real"})
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Errorf("expected -32601 error, got %+v", resp.Error)
	}
}

// --- callTool ----------------------------------------------------------------

func TestCallTool_UnknownToolReturnsErrorResult(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}}
	raw, _ := json.Marshal(toolCallParams{Name: "nope", Arguments: map[string]any{}})
	got := srv.callTool(raw)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if msg, _ := m["error"].(string); !strings.Contains(msg, "unknown tool") {
		t.Errorf("error message = %q", msg)
	}
}

func TestCallTool_MalformedParamsReturnsErrorResult(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}}
	got := srv.callTool(json.RawMessage(`not json`))
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if _, has := m["error"]; !has {
		t.Errorf("expected error key in %v", m)
	}
}

// --- serve -------------------------------------------------------------------

func TestServe_EncodesOneResponsePerLine(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}}
	initParams, _ := json.Marshal(map[string]string{"protocolVersion": protocolVersion})
	initReq, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "initialize", Params: initParams})
	toolsReq, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "tools/list"})
	in := bytes.NewReader(append(append(initReq, '\n'), append(toolsReq, '\n')...))
	var out bytes.Buffer
	if err := srv.serve(in, &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d response lines, want 2:\n%s", len(lines), out.String())
	}
	for i, line := range lines {
		var got rpcResponse
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Errorf("line %d not JSON: %v\n%s", i, err, line)
		}
	}
}

func TestServe_SkipsMalformedRequests(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}}
	in := bytes.NewReader([]byte(`not json` + "\n" + `{"jsonrpc":"2.0","method":"tools/list","id":1}` + "\n"))
	var out bytes.Buffer
	if err := srv.serve(in, &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	if !strings.Contains(out.String(), "load_artifact") {
		t.Errorf("expected tools/list response despite malformed first line; got:\n%s", out.String())
	}
}

// --- proxyGet ----------------------------------------------------------------
// proxyGet wraps fetchJSON; covered transitively where the registry is
// unreachable: the result is an error map.

func TestProxyGet_UnreachableRegistryReturnsErrorResult(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{
		cfg:  &config{registry: "http://127.0.0.1:1"},
		http: &http.Client{},
	}
	got := srv.proxyGet("/v1/load_domain", map[string]any{"path": "x"})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if _, has := m["error"]; !has {
		t.Errorf("expected error key in %v", m)
	}
}

// --- buildSignatureProvider --------------------------------------------------

func TestBuildSignatureProvider(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", "noop", "sigstore-keyless", "registry-managed"} {
		if _, err := buildSignatureProvider(name); err != nil {
			t.Errorf("buildSignatureProvider(%q) = %v", name, err)
		}
	}
	if _, err := buildSignatureProvider("unknown"); err == nil {
		t.Errorf("buildSignatureProvider(unknown) = nil error, want error")
	}
}

// Avoid unused-import warning if the test pool changes shape.
var _ = os.Getenv
