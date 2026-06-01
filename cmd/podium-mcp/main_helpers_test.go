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
		"":             {},
		"alone":        {"alone"},
		"a,b,c":        {"a", "b", "c"},
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
		"":        nil,
		"x":       {"x"},
		"x,y":     {"x", "y"},
		"a,,b,,c": {"a", "b", "c"},
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

// --- loadConfig --------------------------------------------------------------

// spec: §6.10 / §7.5.2 / §13.10 — registry unset across env, flags, and every
// sync.yaml scope surfaces the canonical config.no_registry code and points the
// user at `podium init` (F-6.11.1), not a bare "required" message.
func TestLoadConfig_MissingRegistryErrors(t *testing.T) {
	// hermetic isolates HOME + cwd so the §7.5.2 sync.yaml fallback
	// cannot resolve a registry from a real ~/.podium/sync.yaml.
	hermetic(t)
	t.Setenv("PODIUM_REGISTRY", "")
	_, err := loadConfig()
	if err == nil {
		t.Fatalf("missing PODIUM_REGISTRY: no error")
	}
	if !strings.Contains(err.Error(), "config.no_registry") {
		t.Errorf("error %q missing config.no_registry code", err)
	}
	if !strings.Contains(err.Error(), "podium init") {
		t.Errorf("error %q should point the user at `podium init`", err)
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

// spec: §6.9 "Workspace overlay path missing" — a configured overlay path that
// does not resolve is skipped (the overlay stays empty and the bridge still
// starts), and the bridge warns exactly once, naming the path, so a developer
// whose drafts are invisible gets a diagnostic rather than silence.
func TestNewServer_MissingOverlayWarnsOnceAndSkips(t *testing.T) {
	dir := t.TempDir()
	absent := filepath.Join(t.TempDir(), "absent")
	cfg := &config{
		registry:    "http://127.0.0.1:1",
		harness:     "none",
		cacheDir:    dir,
		cacheMode:   "always-revalidate",
		overlayPath: absent,
	}
	var srv *mcpServer
	var err error
	out := captureStderr(t, func() { srv, err = newServer(cfg) })
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	if len(srv.overlay) != 0 {
		t.Errorf("expected empty overlay (skipped), got %d records", len(srv.overlay))
	}
	if !strings.Contains(out, absent) || !strings.Contains(out, "overlay") {
		t.Errorf("startup warning = %q, want a single warning naming %q", out, absent)
	}
	// Warn once: the path is resolved a single time at startup, so the
	// warning must not be repeated.
	if n := strings.Count(out, absent); n != 1 {
		t.Errorf("warning emitted %d times, want exactly once", n)
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

// spec: §6.9 "MCP protocol version mismatch" — for a host that tops out below
// this binary's max but within the supported window, initialize negotiates
// DOWN to the host's version (echoing min(host, server)) rather than forcing
// the server's own constant.
func TestHandle_InitializeNegotiatesDownToHost(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}}
	// supportedSince <= 2024-11-03 < protocolVersion (2024-11-05).
	hostMax := "2024-11-03"
	params, _ := json.Marshal(map[string]string{"protocolVersion": hostMax})
	resp := srv.handle(rpcRequest{JSONRPC: "2.0", Method: "initialize", Params: params})
	if resp.Error != nil {
		t.Fatalf("error on in-window host protocol: %v", resp.Error)
	}
	m, _ := resp.Result.(map[string]any)
	if m["protocolVersion"] != hostMax {
		t.Errorf("protocolVersion = %v, want negotiated-down %q", m["protocolVersion"], hostMax)
	}
}

// spec: §6.9 — a host requesting a version newer than this binary's max is
// capped at the server max (the server cannot speak a version it does not
// implement); a host omitting the field gets the server max.
func TestHandle_InitializeCapsNewerAndDefaultsEmpty(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		requested string
		want      string
	}{
		{"newer than server", "2025-06-01", protocolVersion},
		{"empty defaults to max", "", protocolVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := &mcpServer{cfg: &config{}}
			params, _ := json.Marshal(map[string]string{"protocolVersion": tc.requested})
			resp := srv.handle(rpcRequest{JSONRPC: "2.0", Method: "initialize", Params: params})
			if resp.Error != nil {
				t.Fatalf("unexpected error: %v", resp.Error)
			}
			m, _ := resp.Result.(map[string]any)
			if m["protocolVersion"] != tc.want {
				t.Errorf("protocolVersion = %v, want %q", m["protocolVersion"], tc.want)
			}
		})
	}
}

// spec: §6.9 — negotiateProtocol unit table covering the boundary at
// supportedSince and the no-compatible-version case.
func TestNegotiateProtocol(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		requested string
		want      string
		wantOK    bool
	}{
		{"", protocolVersion, true},              // host omitted the field
		{"2023-01-01", "", false},                // older than supportedSince
		{"2024-10-31", "", false},                // one day before supportedSince
		{supportedSince, supportedSince, true},   // exact floor
		{"2024-11-03", "2024-11-03", true},       // in-window, negotiate down
		{protocolVersion, protocolVersion, true}, // exact server max
		{"2025-06-01", protocolVersion, true},    // newer than server, capped
	} {
		got, ok := negotiateProtocol(tc.requested)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("negotiateProtocol(%q) = (%q, %v), want (%q, %v)", tc.requested, got, ok, tc.want, tc.wantOK)
		}
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

// spec: §12 — "Fresh load_domain / search_domains / search_artifacts returns
// an explicit 'offline' status that hosts can surface." An unreachable
// registry yields an offline status rather than an error so the host can tell
// a transient outage from a request rejection (F-12.0.3).
func TestProxyGet_UnreachableRegistryReturnsOfflineStatus(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{
		cfg:  &config{registry: "http://127.0.0.1:1"},
		http: &http.Client{},
	}
	got := srv.proxyGet("/v1/load_domain", map[string]any{"path": "x"}, nil)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if m["status"] != "offline" {
		t.Errorf("status = %v, want offline (%v)", m["status"], m)
	}
	if _, has := m["error"]; has {
		t.Errorf("offline result must not carry an error key: %v", m)
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
