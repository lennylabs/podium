package main

import (
	"context"
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
