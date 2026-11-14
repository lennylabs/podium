package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness/auditharness"
	"github.com/lennylabs/podium/internal/testharness/cmdharness"
	"github.com/lennylabs/podium/pkg/audit"
)

// Spec: §8.3, §13.12 — "Both the registry and local sinks can be
// redirected to external SIEM / log aggregation independently." When
// PODIUM_AUDIT_LOG_PATH is an http(s) URL the standalone registry forwards
// each catalogue event (here domain.loaded) to that receiver as NDJSON
// batches rather than writing a local file, and every request carries the
// header PODIUM_AUDIT_HEADERS_FILE names. The receiver is an in-test
// recorder.
//
// Spec: §8.1 — the read carries X-Podium-Client, so the forwarded
// domain.loaded record's caller.client records the client the request named.
func TestAuditSink_RegistryEndpointRedirect(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var received []string
	var contentTypes []string
	var authorizations []string
	recorder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, string(body))
		contentTypes = append(contentTypes, r.Header.Get("Content-Type"))
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(recorder.Close)

	reg := writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("x")})
	home := t.TempDir()
	headers := filepath.Join(t.TempDir(), "audit-headers")
	if err := os.WriteFile(headers, []byte("Authorization: Bearer hv-3mzt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := startServerArgs(t, []string{
		"HOME=" + home,
		"PODIUM_AUDIT_LOG_PATH=" + recorder.URL + "/sink",
		"PODIUM_AUDIT_HEADERS_FILE=" + headers,
	}, "serve", "--standalone", "--layer-path", reg)

	// A read emits domain.loaded, which the registry forwards to the receiver.
	getWithClient(t, srv.BaseURL+"/v1/load_domain?path=finance", "kind=cli&name=podium")

	deadline := time.Now().Add(10 * time.Second)
	forwarded := ""
	for time.Now().Before(deadline) && forwarded == "" {
		mu.Lock()
		forwarded = findRecordLine(received, "domain.loaded")
		mu.Unlock()
		if forwarded == "" {
			time.Sleep(50 * time.Millisecond)
		}
	}
	mu.Lock()
	bodies := strings.Join(received, "")
	types := append([]string(nil), contentTypes...)
	auths := append([]string(nil), authorizations...)
	mu.Unlock()
	if forwarded == "" {
		t.Fatalf("receiver did not get a forwarded domain.loaded record; got:\n%s", bodies)
	}
	for i, ct := range types {
		if ct != "application/x-ndjson" {
			t.Errorf("request %d Content-Type = %q, want application/x-ndjson", i, ct)
		}
	}
	for i, auth := range auths {
		if auth != "Bearer hv-3mzt" {
			t.Errorf("request %d Authorization = %q, want the headers file's value", i, auth)
		}
	}

	// A batch can carry the standalone boot's records beside domain.loaded,
	// so the record is found by line, and its caller.client records the kind
	// the request named.
	if kind := lineClientKind(t, forwarded); kind != "cli" {
		t.Errorf("forwarded domain.loaded caller.client.kind = %q, want cli\nline: %s", kind, forwarded)
	}

	// The redirect replaces the file sink: no local audit.log is written at
	// the default ~/.podium/audit.log location.
	if _, err := os.Stat(filepath.Join(home, ".podium", "audit.log")); err == nil {
		t.Errorf("redirected registry wrote a local audit.log under %s/.podium", home)
	}
}

// findRecordLine splits each NDJSON body on newlines and returns the first
// record line whose type is typ, or "".
func findRecordLine(bodies []string, typ string) string {
	for _, b := range bodies {
		for _, line := range strings.Split(b, "\n") {
			var ev struct {
				Type string `json:"type"`
			}
			if json.Unmarshal([]byte(line), &ev) == nil && ev.Type == typ {
				return line
			}
		}
	}
	return ""
}

