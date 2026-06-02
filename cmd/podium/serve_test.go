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
// §13.10 explicit-config check (F-13.10.2: a named-but-missing config is a hard
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

// Spec: §13.10 — `podium serve` boots the standalone server in-
// process. The smoke test starts Run via a goroutine, waits for
// `/healthz` to answer 200, and asserts the response shape.
func TestServe_BootsAndAnswersHealthz(t *testing.T) {
	port := freePort(t)
	tmp := t.TempDir()
	t.Setenv("PODIUM_BIND", fmt.Sprintf("127.0.0.1:%d", port))
	t.Setenv("PODIUM_REGISTRY_STORE", "memory")
	t.Setenv("PODIUM_OBJECT_STORE", "none")
	t.Setenv("PODIUM_CONFIG_FILE", emptyServerConfig(t))
	t.Setenv("PODIUM_FILESYSTEM_ROOT", tmp)
	t.Setenv("PODIUM_VECTOR_BACKEND", "")

	go func() { _ = serverboot.Run() }()

	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	deadline := time.Now().Add(5 * time.Second)
	var resp *http.Response
	var err error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err = http.DefaultClient.Do(req)
		cancel()
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("server never came up: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Errorf("empty body")
	}
}
