// Package e2e holds documentation-driven end-to-end tests. Each test
// builds and exercises the real Podium binaries (cmd/podium,
// cmd/podium-mcp, cmd/podium-server) and asserts the observable
// behavior that the documentation and spec claim.
//
// Every helper here owns the lifecycle of the processes it starts:
// CLI invocations run under a context deadline with stdin redirected
// from an empty reader, servers are torn down via SIGINT/SIGKILL in
// t.Cleanup, and the MCP bridge runs as a bounded stdio subprocess.
// No test blocks forever; see the REMEDIATION anti-hang rule.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// cliResult captures one subprocess invocation.
type cliResult struct {
	Stdout string
	Stderr string
	Exit   int
}

// mergeEnv returns the current environment with the supplied KEY=VALUE
// entries overriding any inherited values for the same key. This lets a
// test pin HOME, PODIUM_REGISTRY, etc. without leaking the developer's
// real environment into the assertion.
func mergeEnv(extra ...string) []string {
	override := map[string]bool{}
	for _, kv := range extra {
		if i := strings.IndexByte(kv, '='); i > 0 {
			override[kv[:i]] = true
		}
	}
	out := make([]string, 0, len(extra)+8)
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 && override[kv[:i]] {
			continue
		}
		// Never inherit the developer's PODIUM_NO_BROWSER; the harness pins it
		// below so a device-code login test can never launch the system
		// browser (the login command auto-opens the verification URL).
		if strings.HasPrefix(kv, "PODIUM_NO_BROWSER=") {
			continue
		}
		// Do not inherit ambient backend configuration into CLI subprocesses.
		// PODIUM_POSTGRES_DSN[_VECTOR], PODIUM_PGVECTOR_DSN, and PODIUM_S3_* are
		// set by `make test-live` (and the gap-remediation harness) so the live
		// store + object-store tests run; inherited into a `serve` subprocess
		// they count as explicit server config and suppress the §13.10
		// no-autostandalone refusal these tests assert. A test that needs a
		// backend passes it explicitly in `extra` (applied above and appended
		// below), so this scrub only removes ambient, unrequested config.
		if strings.HasPrefix(kv, "PODIUM_POSTGRES_DSN=") ||
			strings.HasPrefix(kv, "PODIUM_POSTGRES_DSN_VECTOR=") ||
			strings.HasPrefix(kv, "PODIUM_PGVECTOR_DSN=") ||
			strings.HasPrefix(kv, "PODIUM_S3_") {
			continue
		}
		// Do not inherit ambient embedding-provider API keys into CLI
		// subprocesses for the same reason: test.env (and the gap-remediation
		// harness) export real OPENAI_API_KEY / VOYAGE_API_KEY / COHERE_API_KEY so
		// the live embedding tests run, but inherited into a `serve` subprocess
		// they satisfy the §13.12 backend-key requirement and suppress the
		// refuse-to-start assertion the missing-key tests make (e.g.
		// TestVectorBackend_OpenAIMissingAPIKey). A test that needs a key passes
		// it explicitly in `extra`, so this scrub only removes ambient, unrequested
		// keys.
		if strings.HasPrefix(kv, "OPENAI_API_KEY=") ||
			strings.HasPrefix(kv, "VOYAGE_API_KEY=") ||
			strings.HasPrefix(kv, "COHERE_API_KEY=") {
			continue
		}
		out = append(out, kv)
	}
	// Suppress the login browser auto-open for every CLI subprocess unless the
	// caller set PODIUM_NO_BROWSER explicitly.
	if !override["PODIUM_NO_BROWSER"] {
		out = append(out, "PODIUM_NO_BROWSER=1")
	}
	return append(out, extra...)
}

// runPodium invokes the podium CLI from cwd with the given extra env and
// a hard deadline. stdin is empty (anti-hang for any command that might
// read it). PODIUM_NO_AUTOSTANDALONE is forced so a stray `serve`-like
// path never spawns a daemon under a CLI test.
func runPodium(t testing.TB, cwd string, env []string, args ...string) cliResult {
	t.Helper()
	return runBin(t, cmdharness.Bin(t, "podium"), cwd, append(env, "PODIUM_NO_AUTOSTANDALONE=1"), nil, 90*time.Second, args...)
}