// getWithClient sends a GET to url carrying the given X-Podium-Client value
// and fails the test unless the registry answers 200.
func getWithClient(t *testing.T, url, client string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request %s: %v", url, err)
	}
	req.Header.Set(audit.ClientHeader, client)
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s = HTTP %d, want 200\nbody: %s", url, resp.StatusCode, body)
	}
}

// lineClientKind decodes one NDJSON record line and returns its
// caller.client.kind.
func lineClientKind(t *testing.T, line string) string {
	t.Helper()
	var ev struct {
		Caller struct {
			Client struct {
				Kind string `json:"kind"`
			} `json:"client"`
		} `json:"caller"`
	}
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("decode forwarded record: %v\nline: %s", err, line)
	}
	return ev.Caller.Client.Kind
}

// lockedBuffer collects a subprocess stream that the test reads while the
// process still writes it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// mcpProc is a podium-mcp process whose stdin stays open until the test
// closes it, so a case can signal the process mid-session.
type mcpProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout lockedBuffer
	stderr lockedBuffer
	exited <-chan struct{}
}

// mcpEnv is the environment of a bridge whose registry is unreachable, with
// extra appended. The bridge appends a search's record before it proxies the
// call, so the unreachable registry still yields a record. Signature
// verification is off, because no case serves a signed artifact.
func mcpEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	return append([]string{
		"PODIUM_REGISTRY=http://127.0.0.1:1",
		"PODIUM_HARNESS=none",
		"PODIUM_VERIFY_SIGNATURES=never",
		"PODIUM_CACHE_DIR=" + t.TempDir(),
	}, extra...)
}

// withoutVar removes every entry for key from env, so the subprocess sees
// the variable unset rather than empty.
func withoutVar(env []string, key string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// startMCP starts podium-mcp with env in an isolated HOME. unset names
// variables the process must not inherit. The cleanup closes stdin and
// stops a process the test left running.
func startMCP(t *testing.T, env []string, unset ...string) *mcpProc {
	t.Helper()
	home := cmdharness.IsolatedHome(t)
	cmd := exec.Command(cmdharness.Bin(t, "podium-mcp"))
	cmd.Dir = home
	full := mergeEnv(withIsolatedHome(env, home)...)
	for _, key := range unset {
		full = withoutVar(full, key)
	}
	cmd.Env = full
	p := &mcpProc{cmd: cmd}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	p.stdin = stdin
	cmd.Stdout = &p.stdout
	cmd.Stderr = &p.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start podium-mcp: %v", err)
	}
	p.exited = reapOnExit(cmd)
	t.Cleanup(func() {
		_ = p.stdin.Close()
		stopProc(p.cmd)
		if strings.Contains(p.stderr.String(), "panic:") {
			t.Errorf("podium-mcp panicked:\n%s", p.stderr.String())
		}
	})
	return p
}

// send writes one JSON-RPC request frame. A write to a process that has
// already exited is reported by the case's own assertions, so its error is
// ignored here.
func (p *mcpProc) send(r rpcReq) {
	obj := map[string]any{"jsonrpc": "2.0", "id": r.ID, "method": r.Method}
	if r.Params != nil {
		obj["params"] = r.Params
	}
	b, _ := json.Marshal(obj)
	_, _ = p.stdin.Write(append(b, '\n'))
}

// hasResponse reports whether stdout holds a response with id.
func (p *mcpProc) hasResponse(id int) bool {
	for _, line := range strings.Split(p.stdout.String(), "\n") {
		var env map[string]any
		if json.Unmarshal([]byte(line), &env) == nil && asInt(env["id"]) == id {
			return true
		}
	}
	return false
}

// awaitResponse waits for the response with id.
func (p *mcpProc) awaitResponse(t *testing.T, id int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if p.hasResponse(id) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no response with id %d\nstderr:\n%s", id, p.stderr.String())
}

func (p *mcpProc) signal(t *testing.T, sig os.Signal) {
	t.Helper()
	if err := p.cmd.Process.Signal(sig); err != nil {
		t.Fatalf("signal podium-mcp: %v", err)
	}
}

