package integration

// G-CONFIG-1: probe-driven read-only flip on a Postgres-backed registry.
//
// The §13.2.1 read-only mode is driven by ReadOnlyProbe pinging the metadata
// store (GetTenant) on a tick and flipping a shared ModeTracker after a run of
// failures, then flipping back after a run of successes. A standalone SQLite
// store cannot be made to fail after boot, so the e2e read-only cases skip and
// http_api_test forces the mode with mode.Set, which bypasses the probe, the
// audit events, and the recovery path.
//
// This test induces the flip against a live Postgres metadata store. The real
// Postgres store backs the catalogue reads and the writes; a storetest.FaultStore
// decorator severs only the GetTenant health call the probe pings, modeling a
// primary/replica outage the §13.2.1 probe observes without tearing down the
// shared container. The real ReadOnlyProbe and the serverboot read-only audit
// callbacks are wired into an in-process server (the core read/write surface plus
// the §7.3.1 layer-register ingest endpoint, sharing one ModeTracker). The journey:
// healthy reads load a seeded Postgres row and a write is accepted, concurrent
// readers run while the primary is severed, the probe flips to read_only with no
// mode.Set call, /readyz reports read_only (200, still in rotation), reads keep
// serving from Postgres while both the layer-register ingest and an admin grant
// are refused with registry.read_only, a read_only_entered event lands, then the
// primary is restored, the probe recovers to ready, writes resume, and a
// read_only_exited event lands.
//
// Gated on PODIUM_POSTGRES_DSN. The FaultStore is the documented severable-
// Postgres inducement for G-INFRA-6 (the in-process Memory proof lives in
// pkg/registry/server/readonly_faultstore_test.go); this realizes it against the
// real Postgres data plane so reads are proven to keep serving stored rows during
// the read-only window and writes against Postgres are refused.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

const roflipTenant = "roflip-default"

// roflipAdmin is the identity both the core server and the layer endpoint
// resolve every request to. The test pre-grants this Sub admin in the store, so
// AdminAuthorize accepts the admin grant and the admin-defined layer-register
// ingest; the only thing that can then refuse them is the §13.2.1 read-only gate.
var roflipAdmin = layer.Identity{
	Sub: "alice@acme.com", Email: "alice@acme.com", OrgID: roflipTenant,
	IsAuthenticated: true,
}