// runPodiumStdin is runPodium with a stdin payload, for the CLI's
// interactive prompts (scaffold without --yes). The payload is fed verbatim
// and the process reads it to EOF under the same hard deadline.
func runPodiumStdin(t testing.TB, cwd string, env []string, stdin string, args ...string) cliResult {
	t.Helper()
	return runBin(t, cmdharness.Bin(t, "podium"), cwd, append(env, "PODIUM_NO_AUTOSTANDALONE=1"), []byte(stdin), 90*time.Second, args...)
}

// withIsolatedHome appends HOME (and USERPROFILE on Windows) pointing at home
// so a CLI subprocess resolves the §7.5.2 user-global config scope from an
// empty directory instead of the developer's real ~/.podium, which would
// otherwise leak a registry or other defaults into a test that assumes none. A
// caller that already set HOME in env keeps it. mergeEnv scrubs the inherited
// HOME in favor of this one because it appears in the extra set.
func withIsolatedHome(env []string, home string) []string {
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") {
			return env
		}
	}
	return append(env, "HOME="+home, "USERPROFILE="+home)
}

// runBin is the generic bounded subprocess runner.
func runBin(t testing.TB, bin, cwd string, env []string, stdin []byte, timeout time.Duration, args ...string) cliResult {
	t.Helper()
	home := cmdharness.IsolatedHome(t)
	env = withIsolatedHome(env, home)
	if cwd == "" {
		// Run in the isolated HOME dir so §7.5.2 workspace discovery walks up
		// through empty parents instead of climbing from the repo working dir
		// into the developer's ~/.podium and reading it as a project config.
		cwd = home
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = cwd
	cmd.Env = mergeEnv(env...)
	cmd.Stdin = bytes.NewReader(stdin)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("%s %s timed out after %s\nstderr:\n%s", bin, strings.Join(args, " "), timeout, se.String())
	}
	res := cliResult{Stdout: so.String(), Stderr: se.String()}
	if ee, ok := err.(*exec.ExitError); ok {
		res.Exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %s %s: %v\nstderr:\n%s", bin, strings.Join(args, " "), err, se.String())
	}
	return res
}

// writeRegistry stages a filesystem registry from a flat path->content map.
func writeRegistry(t testing.TB, entries map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	opts := make([]testharness.WriteTreeOption, 0, len(entries))
	for path, content := range entries {
		opts = append(opts, testharness.WriteTreeOption{Path: path, Content: content})
	}
	testharness.WriteTree(t, dir, opts...)
	return dir
}

// ---- HTTP helpers -------------------------------------------------------

// httpClient has a short timeout so a wedged server never hangs a test.
var httpClient = &http.Client{Timeout: 5 * time.Second}

func getRaw(t testing.TB, url string) (int, []byte) {
	t.Helper()
	resp, err := httpClient.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(resp.Body)
	return resp.StatusCode, body.Bytes()
}

func getStatus(t testing.TB, url string) int {
	t.Helper()
	st, _ := getRaw(t, url)
	return st
}

// metricsDashboardSeries lists the Prometheus series the reference Grafana
// dashboard (deploy/grafana-dashboard.json) queries. The §13.8 /metrics
// endpoint must emit every one so the shipped dashboard resolves.
var metricsDashboardSeries = []string{
	"podium_request_total",
	"podium_request_errors_total",
	"podium_request_duration_seconds",
	"podium_visibility_denied_total",
	"podium_cache_hits_total",
	"podium_cache_misses_total",
	"podium_ingest_success_total",
	"podium_ingest_failure_total",
	"podium_vector_outbox_depth",
}

// assertMetricsScrape drives one observed meta-tool request, then GETs
// baseURL/metrics, requires HTTP 200, and asserts the body carries the
// Prometheus exposition format plus every §13.8 dashboard series. The leading
// request populates the per-endpoint request_total / request_duration_seconds
// label vectors, which emit no series on a server that has served no traffic.
// It returns the scrape body for further per-test assertions.
func assertMetricsScrape(t testing.TB, baseURL string) string {
	t.Helper()
	// A search with no match still returns 200 and records one observation.
	_ = getStatus(t, baseURL+"/v1/search_artifacts?q=ping")
	// A load_artifact with no id returns 400, populating the per-endpoint
	// podium_request_errors_total series (§13.8) so the scrape carries it.
	_ = getStatus(t, baseURL+"/v1/load_artifact")
	st, body := getRaw(t, baseURL+"/metrics")
	if st != 200 {
		t.Fatalf("GET /metrics = HTTP %d, want 200\nbody: %s", st, body)
	}
	text := string(body)
	if !strings.Contains(text, "# HELP ") || !strings.Contains(text, "# TYPE ") {
		t.Errorf("/metrics body is not Prometheus exposition format:\n%s", text)
	}
	for _, series := range metricsDashboardSeries {
		if !strings.Contains(text, series) {
			t.Errorf("/metrics missing series %q", series)
		}
	}
	return text
}

