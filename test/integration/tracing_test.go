package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §13.8 — the registry exports OpenTelemetry spans and propagates W3C
// Trace Context. A request carrying a parent trace (the otelhttp client span)
// produces a registry server span named by the operation that joins the same
// trace, so a cross-process call (MCP bridge -> registry) is one connected
// trace rather than two disjoint ones.
func TestTracing_RegistryServerSpanJoinsInboundTrace(t *testing.T) {
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

	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutManifest(ctx, store.ManifestRecord{
		TenantID:    "default",
		ArtifactID:  "finance/variance",
		Version:     "1.0.0",
		ContentHash: "sha256:" + "0000000000000000000000000000000000000000000000000000000000000001",
		Type:        "context",
		Description: "Variance analysis reference for vendor payments here today.",
		Layer:       "shared",
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	reg := core.New(st, "default", []layer.Layer{
		{ID: "shared", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	srv := server.New(reg)
	handler := otelhttp.NewHandler(srv.Handler(), "podium-registry",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			if op := server.OperationName(r.URL.Path); op != "" {
				return op
			}
			return r.Method + " " + r.URL.Path
		}))
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	// A traced client mirrors the bridge's otelhttp-wrapped transport.
	client := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	rootCtx, root := tp.Tracer("test").Start(ctx, "test.root")
	req, _ := http.NewRequestWithContext(rootCtx, http.MethodGet, ts.URL+"/v1/load_artifact?id=finance/variance", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("load_artifact = HTTP %d, want 200", resp.StatusCode)
	}
	root.End()

	rootTrace := root.SpanContext().TraceID()
	var serverSpan, clientSpan bool
	for _, s := range exp.GetSpans() {
		if s.SpanContext.TraceID() != rootTrace {
			t.Errorf("span %q has trace %s, want %s (trace not propagated)", s.Name, s.SpanContext.TraceID(), rootTrace)
		}
		if s.Name == "load_artifact" {
			serverSpan = true
		}
		if s.SpanKind.String() == "client" {
			clientSpan = true
		}
	}
	if !serverSpan {
		t.Error("no registry server span named load_artifact was exported")
	}
	if !clientSpan {
		t.Error("no client round-trip span was exported")
	}
}