// TestConfigReadOnlyFlip_PostgresPrimaryOutage closes G-CONFIG-1.
func TestConfigReadOnlyFlip_PostgresPrimaryOutage(t *testing.T) {
	// Not parallel: drives a global read-only transition on a server wired to the
	// shared Postgres database, and resets the tenant's schema at start.
	dsn := os.Getenv("PODIUM_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN unset; skipping Postgres-backed read-only flip test")
	}
	pg, err := store.OpenPostgres(dsn)
	if err != nil {
		t.Skipf("OpenPostgres %q: %v (database unreachable)", dsn, err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	ctx := context.Background()
	if err := pg.ResetForTest(ctx); err != nil {
		t.Fatalf("ResetForTest: %v", err)
	}
	if err := pg.CreateTenant(ctx, store.Tenant{ID: roflipTenant, Name: roflipTenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	// Grant alice admin up front so POST /v1/admin/grants is authorized and the
	// only thing that can reject it is the read-only gate.
	if err := pg.GrantAdmin(ctx, store.AdminGrant{UserID: "alice@acme.com", OrgID: roflipTenant}); err != nil {
		t.Fatalf("GrantAdmin: %v", err)
	}
	// Register the "ops" layer as a public admin layer config so resolveLayers
	// (which reads ListLayerConfigs and ignores the boot-time slice once any
	// config row exists) keeps the seeded artifact's layer visible after the
	// runtime layer registrations below add their own config rows.
	if err := pg.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: roflipTenant, ID: "ops", SourceType: "local", Public: true, Order: 0,
	}); err != nil {
		t.Fatalf("PutLayerConfig(ops): %v", err)
	}
	// Seed one manifest under a public layer so load_artifact and search serve a
	// real Postgres row throughout the read-only window.
	const seededID = "ops/runbooks/restart-gateway"
	if err := pg.PutManifest(ctx, store.ManifestRecord{
		TenantID: roflipTenant, ArtifactID: seededID, Version: "1.0.0",
		ContentHash: "sha256:roflipseed", Type: "context",
		Description: "restart the api gateway", Layer: "ops",
		IngestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}

	// Decorate the live Postgres store so the probe's GetTenant health call can be
	// severed on demand. Catalogue reads/writes route through the wrapped store
	// unchanged; only GetTenant fails while severed.
	fault := storetest.NewFaultStore(pg)

	mode := server.NewModeTracker()
	sink := audit.NewMemory()
	bootLayers := []layer.Layer{{ID: "ops", Precedence: 1, Visibility: layer.Visibility{Public: true}}}

	// Mirror serverboot's read-only probe wiring against the fault store with a
	// fast tick. OnEnter/OnExit append the same audit events serverboot's
	// callbacks do (registry.read_only_entered / registry.read_only_exited).
	probe := &server.ReadOnlyProbe{
		Store:      fault,
		Tracker:    mode,
		TenantID:   roflipTenant,
		Interval:   10 * time.Millisecond,
		Failures:   2,
		Recoveries: 2,
		OnEnter: func() {
			_ = sink.Append(context.Background(), audit.Event{
				Type: audit.EventReadOnlyEntered, Caller: "system", Target: roflipTenant,
				Context: map[string]string{"reason": "store_probe_failed"},
			})
		},
		OnExit: func() {
			_ = sink.Append(context.Background(), audit.Event{
				Type: audit.EventReadOnlyExited, Caller: "system", Target: roflipTenant,
			})
		},
	}
	probeCtx, cancelProbe := context.WithCancel(context.Background())
	defer cancelProbe()
	go probe.Run(probeCtx)

	// One mux serving both the core read/write surface and the §7.3.1
	// layer-register ingest endpoint, sharing the single ModeTracker so the
	// read-only gate covers both writes. The fault store backs both.
	coreSrv := server.New(
		core.New(fault, roflipTenant, bootLayers),
		server.WithMode(mode),
		server.WithAudit(sink),
		server.WithIdentityResolver(func(*http.Request) layer.Identity { return roflipAdmin }),
	)
	layerEP := server.NewLayerEndpoint(fault, roflipTenant, mode).
		WithIdentityResolver(func(*http.Request) layer.Identity { return roflipAdmin }).
		WithAudit(sink)

	mux := http.NewServeMux()
	mux.Handle("/v1/layers", layerEP.Handler())
	mux.Handle("/", coreSrv.Handler())
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// ---- 1. Healthy: reads serve the Postgres row and writes are accepted ----
	if code := roflipLoadStatus(t, ts.URL, seededID); code != http.StatusOK {
		t.Fatalf("initial load_artifact = %d, want 200 (Postgres read)", code)
	}
	if !roflipSearchHasID(t, ts.URL, seededID) {
		t.Fatalf("initial search did not return the seeded artifact %q", seededID)
	}
	if code := roflipReadyMode(t, ts.URL); code != "ready" {
		t.Fatalf("initial /readyz mode = %q, want ready", code)
	}
	if code, _ := roflipRegisterLayer(t, ts.URL, "ops-runtime-a"); code != http.StatusCreated {
		t.Fatalf("initial layer register = %d, want 201", code)
	}
	if code, _ := roflipGrant(t, ts.URL, "bob@acme.com"); code != http.StatusCreated {
		t.Fatalf("initial admin grant = %d, want 201", code)
	}

	// ---- 2. Concurrent readers + an in-flight ingest while the primary is severed
	// Drive a steady stream of Postgres-backed reads on a background goroutine so
	// the assertion that reads keep serving spans the actual flip, not just a
	// snapshot after it.
	stopReaders := make(chan struct{})
	var readerErr atomic.Value // string
	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stopReaders:
				return
			default:
				if code := roflipLoadStatus(t, ts.URL, seededID); code != http.StatusOK {
					readerErr.Store("concurrent load_artifact returned " + http.StatusText(code))
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()

	// Sever the primary: the probe's GetTenant health call now fails. The real
	// probe must flip the tracker to read_only after the failure threshold, with
	// no ModeTracker.Set call anywhere in the test.
	fault.Sever()
	roflipWaitMode(t, ts.URL, "read_only")

	// ---- 3. Read-only: reads serve, /readyz reports read_only, writes refused ----
	if code := roflipLoadStatus(t, ts.URL, seededID); code != http.StatusOK {
		t.Errorf("read-only load_artifact = %d, want 200 (reads keep serving from Postgres)", code)
	}
	if !roflipSearchHasID(t, ts.URL, seededID) {
		t.Errorf("read-only search dropped the seeded artifact; reads must keep serving")
	}
	// §13.9: /readyz reports read_only and stays 200 (in rotation).
	rmode, rstatus := roflipReadyz(t, ts.URL)
	if rmode != "read_only" {
		t.Errorf("read-only /readyz mode = %q, want read_only", rmode)
	}
	if rstatus != http.StatusOK {
		t.Errorf("read-only /readyz status = %d, want 200 (still in rotation)", rstatus)
	}
	// The §7.3.1 layer-register ingest write is refused with registry.read_only.
	code, body := roflipRegisterLayer(t, ts.URL, "ops-runtime-b")
	if code != http.StatusServiceUnavailable {
		t.Errorf("read-only layer register = %d, want 503", code)
	}
	if got := roflipErrCode(t, body); got != "registry.read_only" {
		t.Errorf("read-only layer register code = %q, want registry.read_only", got)
	}
	// The admin grant write is likewise refused with registry.read_only.
	gcode, gbody := roflipGrant(t, ts.URL, "carol@acme.com")
	if gcode != http.StatusServiceUnavailable {
		t.Errorf("read-only admin grant = %d, want 503", gcode)
	}
	if got := roflipErrCode(t, gbody); got != "registry.read_only" {
		t.Errorf("read-only admin grant code = %q, want registry.read_only", got)
	}
	// The probe must have recorded exactly one read_only_entered event.
	if n := roflipCountEvents(sink, audit.EventReadOnlyEntered); n != 1 {
		t.Errorf("read_only_entered events = %d, want 1", n)
	}

	// ---- 4. Restore the primary: the probe recovers, writes resume ----
	fault.Restore()
	roflipWaitMode(t, ts.URL, "ready")

	if code, _ := roflipRegisterLayer(t, ts.URL, "ops-runtime-c"); code != http.StatusCreated {
		t.Errorf("recovered layer register = %d, want 201", code)
	}
	if code, _ := roflipGrant(t, ts.URL, "dave@acme.com"); code != http.StatusCreated {
		t.Errorf("recovered admin grant = %d, want 201", code)
	}
	if n := roflipCountEvents(sink, audit.EventReadOnlyExited); n != 1 {
		t.Errorf("read_only_exited events = %d, want 1", n)
	}

	close(stopReaders)
	readerWG.Wait()
	if v := readerErr.Load(); v != nil {
		t.Errorf("a concurrent Postgres-backed read failed during the flip: %v", v)
	}
	if fault.HealthCalls() == 0 {
		t.Error("FaultStore.HealthCalls = 0; probe never pinged the read path")
	}
	cancelProbe()
}

// ---- helpers -------------------------------------------------------------

func roflipLoadStatus(t *testing.T, baseURL, id string) int {
	t.Helper()
	resp, err := http.Get(baseURL + "/v1/load_artifact?id=" + id)
	if err != nil {
		t.Fatalf("GET load_artifact: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func roflipSearchHasID(t *testing.T, baseURL, id string) bool {
	t.Helper()
	resp, err := http.Get(baseURL + "/v1/search_artifacts?query=gateway&top_k=10")
	if err != nil {
		t.Fatalf("GET search_artifacts: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode search_artifacts: %v", err)
	}
	for _, r := range body.Results {
		if r.ID == id {
			return true
		}
	}
	return false
}

// roflipReadyz returns the /readyz mode and HTTP status.
func roflipReadyz(t *testing.T, baseURL string) (mode string, status int) {
	t.Helper()
	resp, err := http.Get(baseURL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /readyz: %v", err)
	}
	return body.Mode, resp.StatusCode
}

func roflipReadyMode(t *testing.T, baseURL string) string {
	t.Helper()
	m, _ := roflipReadyz(t, baseURL)
	return m
}

// roflipRegisterLayer issues a §7.3.1 layer-register ingest (POST /v1/layers) and
// returns the status and body.
func roflipRegisterLayer(t *testing.T, baseURL, layerID string) (int, []byte) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"id": layerID, "source_type": "local", "local_path": "/tmp/" + layerID, "user_defined": false,
	})
	resp, err := http.Post(baseURL+"/v1/layers", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /v1/layers: %v", err)
	}
	defer resp.Body.Close()
	buf, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, buf
}

// roflipGrant issues a write (POST /v1/admin/grants) and returns the status and body.
func roflipGrant(t *testing.T, baseURL, userID string) (int, []byte) {
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

func roflipErrCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope %q: %v", body, err)
	}
	return env.Code
}

// roflipWaitMode polls /readyz until the mode matches want, driving the real
// probe transition rather than forcing it.
func roflipWaitMode(t *testing.T, baseURL, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m, _ := roflipReadyz(t, baseURL); m == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	last, _ := roflipReadyz(t, baseURL)
	t.Fatalf("/readyz mode never reached %q within deadline (last = %q)", want, last)
}

func roflipCountEvents(sink *audit.Memory, typ audit.EventType) int {
	n := 0
	for _, e := range sink.Events() {
		if e.Type == typ {
			n++
		}
	}
	return n
}
