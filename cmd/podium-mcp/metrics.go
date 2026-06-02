package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// startMetricsListener binds the §13.8 opt-in Prometheus endpoint for the
// bridge at addr and serves the metric set at /metrics. It returns a stop func
// that shuts the listener down, mirroring the token- and overlay-watch
// lifecycle in serve(). A bind error is non-fatal: the bridge keeps serving
// stdio and the returned stop func is a no-op. The listener is started only
// when s.metrics is non-nil (an operator named PODIUM_MCP_METRICS_ADDR).
func (s *mcpServer) startMetricsListener(addr string) func() {
	if s.metrics == nil || addr == "" {
		return func() {}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: metrics listener on %q disabled: %v\n", addr, err)
		return func() {}
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", s.metrics.Handler())
	srv := &http.Server{Handler: mux}
	go func() {
		if serr := srv.Serve(ln); serr != nil && serr != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "WARN: metrics listener stopped: %v\n", serr)
		}
	}()
	fmt.Fprintf(os.Stderr, "metrics: Prometheus endpoint mounted at http://%s/metrics (§13.8)\n", ln.Addr())
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}