// getJSON GETs url, requires HTTP 200, and decodes the body into dst.
func getJSON(t testing.TB, url string, dst any) {
	t.Helper()
	st, body := getRaw(t, url)
	if st != 200 {
		t.Fatalf("GET %s = HTTP %d, want 200\nbody: %s", url, st, body)
	}
	if dst != nil {
		if err := json.Unmarshal(body, dst); err != nil {
			t.Fatalf("decode %s: %v\nbody: %s", url, err, body)
		}
	}
}

func postJSON(t testing.TB, url string, reqBody any) (int, []byte) {
	t.Helper()
	b, _ := json.Marshal(reqBody)
	resp, err := httpClient.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	out := new(bytes.Buffer)
	_, _ = out.ReadFrom(resp.Body)
	return resp.StatusCode, out.Bytes()
}

// ---- server lifecycle ---------------------------------------------------

type serverProc struct {
	BaseURL string
	Home    string
	logPath string
	cmd     *exec.Cmd
	// adminToken is a minted admin Bearer token for the server, set by the
	// webhook-admin boot helpers so the admin-gated receiver CRUD helpers can
	// authenticate. Empty when the server boots without an identity provider.
	adminToken string
}

func (s *serverProc) log() string {
	b, _ := os.ReadFile(s.logPath)
	return string(b)
}

// cliEnv returns the env a CLI subprocess needs to authenticate against this
// server. When the server boots an identity provider and the helper minted an
// admin token, the CLI attaches it through PODIUM_SESSION_TOKEN (the §6.3.2
// injected session token the CLI reads in readCLIToken) so admin-gated commands
// such as layer register and reingest authenticate as the admin. A server with
// no minted token returns nil so the anonymous standalone path is unchanged.
func (s *serverProc) cliEnv() []string {
	if s.adminToken == "" {
		return nil
	}
	return []string{"PODIUM_SESSION_TOKEN=" + s.adminToken}
}

// getMaybeAuth GETs url, attaching the server's minted admin token as a Bearer
// header when one is set. A server with an identity provider verifies every
// non-exempt route (§6.3.2), so a read against it must carry a token; a
// no-identity standalone server has no token and the request is anonymous, and
// /v1/load_artifact is an ungated read route, so the anonymous read is served.
func (s *serverProc) getMaybeAuth(t testing.TB, url string) (int, []byte) {
	t.Helper()
	if s.adminToken == "" {
		return getRaw(t, url)
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request %s: %v", url, err)
	}
	req.Header.Set("Authorization", "Bearer "+s.adminToken)
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(resp.Body)
	return resp.StatusCode, body.Bytes()
}

func freePort(t testing.TB) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// startServerArgs starts `podium <args> --bind 127.0.0.1:<freeport>` with
// the given env (which must pin HOME to a temp dir), waits for /healthz to
// return 200, and registers SIGINT/SIGKILL teardown. It never blocks past
// the readiness deadline.
func startServerArgs(t testing.TB, env []string, args ...string) *serverProc {
	t.Helper()
	port := freePort(t)
	bind := fmt.Sprintf("127.0.0.1:%d", port)
	full := append(append([]string{}, args...), "--bind", bind)

	logf, err := os.CreateTemp(t.TempDir(), "server-*.log")
	if err != nil {
		t.Fatalf("server log: %v", err)
	}
	cmd := exec.Command(cmdharness.Bin(t, "podium"), full...)
	cmd.Env = mergeEnv(env...)
	cmd.Stdin = bytes.NewReader(nil)
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	s := &serverProc{BaseURL: "http://" + bind, logPath: logf.Name(), cmd: cmd}
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") {
			s.Home = strings.TrimPrefix(kv, "HOME=")
		}
	}
	t.Cleanup(func() { stopProc(s.cmd) })

	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := httpClient.Get(s.BaseURL + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return s
			}
		}
		if s.cmd.ProcessState != nil && s.cmd.ProcessState.Exited() {
			t.Fatalf("server exited before ready\nlog:\n%s", s.log())
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("server not ready at %s within deadline\nlog:\n%s", s.BaseURL, s.log())
	return nil
}