// exitedWithin reports whether the process exits within d.
func (p *mcpProc) exitedWithin(d time.Duration) bool {
	select {
	case <-p.exited:
		return true
	case <-time.After(d):
		return false
	}
}

// waitExit waits until the process exits or deadline passes, and returns
// its exit status.
func (p *mcpProc) waitExit(t *testing.T, deadline time.Time) int {
	t.Helper()
	if !p.exitedWithin(time.Until(deadline)) {
		t.Fatalf("podium-mcp did not exit by the deadline\nstderr:\n%s", p.stderr.String())
	}
	return p.cmd.ProcessState.ExitCode()
}

// searchCall is a search_artifacts tool call for query.
func searchCall(id int, query string) rpcReq {
	return toolCall(id, "search_artifacts", map[string]any{"query": query})
}

// flushDeadline is the §8.3 5-second flush budget plus a margin, the
// longest a flushing bridge may take to exit.
const flushDeadline = 8 * time.Second

// Spec: §8.3 — on stdin EOF the bridge flushes its local stream. A record
// whose receiver accepts is delivered by the time the process exits with
// status 0, and a receiver that refuses throughout leaves the record queued,
// as podium_audit_queue_depth shows, and does not hold the exit past the
// flush budget.
func TestAuditSink_MCPFlushesOnSessionEnd(t *testing.T) {
	t.Parallel()
	t.Run("delivered", func(t *testing.T) {
		t.Parallel()
		rcv := auditharness.NewReceiver(t)
		res := mcpExec(t, mcpEnv(t, "PODIUM_AUDIT_SINK="+rcv.URL()), searchCall(1, "q-session"))
		cliWantExit(t, res, 0, "podium-mcp session")
		if _, ok := rcv.WaitForRecord(t, 0, auditharness.OfType("artifacts.searched")); !ok {
			t.Errorf("the receiver holds no artifacts.searched record when the process exits\nstderr:\n%s", res.Stderr)
		}
		if strings.Contains(res.Stderr, "panic:") {
			t.Errorf("podium-mcp panicked:\n%s", res.Stderr)
		}
	})
	t.Run("receiver down", func(t *testing.T) {
		t.Parallel()
		rcv := auditharness.NewReceiver(t)
		rcv.SetResponse(http.StatusServiceUnavailable, "")
		p := startMCP(t, mcpEnv(t, "PODIUM_AUDIT_SINK="+rcv.URL(), "PODIUM_MCP_METRICS_ADDR=127.0.0.1:0"))
		p.send(searchCall(1, "q-down"))
		p.awaitResponse(t, 1)
		addr := metricsAddrOf(t, p)
		deadline := time.Now().Add(10 * time.Second)
		depth := -1.0
		for time.Now().Before(deadline) && depth <= 0 {
			_, body := getRaw(t, "http://"+addr+"/metrics")
			depth = metricValue(string(body), `podium_audit_queue_depth{destination="http"}`)
			time.Sleep(50 * time.Millisecond)
		}
		if depth <= 0 {
			t.Errorf("podium_audit_queue_depth{http} = %v, want above 0", depth)
		}
		closedAt := time.Now()
		_ = p.stdin.Close()
		if code := p.waitExit(t, closedAt.Add(flushDeadline)); code != 0 {
			t.Errorf("exit status = %d, want 0\nstderr:\n%s", code, p.stderr.String())
		}
	})
}

