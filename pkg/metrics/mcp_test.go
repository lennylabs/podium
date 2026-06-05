package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func scrapeMCP(t *testing.T, m *MCPRegistry) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape status = %d, want 200", rec.Code)
	}
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

func TestMCPRegistry_EmitsSeries(t *testing.T) {
	m := NewMCP()

	m.ObserveCall("load_artifact", false, 20*time.Millisecond)
	m.ObserveCall("load_artifact", true, 5*time.Millisecond)
	m.ObserveCache(true)
	m.ObserveCache(false)

	body := scrapeMCP(t, m)
	wantLines := []string{
		`podium_mcp_requests_total{tool="load_artifact"} 2`,
		`podium_mcp_request_errors_total{tool="load_artifact"} 1`,
		"podium_mcp_request_duration_seconds_bucket",
		`podium_cache_hits_total 1`,
		`podium_cache_misses_total 1`,
	}
	for _, l := range wantLines {
		if !strings.Contains(body, l) {
			t.Errorf("scrape missing %q\n--- body ---\n%s", l, body)
		}
	}
}

func TestMCPRegistry_EmptyToolNotRecorded(t *testing.T) {
	m := NewMCP()
	m.ObserveCall("", false, time.Millisecond)
	body := scrapeMCP(t, m)
	if strings.Contains(body, `tool=""`) {
		t.Errorf("empty tool was recorded:\n%s", body)
	}
}
