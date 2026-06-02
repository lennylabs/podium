package server

import (
	"net/http"
	"strings"
	"time"
)

// LatencyObserver receives one observation per served request: the
// canonical operation name (§7.1), the response status code, and the
// elapsed wall-clock time the handler chain took. It exists so a
// deployment can compare observed latency against the §7.1 SLO budgets
// (load_domain / search_domains / search_artifacts p99 < 200 ms,
// load_artifact p99 < 500 ms for the manifest and < 2 s with resources).
// The registry holds no metrics dependency; the operator supplies the
// sink (a structured access log by default, a histogram exporter when one
// is wired).
//
// spec: §7.1 Latency budgets (SLO targets, server source)
type LatencyObserver func(op string, status int, elapsed time.Duration)

// WithLatencyObserver installs the §7.1 per-request latency sink. The
// observer fires once per request after the handler chain returns, keyed
// by the operation name so a deployment can bucket durations per meta-tool
// and compare them against the SLO budgets. Without it the server records
// no timing and adds no per-request overhead.
//
// spec: §7.1 Latency budgets (SLO targets, server source)
func WithLatencyObserver(fn LatencyObserver) Option {
	return func(s *Server) { s.latency = fn }
}

// OperationName maps a request path to the stable §7.1 operation key, exported
// so the boot layer names §13.8 trace spans with the same operation keys the
// latency observer and request metrics use. Returns "" for unobserved paths.
func OperationName(path string) string { return operationName(path) }

// operationName maps a request path to the stable §7.1 operation key used
// in latency observations. The four SLO-budgeted meta-tools get their spec
// names; the other meta-tool routes get a stable key so the access log
// stays useful across the surface. Liveness/readiness probes and the
// long-lived /v1/events stream return "" and are not observed: they carry
// no SLO budget and an SSE connection lifetime is not a request latency.
func operationName(path string) string {
	switch path {
	case "/v1/load_domain":
		return "load_domain"
	case "/v1/search_domains":
		return "search_domains"
	case "/v1/search_artifacts":
		return "search_artifacts"
	case "/v1/load_artifact":
		return "load_artifact"
	case "/v1/artifacts:batchLoad":
		return "batch_load"
	case "/v1/dependents":
		return "dependents"
	case "/v1/scope/preview":
		return "scope_preview"
	case "/v1/domain/analyze":
		return "domain_analyze"
	case "/v1/quota":
		return "quota"
	case "/healthz", "/readyz", "/v1/events":
		return ""
	}
	switch {
	case strings.HasPrefix(path, "/v1/admin/"):
		return "admin"
	case strings.HasPrefix(path, "/v1/webhooks"):
		return "webhooks"
	case strings.HasPrefix(path, "/objects/"):
		return "objects"
	case strings.HasPrefix(path, "/scim/"):
		return "scim"
	}
	return ""
}

// withLatencyObserver wraps next so each served request reports its
// operation name, status, and elapsed time to the configured
// LatencyObserver. It is the outermost middleware so the measured duration
// spans the full chain (identity verification, audit metadata, the
// handler). With no observer configured it returns next unchanged so there
// is zero per-request overhead.
//
// spec: §7.1 Latency budgets (SLO targets, server source)
func (s *Server) withLatencyObserver(next http.Handler) http.Handler {
	if s.latency == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op := operationName(r.URL.Path)
		if op == "" {
			next.ServeHTTP(w, r)
			return
		}
		rec := &latencyRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		s.latency(op, rec.status, time.Since(start))
	})
}

// latencyRecorder captures the response status code while delegating every
// write to the underlying ResponseWriter. It forwards Flush so a streaming
// handler still type-asserts http.Flusher successfully even though this
// wrapper sits in front of it.
type latencyRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (rec *latencyRecorder) WriteHeader(code int) {
	if !rec.wroteHeader {
		rec.status = code
		rec.wroteHeader = true
	}
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *latencyRecorder) Write(b []byte) (int, error) {
	// A handler that writes a body without an explicit WriteHeader implies
	// a 200 (net/http does the same); mark the header written so a later
	// superfluous WriteHeader does not overwrite the recorded status.
	rec.wroteHeader = true
	return rec.ResponseWriter.Write(b)
}

// Flush forwards to the underlying writer when it supports flushing so the
// /v1/events SSE handler (which type-asserts http.Flusher) keeps working
// behind this wrapper.
func (rec *latencyRecorder) Flush() {
	if f, ok := rec.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