// startServer is the common standalone case: a fresh HOME, --standalone,
// and the given filesystem registry ingested at startup.
func startServer(t testing.TB, registry string) *serverProc {
	t.Helper()
	args := []string{"serve", "--standalone"}
	if registry != "" {
		args = append(args, "--layer-path", registry)
	}
	return startServerArgs(t, []string{"HOME=" + t.TempDir()}, args...)
}

// stopProc asks the process to stop, then force-kills if it lingers.
func stopProc(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

// ---- watch-mode lifecycle ----------------------------------------------

type watchProc struct {
	cmd     *exec.Cmd
	logPath string
}

func (w *watchProc) log() string {
	b, _ := os.ReadFile(w.logPath)
	return string(b)
}

// startWatch launches `podium sync --watch` in the background. The caller
// polls the target for materialized files; stop() sends SIGINT and returns
// the exit code. t.Cleanup force-kills if the test forgets to stop it.
func startWatch(t testing.TB, registry, target, harness string) *watchProc {
	t.Helper()
	logf, err := os.CreateTemp(t.TempDir(), "watch-*.log")
	if err != nil {
		t.Fatalf("watch log: %v", err)
	}
	cmd := exec.Command(cmdharness.Bin(t, "podium"),
		"sync", "--registry", registry, "--target", target, "--harness", harness, "--watch")
	cmd.Env = mergeEnv("PODIUM_NO_AUTOSTANDALONE=1")
	cmd.Stdin = bytes.NewReader(nil)
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start watch: %v", err)
	}
	w := &watchProc{cmd: cmd, logPath: logf.Name()}
	t.Cleanup(func() { stopProc(w.cmd) })
	return w
}

// stop sends SIGINT and waits (bounded) for the watcher to exit, returning
// its exit code (0 for a clean shutdown).
func (w *watchProc) stop(t testing.TB) int {
	t.Helper()
	_ = w.cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- w.cmd.Wait() }()
	select {
	case err := <-done:
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		if err != nil {
			return -1
		}
		return 0
	case <-time.After(10 * time.Second):
		_ = w.cmd.Process.Kill()
		<-done
		t.Fatalf("watch did not exit within 10s of SIGINT\nlog:\n%s", w.log())
		return -1
	}
}

// ---- MCP stdio bridge ---------------------------------------------------

type rpcReq struct {
	ID     int
	Method string
	Params any
}

// mcpExec runs podium-mcp as a bounded stdio subprocess, feeding the
// supplied JSON-RPC requests (one per line) on stdin and returning the
// captured result. The process reads stdin to EOF and exits; the context
// deadline guarantees teardown.
func mcpExec(t testing.TB, env []string, reqs ...rpcReq) cliResult {
	t.Helper()
	var in bytes.Buffer
	for _, r := range reqs {
		obj := map[string]any{"jsonrpc": "2.0", "id": r.ID, "method": r.Method}
		if r.Params != nil {
			obj["params"] = r.Params
		}
		b, _ := json.Marshal(obj)
		in.Write(b)
		in.WriteByte('\n')
	}
	return runBin(t, cmdharness.Bin(t, "podium-mcp"), "", env, in.Bytes(), 45*time.Second)
}

// toolCall is a convenience for a tools/call request.
func toolCall(id int, name string, args map[string]any) rpcReq {
	return rpcReq{ID: id, Method: "tools/call", Params: map[string]any{"name": name, "arguments": args}}
}

// rpcEnvelope finds the JSON-RPC response with the given id among the
// newline-delimited stdout lines and returns the decoded envelope.
func rpcEnvelope(t testing.TB, stdout string, id int) map[string]any {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var env map[string]any
		if json.Unmarshal([]byte(line), &env) != nil {
			continue
		}
		if asInt(env["id"]) == id {
			return env
		}
	}
	t.Fatalf("no JSON-RPC response with id=%d in:\n%s", id, stdout)
	return nil
}

// rpcResult returns the `result` object of the response with the given id,
// failing if the envelope carried an `error`. A tools/call response is an MCP
// CallToolResult (§6.1.1); this returns the meta-tool domain object carried in
// structuredContent so callers read the domain fields directly. Other methods
// (initialize, tools/list, resources/*) carry no structuredContent and pass
// through unchanged.
func rpcResult(t testing.TB, stdout string, id int) map[string]any {
	t.Helper()
	env := rpcEnvelope(t, stdout, id)
	if e, ok := env["error"]; ok && e != nil {
		t.Fatalf("JSON-RPC id=%d returned error: %v", id, e)
	}
	res, _ := env["result"].(map[string]any)
	if sc, ok := res["structuredContent"].(map[string]any); ok {
		return sc
	}
	return res
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return -1
}