// metricsAddrOf reads the metrics listener address the bridge reports on
// stderr.
func metricsAddrOf(t *testing.T, p *mcpProc) string {
	t.Helper()
	const marker = "Prometheus endpoint mounted at http://"
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, rest, ok := strings.Cut(p.stderr.String(), marker); ok {
			if addr, _, ok := strings.Cut(rest, "/metrics"); ok {
				return addr
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the bridge did not report its metrics address\nstderr:\n%s", p.stderr.String())
	return ""
}

// Spec: §8.3 — when the response write of a search_artifacts call fails, the
// bridge exits with status 1, and the record the call appended before its
// response is at the receiver once the process has exited, which a Shutdown
// that os.Exit skipped would not deliver.
func TestAuditSink_MCPFlushesOnErrorExit(t *testing.T) {
	t.Parallel()
	rcv := auditharness.NewReceiver(t)
	readOnly, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = readOnly.Close()
	stdout, err := os.Open(readOnly.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdout.Close() })
	home := cmdharness.IsolatedHome(t)
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "search_artifacts", "arguments": map[string]any{"query": "q-error"}}})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cmdharness.Bin(t, "podium-mcp"))
	cmd.Dir = home
	cmd.Env = mergeEnv(withIsolatedHome(mcpEnv(t, "PODIUM_AUDIT_SINK="+rcv.URL()), home)...)
	cmd.Stdin = bytes.NewReader(append(req, '\n'))
	cmd.Stdout = stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("podium-mcp = %v, want exit status 1\nstderr:\n%s", runErr, stderr.String())
	}
	if _, ok := rcv.WaitForRecord(t, 0, auditharness.OfType("artifacts.searched")); !ok {
		t.Errorf("the receiver holds no artifacts.searched record after the error exit\nstderr:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "panic:") {
		t.Errorf("podium-mcp panicked:\n%s", stderr.String())
	}
}

// refusingReceiver starts a receiver that refuses under a Retry-After that
// outlasts every case's deadline, so only a flush delivers a record.
func refusingReceiver(t *testing.T) *auditharness.Receiver {
	t.Helper()
	rcv := auditharness.NewReceiver(t)
	rcv.SetResponse(http.StatusServiceUnavailable, "60")
	return rcv
}

// acceptHeld makes the receiver accept and hold every later request, so a
// case can act while the flush waits on it.
func acceptHeld(rcv *auditharness.Receiver) {
	rcv.Hold()
	rcv.SetResponse(http.StatusOK, "")
}

