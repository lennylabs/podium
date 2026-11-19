package tracing

import (
	"context"
	"crypto/rand"
	"io"

	"go.opentelemetry.io/otel/trace"
)

// EnsureTraceID returns ctx unchanged when it carries a valid span context,
// and otherwise a context carrying a fresh, unsampled, non-recording span
// context with a random trace ID and span ID.
//
// With tracing off, a span started through Tracer() carries no trace ID, so a
// caller that stamps the span's trace ID on an audit event would write none.
// The minted span context gives the caller an ID to stamp, and the installed
// W3C propagator injects it as traceparent on an outbound request, so the
// receiving registry derives the same trace ID for its own audit record. A
// read failure of the random source returns ctx unchanged.
//
// Spec: §8.1
func EnsureTraceID(ctx context.Context) context.Context {
	return ensureTraceID(ctx, rand.Reader)
}

// ensureTraceID is EnsureTraceID with an injectable random source.
func ensureTraceID(ctx context.Context, random io.Reader) context.Context {
	if trace.SpanContextFromContext(ctx).IsValid() {
		return ctx
	}
	var b [24]byte
	if _, err := io.ReadFull(random, b[:]); err != nil {
		return ctx
	}
	var cfg trace.SpanContextConfig
	copy(cfg.TraceID[:], b[:16])
	copy(cfg.SpanID[:], b[16:])
	sc := trace.NewSpanContext(cfg)
	if !sc.IsValid() {
		// An all-zero trace ID or span ID is invalid under W3C Trace Context,
		// and a propagator would not inject it.
		return ctx
	}
	return trace.ContextWithSpanContext(ctx, sc)
}
