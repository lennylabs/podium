// Package metrics implements the §13.8 Prometheus instrumentation surface for
// the registry and the MCP server. It owns the only Prometheus client
// dependency in the tree; every other package reports through plain callback
// seams (the server LatencyObserver, the core cache observer, the audit
// emitter) so the registry core stays free of a metrics dependency.
//
// The exported series names and labels match the reference Grafana dashboard
// at deploy/grafana-dashboard.json, which is the de facto contract for the
// instrumentation:
//
//   - podium_request_total{endpoint}                 counter
//   - podium_request_errors_total{endpoint}          counter
//   - podium_request_duration_seconds{endpoint}      histogram
//   - podium_visibility_denied_total                 counter
//   - podium_cache_hits_total                         counter
//   - podium_cache_misses_total                       counter
//   - podium_ingest_success_total                     counter
//   - podium_ingest_failure_total                     counter
//   - podium_vector_outbox_depth                      gauge
//
// spec: §13.8 Observability (Prometheus endpoint on registry and MCP server).
package metrics

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry holds the registered Prometheus collectors and the dedicated
// prometheus.Registry they are registered against. A dedicated registry (not
// the global default) keeps the exposed series limited to Podium's own metrics
// plus the Go runtime and process collectors.
type Registry struct {
	reg *prometheus.Registry

	requestTotal     *prometheus.CounterVec
	requestErrors    *prometheus.CounterVec
	requestDuration  *prometheus.HistogramVec
	visibilityDenied prometheus.Counter
	cacheHits        prometheus.Counter
	cacheMisses      prometheus.Counter
	ingestSuccess    prometheus.Counter
	ingestFailure    prometheus.Counter
	vectorOutbox     atomic.Int64
}

// New builds the Podium metric set, registers it (plus the Go runtime and
// process collectors) against a fresh prometheus.Registry, and returns the
// handle the server uses to record observations and serve /metrics.
func New() *Registry {
	m := &Registry{reg: prometheus.NewRegistry()}

	m.requestTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "podium_request_total",
		Help: "Total meta-tool requests served, by operation endpoint.",
	}, []string{"endpoint"})

	// spec: §13.8 mandates an error-rate counter on the registry metrics
	// surface. ObserveRequest increments it for any response status at or
	// above 400, so the error rate is derivable per endpoint.
	m.requestErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "podium_request_errors_total",
		Help: "Total meta-tool requests that returned a 4xx/5xx status, by operation endpoint.",
	}, []string{"endpoint"})

	m.requestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "podium_request_duration_seconds",
		Help: "Request handler latency in seconds, by operation endpoint.",
		// Buckets straddle the §7.1 SLO thresholds (200 ms, 500 ms, 2 s) so the
		// shipped dashboard's p99 panel resolves around the budgeted values.
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.2, 0.35, 0.5, 1, 2, 5},
	}, []string{"endpoint"})

	m.visibilityDenied = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "podium_visibility_denied_total",
		Help: "Total reads rejected because the caller could not see the target (§4.6).",
	})
	m.cacheHits = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "podium_cache_hits_total",
		Help: "Total server-side cache hits (DOMAIN.md import-glob expansion).",
	})
	m.cacheMisses = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "podium_cache_misses_total",
		Help: "Total server-side cache misses (DOMAIN.md import-glob expansion).",
	})
	m.ingestSuccess = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "podium_ingest_success_total",
		Help: "Total ingest attempts that stored or confirmed at least one artifact.",
	})
	m.ingestFailure = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "podium_ingest_failure_total",
		Help: "Total ingest attempts that failed (lint, conflict, quota, source, or transport).",
	})

	vectorOutbox := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "podium_vector_outbox_depth",
		Help: "Pending rows in the §4.7.2 external-vector-backend outbox.",
	}, func() float64 { return float64(m.vectorOutbox.Load()) })

	m.reg.MustRegister(
		m.requestTotal, m.requestErrors, m.requestDuration, m.visibilityDenied,
		m.cacheHits, m.cacheMisses, m.ingestSuccess, m.ingestFailure,
		vectorOutbox,
		collectors(),
	)
	return m
}

// Handler returns the /metrics scrape handler exposing this registry's series
// in the Prometheus text exposition format.
func (m *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// Gatherer exposes the underlying registry so a second process (the MCP
// metrics listener) can serve the same collectors, and so tests can gather and
// assert on the emitted families.
func (m *Registry) Gatherer() prometheus.Gatherer { return m.reg }

// ObserveRequest records one served request: the operation endpoint name, the
// response status, and the elapsed handler time. It is the sink wired into
// server.LatencyObserver, so it fires once per meta-tool request. A status at
// or above 400 also increments podium_request_errors_total{endpoint} (§13.8),
// so the per-endpoint error rate is derivable from the scrape.
func (m *Registry) ObserveRequest(endpoint string, status int, elapsed time.Duration) {
	if endpoint == "" {
		return
	}
	m.requestTotal.WithLabelValues(endpoint).Inc()
	m.requestDuration.WithLabelValues(endpoint).Observe(elapsed.Seconds())
	if status >= 400 {
		m.requestErrors.WithLabelValues(endpoint).Inc()
	}
}

// IncVisibilityDenied counts one read rejected by §4.6 visibility filtering.
func (m *Registry) IncVisibilityDenied() { m.visibilityDenied.Inc() }

// ObserveCache counts one server-side cache lookup as a hit or a miss.
func (m *Registry) ObserveCache(hit bool) {
	if hit {
		m.cacheHits.Inc()
		return
	}
	m.cacheMisses.Inc()
}

// IncIngestSuccess counts one ingest attempt that stored or confirmed at least
// one artifact.
func (m *Registry) IncIngestSuccess() { m.ingestSuccess.Inc() }

// IncIngestFailure counts one ingest attempt that failed outright.
func (m *Registry) IncIngestFailure() { m.ingestFailure.Inc() }

// SetVectorOutboxDepth publishes the current depth of the §4.7.2 external
// vector-backend outbox to the podium_vector_outbox_depth gauge. The drain
// worker calls it after each pass; the gauge reads 0 on a collocated backend
// that uses no outbox.
func (m *Registry) SetVectorOutboxDepth(n int64) { m.vectorOutbox.Store(n) }

// collectors bundles the Go runtime and process collectors so /metrics carries
// the standard process_* and go_* series alongside the Podium metrics.
func collectors() prometheus.Collector {
	return collectorSet{
		runtime: prometheus.NewGoCollector(),
		process: prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	}
}

// collectorSet adapts the two standard collectors to a single Collector so
// MustRegister takes them in one argument.
type collectorSet struct {
	runtime prometheus.Collector
	process prometheus.Collector
}

func (c collectorSet) Describe(ch chan<- *prometheus.Desc) {
	c.runtime.Describe(ch)
	c.process.Describe(ch)
}

func (c collectorSet) Collect(ch chan<- prometheus.Metric) {
	c.runtime.Collect(ch)
	c.process.Collect(ch)
}
