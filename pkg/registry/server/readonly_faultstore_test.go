package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// Spec: §13.2.1 / §8.1 — this is the G-INFRA-6 proof that the
// fault-injectable store decorator drives the real read-only journey
// end to end. A storetest.FaultStore wraps a Memory store and is wired
// into an in-process server alongside the real server.ReadOnlyProbe and
// the read-only audit callbacks (the same wiring serverboot installs).
// Severing the store makes the probe's GetTenant health call fail; after
// the failure threshold the probe flips the shared ModeTracker to
// read_only without anyone calling ModeTracker.Set. While read_only the
// server serves reads (search_artifacts, /healthz) and refuses writes
// (POST /v1/admin/grants) with the §6.10 registry.read_only envelope,
// and a registry.read_only_entered event lands in the audit sink.
// Restoring the store lets the probe accumulate the recovery successes,
// flip back to ready, emit registry.read_only_exited, and accept writes
// again.
func TestReadOnlyFaultStore_InducesFullJourney(t *testing.T) {
	t.Parallel()

	mem := store.NewMemory()
	if err := mem.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	fault := storetest.NewFaultStore(mem)

	// Grant alice admin up front so POST /v1/admin/grants is authorized
	// and the only thing that can reject it is the read-only gate.
	if err := mem.GrantAdmin(context.Background(), store.AdminGrant{UserID: "alice", OrgID: "default"}); err != nil {
		t.Fatalf("GrantAdmin: %v", err)
	}

	mode := server.NewModeTracker()
	sink := audit.NewMemory()

	// Mirror serverboot's read-only probe wiring, but against the fault
	// store and with a fast tick so the test is quick. OnEnter/OnExit
	// write the same audit events serverboot's callbacks do.
	probe := &server.ReadOnlyProbe{
		Store:      fault,
		Tracker:    mode,
		TenantID:   "default",
		Interval:   10 * time.Millisecond,
		Failures:   2,
		Recoveries: 2,
		OnEnter: func() {
			_ = sink.Append(context.Background(), audit.Event{
				Type:    audit.EventReadOnlyEntered,
				Caller:  "system",
				Target:  "default",
				Context: map[string]string{"reason": "store_probe_failed"},
			})
		},
		OnExit: func() {
			_ = sink.Append(context.Background(), audit.Event{
				Type:   audit.EventReadOnlyExited,
				Caller: "system",
				Target: "default",
			})
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go probe.Run(ctx)

	srv := server.New(
		core.New(fault, "default", []layer.Layer{
			{ID: "team", Visibility: layer.Visibility{Public: true}},
		}),
		server.WithMode(mode),
		server.WithAudit(sink),
		server.WithIdentityResolver(func(*http.Request) layer.Identity {
			return adminIdentity("alice")
		}),
	)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 1. Healthy: /healthz reports ready, a read succeeds, and a write
	// succeeds (201 Created).
	if mode := healthMode(t, ts.URL); mode != "ready" {
		t.Fatalf("initial /healthz mode = %q, want ready", mode)
	}
	if code := searchStatus(t, ts.URL); code != http.StatusOK {
		t.Fatalf("initial search status = %d, want 200", code)
	}
	if code, _ := grantStatus(t, ts.URL, "bob"); code != http.StatusCreated {
		t.Fatalf("initial grant status = %d, want 201", code)
	}

	// 2. Sever the store: the probe's GetTenant health call now fails. The
	// real probe must flip the tracker to read_only after the failure
	// threshold, with no ModeTracker.Set call anywhere in the test.
	fault.Sever()
	waitForMode(t, ts.URL, "read_only")

	// 3. Read-only: reads are still served while writes are refused with
	// the registry.read_only envelope (503).
	if code := searchStatus(t, ts.URL); code != http.StatusOK {
		t.Errorf("read-only search status = %d, want 200 (reads still served)", code)
	}
	code, body := grantStatus(t, ts.URL, "carol")
	if code != http.StatusServiceUnavailable {
		t.Errorf("read-only grant status = %d, want 503", code)
	}
	if got := errorCode(t, body); got != "registry.read_only" {
		t.Errorf("read-only grant code = %q, want registry.read_only", got)
	}

	// The probe must have observed a registry.read_only_entered event.
	if n := countEvents(sink, audit.EventReadOnlyEntered); n != 1 {
		t.Errorf("read_only_entered events = %d, want 1", n)
	}

	// 4. Restore the store: after the recovery successes the probe flips
	// back to ready and a write is accepted again.
	fault.Restore()
	waitForMode(t, ts.URL, "ready")

	if code, _ := grantStatus(t, ts.URL, "dave"); code != http.StatusCreated {
		t.Errorf("recovered grant status = %d, want 201", code)
	}
	if n := countEvents(sink, audit.EventReadOnlyExited); n != 1 {
		t.Errorf("read_only_exited events = %d, want 1", n)
	}

	// The decorator actually saw the probe pinging the read path.
	if fault.HealthCalls() == 0 {
		t.Error("FaultStore.HealthCalls = 0; probe never pinged the read path")
	}

	cancel()
}

// healthMode returns the §13.2.1 mode string from /healthz.
func healthMode(t *testing.T, baseURL string) string {
	t.Helper()
	resp, err := http.Get(baseURL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /healthz: %v", err)
	}
	return body.Mode
}

// searchStatus issues a read (search_artifacts) and returns its status.
func searchStatus(t *testing.T, baseURL string) int {
	t.Helper()
	resp, err := http.Get(baseURL + "/v1/search_artifacts?query=anything")
	if err != nil {
		t.Fatalf("GET /v1/search_artifacts: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// grantStatus issues a write (POST /v1/admin/grants) and returns the
// status plus the response body.
func grantStatus(t *testing.T, baseURL, userID string) (int, []byte) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"user_id": userID})
	resp, err := http.Post(baseURL+"/v1/admin/grants", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /v1/admin/grants: %v", err)
	}
	defer resp.Body.Close()
	buf, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, buf
}

// errorCode pulls the §6.10 code out of an error envelope body.
func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope %q: %v", body, err)
	}
	return env.Code
}

// waitForMode polls /healthz until the mode matches want or the deadline
// passes. It drives the real probe transition rather than forcing it.
func waitForMode(t *testing.T, baseURL, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if healthMode(t, baseURL) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("/healthz mode never reached %q within deadline (last = %q)", want, healthMode(t, baseURL))
}

// countEvents returns how many events of the given type the sink holds.
func countEvents(sink *audit.Memory, typ audit.EventType) int {
	n := 0
	for _, e := range sink.Events() {
		if e.Type == typ {
			n++
		}
	}
	return n
}
