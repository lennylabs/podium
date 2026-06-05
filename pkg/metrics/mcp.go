package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MCPRegistry is the §13.8 Prometheus metric set for the MCP bridge process.
// The bridge is a stdio JSON-RPC server, so it has no request HTTP listener of
// its own; this set is served by the opt-in metrics listener
// (PODIUM_MCP_METRICS_ADDR). It exposes per-tool request, error, and latency
// series plus the resolution-cache hit/miss counters, reusing the shared
// podium_cache_hits_total / podium_cache_misses_total names so the dashboard's
// cache-hit-rate panel resolves against a bridge scrape too.
type MCPRegistry struct {
	reg *prometheus.Registry

	requests    *prometheus.CounterVec
	errors      *prometheus.CounterVec
	duration    *prometheus.HistogramVec
	cacheHits   prometheus.Counter
	cacheMisses prometheus.Counter
}

// NewMCP builds and registers the MCP bridge metric set against a fresh
// registry (plus the Go runtime and process collectors).
func NewMCP() *MCPRegistry {
	m := &MCPRegistry{reg: prometheus.NewRegistry()}

	m.requests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "podium_mcp_requests_total",
		Help: "Total MCP meta-tool calls handled, by tool name.",
	}, []string{"tool"})
	m.errors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "podium_mcp_request_errors_total",
		Help: "Total MCP meta-tool calls that returned an error envelope, by tool name.",
	}, []string{"tool"})
	m.duration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "podium_mcp_request_duration_seconds",
		Help:    "MCP meta-tool call latency in seconds, by tool name.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.2, 0.35, 0.5, 1, 2, 5},
	}, []string{"tool"})
	m.cacheHits = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "podium_cache_hits_total",
		Help: "Total resolution-cache hits in the MCP bridge.",
	})
	m.cacheMisses = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "podium_cache_misses_total",
		Help: "Total resolution-cache misses in the MCP bridge.",
	})

	m.reg.MustRegister(
		m.requests, m.errors, m.duration,
		m.cacheHits, m.cacheMisses,
		collectors(),
	)
	return m
}

// Handler returns the /metrics scrape handler for the bridge metric set.
func (m *MCPRegistry) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// Gatherer exposes the underlying registry for tests.
func (m *MCPRegistry) Gatherer() prometheus.Gatherer { return m.reg }

// ObserveCall records one meta-tool call: the tool name, whether it returned an
// error envelope, and the elapsed time. An empty tool name is dropped.
func (m *MCPRegistry) ObserveCall(tool string, isError bool, elapsed time.Duration) {
	if tool == "" {
		return
	}
	m.requests.WithLabelValues(tool).Inc()
	m.duration.WithLabelValues(tool).Observe(elapsed.Seconds())
	if isError {
		m.errors.WithLabelValues(tool).Inc()
	}
}

// ObserveCache counts one resolution-cache lookup as a hit or a miss.
func (m *MCPRegistry) ObserveCache(hit bool) {
	if hit {
		m.cacheHits.Inc()
		return
	}
	m.cacheMisses.Inc()
}
