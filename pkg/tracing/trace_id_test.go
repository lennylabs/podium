package tracing

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Spec: §8.1 — a context with no span context gains a valid, unsampled one
// whose trace ID the W3C propagator injects as traceparent, and each call
// mints a distinct trace ID.
func TestEnsureTraceID_MintsAPropagatedTraceID(t *testing.T) {
	t.Parallel()
	ctx := EnsureTraceID(context.Background())
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() || sc.IsSampled() || sc.IsRemote() {
		t.Fatalf("span context = %+v, want valid, unsampled, and local", sc)
	}
	h := http.Header{}
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(h))
	if tp := h.Get("traceparent"); !strings.Contains(tp, "-"+sc.TraceID().String()+"-") {
		t.Errorf("traceparent = %q, want trace ID %s", tp, sc.TraceID())
	}
	other := trace.SpanContextFromContext(EnsureTraceID(context.Background()))
	if other.TraceID() == sc.TraceID() {
		t.Errorf("two calls minted the same trace ID %s", sc.TraceID())
	}
}

// Spec: §8.1 — a context that already carries a valid span context keeps it,
// so a recording span's trace ID is the one stamped and propagated.
func TestEnsureTraceID_KeepsAnExistingSpanContext(t *testing.T) {
	t.Parallel()
	parent := EnsureTraceID(context.Background())
	if got := EnsureTraceID(parent); got != parent {
		t.Error("a context with a valid span context was replaced")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

// A random source that fails, or yields an all-zero trace ID, leaves the
// context without a span context rather than minting an invalid one.
func TestEnsureTraceID_RandomSourceFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if got := ensureTraceID(ctx, failingReader{}); got != ctx {
		t.Error("a failed random read changed the context")
	}
	if got := ensureTraceID(ctx, bytes.NewReader(make([]byte, 24))); got != ctx {
		t.Error("an all-zero trace ID was installed")
	}
}
