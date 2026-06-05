package serverboot

import (
	"log"
	"time"

	"github.com/lennylabs/podium/pkg/registry/server"
)

// accessLogEnabled reports whether the §7.1 latency access log is on. It
// defaults to enabled so a fresh deployment has a timing surface to compare
// against the SLO budgets; PODIUM_ACCESS_LOG=false (or 0/off/no) silences
// it for an operator who routes latency through a different sink.
func accessLogEnabled() bool {
	if v := envBoolPtr("PODIUM_ACCESS_LOG"); v != nil {
		return *v
	}
	return true
}

// accessLogObserver returns the default §7.1 latency sink: one structured
// access-log line per served request, keyed by operation name, with the
// status and elapsed time. The duration is rendered in milliseconds with
// microsecond precision so an operator can compare it against the SLO
// budgets (load_domain/search p99 < 200 ms, load_artifact p99 < 500 ms).
// The line is parseable key=value text; a deployment that wants histograms
// supplies its own server.LatencyObserver instead.
//
// spec: §7.1 Latency budgets (SLO targets, server source)
func accessLogObserver() server.LatencyObserver {
	return func(op string, status int, elapsed time.Duration) {
		log.Printf("access op=%s status=%d duration_ms=%.3f",
			op, status, float64(elapsed.Microseconds())/1000)
	}
}
