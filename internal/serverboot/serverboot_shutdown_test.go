package serverboot

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strings"
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
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	srv := &http.Server{Handler: http.NewServeMux()}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- serveUntilShutdown(ctx, func() {}, srv, ln) }()
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

// Spec: §13.9 — an accept loop that stops on its own surfaces srv.Serve's error
// rather than hanging on the cancelled-context arm. The caller now opens the
// listener, so a bind that fails is reported before this function runs and is
// covered by TestRun_ReportsBindFailure.
func TestServeUntilShutdown_ReturnsServeError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_ = ln.Close() // Serve returns immediately on a closed listener.
	srv := &http.Server{Handler: http.NewServeMux()}
	if err := serveUntilShutdown(context.Background(), func() {}, srv, ln); err == nil {
		t.Error("want an error from a listener that cannot accept, got nil")
	}
}

// Spec: §13.9 — a bind that fails is returned as an error. The startup line
// previously printed the configured address before anything was bound, so a
// refused bind was announced as a success and then surfaced as a serve error
// with no address in it.
func TestRun_ReportsBindFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PODIUM_BIND", "127.0.0.1:99999") // out-of-range port
	t.Setenv("PODIUM_POSTGRES_DSN", "")
	t.Setenv("PODIUM_S3_ENDPOINT", "")
	t.Setenv("PODIUM_CONFIG_FILE", "")

	// The bind failure is reported after the startup refusals have run, so the
	// boot has already started its background daemons by the time run returns.
	// Cancelling releases them; without it they outlive this test and the next
	// goroutine-leak check attributes them to whatever runs after it.
	ctx, cancel := context.WithCancel(context.Background())
	err := run(ctx, func() {})
	cancel()
	if err == nil {
		t.Fatal("run = nil, want an error from a bind that cannot succeed")
	}
	if !strings.Contains(err.Error(), "bind 127.0.0.1:99999") {
		t.Errorf("run error = %q, want it to name the address that could not be bound", err)
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
