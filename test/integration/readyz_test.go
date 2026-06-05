package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §13.9 — /readyz runs a request-time
// metadata-store reachability probe and reports not_ready (503) when the
// store is down. This drives the real /readyz handler over HTTP against a
// file-backed SQLite store, then closes the store mid-flight to simulate
// a Postgres/metadata outage and confirms the endpoint flips from
// ready (200) to not_ready (503). The check mirrors
// serverboot.storeReadinessCheck (GetTenant on the read path).
func TestReadyz_StoreOutageReportsNotReady(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "registry.db")
	st, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = st.Close()
		}
	})

	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	check := func(ctx context.Context) error {
		_, err := st.GetTenant(ctx, "default")
		return err
	}
	srv := server.New(core.New(st, "default", nil), server.WithReadinessChecks(check))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Store reachable: ready / 200.
	if status, mode := getReadyz(t, ts.URL); status != http.StatusOK || mode != "ready" {
		t.Fatalf("healthy /readyz = (%d, %q), want (200, ready)", status, mode)
	}

	// Simulate a metadata-store outage.
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	closed = true

	// Store unreachable: not_ready / 503.
	if status, mode := getReadyz(t, ts.URL); status != http.StatusServiceUnavailable || mode != "not_ready" {
		t.Fatalf("outage /readyz = (%d, %q), want (503, not_ready)", status, mode)
	}
}

func getReadyz(t *testing.T, baseURL string) (int, string) {
	t.Helper()
	resp, err := http.Get(baseURL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	var body server.ReadyResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /readyz: %v", err)
	}
	return resp.StatusCode, body.Mode
}
