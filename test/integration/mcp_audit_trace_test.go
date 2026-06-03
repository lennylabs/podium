package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Spec: §8.1 (F-8.1.1) — "Both streams share trace IDs." The MCP bridge
// injects its in-flight span as a W3C traceparent on the outbound registry
// call (the registry derives its stream's trace id from that header), and now
// stamps the same trace id on the local audit event. This drives the real
// podium-mcp binary with tracing on against a stub that captures the inbound
// traceparent, then asserts the local artifact.loaded event carries the
// matching trace id.
func TestPodiumMCP_LocalAuditSharesTraceID(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var gotTraceparent string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/load_artifact") {
			mu.Lock()
			gotTraceparent = r.Header.Get("traceparent")
			mu.Unlock()
			// A non-empty id drives the bridge into its delivery path, where the
			// artifact.loaded event is emitted before content-hash verification.
			// The bogus hash then fails verification, but the audit event has
			// already been written, which is exactly what this test inspects.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":           "finance/x",
				"version":      "1.0.0",
				"content_hash": "sha256:" + strings.Repeat("a", 64),
				"frontmatter":  "---\nname: x\ntype: context\nversion: 1.0.0\n---\n",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(stub.Close)

	// A dummy OTLP endpoint turns on a recording tracer so the bridge injects a
	// valid (non-zero) traceparent. Exports fail silently against the dummy;
	// only the trace-context propagation matters here.
	otlp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(otlp.Close)

	auditPath := filepath.Join(t.TempDir(), "local-audit.log")
	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"PODIUM_REGISTRY="+stub.URL,
		"PODIUM_AUDIT_SINK="+auditPath,
		"PODIUM_VERIFY_SIGNATURES=never",
		"OTEL_EXPORTER_OTLP_ENDPOINT="+otlp.URL,
		"OTEL_EXPORTER_OTLP_TRACES_INSECURE=true",
	)
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "tools/call", ID: 1, Params: map[string]any{
			"name": "load_artifact", "arguments": map[string]any{"id": "finance/x"},
		}},
	}))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\nstderr:\n%s", err, stderr.String())
	}

	mu.Lock()
	tp := gotTraceparent
	mu.Unlock()
	if tp == "" {
		t.Fatalf("registry stub never received a traceparent header (tracing not propagated)")
	}
	// traceparent = version-traceid-spanid-flags; the trace id is field 1.
	parts := strings.Split(tp, "-")
	if len(parts) < 4 {
		t.Fatalf("malformed traceparent %q", tp)
	}
	wantTraceID := parts[1]
	if wantTraceID == strings.Repeat("0", 32) {
		t.Fatalf("traceparent carried the all-zero trace id (no recording span): %q", tp)
	}

	localTraceID := readArtifactLoadedTraceID(t, auditPath)
	if localTraceID == "" {
		t.Fatalf("local audit artifact.loaded event carried no trace_id\nfile:\n%s", readFileOrEmpty(auditPath))
	}
	if localTraceID != wantTraceID {
		t.Errorf("trace ids differ: local=%q registry-injected=%q (streams must share one id)", localTraceID, wantTraceID)
	}
}

// readArtifactLoadedTraceID returns the trace_id of the first artifact.loaded
// event in a JSON-Lines local audit file, or "" if none.
func readArtifactLoadedTraceID(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if line == "" {
					continue
				}
				var ev struct {
					Type    string `json:"type"`
					TraceID string `json:"trace_id"`
				}
				if json.Unmarshal([]byte(line), &ev) == nil && ev.Type == "artifact.loaded" {
					return ev.TraceID
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	return ""
}

func readFileOrEmpty(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}
