package tracing

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Init installs the W3C propagator even with tracing off, so the bridge and
// registry inject and extract trace context regardless of export. A trace
// context injected into a carrier round-trips back through extraction.
func TestInit_InstallsW3CPropagator(t *testing.T) {
	t.Setenv("PODIUM_TRACING", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")

	shutdown, err := Init(context.Background(), "test")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	tid, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	sid, _ := trace.SpanIDFromHex("0102030405060708")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	if carrier.Get("traceparent") == "" {
		t.Fatal("propagator did not inject a traceparent header")
	}

	got := trace.SpanContextFromContext(
		otel.GetTextMapPropagator().Extract(context.Background(), carrier))
	if got.TraceID() != tid {
		t.Errorf("extracted trace id %s, want %s", got.TraceID(), tid)
	}
}

// PODIUM_TRACING=stdout configures an exporter and a working shutdown.
func TestInit_StdoutExporter(t *testing.T) {
	t.Setenv("PODIUM_TRACING", "stdout")
	shutdown, err := Init(context.Background(), "test")
	if err != nil {
		t.Fatalf("Init(stdout): %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

// An unrecognized PODIUM_TRACING value is a configuration error.
func TestInit_RejectsUnknownMode(t *testing.T) {
	t.Setenv("PODIUM_TRACING", "jaeger")
	if _, err := Init(context.Background(), "test"); err == nil {
		t.Fatal("Init accepted an unknown PODIUM_TRACING value")
	}
}