// pollFile waits until path exists (or the deadline elapses), returning
// whether it appeared. Used by watch-mode tests with a bounded wait.
func pollFile(path string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// ---- shared fixture content --------------------------------------------

const (
	// greetSkillArtifact is the ARTIFACT.md from the quickstart's hello
	// world example: a type:skill manifest whose body lives in SKILL.md.
	greetSkillArtifact = "---\ntype: skill\nversion: 1.0.0\ntags: [demo, hello-world]\nsensitivity: low\n---\n\n<!-- Skill body lives in SKILL.md. -->\n"
	// greetSkillBody is the matching SKILL.md.
	greetSkillBody = "---\nname: greet\ndescription: Greet the user by name and tell them today's date. Use when the user greets you or asks who you are.\nlicense: MIT\n---\n\nGreet the user warmly and state today's date.\n"
)

// runExternal runs an arbitrary command (go, make, ...) found on PATH with a
// hard deadline, returning its captured output. Used by README build-tooling
// tests. Returns (result, false) when the command is not installed.
func runExternal(t testing.TB, dir string, timeout time.Duration, name string, args ...string) (cliResult, bool) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		return cliResult{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(nil)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("%s %s timed out after %s", name, strings.Join(args, " "), timeout)
	}
	res := cliResult{Stdout: so.String(), Stderr: se.String()}
	if ee, ok := err.(*exec.ExitError); ok {
		res.Exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %s %s: %v", name, strings.Join(args, " "), err)
	}
	return res, true
}

// repoRoot walks up from the test working directory to the module root.
func repoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repo root (go.mod) not found from %s", dir)
		}
		dir = parent
	}
}

// ---- filesystem assertion helpers --------------------------------------

func readFile(t testing.TB, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func mustExist(t testing.TB, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected %s to exist: %v", path, err)
	}
}

func mustNotExist(t testing.TB, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Errorf("expected %s to not exist", path)
	}
}

func appendLine(t testing.TB, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s for append: %v", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatalf("append to %s: %v", path, err)
	}
}

// mkArtifact writes a single ARTIFACT.md under dir, creating parents.
func mkArtifact(t testing.TB, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ARTIFACT.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write ARTIFACT.md: %v", err)
	}
}

// readTreeAll returns every file under dir keyed by relative path.
func readTreeAll(t testing.TB, dir string) map[string]string {
	t.Helper()
	return testharness.ReadTree(t, dir)
}

// readTreeFiltered returns the target's files excluding the .podium/ sync
// metadata, so artifact output can be compared without deployment state.
func readTreeFiltered(t testing.TB, dir string) map[string]string {
	t.Helper()
	all := testharness.ReadTree(t, dir)
	out := map[string]string{}
	for k, v := range all {
		if strings.HasPrefix(k, ".podium/") {
			continue
		}
		out[k] = v
	}
	return out
}

// syncAndSnapshot runs `podium sync --harness none` then returns the
// artifact file snapshot (excluding .podium/ metadata).
func syncAndSnapshot(t testing.TB, registry, target string) map[string]string {
	t.Helper()
	res := runPodium(t, "", nil, "sync", "--registry", registry, "--target", target, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	return readTreeFiltered(t, target)
}

// contextArtifact builds an ARTIFACT.md for a type:context artifact with
// the given description (descriptions in ARTIFACT.md are what the registry
// indexes for search).
func contextArtifact(desc string) string {
	return fmt.Sprintf("---\ntype: context\nversion: 1.0.0\ndescription: %s\n---\n\n%s body.\n", desc, desc)
}

// skillBody returns a SKILL.md whose name matches the leaf directory. The
// linter (run at server boot ingest) rejects a skill whose name differs
// from its parent directory, so the caller must place this under a
// directory named <name>.
func skillBody(name string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: The %s skill for tests. Use when the user needs %s.\n---\n\n%s body.\n", name, name, name, name)
}

// skillBodyDesc builds a SKILL.md with a specific name and description. Per
// §4.3.4 the agentskills.io name/description live in SKILL.md; this helper is
// used where a search query must match the skill's description.
func skillBodyDesc(name, description string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s body.\n", name, description, name)
}
