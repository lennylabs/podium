package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §7.6 — /v1/events streams change events as NDJSON. The TS
// SDK's `subscribe()` parses this stream; the server keeps a per-
// connection subscription on the in-process bus.
func TestEvents_StreamsPublishedEvents(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	srv := server.New(core.New(st, "t", nil))
	srv.SetHeartbeatForTesting(50 * time.Millisecond)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Open the stream in a goroutine so we can publish concurrently.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		ts.URL+"/v1/events?type=artifact.published", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/events: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-ndjson") {
		t.Errorf("Content-Type = %q, want application/x-ndjson", ct)
	}

	// Publish a matching event after the connection settles.
	go func() {
		time.Sleep(50 * time.Millisecond)
		srv.PublishEvent(context.Background(), "artifact.published", map[string]any{
			"id": "finance/run", "version": "1.0.0",
		})
	}()

	scan := bufio.NewScanner(resp.Body)
	deadline := time.Now().Add(2 * time.Second)
	for scan.Scan() {
		if time.Now().After(deadline) {
			break
		}
		var ev map[string]any
		if err := json.Unmarshal(scan.Bytes(), &ev); err != nil {
			t.Fatalf("unmarshal event: %v (body=%q)", err, scan.Text())
		}
		if ev["event"] == "_heartbeat" {
			continue
		}
		if ev["event"] != "artifact.published" {
			t.Errorf("event type = %v, want artifact.published", ev["event"])
		}
		data, _ := ev["data"].(map[string]any)
		if data["id"] != "finance/run" {
			t.Errorf("data.id = %v, want finance/run", data["id"])
		}
		return
	}
	t.Fatal("no event received within timeout")
}

// Spec: §7.6 — events whose type is not in the subscriber's filter
// are dropped. Subscribing to type=A while only type=B fires
// produces no output (other than heartbeats).
func TestEvents_FilterDropsUnmatchedTypes(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	srv := server.New(core.New(st, "t", nil))
	srv.SetHeartbeatForTesting(50 * time.Millisecond)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		ts.URL+"/v1/events?type=does.not.match", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	go func() {
		time.Sleep(50 * time.Millisecond)
		srv.PublishEvent(context.Background(), "artifact.published", map[string]any{"id": "x"})
	}()

	// Read for 300ms; should see no artifact.published event.
	deadline := time.Now().Add(300 * time.Millisecond)
	scan := bufio.NewScanner(resp.Body)
	for scan.Scan() {
		if time.Now().After(deadline) {
			return
		}
		var ev map[string]any
		_ = json.Unmarshal(scan.Bytes(), &ev)
		if ev["event"] == "artifact.published" {
			t.Errorf("filter leak: got %v", ev)
		}
	}
}