// Spec: §6.8, §8.3 — SIGINT and SIGTERM end the bridge the way stdin EOF
// does: the bridge stops dispatching, flushes its local stream within the
// budget, and exits with status 0. A later signal does not cut the flush
// short, and a tool call in progress is not waited for.
func TestAuditSink_MCPFlushesOnSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test cannot deliver SIGINT or SIGTERM to a process on Windows")
	}
	t.Parallel()
	for name, sig := range map[string]syscall.Signal{"SIGTERM": syscall.SIGTERM, "SIGINT": syscall.SIGINT} {
		t.Run(name+" with stdin open", func(t *testing.T) {
			t.Parallel()
			rcv := refusingReceiver(t)
			p := startMCP(t, mcpEnv(t, "PODIUM_AUDIT_SINK="+rcv.URL()))
			p.send(searchCall(1, "q-signal"))
			awaitRefused(t, rcv, "artifacts.searched")
			rcv.SetResponse(http.StatusOK, "")
			signalledAt := time.Now()
			p.signal(t, sig)
			if code := p.waitExit(t, signalledAt.Add(flushDeadline)); code != 0 {
				t.Errorf("exit status = %d, want 0\nstderr:\n%s", code, p.stderr.String())
			}
			if _, ok := rcv.WaitForRecord(t, 0, auditharness.OfType("artifacts.searched")); !ok {
				t.Error("the receiver holds no artifacts.searched record after the signal")
			}
		})
	}
	t.Run("SIGTERM during the stdin-EOF flush", func(t *testing.T) {
		t.Parallel()
		rcv := refusingReceiver(t)
		p := startMCP(t, mcpEnv(t, "PODIUM_AUDIT_SINK="+rcv.URL()))
		p.send(searchCall(1, "q-eof"))
		awaitRefused(t, rcv, "artifacts.searched")
		acceptHeld(rcv)
		_ = p.stdin.Close()
		rcv.AwaitHeld(t, 5*time.Second)
		signalledAt := time.Now()
		p.signal(t, syscall.SIGTERM)
		if p.exitedWithin(time.Second) {
			t.Error("SIGTERM ended the process before the receiver answered the flush")
		}
		rcv.Release()
		if code := p.waitExit(t, signalledAt.Add(flushDeadline)); code != 0 {
			t.Errorf("exit status = %d, want 0\nstderr:\n%s", code, p.stderr.String())
		}
	})
	t.Run("second signal during the signal-path flush", func(t *testing.T) {
		t.Parallel()
		rcv := refusingReceiver(t)
		p := startMCP(t, mcpEnv(t, "PODIUM_AUDIT_SINK="+rcv.URL()))
		p.send(searchCall(1, "q-second"))
		awaitRefused(t, rcv, "artifacts.searched")
		acceptHeld(rcv)
		signalledAt := time.Now()
		p.signal(t, syscall.SIGINT)
		rcv.AwaitHeld(t, 5*time.Second)
		p.signal(t, syscall.SIGTERM)
		if p.exitedWithin(time.Second) {
			t.Error("the second signal ended the process before the receiver answered the flush")
		}
		rcv.Release()
		if code := p.waitExit(t, signalledAt.Add(flushDeadline)); code != 0 {
			t.Errorf("exit status = %d, want 0\nstderr:\n%s", code, p.stderr.String())
		}
		if _, ok := rcv.WaitForRecord(t, 0, auditharness.OfType("artifacts.searched")); !ok {
			t.Error("the receiver holds no artifacts.searched record after the flush")
		}
	})
	t.Run("SIGTERM without a local stream", func(t *testing.T) {
		t.Parallel()
		p := startMCP(t, mcpEnv(t), "PODIUM_AUDIT_SINK")
		p.send(rpcReq{ID: 1, Method: "tools/list"})
		p.awaitResponse(t, 1)
		signalledAt := time.Now()
		p.signal(t, syscall.SIGTERM)
		if code := p.waitExit(t, signalledAt.Add(flushDeadline)); code != 0 {
			t.Errorf("exit status = %d, want 0\nstderr:\n%s", code, p.stderr.String())
		}
	})
	t.Run("no dispatch after the signal", func(t *testing.T) {
		t.Parallel()
		rcv := refusingReceiver(t)
		logPath := filepath.Join(t.TempDir(), "mcp.log")
		p := startMCP(t, mcpEnv(t, "PODIUM_AUDIT_SINK="+logPath+","+rcv.URL()))
		p.send(searchCall(1, "q-before"))
		p.awaitResponse(t, 1)
		awaitRefused(t, rcv, "artifacts.searched")
		acceptHeld(rcv)
		signalledAt := time.Now()
		p.signal(t, syscall.SIGTERM)
		rcv.AwaitHeld(t, 5*time.Second)
		p.send(searchCall(2, "q-after"))
		time.Sleep(500 * time.Millisecond)
		rcv.Release()
		if code := p.waitExit(t, signalledAt.Add(flushDeadline)); code != 0 {
			t.Errorf("exit status = %d, want 0\nstderr:\n%s", code, p.stderr.String())
		}
		if p.hasResponse(2) {
			t.Error("the bridge answered a call written after the signal")
		}
		if n := strings.Count(readOrEmpty(logPath), `"type":"artifacts.searched"`); n != 1 {
			t.Errorf("the local file holds %d artifacts.searched record(s), want 1", n)
		}
		if _, ok := rcv.WaitForRecord(t, 0, auditharness.OfType("artifacts.searched")); !ok {
			t.Error("the receiver holds no artifacts.searched record")
		}
	})
	t.Run("SIGTERM during a tool call", func(t *testing.T) {
		t.Parallel()
		rcv := refusingReceiver(t)
		registry, arrived, _ := holdingRegistry(t, "/v1/search_artifacts", nil)
		p := startMCP(t, append(mcpEnv(t, "PODIUM_AUDIT_SINK="+rcv.URL()), "PODIUM_REGISTRY="+registry))
		p.send(searchCall(1, "q-held"))
		awaitClosed(t, arrived, "the search never reached the registry")
		awaitRefused(t, rcv, "artifacts.searched")
		rcv.SetResponse(http.StatusOK, "")
		signalledAt := time.Now()
		p.signal(t, syscall.SIGTERM)
		if code := p.waitExit(t, signalledAt.Add(flushDeadline)); code != 0 {
			t.Errorf("exit status = %d, want 0\nstderr:\n%s", code, p.stderr.String())
		}
		if p.hasResponse(1) {
			t.Error("the held call got a response, want none")
		}
		if _, ok := rcv.WaitForRecord(t, 0, auditharness.OfType("artifacts.searched")); !ok {
			t.Error("the receiver holds no record of the held call")
		}
	})
	t.Run("tool call that appends during the flush", func(t *testing.T) {
		t.Parallel()
		rcv := refusingReceiver(t)
		const id = "finance/glossary"
		stub, _ := mbStubRegistry(t, id, contextArtifact("Glossary"), http.StatusOK)
		registry, arrived, release := holdingRegistry(t, "/v1/load_artifact", stub)
		logPath := filepath.Join(t.TempDir(), "mcp.log")
		p := startMCP(t, append(mcpEnv(t, "PODIUM_AUDIT_SINK="+logPath+","+rcv.URL()),
			"PODIUM_REGISTRY="+registry, "PODIUM_MATERIALIZE_ROOT="+t.TempDir()))
		p.send(searchCall(1, "q-first"))
		p.awaitResponse(t, 1)
		awaitRefused(t, rcv, "artifacts.searched")
		p.send(toolCall(2, "load_artifact", map[string]any{"id": id}))
		awaitClosed(t, arrived, "the load never reached the registry")
		acceptHeld(rcv)
		signalledAt := time.Now()
		p.signal(t, syscall.SIGTERM)
		rcv.AwaitHeld(t, 5*time.Second)
		release()
		if pollAuditLine(t, logPath, `"type":"artifact.loaded"`, 3*time.Second) == "" {
			t.Errorf("the released call appended no artifact.loaded record\nstderr:\n%s", p.stderr.String())
		}
		rcv.Release()
		if code := p.waitExit(t, signalledAt.Add(flushDeadline)); code != 0 {
			t.Errorf("exit status = %d, want 0\nstderr:\n%s", code, p.stderr.String())
		}
		local := []string{readOrEmpty(logPath)}
		searched := findRecordLine(local, "artifacts.searched")
		loaded := findRecordLine(local, "artifact.loaded")
		var s, l auditharness.Record
		_ = json.Unmarshal([]byte(searched), &s)
		_ = json.Unmarshal([]byte(loaded), &l)
		if s.Hash == "" || l.PrevHash != s.Hash {
			t.Errorf("artifact.loaded prev_hash = %q, want the artifacts.searched hash %q", l.PrevHash, s.Hash)
		}
		if _, ok := rcv.WaitForRecord(t, 0, auditharness.OfType("artifacts.searched")); !ok {
			t.Error("the receiver holds no artifacts.searched record")
		}
		if _, ok := rcv.WaitForRecord(t, 0, auditharness.OfType("artifact.loaded")); ok {
			t.Error("the receiver holds the artifact.loaded record appended during the flush")
		}
		if want := "for destination " + rcv.URL() + ": shutdown"; !strings.Contains(p.stderr.String(), want) {
			t.Errorf("stderr holds no shutdown drop line %q:\n%s", want, p.stderr.String())
		}
	})
}

