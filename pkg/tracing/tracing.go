// Package tracing wires the §13.8 OpenTelemetry trace export and W3C Trace
// Context propagation for the registry and the MCP bridge. It installs the
// global propagator unconditionally (so inbound and outbound W3C context flows
// even with no exporter) and a batch span exporter only when one is configured.
//
// Tracing is off by default. It activates when PODIUM_TRACING names an exporter
// ("stdout" or "otlp") or when the standard OTEL_EXPORTER_OTLP_ENDPOINT /
// OTEL_EXPORTER_OTLP_TRACES_ENDPOINT is set. When off, spans created through
// Tracer() use the global no-op provider and are non-recording, so the
// instrumentation adds negligible overhead.
//
// spec: §13.8 Observability (OpenTelemetry trace export, W3C Trace Context).
package tracing

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// scopeName is the instrumentation scope every Podium span is created under.
const scopeName = "github.com/lennylabs/podium"

// Tracer returns the process tracer Podium spans are created from. It resolves
// against the global provider, so it is a no-op tracer until Init installs an
// exporter-backed provider.
func Tracer() trace.Tracer { return otel.Tracer(scopeName) }

// Init installs the global W3C TraceContext + Baggage propagator and, when an
// exporter is configured, a batch span processor exporting to it for the named
// service. The returned shutdown func flushes and stops the exporter; it is a
// no-op when tracing is off. A misconfigured PODIUM_TRACING value is an error.
func Init(ctx context.Context, service string) (func(context.Context) error, error) {
	// Always install the propagator so inbound traceparent extraction and
	// outbound injection work regardless of whether spans are exported.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	exp, err := newExporter(ctx)
	if err != nil {
		return nil, err
	}
	if exp == nil {
		return func(context.Context) error { return nil }, nil
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(resource.NewSchemaless(
			attribute.String("service.name", service),
		)),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// newExporter builds the configured span exporter, or nil when tracing is off.
//
//   - PODIUM_TRACING=stdout writes spans to stdout (debugging and tests).
//   - PODIUM_TRACING=otlp, or a set OTEL_EXPORTER_OTLP[_TRACES]_ENDPOINT, uses
//     the OTLP/HTTP exporter, which honors the standard OTEL_ environment.
//   - unset / off / none / false leaves tracing disabled.
func newExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	mode := os.Getenv("PODIUM_TRACING")
	otlpSet := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != ""
	switch {
	case mode == "stdout":
		return stdouttrace.New()
	case mode == "otlp" || (otlpSet && mode == ""):
		return otlptracehttp.New(ctx)
	case mode == "" || mode == "off" || mode == "none" || mode == "false":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown PODIUM_TRACING value %q (want stdout, otlp, or off)", mode)
	}
}
