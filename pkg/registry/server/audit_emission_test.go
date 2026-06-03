package server_test

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

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

func newAuditSink(t *testing.T) *audit.FileSink {
	t.Helper()
	sink, err := audit.NewFileSink(filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	return sink
}

func readAuditLog(t *testing.T, sink *audit.FileSink) string {
	t.Helper()
	b, err := os.ReadFile(sink.Path())
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	return string(b)
}

// spec: §8.1 — "admin.granted | When an admin grant was added or revoked."
// The grants handler records admin.granted on both POST (grant) and DELETE
// (revoke) with the acting admin as caller, the affected user as target, and
// the add-versus-revoke action in context. F-8.1.2.
func TestAdminGrants_EmitsAdminGranted(t *testing.T) {
	t.Parallel()
	sink := newAuditSink(t)
	ts := bootRegistryWithAdmin(t, "alice", []layer.Layer{
		{ID: "team", Visibility: layer.Visibility{Public: true}},
	}, server.WithAudit(sink))

	body, _ := json.Marshal(map[string]string{"user_id": "bob"})
	resp, err := http.Post(ts.URL+"/v1/admin/grants", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("grant status = %d, want 201", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/v1/admin/grants?user_id=bob", nil)
	delResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204", delResp.StatusCode)
	}

	got := readAuditLog(t, sink)
	for _, want := range []string{
		`"type":"admin.granted"`,
		`"caller":{"identity":"alice"`,
		`"target":"bob"`,
		`"action":"grant"`,
		`"action":"revoke"`,
		`"trace_id"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("admin audit log missing %s\nlog:\n%s", want, got)
		}
	}
}

// layerAuditHarness builds a LayerEndpoint wired to an audit sink and an
// identity resolver, so register/unregister/reorder emit §8.1 events.
func layerAuditHarness(t *testing.T, sink *audit.FileSink, id layer.Identity) string {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithAudit(sink).
		WithIdentityResolver(func(*http.Request) layer.Identity { return id })
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

// spec: §8.1 — "layer.user_registered | When a user registered or
// unregistered a personal layer." A user-defined layer register and the
// matching unregister both emit layer.user_registered, naming the owner and
// the register-versus-unregister action. F-8.1.4.
func TestLayerEndpoint_EmitsUserRegistered(t *testing.T) {
	t.Parallel()
	sink := newAuditSink(t)
	base := layerAuditHarness(t, sink, layer.Identity{Sub: "alice", IsAuthenticated: true})

	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "alice-personal", "source_type": "local", "local_path": "/tmp/alice",
		"user_defined": true, "owner": "alice",
	})
	mustDelete(t, base, "/v1/layers?id=alice-personal")

	got := readAuditLog(t, sink)
	for _, want := range []string{
		`"type":"layer.user_registered"`,
		`"target":"alice-personal"`,
		`"caller":{"identity":"alice"`,
		`"owner":"alice"`,
		`"action":"register"`,
		`"action":"unregister"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("user-layer audit log missing %s\nlog:\n%s", want, got)
		}
	}
	if strings.Contains(got, "layer.config_changed") {
		t.Errorf("personal layer must not emit layer.config_changed:\n%s", got)
	}
}

// spec: §8.1 — "layer.config_changed | When an admin added, removed, or
// reordered admin-defined layers." Registering and reordering admin-defined
// layers emits layer.config_changed with the add and reorder actions; a
// personal-layer event type must not appear. F-8.1.3.
func TestLayerEndpoint_EmitsConfigChanged(t *testing.T) {
	t.Parallel()
	sink := newAuditSink(t)
	base := layerAuditHarness(t, sink, layer.Identity{Sub: "admin", IsAuthenticated: true})

	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "team-a", "source_type": "local", "local_path": "/x",
	})
	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "team-b", "source_type": "local", "local_path": "/y",
	})
	resp, body := mustPost(t, base, "/v1/layers/reorder", map[string]any{
		"order": []string{"team-b", "team-a"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reorder status %d: %s", resp.StatusCode, body)
	}

	got := readAuditLog(t, sink)
	for _, want := range []string{
		`"type":"layer.config_changed"`,
		`"action":"register"`,
		`"action":"reorder"`,
		`"target":"team-a"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("admin-layer audit log missing %s\nlog:\n%s", want, got)
		}
	}
	if strings.Contains(got, "layer.user_registered") {
		t.Errorf("admin layer must not emit layer.user_registered:\n%s", got)
	}
}

// spec: §8.1 — a nil audit sink (no §8.3 sink configured) leaves the layer
// handlers as a no-op rather than panicking, so an unconfigured deployment
// still serves registrations. F-8.1.3 / F-8.1.4.
func TestLayerEndpoint_NoAuditSinkIsNoop(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()
	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "team-a", "source_type": "local", "local_path": "/x",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register without sink status %d: %s", resp.StatusCode, body)
	}
}
