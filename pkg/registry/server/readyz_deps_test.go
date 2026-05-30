package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
)

// readReadyz GETs /readyz and returns the status and decoded body.
func readReadyz(t *testing.T, baseURL string) (int, server.ReadyResponse) {
	t.Helper()
	resp, err := http.Get(baseURL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	var body server.ReadyResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /readyz: %v", err)
	}
	return resp.StatusCode, body
}

// Spec: §13.9 (F-13.9.2, F-13.9.3) — a failing dependency probe makes
// /readyz report not_ready and answer 503 so a load balancer pulls the
// registry out of rotation.
func TestReadyz_DependencyDownReturnsNotReady(t *testing.T) {
	t.Parallel()
	ts, _ := newHealthFixture(t, server.WithReadinessChecks(
		func(context.Context) error { return errors.New("postgres: connection refused") },
	))
	status, body := readReadyz(t, ts.URL)
	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", status)
	}
	if body.Mode != "not_ready" {
		t.Errorf("mode = %q, want not_ready", body.Mode)
	}
}

// Spec: §13.9 (F-13.9.2) — when every dependency probe passes, /readyz
// reports ready and answers 200.
func TestReadyz_DependencyUpReturnsReady(t *testing.T) {
	t.Parallel()
	ts, _ := newHealthFixture(t, server.WithReadinessChecks(
		func(context.Context) error { return nil },
	))
	status, body := readReadyz(t, ts.URL)
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if body.Mode != "ready" {
		t.Errorf("mode = %q, want ready", body.Mode)
	}
}

// Spec: §13.9 (F-13.9.3) — not_ready (a hard dependency outage) takes
// precedence over the §13.2.1 read_only replication-fallback state: a
// registry that is both flipped read-only and has a down dependency is
// not_ready, not read_only.
func TestReadyz_NotReadyTakesPrecedenceOverReadOnly(t *testing.T) {
	t.Parallel()
	ts, mode := newHealthFixture(t, server.WithReadinessChecks(
		func(context.Context) error { return errors.New("store unreachable") },
	))
	mode.Set(server.ModeReadOnly)
	status, body := readReadyz(t, ts.URL)
	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", status)
	}
	if body.Mode != "not_ready" {
		t.Errorf("mode = %q, want not_ready", body.Mode)
	}
}

// Spec: §13.9 (F-13.9.3) — a read_only registry whose dependencies are
// all reachable stays in rotation: /readyz reports read_only with 200.
func TestReadyz_ReadOnlyWithHealthyDepsStaysInRotation(t *testing.T) {
	t.Parallel()
	ts, mode := newHealthFixture(t, server.WithReadinessChecks(
		func(context.Context) error { return nil },
	))
	mode.Set(server.ModeReadOnly)
	status, body := readReadyz(t, ts.URL)
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if body.Mode != "read_only" {
		t.Errorf("mode = %q, want read_only", body.Mode)
	}
}

// Spec: §13.9 — the dependency probes run under a bounded context so a
// hung store or object-store call cannot block /readyz indefinitely.
func TestReadyz_ProbeContextIsBounded(t *testing.T) {
	t.Parallel()
	deadlineCh := make(chan bool, 1)
	ts, _ := newHealthFixture(t, server.WithReadinessChecks(
		func(ctx context.Context) error {
			_, ok := ctx.Deadline()
			deadlineCh <- ok
			return nil
		},
	))
	readReadyz(t, ts.URL)
	if ok := <-deadlineCh; !ok {
		t.Error("readiness probe received a context with no deadline; want bounded")
	}
}

// Spec: §13.9 (F-13.9.4) — the /readyz body carries the observed
// replication lag in seconds from the wired reporter.
func TestReadyz_ReplicationLagInBody(t *testing.T) {
	t.Parallel()
	ts, _ := newHealthFixture(t, server.WithLagReporter(
		func(context.Context) int { return 5 },
	))
	_, body := readReadyz(t, ts.URL)
	if body.ReplicationLagSecs != 5 {
		t.Errorf("replication_lag_seconds = %d, want 5", body.ReplicationLagSecs)
	}
}

// Spec: §13.9 (F-13.9.4) — with no reporter wired (a standalone
// deployment with no replica), /readyz reports lag 0.
func TestReadyz_DefaultLagIsZero(t *testing.T) {
	t.Parallel()
	ts, _ := newHealthFixture(t)
	_, body := readReadyz(t, ts.URL)
	if body.ReplicationLagSecs != 0 {
		t.Errorf("replication_lag_seconds = %d, want 0", body.ReplicationLagSecs)
	}
}

// Spec: §13.2.1 (F-13.9.4) — the X-Podium-Read-Only-Lag-Seconds header on
// read responses in read-only mode reports the observed lag, not a
// hardcoded 0.
func TestReadOnlyHeader_ReportsObservedLag(t *testing.T) {
	t.Parallel()
	ts, mode := newHealthFixture(t, server.WithLagReporter(
		func(context.Context) int { return 7 },
	))
	mode.Set(server.ModeReadOnly)
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Podium-Read-Only"); got != "true" {
		t.Errorf("X-Podium-Read-Only = %q, want true", got)
	}
	if got := resp.Header.Get("X-Podium-Read-Only-Lag-Seconds"); got != "7" {
		t.Errorf("X-Podium-Read-Only-Lag-Seconds = %q, want 7", got)
	}
}
