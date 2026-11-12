package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/podium/pkg/audit"
)

// Both metric sets receive the counts of the process's audit stream. pkg/audit
// does not import pkg/metrics, so these assertions add no import cycle.
var (
	_ audit.Observer = (*Registry)(nil)
	_ audit.Observer = (*MCPRegistry)(nil)
)

// auditSeries is the §8.3 delivery metric pair that the registry and the MCP
// server both export. The families are Vecs because a Vec exports no child
// until a destination kind first reports, so a deployment whose stream has
// only a file destination exposes no permanently zero series.
//
// Spec: §8.3, §13.8
type auditSeries struct {
	queueDepth *prometheus.GaugeVec
	dropped    *prometheus.CounterVec
}

func newAuditSeries() auditSeries {
	return auditSeries{
		queueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "podium_audit_queue_depth",
			Help: "Audit records queued for an HTTP or OTLP destination (§8.3), by destination kind.",
		}, []string{"destination"}),
		dropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "podium_audit_dropped_total",
			Help: "Audit records dropped for a destination (§8.3 Loss accounting), by destination kind and reason.",
		}, []string{"destination", "reason"}),
	}
}

// AddQueued adds delta to the queue depth of the destination kind. It
// implements audit.Observer.
func (a auditSeries) AddQueued(kind string, delta int) {
	a.queueDepth.WithLabelValues(kind).Add(float64(delta))
}

// Dropped counts n records dropped for the destination kind with reason. It
// implements audit.Observer. A count below 1 is ignored, because a Prometheus
// counter panics on a negative increment.
func (a auditSeries) Dropped(kind, reason string, n int) {
	if n < 1 {
		return
	}
	a.dropped.WithLabelValues(kind, reason).Add(float64(n))
}
