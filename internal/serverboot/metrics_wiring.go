package serverboot

import (
	"context"
	"time"

	"github.com/lennylabs/podium/pkg/metrics"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
)

// metricsEnabled reports whether the §13.8 Prometheus /metrics endpoint and
// its instrumentation are on. It defaults to enabled so the shipped Grafana
// dashboard resolves against a fresh deployment; PODIUM_METRICS=false (or
// 0/off/no) removes the endpoint and the per-request recording overhead.
func metricsEnabled() bool {
	if v := envBoolPtr("PODIUM_METRICS"); v != nil {
		return *v
	}
	return true
}

// metricsLatencyObserver returns a server.LatencyObserver that records the
// §13.8 podium_request_total and podium_request_duration_seconds series, keyed
// by the operation endpoint name the server already derives.
func metricsLatencyObserver(m *metrics.Registry) server.LatencyObserver {
	return func(op string, status int, elapsed time.Duration) {
		m.ObserveRequest(op, status, elapsed)
	}
}

// combineObservers returns a LatencyObserver that fans each observation out to
// both sinks. Either may be nil. It lets the metrics recorder and the §7.1
// access log share the single WithLatencyObserver seam.
func combineObservers(a, b server.LatencyObserver) server.LatencyObserver {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	return func(op string, status int, elapsed time.Duration) {
		a(op, status, elapsed)
		b(op, status, elapsed)
	}
}

// metricsAuditEmitter wraps next so the registry's audit stream also drives the
// §13.8 podium_visibility_denied_total counter. It increments on every
// visibility.denied event, then delegates to next (which may be nil when no
// audit sink is configured, in which case the event is counted and dropped).
func metricsAuditEmitter(m *metrics.Registry, next core.AuditEmitter) core.AuditEmitter {
	return func(ctx context.Context, e core.AuditEvent) {
		if e.Type == "visibility.denied" {
			m.IncVisibilityDenied()
		}
		if next != nil {
			next(ctx, e)
		}
	}
}