// holdingRegistry starts a registry stub that holds every request for path
// until release is called or the test ends, and closes arrived when the
// first one comes in. Every request goes on to upstream, or answers 404 when
// upstream is nil.
func holdingRegistry(t *testing.T, path string, upstream *httptest.Server) (string, <-chan struct{}, func()) {
	t.Helper()
	arrived := make(chan struct{})
	gate := make(chan struct{})
	var once, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate) }) }
	proxy := http.NotFoundHandler()
	if upstream != nil {
		target, _ := url.Parse(upstream.URL)
		proxy = httputil.NewSingleHostReverseProxy(target)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == path {
			once.Do(func() { close(arrived) })
			<-gate
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(release)
	return srv.URL, arrived, release
}

// awaitClosed waits for ch to close and fails the test with msg otherwise.
func awaitClosed(t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(15 * time.Second):
		t.Fatal(msg)
	}
}

// Spec: §6.2 — the local stream refuses a stdout destination: the bridge
// exits with status 2, names config.audit_sink_unavailable and
// PODIUM_AUDIT_SINK, and writes no audit line to standard output.
// Matrix: §6.10 (config.audit_sink_unavailable)
func TestAuditSink_MCPRefusesStdout(t *testing.T) {
	t.Parallel()
	res := mcpExec(t, mcpEnv(t, "PODIUM_AUDIT_SINK=stdout"), searchCall(1, "q-stdout"))
	cliWantExit(t, res, 2, "podium-mcp with a stdout audit destination")
	for _, want := range []string{"config.audit_sink_unavailable", "PODIUM_AUDIT_SINK"} {
		if !strings.Contains(res.Stderr, want) {
			t.Errorf("stderr does not name %s:\n%s", want, res.Stderr)
		}
	}
	if strings.Contains(res.Stdout, `"type":`) {
		t.Errorf("stdout carries an audit line:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stderr, "panic:") {
		t.Errorf("podium-mcp panicked:\n%s", res.Stderr)
	}
}

// Spec: §6.2, §8.3 — a PODIUM_AUDIT_SINK file the bridge cannot open refuses
// startup: the bridge exits with status 2, names the code, the variable, and
// the path, and answers no MCP request.
// Matrix: §6.10 (config.audit_sink_unavailable)
func TestAuditSink_MCPRefusesUnopenableFile(t *testing.T) {
	t.Parallel()
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "audit.log")
	res := mcpExec(t, mcpEnv(t, "PODIUM_AUDIT_SINK="+path), searchCall(1, "q-unopenable"))
	cliWantExit(t, res, 2, "podium-mcp with an unopenable audit file")
	for _, want := range []string{"config.audit_sink_unavailable", "PODIUM_AUDIT_SINK", path} {
		if !strings.Contains(res.Stderr, want) {
			t.Errorf("stderr does not name %s:\n%s", want, res.Stderr)
		}
	}
	if strings.TrimSpace(res.Stdout) != "" {
		t.Errorf("stdout carries an MCP response:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stderr, "panic:") {
		t.Errorf("podium-mcp panicked:\n%s", res.Stderr)
	}
}

// Spec: §6.8, §8.3, §13.8 — run refuses an unknown PODIUM_TRACING value
// before it builds the server: the bridge exits with status 2, names the
// variable, answers no MCP request, and opens no local audit stream, so the
// PODIUM_AUDIT_SINK file is never created.
func TestAuditSink_MCPUnknownTracingModeOpensNoStream(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	res := mcpExec(t, mcpEnv(t, "PODIUM_AUDIT_SINK="+path, "PODIUM_TRACING=carrier-pigeon"), searchCall(1, "q-tracing"))
	cliWantExit(t, res, 2, "podium-mcp with an unknown PODIUM_TRACING value")
	if !strings.Contains(res.Stderr, "PODIUM_TRACING") {
		t.Errorf("stderr does not name PODIUM_TRACING:\n%s", res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "" {
		t.Errorf("stdout carries an MCP response:\n%s", res.Stdout)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("audit file %s exists or is unreadable (%v), want it absent", path, err)
	}
}
