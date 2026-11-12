package tracing

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// scrubExporter wraps the configured exporter so that no exported span
// attribute carries a URL query component or fragment. An HTTP client span
// records url.full, which holds the search text and session_id of a
// meta-tool call and the SigV4 signature of a presigned URL.
//
// The scrub sits at export because a span processor cannot do it. OnStart
// runs before otelhttp sets url.full, and OnEnd receives a read-only span.
//
// Only attributes are rewritten. No producer writes a URL query into a span's
// name, status description, events, or links, so those fields pass through
// unchanged. Code that records an error on a span keeps that true only when
// the error carries no URL: an error returned by http.Client.Do wraps a
// *url.Error that names the full request URL, query included, so it must
// never reach span.RecordError or the status description.
//
// Spec: §13.8
type scrubExporter struct{ sdktrace.SpanExporter }

// ExportSpans hands the wrapped exporter a view of each span whose attributes
// are scrubbed. Shutdown and ForceFlush delegate through the embedding.
func (e scrubExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	scrubbed := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, s := range spans {
		scrubbed[i] = scrubbedSpan{ReadOnlySpan: s, attrs: scrubAttrs(s.Attributes())}
	}
	return e.SpanExporter.ExportSpans(ctx, scrubbed)
}

// scrubbedSpan overrides Attributes and nothing else. Embedding the interface
// supplies its unexported method and every other field of the original span.
type scrubbedSpan struct {
	sdktrace.ReadOnlySpan
	attrs []attribute.KeyValue
}

// Attributes returns the scrubbed attribute set.
func (s scrubbedSpan) Attributes() []attribute.KeyValue { return s.attrs }

// scrubAttrs returns attrs with the query and fragment cut from every string
// value that is an http or https URL. Every other attribute is returned
// unchanged, including a non-URL string that contains "?". The input slice is
// left untouched because it belongs to the recorded span.
func scrubAttrs(attrs []attribute.KeyValue) []attribute.KeyValue {
	out := make([]attribute.KeyValue, len(attrs))
	for i, kv := range attrs {
		out[i] = kv
		if kv.Value.Type() != attribute.STRING {
			continue
		}
		if v := kv.Value.AsString(); isHTTPURL(v) {
			out[i] = kv.Key.String(stripQuery(v))
		}
	}
	return out
}

// isHTTPURL reports whether v begins with an http or https scheme, compared
// case-insensitively as RFC 3986 §3.1 requires.
func isHTTPURL(v string) bool {
	return hasPrefixFold(v, "http://") || hasPrefixFold(v, "https://")
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// stripQuery cuts v at its first "?" or "#". In an absolute URL that byte
// begins the query or the fragment (RFC 3986 §3), so the cut needs no parse
// and has no failure branch.
func stripQuery(v string) string {
	if i := strings.IndexAny(v, "?#"); i >= 0 {
		return v[:i]
	}
	return v
}
