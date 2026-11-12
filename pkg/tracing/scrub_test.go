package tracing

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// An exported attribute value that is an http or https URL loses its query
// component and fragment, whatever its key, and every other attribute is
// exported unchanged.
// Spec: §13.8
func TestScrubAttrs(t *testing.T) {
	cases := []struct {
		name string
		in   attribute.KeyValue
		want attribute.KeyValue
	}{
		{"query", attribute.String("url.full", "http://h:1/v1/search_artifacts?query=q&session_id=s"),
			attribute.String("url.full", "http://h:1/v1/search_artifacts")},
		{"fragment", attribute.String("url.full", "https://h/p#frag"),
			attribute.String("url.full", "https://h/p")},
		{"fragment before query", attribute.String("url.full", "https://h/p#f?q=1"),
			attribute.String("url.full", "https://h/p")},
		{"sigv4", attribute.String("url.full", "https://b.s3/obj?X-Amz-Credential=c&X-Amz-Signature=s"),
			attribute.String("url.full", "https://b.s3/obj")},
		{"uppercase scheme", attribute.String("url.full", "HTTPS://h/p?q=1"),
			attribute.String("url.full", "HTTPS://h/p")},
		{"unrelated key", attribute.String("example.link", "http://h/p?q=1"),
			attribute.String("example.link", "http://h/p")},
		{"no query", attribute.String("url.full", "http://h/p"),
			attribute.String("url.full", "http://h/p")},
		{"non-url string", attribute.String("note", "why?"),
			attribute.String("note", "why?")},
		{"other scheme", attribute.String("note", "ftp://h/p?q=1"),
			attribute.String("note", "ftp://h/p?q=1")},
		{"short string", attribute.String("note", "http"),
			attribute.String("note", "http")},
		{"non-string", attribute.Int("http.response.status_code", 200),
			attribute.Int("http.response.status_code", 200)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := []attribute.KeyValue{tc.in}
			got := scrubAttrs(in)
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("scrubAttrs(%v) = %v, want %v", tc.in, got, tc.want)
			}
			if in[0] != tc.in {
				t.Errorf("scrubAttrs modified its input: %v", in[0])
			}
		})
	}
}

// The provider Init installs exports spans through the scrub and leaves the
// span name, the other attributes, and the resource intact.
// Spec: §13.8
func TestNewProvider_ExportsScrubbedAttributes(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := newProvider(exp, "test")
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	_, span := tp.Tracer(scopeName).Start(context.Background(), "GET")
	span.SetAttributes(
		attribute.String("url.full", "http://h/v1/search_artifacts?query=secret"),
		attribute.String("http.request.method", "GET"),
	)
	span.End()
	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported %d spans, want 1", len(spans))
	}
	got := spans[0]
	if got.Name != "GET" {
		t.Errorf("span name %q, want GET", got.Name)
	}
	want := map[attribute.Key]string{
		"url.full":            "http://h/v1/search_artifacts",
		"http.request.method": "GET",
	}
	if len(got.Attributes) != len(want) {
		t.Fatalf("attributes %v, want %v", got.Attributes, want)
	}
	for _, kv := range got.Attributes {
		if want[kv.Key] != kv.Value.AsString() {
			t.Errorf("attribute %s = %q, want %q", kv.Key, kv.Value.AsString(), want[kv.Key])
		}
	}
	if res := got.Resource.Attributes(); len(res) != 1 || res[0] != attribute.String("service.name", "test") {
		t.Errorf("resource attributes %v, want service.name=test", res)
	}
}
