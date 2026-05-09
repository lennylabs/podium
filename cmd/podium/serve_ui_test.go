package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/serverboot"
)

// Spec: §13.10 — when PODIUM_WEB_UI=true, the server mounts the
// embedded SPA at /ui/. A GET /ui/ returns the index HTML.
func TestServe_WebUIServedWhenEnabled(t *testing.T) {
	port := freePort(t)
	tmp := t.TempDir()
	t.Setenv("PODIUM_BIND", fmt.Sprintf("127.0.0.1:%d", port))
	t.Setenv("PODIUM_REGISTRY_STORE", "memory")
	t.Setenv("PODIUM_OBJECT_STORE", "none")
	t.Setenv("PODIUM_CONFIG_FILE", filepath.Join(tmp, "missing.yaml"))
	t.Setenv("PODIUM_FILESYSTEM_ROOT", tmp)
	t.Setenv("PODIUM_VECTOR_BACKEND", "")
	t.Setenv("PODIUM_WEB_UI", "true")

	go func() { _ = serverboot.Run() }()

	url := fmt.Sprintf("http://127.0.0.1:%d/ui/", port)
	deadline := time.Now().Add(5 * time.Second)
	var body []byte
	var err error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err2 := http.DefaultClient.Do(req)
		cancel()
		if err2 == nil {
			body, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			err = nil
			break
		}
		err = err2
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("UI never came up: %v", err)
	}
	if !strings.Contains(string(body), "<title>Podium</title>") {
		t.Errorf("UI response missing index marker: %.200s", body)
	}
}
