package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/serverboot"
)

// emptyServerConfig writes an empty registry.yaml in a fresh temp dir and
// returns its path. A serve test names this as PODIUM_CONFIG_FILE so the
// §13.10 explicit-config check (a named-but-missing config is a hard
// error) is satisfied while the server is still configured through the
// PODIUM_* env vars the test sets. The empty document overlays nothing, so env
// values win, and an explicit PODIUM_CONFIG_FILE suppresses the standalone
// first-run bootstrap, so the test never writes into the real home directory.
func emptyServerConfig(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "registry.yaml")
	if err := os.WriteFile(p, []byte(""), 0o644); err != nil {
		t.Fatalf("write empty config: %v", err)
	}
	return p
}

// freePort grabs an unused TCP port for the test, releases it, and
// returns the chosen number. Acceptable race window for tests.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// serverBootTimeout bounds how long a serve test waits for the in-process
// server to answer /healthz. Boot takes about a second on an idle machine,
// including under -race, but has taken more than 4 s while the full parallel
// suite runs, and a fixed 5 s wait failed a quarter of the runs under that
// load. A healthy boot returns as soon as /healthz answers, so the generous
// bound costs time only when the boot hangs.
const serverBootTimeout = 60 * time.Second

// healthzProbeTimeout bounds one /healthz request, so a listener that accepts
// and never answers costs one probe rather than the whole boot wait.
const healthzProbeTimeout = 2 * time.Second

// bootServer starts serverboot.Run in a goroutine and blocks until the server
// answers /healthz on port with 200. Run returning before that point fails the
// test at once with Run's error: a boot that failed, such as a refused
// configuration or a port another process took, never becomes ready, and
// polling until the deadline would hide the cause behind a timeout.
func bootServer(t *testing.T, port int) {
	t.Helper()
	exited := make(chan error, 1)
	go func() { exited <- serverboot.Run() }()
	waitForServer(t, port, exited)
}

// waitForServer polls /healthz on port until it answers 200, failing the test
// when exited delivers Run's result first or serverBootTimeout passes.
func waitForServer(t *testing.T, port int, exited <-chan error) {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	deadline := time.Now().Add(serverBootTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = probeHealthz(url)
		if lastErr == nil {
			return
		}
		select {
		case err := <-exited:
			t.Fatalf("server exited before answering %s: %v", url, err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatalf("server did not answer %s within %s; last probe: %v", url, serverBootTimeout, lastErr)
}

// probeHealthz issues one bounded GET against url and returns nil when it
// answers 200.
func probeHealthz(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), healthzProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// Spec: §13.10 — `podium serve` boots the standalone server in-
// process. The smoke test starts Run via a goroutine, waits for
// `/healthz` to answer 200, and asserts the response shape.
func TestServe_BootsAndAnswersHealthz(t *testing.T) {
	port := freePort(t)
	tmp := t.TempDir()
	t.Setenv("PODIUM_BIND", fmt.Sprintf("127.0.0.1:%d", port))
	t.Setenv("PODIUM_REGISTRY_STORE", "memory")
	// Signing is on by default (§13.10) and would generate a key under the
	// real home; this case is not about signing.
	t.Setenv("PODIUM_SIGN", "none")
	t.Setenv("PODIUM_OBJECT_STORE", "none")
	t.Setenv("PODIUM_CONFIG_FILE", emptyServerConfig(t))
	t.Setenv("PODIUM_FILESYSTEM_ROOT", tmp)
	t.Setenv("PODIUM_VECTOR_BACKEND", "")

	bootServer(t, port)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/healthz", port), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Errorf("empty body")
	}
}
