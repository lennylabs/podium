package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// The OTLP bytes the compiled podium-mcp exports carry no search text, no
// session_id, and no presigned-URL signature, on a call the registry answers,
// on a presigned fetch that fails, and on a call that fails because the
// registry cannot be reached. otlptracehttp sends uncompressed protobuf, which
// stores strings verbatim, so searching the raw request bodies covers every
// field of every span without a field list.
// Spec: §13.8
func TestMCPTrace_OTLPExportCarriesNoQuery(t *testing.T) {
	bin := cmdharness.Bin(t, "podium-mcp")
	collector := newOTLPCollector(t)
	closedURL := closedListenerURL(t)
	stub := traceScrubRegistry(t, closedURL+"/obj?X-Amz-Credential=CMARK&X-Amz-Signature=SIGMARK")

	runTracedMCP(t, bin, collector.URL, stub.URL, []rpcCall{
		{Method: "initialize", ID: 1, Params: map[string]any{
			"protocolVersion": "2025-06-18",
			"clientInfo":      map[string]any{"name": "trace-test", "version": "1.0.0"},
		}},
		{Method: "tools/call", ID: 2, Params: map[string]any{
			"name": "search_artifacts", "arguments": map[string]any{"query": "QMARK"}}},
		{Method: "tools/call", ID: 3, Params: map[string]any{
			"name": "load_artifact", "arguments": map[string]any{"id": "finance/a"}}},
	})
	run1 := collector.take()

	runTracedMCP(t, bin, collector.URL, closedListenerURL(t), []rpcCall{
		{Method: "initialize", ID: 1, Params: map[string]any{
			"protocolVersion": "2025-06-18",
			"clientInfo":      map[string]any{"name": "trace-test", "version": "1.0.0"},
		}},
		{Method: "tools/call", ID: 2, Params: map[string]any{
			"name": "search_artifacts", "arguments": map[string]any{"query": "RMARK"}}},
	})
	run2 := collector.take()

	for name, bodies := range map[string][][]byte{"run 1": run1, "run 2": run2} {
		if len(bodies) == 0 {
			t.Fatalf("%s: the collector received no OTLP export", name)
		}
		for _, b := range bodies {
			for _, marker := range []string{"QMARK", "RMARK", "CMARK", "SIGMARK"} {
				if bytes.Contains(b, []byte(marker)) {
					t.Errorf("%s: an exported OTLP body contains %q", name, marker)
				}
			}
		}
	}

	// Positive controls. A body that fails to decode as an uncompressed
	// ExportTraceServiceRequest fails here, so the raw-bytes search above
	// cannot pass on bytes it could not read.
	spans1 := clientSpans(t, run1)
	if findSpan(spans1, func(s *tracepb.Span) bool {
		return spanAttr(s, "url.full") == stub.URL+"/v1/search_artifacts"
	}) == nil {
		t.Errorf("run 1: no client span with url.full %s/v1/search_artifacts", stub.URL)
	}
	if findSpan(spans1, func(s *tracepb.Span) bool {
		return spanAttr(s, "url.full") == closedURL+"/obj" &&
			s.GetStatus().GetCode() == tracepb.Status_STATUS_CODE_ERROR
	}) == nil {
		t.Errorf("run 1: no failed client span for the presigned fetch %s/obj", closedURL)
	}
	if findSpan(clientSpans(t, run2), func(s *tracepb.Span) bool {
		return s.GetStatus().GetCode() == tracepb.Status_STATUS_CODE_ERROR && s.GetStatus().GetMessage() != ""
	}) == nil {
		t.Error("run 2: no client span with STATUS_CODE_ERROR and a non-empty message")
	}
}

// otlpCollector records the body of every OTLP/HTTP export it receives.
type otlpCollector struct {
	*httptest.Server
	mu     sync.Mutex // guards bodies
	bodies [][]byte
}

func newOTLPCollector(t *testing.T) *otlpCollector {
	t.Helper()
	c := &otlpCollector{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Encoding") != "" {
			t.Errorf("OTLP export arrived with Content-Encoding %q, want none", r.Header.Get("Content-Encoding"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read OTLP body: %v", err)
		}
		c.mu.Lock()
		c.bodies = append(c.bodies, body)
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	t.Cleanup(c.Close)
	return c
}

// take returns the bodies recorded so far and clears them.
func (c *otlpCollector) take() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.bodies
	c.bodies = nil
	return out
}

// closedListenerURL returns the URL of a listener that has been closed, so a
// request to it fails in the transport.
func closedListenerURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	return u
}

// traceScrubRegistry answers search_artifacts with an empty result and
// load_artifact with one large resource at presignedURL. The bridge fetches
// large resources before the delivery check, so the response needs no sealed
// delivery record.
func traceScrubRegistry(t *testing.T, presignedURL string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/search_artifacts":
			_, _ = io.WriteString(w, `{"results":[],"total_matched":0}`)
		case "/v1/load_artifact":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "finance/a", "version": "1.0.0", "type": "context",
				"content_hash": "sha256:" + strings.Repeat("0", 64),
				"frontmatter":  "---\ntype: context\n---\n",
				"large_resources": map[string]any{
					"data/big.bin": map[string]any{
						"presigned_url": presignedURL,
						"content_hash":  "sha256:" + strings.Repeat("1", 64),
						"size":          4,
					},
				},
			})
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runTracedMCP runs podium-mcp against registry with OTLP export to collector,
// sends calls, and closes stdin so the deferred tracing shutdown flushes the
// batch before the process exits.
func runTracedMCP(t *testing.T, bin, collector, registry string, calls []rpcCall) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	home := cmdharness.IsolatedHome(t)
	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir = home
	cmd.Env = []string{
		"HOME=" + home,
		"PODIUM_REGISTRY=" + registry,
		"PODIUM_VERIFY_SIGNATURES=never",
		"PODIUM_HARNESS=none",
		"PODIUM_CACHE_DIR=" + t.TempDir(),
		"OTEL_EXPORTER_OTLP_ENDPOINT=" + collector,
	}
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests(calls))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run podium-mcp: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
}

// clientSpans decodes each body as an OTLP ExportTraceServiceRequest and
// returns its client spans.
func clientSpans(t *testing.T, bodies [][]byte) []*tracepb.Span {
	t.Helper()
	var out []*tracepb.Span
	for _, b := range bodies {
		var req coltracepb.ExportTraceServiceRequest
		if err := proto.Unmarshal(b, &req); err != nil {
			t.Fatalf("decode OTLP body as ExportTraceServiceRequest: %v", err)
		}
		for _, rs := range req.GetResourceSpans() {
			for _, ss := range rs.GetScopeSpans() {
				for _, s := range ss.GetSpans() {
					if s.GetKind() == tracepb.Span_SPAN_KIND_CLIENT {
						out = append(out, s)
					}
				}
			}
		}
	}
	return out
}

// findSpan returns the first span match accepts, or nil.
func findSpan(spans []*tracepb.Span, match func(*tracepb.Span) bool) *tracepb.Span {
	for _, s := range spans {
		if match(s) {
			return s
		}
	}
	return nil
}

// spanAttr returns the string value of the attribute named key, or "".
func spanAttr(s *tracepb.Span, key string) string {
	for _, kv := range s.GetAttributes() {
		if kv.GetKey() == key {
			return kv.GetValue().GetStringValue()
		}
	}
	return ""
}
