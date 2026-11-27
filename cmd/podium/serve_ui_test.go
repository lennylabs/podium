package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Spec: §13.10 — when PODIUM_WEB_UI=true, the server mounts the
// embedded SPA at /app/. A GET /app/ returns the index HTML.
func TestServe_WebUIServedWhenEnabled(t *testing.T) {
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
	t.Setenv("PODIUM_WEB_UI", "true")

	bootServer(t, port)

	// /healthz answering means the listener serves the full mux, so one
	// request to /app/ is enough.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/app/", port), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /app/: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /app/ body: %v", err)
	}
	if !strings.Contains(string(body), "<title>Podium</title>") {
		t.Errorf("UI response missing index marker: %.200s", body)
	}
}
