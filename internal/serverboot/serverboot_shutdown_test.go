package serverboot

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §13.9 — serveUntilShutdown drains in-flight requests when its context is
// cancelled (a SIGTERM in production) and returns nil, rather than the process
// being killed mid-request.
func TestServeUntilShutdown_GracefulOnCancel(t *testing.T) {
	addr := freeAddr(t)
	srv := &http.Server{Addr: addr, Handler: http.NewServeMux()}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- serveUntilShutdown(ctx, func() {}, srv) }()
	if !dialOK(addr, 2*time.Second) {
		t.Fatal("server did not start listening")
	}
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Errorf("serveUntilShutdown = %v, want nil after a graceful drain", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveUntilShutdown did not return after the context was cancelled")
	}
}

// Spec: §13.9 — a listener that never binds surfaces the ListenAndServe error
// rather than hanging on the cancelled-context arm.
func TestServeUntilShutdown_ReturnsListenError(t *testing.T) {
	srv := &http.Server{Addr: "127.0.0.1:99999"} // out-of-range port
	if err := serveUntilShutdown(context.Background(), func() {}, srv); err == nil {
		t.Error("want an error from a failed ListenAndServe, got nil")
	}
}

// Spec: §8.4 — the audit retention scheduler stops when its context is cancelled
// instead of leaking the goroutine.
func TestStartRetentionScheduler_StopsOnContextCancel(t *testing.T) {
	defer goleak.VerifyNone(t)
	sink, err := audit.NewFileSink(filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	startRetentionScheduler(ctx, &Config{auditRetentionMaxAgeDays: 1, auditRetentionInterval: 3600}, sink, nil)
	cancel()
}

// Spec: §8.4 — the store retention scheduler stops when its context is cancelled.
func TestStartStoreRetentionScheduler_StopsOnContextCancel(t *testing.T) {
	defer goleak.VerifyNone(t)
	ctx, cancel := context.WithCancel(context.Background())
	startStoreRetentionScheduler(ctx, &Config{storeRetentionInterval: 3600, deprecatedRetentionDays: 90, layerRecoveryDays: 30}, store.NewMemory())
	cancel()
}

// freeAddr returns a loopback address whose port was free a moment ago.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// dialOK reports whether addr accepts a TCP connection within the deadline.
func dialOK(addr string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, 50*time.Millisecond); err == nil {
			_ = c.Close()
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// Spec: §13.9 / §13.10 — run boots the standalone server (SQLite, filesystem
// object store, no auth) and shuts down gracefully when its context is
// cancelled, the in-process equivalent of a SIGTERM. It exercises the full boot
// path (config, store, background daemons, serve loop), so the graceful-shutdown
// wiring is covered in-process and not only by the subprocess e2e smoke.
func TestRun_StandaloneGracefulShutdown(t *testing.T) {
	addr := freeAddr(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PODIUM_BIND", addr)
	t.Setenv("PODIUM_POSTGRES_DSN", "") // force the standalone SQLite path
	t.Setenv("PODIUM_S3_ENDPOINT", "")  // force the filesystem object store
	t.Setenv("PODIUM_CONFIG_FILE", "")
	// Enable the otherwise-default-off daemons so their start paths run.
	t.Setenv("PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS", "86400")
	t.Setenv("PODIUM_READONLY_PROBE_FAILURES", "3")

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- run(ctx, func() {}) }()

	if !dialOK(addr, 10*time.Second) {
		cancel()
		t.Fatal("standalone server did not start listening")
	}
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Errorf("run = %v, want nil after a graceful shutdown", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not return after the context was cancelled")
	}
}
