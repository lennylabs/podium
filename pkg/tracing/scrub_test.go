package tracing

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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

// No exported span field carries a URL query, on a request the server answers
// or on a transport failure. The client spans otelhttp records for a meta-tool
// call and a presigned fetch are exported through the provider Init installs,
// and every string the exporter receives is searched: the name, the
// attributes, the status description, the events, and the links.
// Spec: §13.8
func TestProvider_ScrubsEveryExportedField(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := newProvider(exp, "test")
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	// WithTracerProvider scopes the provider to this transport, so the test
	// changes no global state.
	client := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport, otelhttp.WithTracerProvider(tp))}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"results":[]}`)
	}))
	t.Cleanup(ts.Close)
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	const search = "/v1/search_artifacts?query=QMARK&session_id=SMARK"
	resp, err := client.Get(ts.URL + search)
	if err != nil {
		t.Fatalf("GET search: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	for _, u := range []string{closedURL + search, closedURL + "/obj?X-Amz-Credential=CMARK&X-Amz-Signature=SIGMARK"} {
		// The error is discarded unrecorded: http.Client.Do wraps a *url.Error
		// that names the full URL, which §13.8 forbids on a span.
		if resp, err := client.Get(u); err == nil {
			_ = resp.Body.Close()
			t.Fatalf("GET %s against a closed listener succeeded", u)
		}
	}
	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	spans := exp.GetSpans()
	if len(spans) != 3 {
		t.Fatalf("exported %d spans, want 3", len(spans))
	}
	for i, s := range spans {
		for _, v := range spanStrings(s) {
			for _, marker := range []string{"QMARK", "SMARK", "CMARK", "SIGMARK", "?"} {
				if strings.Contains(v, marker) {
					t.Errorf("span %d (%s) exports %q, which contains %q", i, s.Name, v, marker)
				}
			}
		}
	}
	if got := stringAttr(spans[0].Attributes, "url.full"); got != ts.URL+"/v1/search_artifacts" {
		t.Errorf("span 0 url.full = %q, want %q", got, ts.URL+"/v1/search_artifacts")
	}
	for _, i := range []int{1, 2} {
		if st := spans[i].Status; st.Code != codes.Error || st.Description == "" {
			t.Errorf("span %d status = %v %q, want Error with a description", i, st.Code, st.Description)
		}
	}
}

// spanStrings returns every string an exporter receives from s: the name,
// each string attribute value, the status description, each event name and
// string event attribute, and each string link attribute.
func spanStrings(s tracetest.SpanStub) []string {
	out := []string{s.Name, s.Status.Description}
	out = append(out, stringValues(s.Attributes)...)
	for _, e := range s.Events {
		out = append(out, e.Name)
		out = append(out, stringValues(e.Attributes)...)
	}
	for _, l := range s.Links {
		out = append(out, stringValues(l.Attributes)...)
	}
	return out
}

// stringValues returns the values of the string attributes in attrs.
func stringValues(attrs []attribute.KeyValue) []string {
	var out []string
	for _, kv := range attrs {
		if kv.Value.Type() == attribute.STRING {
			out = append(out, kv.Value.AsString())
		}
	}
	return out
}

// stringAttr returns the string value of the attribute named key, or "".
func stringAttr(attrs []attribute.KeyValue, key attribute.Key) string {
	for _, kv := range attrs {
		if kv.Key == key {
			return kv.Value.AsString()
		}
	}
	return ""
}
