package main

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The resolution cache reports each Resolve outcome to its observer so the
// §13.8 cache-hit/miss counters move. A miss precedes the put; a hit follows.
func TestResolutionCache_ObserverRecordsHitAndMiss(t *testing.T) {
	r := newResolutionCache(t.TempDir())
	var hits, misses int
	r.observe = func(hit bool) {
		if hit {
			hits++
		} else {
			misses++
		}
	}
	base := time.Unix(1_700_000_000, 0).UTC()

	if _, ok := r.Resolve("team/x", "1.0.0", base, 30*time.Second, false); ok {
		t.Fatal("expected miss before put")
	}
	r.PutVersion("team/x", "1.0.0", "sha256:pinned", base)
	if _, ok := r.Resolve("team/x", "1.0.0", base, 30*time.Second, false); !ok {
		t.Fatal("expected hit after put")
	}

	if hits != 1 || misses != 1 {
		t.Fatalf("hits=%d misses=%d, want 1 and 1", hits, misses)
	}
}

// A disabled cache (no cache directory, so no backing db) does not report
// outcomes, so a deployment without a cache does not inflate the miss count.
func TestResolutionCache_DisabledDoesNotObserve(t *testing.T) {
	r := newResolutionCache("")
	called := false
	r.observe = func(bool) { called = true }
	_, _ = r.Resolve("team/x", "1.0.0", time.Unix(0, 0), 30*time.Second, false)
	if called {
		t.Fatal("observer fired against a disabled cache")
	}
}

func TestIsErrorResult(t *testing.T) {
	if !isErrorResult(map[string]any{"error": "boom"}) {
		t.Error("error envelope not detected")
	}
	if isErrorResult(map[string]any{"results": []any{}}) {
		t.Error("success result misclassified as error")
	}
	if isErrorResult("not a map") {
		t.Error("non-map misclassified as error")
	}
}

// newServer wires the metric set and the cache observer only when the opt-in
// listener address is configured.
func TestNewServer_MetricsOptIn(t *testing.T) {
	off, err := newServer(&config{cacheDir: t.TempDir(), cacheMode: "always-revalidate"})
	if err != nil {
		t.Fatalf("newServer (off): %v", err)
	}
	if off.metrics != nil || off.resolutions.observe != nil {
		t.Error("metrics wired without PODIUM_MCP_METRICS_ADDR")
	}

	on, err := newServer(&config{cacheDir: t.TempDir(), cacheMode: "always-revalidate", metricsAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("newServer (on): %v", err)
	}
	if on.metrics == nil || on.resolutions.observe == nil {
		t.Error("metrics not wired with metricsAddr set")
	}
}

// The opt-in listener serves the bridge metric set at /metrics over HTTP.
func TestStartMetricsListener_ServesScrape(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	srv, err := newServer(&config{cacheDir: t.TempDir(), cacheMode: "always-revalidate", metricsAddr: addr})
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	srv.metrics.ObserveCall("load_artifact", false, 10*time.Millisecond)

	stop := srv.startMetricsListener(addr)
	defer stop()

	var body string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/metrics")
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			body = string(b)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, `podium_mcp_requests_total{tool="load_artifact"} 1`) {
		t.Errorf("scrape missing recorded call:\n%s", body)
	}
}
