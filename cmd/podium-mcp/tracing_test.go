package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// newTestTracer installs an in-memory span exporter as the global tracer
// provider for the duration of the test and returns it. It restores the prior
// provider on cleanup.
func newTestTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	prevTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
	})
	return exp
}

// §13.8 — the object-storage fetch is one of the four named child spans under a
// load_artifact root span. fetchLargeResources opens an "objectstore.fetch"
// span (nested under the active meta-tool span) when it pulls a large resource
// from object storage via its presigned URL.
func TestTracing_ObjectStoreFetchChildSpan(t *testing.T) {
	exp := newTestTracer(t)

	payload := []byte("large bundled blob")
	sum := sha256.Sum256(payload)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	obj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(obj.Close)

	srv := &mcpServer{cfg: &config{}, http: &http.Client{}}
	ctx, root := otel.Tracer("test").Start(context.Background(), "mcp.load_artifact")
	srv.activeCtx = ctx

	resp := &loadArtifactResponse{
		LargeResources: map[string]largeResourceLink{
			"big.bin": {URL: obj.URL, ContentHash: hash},
		},
	}
	if err := srv.fetchLargeResources(resp, nil); err != nil {
		t.Fatalf("fetchLargeResources: %v", err)
	}
	root.End()

	rootTrace := root.SpanContext().TraceID()
	var found bool
	for _, s := range exp.GetSpans() {
		if s.Name != "objectstore.fetch" {
			continue
		}
		found = true
		if s.SpanContext.TraceID() != rootTrace {
			t.Errorf("objectstore.fetch trace %s != root trace %s", s.SpanContext.TraceID(), rootTrace)
		}
		if !s.Parent.IsValid() {
			t.Error("objectstore.fetch span has no parent; it should nest under the root span")
		}
	}
	if !found {
		t.Error("no objectstore.fetch child span exported")
	}
}

// §13.8 — a manifest-only load_artifact performs no object-storage fetch, so no
// empty objectstore.fetch span is recorded.
func TestTracing_NoObjectStoreSpanWhenNoLargeResources(t *testing.T) {
	exp := newTestTracer(t)

	srv := &mcpServer{cfg: &config{}, http: &http.Client{}}
	ctx, root := otel.Tracer("test").Start(context.Background(), "mcp.load_artifact")
	srv.activeCtx = ctx

	resp := &loadArtifactResponse{} // manifest only, no large resources
	if err := srv.fetchLargeResources(resp, nil); err != nil {
		t.Fatalf("fetchLargeResources: %v", err)
	}
	root.End()

	for _, s := range exp.GetSpans() {
		if s.Name == "objectstore.fetch" {
			t.Error("objectstore.fetch span created for a manifest-only call")
		}
	}
}

// §13.8 — a meta-tool call opens one root span ("mcp.<tool>"), the outbound
// registry call is a child round-trip span in the same trace, and the bridge
// injects the W3C traceparent header so the registry joins the trace.
func TestTracing_CallToolRootSpanAndPropagation(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	var gotTraceparent string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceparent = r.Header.Get("traceparent")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query":         "variance",
			"total_matched": 0,
			"results":       []map[string]any{},
		})
	}))
	t.Cleanup(ts.Close)

	srv := &mcpServer{
		cfg:  &config{registry: ts.URL},
		http: &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)},
	}
	raw, _ := json.Marshal(toolCallParams{Name: "search_artifacts", Arguments: map[string]any{"query": "variance"}})
	srv.callTool(raw)

	if gotTraceparent == "" {
		t.Error("registry did not receive a traceparent header (W3C context not injected)")
	}

	spans := exp.GetSpans()
	var rootTrace string
	var hasRoot, hasRoundTrip bool
	for _, s := range spans {
		if s.Name == "mcp.search_artifacts" {
			hasRoot = true
			rootTrace = s.SpanContext.TraceID().String()
		}
	}
	if !hasRoot {
		t.Fatalf("no root span mcp.search_artifacts exported; got %d spans", len(spans))
	}
	for _, s := range spans {
		if s.SpanKind.String() == "client" {
			hasRoundTrip = true
			if s.SpanContext.TraceID().String() != rootTrace {
				t.Errorf("round-trip span trace %s != root trace %s", s.SpanContext.TraceID(), rootTrace)
			}
			if !s.Parent.IsValid() {
				t.Error("round-trip span has no parent; it should nest under the root span")
			}
		}
	}
	if !hasRoundTrip {
		t.Error("no client round-trip span exported under the call")
	}
}

// activeCtx is cleared after a call so off-call paths fall back to Background.
func TestTracing_ActiveCtxClearedAfterCall(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{}})
	}))
	t.Cleanup(ts.Close)

	srv := &mcpServer{cfg: &config{registry: ts.URL}, http: &http.Client{}}
	raw, _ := json.Marshal(toolCallParams{Name: "search_artifacts", Arguments: map[string]any{"query": "x"}})
	srv.callTool(raw)
	if srv.activeCtx != nil {
		t.Error("activeCtx not cleared after callTool returned")
	}
	if srv.reqCtx() != context.Background() {
		t.Error("reqCtx should fall back to context.Background() off-call")
	}
}
