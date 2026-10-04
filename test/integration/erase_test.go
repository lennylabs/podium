package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §8.5 — `podium admin erase <user_id>` unregisters and purges the
// user's owned layers and the artifacts ingested from them, redacts the user
// identity across the registry audit stream, and records a user.erased event
// naming the invoking admin. Drives the real LayerEndpoint over HTTP against a
// file-backed SQLite store and a file-backed audit sink.
func TestErase_SQLitePurgesLayersAndRedactsAudit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "registry.db")
	st, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	sinkPath := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(sinkPath)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}

	admin := layer.Identity{Sub: "carol@acme.com", IsAuthenticated: true}
	ep := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithAudit(sink).
		WithEraseSink(sink).
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) { return admin, nil }).
		WithAdminAuth(func(*http.Request) error { return nil })
	mux := http.NewServeMux()
	mux.Handle("/v1/admin/erase", ep.EraseHandler())
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	// alice owns a user-defined layer with an artifact ingested from it.
	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "alice-personal", SourceType: "local", LocalPath: "/tmp/x",
		UserDefined: true, Owner: "alice@acme.com", Users: []string{"alice@acme.com"},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	if err := st.PutManifest(ctx, store.ManifestRecord{
		TenantID: "t", ArtifactID: "skill/a", Version: "1.0.0", ContentHash: "h",
		Type: "skill", Layer: "alice-personal",
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	_ = sink.Append(ctx, audit.Event{
		Type: audit.EventArtifactsSearched, Caller: "alice@acme.com",
		CallerEmail: "alice@acme.com", CallerGroups: []string{"acme-engineering"},
		Timestamp: time.Now().UTC(),
	})

	body, _ := json.Marshal(map[string]any{"user_id": "alice@acme.com", "salt": "tenant-salt"})
	resp, err := http.Post(ts.URL+"/v1/admin/erase", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST erase: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Layer + artifact soft-deleted.
	if _, err := st.GetLayerConfig(ctx, "t", "alice-personal"); err == nil {
		t.Errorf("layer still visible after erase")
	}
	if _, err := st.GetManifest(ctx, "t", "skill/a", "1.0.0"); err == nil {
		t.Errorf("artifact still visible after erase")
	}
	// Layer recoverable within the §8.4 window (soft-delete, not hard-delete).
	if deleted, _ := st.ListDeletedLayerConfigs(ctx, "t"); len(deleted) != 1 {
		t.Errorf("ListDeletedLayerConfigs = %d, want 1", len(deleted))
	}

	// Registry audit stream redacted, chain intact.
	data, _ := os.ReadFile(sinkPath)
	if strings.Contains(string(data), "alice@acme.com") {
		t.Errorf("erased identity still present in audit stream")
	}
	// the attached email and group membership are redacted too.
	if strings.Contains(string(data), "acme-engineering") {
		t.Errorf("erased user's group membership still present in audit stream")
	}
	if !strings.Contains(string(data), "carol@acme.com") {
		t.Errorf("invoking admin not recorded")
	}
	verify, _ := audit.NewFileSink(sinkPath)
	if err := verify.Verify(ctx); err != nil {
		t.Errorf("chain broken after erase: %v", err)
	}
}

// Spec: §8.5, §6.3.1, §4.7.2 — on a multi-tenant registry over SQLite, an
// erase routed to tenant A by the caller's organization purges the user's A
// layer and its artifact, redacts only A-labeled audit records, and records
// user.erased labeled A. The user's B layer, its artifact, the B-labeled
// record, and the unlabeled record stay unchanged.
func TestErase_SQLiteTwoTenantsScopedToRoutedTenant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sinkPath := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(sinkPath)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	ts := twoTenantEraseServer(t, st, sink)
	for _, tenant := range []string{"A", "B"} {
		seedAliceLayer(t, st, tenant)
	}
	if err := st.GrantAdmin(ctx, store.AdminGrant{UserID: "carol@acme.com", OrgID: "A"}); err != nil {
		t.Fatalf("GrantAdmin: %v", err)
	}
	for _, tenant := range []string{"A", "B", ""} {
		if err := sink.Append(ctx, audit.Event{Type: audit.EventArtifactLoaded,
			Caller: "alice@acme.com", Tenant: tenant, Timestamp: time.Now().UTC()}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	body, _ := json.Marshal(map[string]any{"user_id": "alice@acme.com", "salt": "tenant-salt"})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/admin/erase", bytes.NewReader(body))
	req.Header.Set("X-Test-User", "carol@acme.com")
	req.Header.Set("X-Test-Org", "A")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST erase: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Purged   []string `json:"layers_purged"`
		Redacted int      `json:"audit_events_redacted"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The A-labeled seed and the record of the A layer purge.
	if len(out.Purged) != 1 || out.Purged[0] != "alice-personal" || out.Redacted != 2 {
		t.Errorf("erase = %+v, want alice-personal purged and 2 records redacted", out)
	}
	if _, err := st.GetLayerConfig(ctx, "A", "alice-personal"); err == nil {
		t.Errorf("A layer still visible after erase")
	}
	if _, err := st.GetManifest(ctx, "A", "skill/a", "1.0.0"); err == nil {
		t.Errorf("A artifact still visible after erase")
	}
	if _, err := st.GetLayerConfig(ctx, "B", "alice-personal"); err != nil {
		t.Errorf("B layer purged by A's erase: %v", err)
	}
	if _, err := st.GetManifest(ctx, "B", "skill/a", "1.0.0"); err != nil {
		t.Errorf("B artifact purged by A's erase: %v", err)
	}
	assertAuditScopedTo(t, sinkPath, "A", "alice@acme.com")
}

// twoTenantEraseServer mounts the erase route of a multi-tenant endpoint
// bound to the boot tenant P behind the server's §6.3.1 tenant router, as
// serverboot mounts it. The stub verifier reads the subject and organization
// from the X-Test-User and X-Test-Org headers, and the admin check reads the
// routed tenant's grants.
func twoTenantEraseServer(t *testing.T, st store.Store, sink *audit.FileSink) *httptest.Server {
	t.Helper()
	for _, id := range []string{"P", "A", "B"} {
		if err := st.CreateTenant(context.Background(), store.Tenant{ID: id, Name: id}); err != nil {
			t.Fatalf("CreateTenant %s: %v", id, err)
		}
	}
	verify := func(r *http.Request) (layer.Identity, error) {
		user := r.Header.Get("X-Test-User")
		return layer.Identity{Sub: user, Email: user, OrgID: r.Header.Get("X-Test-Org"), IsAuthenticated: true}, nil
	}
	resolve := func(ctx context.Context, org string) (store.Tenant, bool) {
		if org == "" || org == "P" {
			return store.Tenant{}, false
		}
		tn, err := st.GetTenant(ctx, org)
		return tn, err == nil && tn.Active
	}
	reg := core.New(st, "P", nil)
	srv := server.New(reg, server.WithIdentityVerifier(verify), server.WithTenantRouter(resolve, false))
	ep := server.NewLayerEndpoint(st, "P", server.NewModeTracker()).
		WithTenantRouting().
		WithAudit(sink).
		WithEraseSink(sink).
		WithIdentityResolver(verify).
		WithAdminAuth(func(r *http.Request) error {
			id, _ := verify(r)
			return reg.AdminAuthorize(r.Context(), id)
		})
	mux := http.NewServeMux()
	mux.Handle("/v1/admin/erase", srv.TenantRouted(ep.EraseHandler()))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// seedAliceLayer stores alice's user-defined layer alice-personal in tenant
// with one artifact ingested from it.
func seedAliceLayer(t *testing.T, st store.Store, tenant string) {
	t.Helper()
	ctx := context.Background()
	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: tenant, ID: "alice-personal", SourceType: "local", LocalPath: "/tmp/" + tenant,
		UserDefined: true, Owner: "alice@acme.com", Users: []string{"alice@acme.com"},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PutLayerConfig %s: %v", tenant, err)
	}
	if err := st.PutManifest(ctx, store.ManifestRecord{
		TenantID: tenant, ArtifactID: "skill/a", Version: "1.0.0", ContentHash: "h",
		Type: "skill", Layer: "alice-personal",
	}); err != nil {
		t.Fatalf("PutManifest %s: %v", tenant, err)
	}
}

// assertAuditScopedTo checks that every record labeled tenant, user.erased
// included, no longer names user, that every other record still does, that
// the last record is user.erased labeled tenant, and that the chain verifies.
func assertAuditScopedTo(t *testing.T, path, tenant, user string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	type record struct {
		Type   string `json:"type"`
		Tenant string `json:"tenant"`
	}
	var last record
	for _, line := range lines {
		// A fresh value per line, because an unlabeled record omits the
		// tenant key and would otherwise keep the previous record's label.
		last = record{}
		if err := json.Unmarshal(line, &last); err != nil {
			t.Fatalf("parse %s: %v", line, err)
		}
		if named := bytes.Contains(line, []byte(user)); named == (last.Tenant == tenant) {
			t.Errorf("record labeled %q names %s = %v: %s", last.Tenant, user, named, line)
		}
	}
	if last.Type != string(audit.EventUserErased) || last.Tenant != tenant {
		t.Errorf("last record = %+v, want user.erased labeled %s", last, tenant)
	}
	verify, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	if err := verify.Verify(context.Background()); err != nil {
		t.Errorf("chain broken after erase: %v", err)
	}
}
