package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// readyzStub returns an httptest server whose /readyz answers with the
// given mode and status. Any other path 404s.
func readyzStub(t *testing.T, mode string, status int) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"mode": mode, "replication_lag_seconds": 0})
	}))
	t.Cleanup(ts.Close)
	return ts
}

func newHealthServer(registry string) *mcpServer {
	return &mcpServer{
		cfg:         &config{registry: registry},
		http:        &http.Client{},
		resolutions: newResolutionCache(""),
	}
}

// Spec: §13.9 (F-13.9.1) — the health tool reports mode ready and marks
// the registry connected when /readyz answers ready, and stamps the last
// successful call timestamp.
func TestHealthTool_ReadyRegistry(t *testing.T) {
	t.Parallel()
	ts := readyzStub(t, "ready", http.StatusOK)
	s := newHealthServer(ts.URL)

	res, ok := s.healthTool().(healthResult)
	if !ok {
		t.Fatalf("healthTool() = %T, want healthResult", s.healthTool())
	}
	if res.Mode != "ready" {
		t.Errorf("mode = %q, want ready", res.Mode)
	}
	if !res.Connected {
		t.Errorf("connected = false, want true")
	}
	if res.Registry != ts.URL {
		t.Errorf("registry = %q, want %q", res.Registry, ts.URL)
	}
	if res.LastSuccessfulCall == "" {
		t.Errorf("last_successful_call empty, want a timestamp after a successful probe")
	}
	if _, err := time.Parse(time.RFC3339, res.LastSuccessfulCall); err != nil {
		t.Errorf("last_successful_call %q not RFC3339: %v", res.LastSuccessfulCall, err)
	}
}

// Spec: §13.2.1 / §13.9 (F-13.9.1) — the health tool surfaces mode
// read_only when the registry has flipped to read-only.
func TestHealthTool_ReadOnlyRegistry(t *testing.T) {
	t.Parallel()
	ts := readyzStub(t, "read_only", http.StatusOK)
	s := newHealthServer(ts.URL)

	res := s.healthTool().(healthResult)
	if res.Mode != "read_only" {
		t.Errorf("mode = %q, want read_only", res.Mode)
	}
	if !res.Connected {
		t.Errorf("connected = false, want true (registry answered)")
	}
}

// Spec: §13.9 (F-13.9.1) — a registry that answers not_ready (503) is
// reachable on the wire but unusable for fresh reads, so the tool reports
// mode unreachable while connected stays true.
func TestHealthTool_NotReadyRegistry(t *testing.T) {
	t.Parallel()
	ts := readyzStub(t, "not_ready", http.StatusServiceUnavailable)
	s := newHealthServer(ts.URL)

	res := s.healthTool().(healthResult)
	if res.Mode != "unreachable" {
		t.Errorf("mode = %q, want unreachable", res.Mode)
	}
	if !res.Connected {
		t.Errorf("connected = false, want true (registry answered with 503)")
	}
}

// Spec: §13.9 (F-13.9.1) — when the registry does not answer at all, the
// tool reports mode unreachable, connected false, and no last successful
// call.
func TestHealthTool_UnreachableRegistry(t *testing.T) {
	t.Parallel()
	ts := readyzStub(t, "ready", http.StatusOK)
	dead := ts.URL
	ts.Close() // nothing listens at dead now; the probe gets connection refused.
	s := newHealthServer(dead)

	res := s.healthTool().(healthResult)
	if res.Mode != "unreachable" {
		t.Errorf("mode = %q, want unreachable", res.Mode)
	}
	if res.Connected {
		t.Errorf("connected = true, want false (no listener)")
	}
	if res.LastSuccessfulCall != "" {
		t.Errorf("last_successful_call = %q, want empty (no call ever succeeded)", res.LastSuccessfulCall)
	}
}

// Spec: §13.9 (F-13.9.1) — cache size reports the resolution-cache entry
// count.
func TestHealthTool_CacheSize(t *testing.T) {
	t.Parallel()
	ts := readyzStub(t, "ready", http.StatusOK)
	s := newHealthServer(ts.URL)
	s.resolutions = newResolutionCache(t.TempDir())
	now := time.Now()
	s.resolutions.PutVersion("finance/x", "1.0.0", "sha256:aaa", now)
	s.resolutions.PutVersion("finance/y", "2.0.0", "sha256:bbb", now)

	res := s.healthTool().(healthResult)
	if res.CacheSize != 2 {
		t.Errorf("cache_size = %d, want 2", res.CacheSize)
	}
}

// Spec: §13.9 (F-13.9.1) — tools/call dispatches the health tool, and
// tools/list advertises it.
func TestHealthTool_RegisteredAndDispatched(t *testing.T) {
	t.Parallel()
	ts := readyzStub(t, "ready", http.StatusOK)
	s := newHealthServer(ts.URL)

	listed := s.handle(rpcRequest{Method: "tools/list"})
	listBody, _ := json.Marshal(listed.Result)
	if !strings.Contains(string(listBody), `"health"`) {
		t.Errorf("tools/list missing health: %s", listBody)
	}

	params, _ := json.Marshal(toolCallParams{Name: "health"})
	out := s.callTool(params)
	if _, ok := out.(healthResult); !ok {
		t.Fatalf("callTool(health) = %T, want healthResult", out)
	}
}
