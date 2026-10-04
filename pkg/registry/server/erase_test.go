package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// eraseTestEndpoint builds a LayerEndpoint wired with a file-backed audit
// sink (so the §8.5 redaction has a registry stream to rewrite), an admin
// identity, and an admin-auth gate. It returns the endpoint, the running
// test server, the sink path, and the store.
func eraseTestEndpoint(t *testing.T, admin layer.Identity, authErr error) (*httptest.Server, string, store.Store) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	sinkPath := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(sinkPath)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	ep := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithAudit(sink).
		WithEraseSink(sink).
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) { return admin, nil }).
		WithAdminAuth(func(*http.Request) error { return authErr })
	mux := http.NewServeMux()
	mux.Handle("/v1/layers", ep.Handler())
	mux.Handle("/v1/layers/", ep.Handler())
	mux.Handle("/v1/admin/erase", ep.EraseHandler())
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, sinkPath, st
}

func postErase(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url+"/v1/admin/erase", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST erase: %v", err)
	}
	return resp
}

// Spec: §8.5 — erase unregisters and soft-deletes
// the user's owned layers and the artifacts ingested from them, redacts the
// user identity across the registry audit stream, and appends a user.erased
// event naming the invoking admin.
func TestErase_PurgesLayersAndRedactsRegistryStream(t *testing.T) {
	t.Parallel()
	admin := layer.Identity{Sub: "carol@acme.com", IsAuthenticated: true}
	ts, sinkPath, st := eraseTestEndpoint(t, admin, nil)
	ctx := context.Background()

	// alice owns a user-defined layer with an artifact ingested from it.
	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "alice-personal", SourceType: "local", LocalPath: "/tmp/x",
		UserDefined: true, Owner: "alice@acme.com", Users: []string{"alice@acme.com"},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	// A separate layer owned by bob must survive.
	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "bob-personal", SourceType: "local", LocalPath: "/tmp/y",
		UserDefined: true, Owner: "bob@acme.com", Users: []string{"bob@acme.com"},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PutLayerConfig bob: %v", err)
	}
	if err := st.PutManifest(ctx, store.ManifestRecord{
		TenantID: "t", ArtifactID: "skill/a", Version: "1.0.0", ContentHash: "h",
		Type: "skill", Layer: "alice-personal",
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	// Seed an audit event whose caller is the erased identity, carrying the
	// §8.1 attached email and group membership so the redaction pass has PII to
	// remove.
	sink, _ := audit.NewFileSink(sinkPath)
	_ = sink.Append(ctx, audit.Event{
		Type: audit.EventArtifactsSearched, Caller: "alice@acme.com",
		CallerEmail: "alice@acme.com", CallerGroups: []string{"acme-engineering"},
		Timestamp: time.Now().UTC(),
	})

	resp := postErase(t, ts.URL, map[string]any{"user_id": "alice@acme.com", "salt": "tenant-salt"})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Erased       string   `json:"erased"`
		LayersPurged []string `json:"layers_purged"`
		Redacted     int      `json:"audit_events_redacted"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.LayersPurged) != 1 || out.LayersPurged[0] != "alice-personal" {
		t.Errorf("layers_purged = %v, want [alice-personal]", out.LayersPurged)
	}

	// Layer + artifact soft-deleted.
	if _, err := st.GetLayerConfig(ctx, "t", "alice-personal"); err == nil {
		t.Errorf("alice's layer still visible after erase")
	}
	if _, err := st.GetManifest(ctx, "t", "skill/a", "1.0.0"); err == nil {
		t.Errorf("artifact still visible after erase (no soft-delete)")
	}
	// bob's layer survives.
	if _, err := st.GetLayerConfig(ctx, "t", "bob-personal"); err != nil {
		t.Errorf("bob's layer wrongly purged: %v", err)
	}

	// Registry audit stream redacted: original identity gone, tombstone and
	// admin-named user.erased present, chain still valid.
	data, _ := os.ReadFile(sinkPath)
	if strings.Contains(string(data), "alice@acme.com") {
		t.Errorf("erased identity still present in registry audit stream")
	}
	// the attached email and group membership are gone too.
	if strings.Contains(string(data), "acme-engineering") {
		t.Errorf("erased user's group membership still present in registry audit stream")
	}
	if !strings.Contains(string(data), "user.erased") {
		t.Errorf("user.erased event not appended")
	}
	if !strings.Contains(string(data), "carol@acme.com") {
		t.Errorf("invoking admin carol@acme.com not recorded on user.erased")
	}
	verifySink, _ := audit.NewFileSink(sinkPath)
	if err := verifySink.Verify(ctx); err != nil {
		t.Errorf("hash chain broken after erase: %v", err)
	}
}

// Spec: §8.5 — an empty salt is rejected with a 400 before any
// state mutates.
func TestErase_EmptySaltRejected(t *testing.T) {
	t.Parallel()
	ts, sinkPath, st := eraseTestEndpoint(t, layer.Identity{Sub: "carol@acme.com", IsAuthenticated: true}, nil)
	if err := st.PutLayerConfig(context.Background(), store.LayerConfig{
		TenantID: "t", ID: "alice-personal", SourceType: "local", UserDefined: true,
		Owner: "alice@acme.com", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	resp := postErase(t, ts.URL, map[string]any{"user_id": "alice@acme.com", "salt": ""})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	// No layer purged, no audit rewrite.
	if _, err := st.GetLayerConfig(context.Background(), "t", "alice-personal"); err != nil {
		t.Errorf("layer purged despite rejected erase: %v", err)
	}
	if data, _ := os.ReadFile(sinkPath); strings.Contains(string(data), "user.erased") {
		t.Errorf("user.erased appended despite rejected erase")
	}
}

// Spec: §8.5 — erase is admin-only; a non-admin caller is forbidden.
func TestErase_RequiresAdmin(t *testing.T) {
	t.Parallel()
	ts, _, _ := eraseTestEndpoint(t, layer.Identity{IsPublic: true}, server.ErrAdminRequired)
	resp := postErase(t, ts.URL, map[string]any{"user_id": "alice@acme.com", "salt": "s"})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

// Spec: §8.5 — erase is POST-only.
func TestErase_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	ts, _, _ := eraseTestEndpoint(t, layer.Identity{Sub: "carol@acme.com", IsAuthenticated: true}, nil)
	resp, err := http.Get(ts.URL + "/v1/admin/erase")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

// Spec: §8.5 — missing user_id is a 400.
func TestErase_MissingUserID(t *testing.T) {
	t.Parallel()
	ts, _, _ := eraseTestEndpoint(t, layer.Identity{Sub: "carol@acme.com", IsAuthenticated: true}, nil)
	resp := postErase(t, ts.URL, map[string]any{"salt": "s"})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// failingSink is an audit.Sink whose Append always fails, standing in for an
// unreachable external endpoint.
type failingSink struct{ audit.Sink }

func (failingSink) Append(context.Context, audit.Event) error {
	return errors.New("endpoint unreachable")
}

// endpointSinkErase builds a single-tenant endpoint redirected to an external
// audit sink (no local file to rewrite) with an afterErase hook counter.
func endpointSinkErase(t *testing.T, sink audit.Sink, hooks *int) *httptest.Server {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	admin := layer.Identity{Sub: "carol@acme.com", IsAuthenticated: true}
	ep := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithAudit(sink).
		WithEraseSink(nil).
		WithAfterErase(func(context.Context) { *hooks++ }).
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) { return admin, nil }).
		WithAdminAuth(func(*http.Request) error { return nil })
	ts := httptest.NewServer(ep.EraseHandler())
	t.Cleanup(ts.Close)
	return ts
}

// Spec: §8.5, §8.6 — with an external endpoint sink the registry rewrites no
// record, appends a tenant-labeled user.erased naming the admin, and runs no
// re-anchor because no local chain changed.
func TestErase_EndpointSinkRecordsUserErased(t *testing.T) {
	t.Parallel()
	sink := audit.NewMemory()
	hooks := 0
	ts := endpointSinkErase(t, sink, &hooks)
	resp := postErase(t, ts.URL, map[string]any{"user_id": "alice@acme.com", "salt": "s"})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("events = %+v, want one user.erased", events)
	}
	ev := events[0]
	if ev.Type != audit.EventUserErased || ev.Tenant != "t" || ev.Caller != "carol@acme.com" || ev.Context["transformed"] != "0" {
		t.Errorf("user.erased = %+v, want tenant t, caller carol, transformed 0", ev)
	}
	if _, ok := ev.Context["superseded_head"]; ok {
		t.Errorf("superseded_head recorded with no chain rewrite")
	}
	if hooks != 0 {
		t.Errorf("afterErase ran %d times with no file sink, want 0", hooks)
	}
}

// Spec: §8.5 — a failed user.erased delivery to the endpoint sink is reported
// as 500 registry.unavailable rather than dropped.
func TestErase_EndpointSinkAppendFailure(t *testing.T) {
	t.Parallel()
	hooks := 0
	ts := endpointSinkErase(t, failingSink{}, &hooks)
	resp := postErase(t, ts.URL, map[string]any{"user_id": "alice@acme.com", "salt": "s"})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if env := decodeEnvelope(t, resp); env.Code != "registry.unavailable" || !strings.Contains(env.Message, "user.erased") {
		t.Errorf("envelope = %s %q, want registry.unavailable naming user.erased", env.Code, env.Message)
	}
}

// eraseMarkerKey is the request-context key the after-erase test sets, so the
// hook can show that it received a context derived from the request.
type eraseMarkerKey struct{}

// Spec: §8.6 — the after-erase re-anchor hook runs exactly once after the
// erasure rewrites the file-backed chain, with a context derived from the
// request context, and only after user.erased is on disk.
func TestErase_AfterEraseRunsOnceWithRequestContext(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	sinkPath := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(sinkPath)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	if err := sink.Append(context.Background(), audit.Event{
		Type: audit.EventArtifactLoaded, Caller: "alice@acme.com", Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	var calls int
	var marker any
	var erasedOnDisk bool
	admin := layer.Identity{Sub: "carol@acme.com", IsAuthenticated: true}
	h := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithAudit(sink).
		WithEraseSink(sink).
		WithAfterErase(func(ctx context.Context) {
			calls++
			marker = ctx.Value(eraseMarkerKey{})
			data, _ := os.ReadFile(sinkPath)
			erasedOnDisk = strings.Contains(string(data), string(audit.EventUserErased))
		}).
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) { return admin, nil }).
		WithAdminAuth(func(*http.Request) error { return nil }).
		EraseHandler()
	b, _ := json.Marshal(map[string]any{"user_id": "alice@acme.com", "salt": "s"})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/erase", bytes.NewReader(b))
	req = req.WithContext(context.WithValue(req.Context(), eraseMarkerKey{}, "request"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if calls != 1 || marker != "request" || !erasedOnDisk {
		t.Errorf("afterErase calls = %d, marker = %v, user.erased on disk = %v; want 1, request, true",
			calls, marker, erasedOnDisk)
	}
}

// multiTenantEraseServer builds a multi-tenant endpoint bound to the boot
// tenant P over tenants A and B, with an admin that every request admits.
// Each request acts in the tenant its X-Test-Tenant header names, standing in
// for the §6.3.1 router serverboot mounts the route behind.
func multiTenantEraseServer(t *testing.T, st store.Store, sink *audit.FileSink) *httptest.Server {
	t.Helper()
	for _, id := range []string{"A", "B"} {
		if err := st.CreateTenant(context.Background(), store.Tenant{ID: id}); err != nil {
			t.Fatalf("CreateTenant %s: %v", id, err)
		}
	}
	admin := layer.Identity{Sub: "carol@acme.com", IsAuthenticated: true}
	h := server.NewLayerEndpoint(st, "P", server.NewModeTracker()).
		WithTenantRouting().
		WithAudit(sink).
		WithEraseSink(sink).
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) { return admin, nil }).
		WithAdminAuth(func(*http.Request) error { return nil }).
		EraseHandler()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tenant := r.Header.Get("X-Test-Tenant"); tenant != "" {
			r = r.WithContext(core.ContextWithTenant(r.Context(), tenant))
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// postEraseIn sends an erase of user acting in tenant and returns the
// decoded 200 response body.
func postEraseIn(t *testing.T, base, tenant, user string) map[string]any {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"user_id": user, "salt": "s"})
	req, err := http.NewRequest(http.MethodPost, base+"/v1/admin/erase", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-Test-Tenant", tenant)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST erase: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("erase %s in %s: status = %d, want 200", user, tenant, resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// Spec: §8.5 — on a multi-tenant registry the erase response carries no
// information about records outside the routed tenant. Erasing bob, who
// appears only in B-labeled and unlabeled records and owns a layer in B,
// answers A's admin exactly as erasing dave, who appears nowhere, and
// neither erase changes bob's records or layer.
func TestErase_MultiTenantForeignUserMatchesAbsentUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := store.NewMemory()
	sinkPath := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(sinkPath)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	ts := multiTenantEraseServer(t, st, sink)
	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "B", ID: "bob-personal", SourceType: "local", LocalPath: "/tmp/b",
		UserDefined: true, Owner: "bob@acme.com", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	for _, ev := range []audit.Event{
		{Caller: "bob@acme.com", Tenant: "B"},
		{Caller: "bob@acme.com"},
		{Caller: "erin@acme.com", Tenant: "A"},
	} {
		ev.Type, ev.Timestamp = audit.EventArtifactLoaded, time.Now().UTC()
		if err := sink.Append(ctx, ev); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	foreign := postEraseIn(t, ts.URL, "A", "bob@acme.com")
	absent := postEraseIn(t, ts.URL, "A", "dave@acme.com")
	for _, body := range []map[string]any{foreign, absent} {
		if len(body) != 3 || body["audit_events_redacted"] != float64(0) {
			t.Errorf("body = %v, want only erased, layers_purged, and audit_events_redacted 0", body)
		}
		if purged, ok := body["layers_purged"].([]any); !ok || len(purged) != 0 {
			t.Errorf("layers_purged = %v, want []", body["layers_purged"])
		}
	}
	delete(foreign, "erased")
	delete(absent, "erased")
	if !reflect.DeepEqual(foreign, absent) {
		t.Errorf("foreign-user body %v differs from absent-user body %v", foreign, absent)
	}
	if data, _ := os.ReadFile(sinkPath); strings.Count(string(data), "bob@acme.com") != 2 {
		t.Errorf("bob's B-labeled and unlabeled records changed:\n%s", data)
	}
	if _, err := st.GetLayerConfig(ctx, "B", "bob-personal"); err != nil {
		t.Errorf("bob's B layer purged by A's erase: %v", err)
	}
}

// Spec: §8.5, §8.1 — on a single-tenant endpoint the erase scope and the
// endpoint's own emitter label read the same bound tenant, so an erase
// reaches the records the endpoint labeled, together with unlabeled records,
// and leaves a record labeled with a foreign tenant unchanged.
func TestErase_SingleTenantReachesOwnLabels(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	sinkPath := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(sinkPath)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	ep := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithAudit(sink).
		WithEraseSink(sink).
		WithIdentityResolver(func(r *http.Request) (layer.Identity, error) {
			return layer.Identity{Sub: r.Header.Get("X-Test-User"), IsAuthenticated: true}, nil
		}).
		WithAdminAuth(func(*http.Request) error { return nil })
	mux := http.NewServeMux()
	mux.Handle("/v1/layers", ep.Handler())
	mux.Handle("/v1/admin/erase", ep.EraseHandler())
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	sendAs(t, ts.URL+"/v1/layers", "alice@acme.com", map[string]any{
		"id": "alice-personal", "source_type": "git", "repo": "https://git.invalid/alice.git", "user_defined": true,
	}, http.StatusCreated)
	const alice = "alice@acme.com"
	for _, tenant := range []string{"", "f"} {
		if err := sink.Append(ctx, audit.Event{Type: audit.EventArtifactLoaded, Caller: alice,
			Tenant: tenant, Timestamp: time.Now().UTC()}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	before := eraseRecords(t, sinkPath)
	if len(before) != 3 || before[0]["type"] != string(audit.EventLayerUserRegistered) || before[0]["tenant"] != "t" {
		t.Fatalf("records before erase = %v, want the endpoint's t-labeled register record first", before)
	}

	var out struct {
		Redacted int `json:"audit_events_redacted"`
	}
	sendAs(t, ts.URL+"/v1/admin/erase", "carol@acme.com",
		map[string]any{"user_id": alice, "salt": "s"}, http.StatusOK, &out)
	// The register record, the unlabeled seed, and the record of the purge.
	if out.Redacted != 3 {
		t.Errorf("audit_events_redacted = %d, want 3", out.Redacted)
	}
	after := eraseRecords(t, sinkPath)
	for i, rec := range after {
		enc, _ := json.Marshal(rec)
		if rec["tenant"] == "f" {
			continue
		}
		if strings.Contains(string(enc), alice) {
			t.Errorf("record %d still names alice: %s", i, enc)
		}
		// The t-labeled register record and the unlabeled seed carry the
		// tombstone where they named alice.
		if i < 2 && !strings.Contains(string(enc), `"redacted-`) {
			t.Errorf("record %d carries no tombstone: %s", i, enc)
		}
	}
	if !reflect.DeepEqual(withoutChain(before[2]), withoutChain(after[2])) {
		t.Errorf("f-labeled record changed:\nbefore %v\nafter  %v", before[2], after[2])
	}
	erased := after[len(after)-1]
	if erased["type"] != string(audit.EventUserErased) || erased["tenant"] != "t" {
		t.Errorf("last record = %v, want user.erased labeled t", erased)
	}
	if err := sink.Verify(ctx); err != nil {
		t.Errorf("Verify after erase: %v", err)
	}
}

// sendAs posts body to url as the subject user, checks the status, and
// decodes the response into out when one is passed.
func sendAs(t *testing.T, url, user string, body any, wantStatus int, out ...any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-Test-User", user)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != wantStatus {
		t.Fatalf("POST %s as %s: status = %d, want %d", url, user, resp.StatusCode, wantStatus)
	}
	for _, o := range out {
		if err := json.NewDecoder(resp.Body).Decode(o); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
}

// eraseRecords parses every line of the audit file at path.
func eraseRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var recs []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("parse %s: %v", line, err)
		}
		recs = append(recs, rec)
	}
	return recs
}

// withoutChain returns rec without the hash and prev_hash keys, which a chain
// rewrite recomputes.
func withoutChain(rec map[string]any) map[string]any {
	out := maps.Clone(rec)
	delete(out, "hash")
	delete(out, "prev_hash")
	return out
}
