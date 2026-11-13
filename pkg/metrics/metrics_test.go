package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
)

// scrape renders the /metrics body once.
func scrape(t *testing.T, m *Registry) string {
	t.Helper()
	return scrapeHandler(t, m.Handler())
}

// scrapeHandler renders the /metrics body of h once.
func scrapeHandler(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape status = %d, want 200", rec.Code)
	}
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

// The dashboard at deploy/grafana-dashboard.json is the contract: every series
// it queries must appear in the scrape after the corresponding record call.
func TestRegistry_EmitsDashboardSeries(t *testing.T) {
	m := New()

	m.ObserveRequest("load_domain", 200, 12*time.Millisecond)
	m.ObserveRequest("search_artifacts", 200, 80*time.Millisecond)
	m.ObserveRequest("load_artifact", 404, 5*time.Millisecond)
	m.IncVisibilityDenied()
	m.ObserveCache(true)
	m.ObserveCache(false)
	m.IncIngestSuccess()
	m.IncIngestFailure()
	m.SetVectorOutboxDepth(3)

	body := scrape(t, m)

	wantSeries := []string{
		"podium_request_total",
		"podium_request_errors_total",
		"podium_request_duration_seconds_bucket",
		"podium_visibility_denied_total",
		"podium_cache_hits_total",
		"podium_cache_misses_total",
		"podium_ingest_success_total",
		"podium_ingest_failure_total",
		"podium_vector_outbox_depth",
	}
	for _, s := range wantSeries {
		if !strings.Contains(body, s) {
			t.Errorf("scrape missing series %q", s)
		}
	}

	// Spot-check a few concrete values and the endpoint label.
	wantLines := []string{
		`podium_request_total{endpoint="load_domain"} 1`,
		`podium_request_total{endpoint="search_artifacts"} 1`,
		`podium_request_total{endpoint="load_artifact"} 1`,
		`podium_request_errors_total{endpoint="load_artifact"} 1`,
		`podium_visibility_denied_total 1`,
		`podium_cache_hits_total 1`,
		`podium_cache_misses_total 1`,
		`podium_ingest_success_total 1`,
		`podium_ingest_failure_total 1`,
		`podium_vector_outbox_depth 3`,
	}
	for _, l := range wantLines {
		if !strings.Contains(body, l) {
			t.Errorf("scrape missing line %q\n--- body ---\n%s", l, body)
		}
	}
}

// spec: §13.8 — the registry exposes an error-rate counter. ObserveRequest
// increments podium_request_errors_total{endpoint} only for a status at or
// above 400; a 2xx/3xx response increments request_total but not the error
// counter, so the per-endpoint error rate is derivable.
func TestRegistry_ErrorCounterBoundary(t *testing.T) {
	m := New()
	m.ObserveRequest("load_artifact", 200, time.Millisecond) // success
	m.ObserveRequest("load_artifact", 304, time.Millisecond) // not-modified, not an error
	m.ObserveRequest("load_artifact", 400, time.Millisecond) // client error
	m.ObserveRequest("load_artifact", 500, time.Millisecond) // server error

	body := scrape(t, m)

	// Four requests counted, two of them errors.
	if !strings.Contains(body, `podium_request_total{endpoint="load_artifact"} 4`) {
		t.Errorf("request_total not 4 for load_artifact:\n%s", body)
	}
	if !strings.Contains(body, `podium_request_errors_total{endpoint="load_artifact"} 2`) {
		t.Errorf("request_errors_total not 2 for load_artifact (400+500):\n%s", body)
	}
}

// The registry exports no podium_audit_outbox_depth series, because no audit
// outbox exists to feed it. The §13.8 queue-depth gauges are the vector
// outbox and podium_audit_queue_depth, which the audit stream feeds for its
// HTTP and OTLP destinations (§8.3) and which, as a Vec, exports no child
// until the stream first reports.
func TestRegistry_NoPhantomAuditOutboxGauge(t *testing.T) {
	m := New()
	m.SetVectorOutboxDepth(1)
	body := scrape(t, m)
	if strings.Contains(body, "podium_audit_outbox_depth") {
		t.Errorf("podium_audit_outbox_depth should not be exported:\n%s", body)
	}
	if !strings.Contains(body, "podium_vector_outbox_depth") {
		t.Errorf("podium_vector_outbox_depth missing:\n%s", body)
	}
}

// ObserveRequest with an empty endpoint (a health/probe path) records nothing,
// so /metrics scrapes do not pollute the per-endpoint series.
func TestRegistry_EmptyEndpointNotRecorded(t *testing.T) {
	m := New()
	m.ObserveRequest("", 200, time.Millisecond)
	body := scrape(t, m)
	if strings.Contains(body, `podium_request_total{endpoint=""}`) {
		t.Errorf("empty-endpoint request was recorded:\n%s", body)
	}
}

// The audit delivery series are absent from a fresh registry and appear with
// the counts the stream reports through audit.Observer.
//
// Spec: §13.8
func TestRegistry_AuditSeriesAppearOnFirstUse(t *testing.T) {
	m := New()
	assertAuditSeriesAppearOnFirstUse(t, m)
}

// auditObserverSet is a metric set that feeds the audit series and serves
// them on /metrics.
type auditObserverSet interface {
	audit.Observer
	Handler() http.Handler
}

// assertAuditSeriesAppearOnFirstUse pins the §8.3 audit series on one metric
// set: no family before the first report, the net queue depth after an
// increment and a decrement, and the dropped count per kind and reason. A
// drop count below 1 records nothing.
func assertAuditSeriesAppearOnFirstUse(t *testing.T, m auditObserverSet) {
	t.Helper()
	body := scrapeHandler(t, m.Handler())
	for _, name := range []string{"podium_audit_queue_depth", "podium_audit_dropped_total"} {
		if strings.Contains(body, name) {
			t.Errorf("fresh registry exports %s:\n%s", name, body)
		}
	}

	m.AddQueued("http", 2)
	m.AddQueued("http", -1)
	m.Dropped("otlp", audit.ReasonRejected, 3)
	m.Dropped("otlp", audit.ReasonQueueFull, 0)

	body = scrapeHandler(t, m.Handler())
	wantLines := []string{
		`podium_audit_queue_depth{destination="http"} 1`,
		`podium_audit_dropped_total{destination="otlp",reason="rejected"} 3`,
	}
	for _, l := range wantLines {
		if !strings.Contains(body, l) {
			t.Errorf("scrape missing line %q\n--- body ---\n%s", l, body)
		}
	}
	if strings.Contains(body, audit.ReasonQueueFull) {
		t.Errorf("a zero drop count created a child:\n%s", body)
	}
}
