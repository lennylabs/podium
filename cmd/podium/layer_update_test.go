package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §7.3.1 — `podium layer update --ref` patches an existing
// layer. The CLI hits PUT /v1/layers/update?id=ID; the registered
// layer's Ref is replaced.
func TestLayerUpdateCmd_PatchesRef(t *testing.T) {
	const tenantID = "default"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenantID}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutLayerConfig(context.Background(), store.LayerConfig{
		TenantID:   tenantID,
		ID:         "team",
		SourceType: "git",
		Repo:       "git@example/team.git",
		Ref:        "main",
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, tenantID, server.NewModeTracker())
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	rc := layerUpdate([]string{
		"--registry", ts.URL,
		"--id", "team",
		"--ref", "release-26",
	})
	if rc != 0 {
		t.Fatalf("layerUpdate rc = %d, want 0", rc)
	}
	got, err := st.GetLayerConfig(context.Background(), tenantID, "team")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if got.Ref != "release-26" {
		t.Errorf("Ref = %q, want release-26", got.Ref)
	}
}

// Spec: §12 — `podium layer update --rotate-webhook-secret` regenerates
// the git layer's HMAC secret through PUT /v1/layers/update.
func TestLayerUpdateCmd_RotateWebhookSecret(t *testing.T) {
	const tenantID = "default"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenantID}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutLayerConfig(context.Background(), store.LayerConfig{
		TenantID: tenantID, ID: "vendor", SourceType: "git",
		Repo: "git@example/vendor.git", Ref: "main",
		WebhookSecret: "old-secret", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, tenantID, server.NewModeTracker())
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	rc := layerUpdate([]string{
		"--registry", ts.URL,
		"--id", "vendor",
		"--rotate-webhook-secret",
	})
	if rc != 0 {
		t.Fatalf("layerUpdate rc = %d, want 0", rc)
	}
	got, err := st.GetLayerConfig(context.Background(), tenantID, "vendor")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if got.WebhookSecret == "" || got.WebhookSecret == "old-secret" {
		t.Errorf("WebhookSecret = %q, want a freshly rotated value", got.WebhookSecret)
	}
}

// Spec: §7.3.1 — running update without any mutable flag is an
// argument error.
func TestLayerUpdateCmd_RequiresMutableField(t *testing.T) {
	rc := layerUpdate([]string{
		"--registry", "http://example",
		"--id", "team",
	})
	if rc != 2 {
		t.Errorf("rc = %d, want 2 (argument error)", rc)
	}
	_ = fmt.Sprintf("noop") // keep fmt import alive on stripped builds
}

// captureUpdateBody runs layerUpdate against a stub registry and returns the
// decoded request body it sent along with the exit code.
func captureUpdateBody(t *testing.T, args ...string) (map[string]any, int) {
	t.Helper()
	var got map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("decode body %q: %v", raw, err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	}))
	t.Cleanup(ts.Close)
	rc := layerUpdate(append([]string{"--registry", ts.URL, "--id", "team"}, args...))
	return got, rc
}

// Spec: §7.3.1 — the update body carries the members the operator set, so
// `--public=false` withdraws the axis instead of being dropped by a
// non-zero-value guard.
func TestLayerUpdateCmd_BodyFromSetFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want map[string]any
	}{
		{"public false", []string{"--public=false"}, map[string]any{"public": false}},
		{"organization false", []string{"--organization=false"}, map[string]any{"organization": false}},
		{"public true", []string{"--public"}, map[string]any{"public": true}},
		{"ref alone", []string{"--ref", "release-26"}, map[string]any{"ref": "release-26"}},
		{"clear groups", []string{"--clear-groups"}, map[string]any{"groups": []any{}}},
		{"clear users", []string{"--clear-users"}, map[string]any{"users": []any{}}},
		{"groups", []string{"--group", "eng"}, map[string]any{"groups": []any{"eng"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, rc := captureUpdateBody(t, tc.args...)
			if rc != 0 {
				t.Fatalf("rc = %d, want 0", rc)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("body = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// Spec: §7.3.1 — a patch naming only a boolean withdrawal is a mutable field,
// so the `len(body) == 0` guard admits it.
func TestLayerUpdateCmd_WithdrawalSatisfiesMutableFieldGuard(t *testing.T) {
	got, rc := captureUpdateBody(t, "--public=false")
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if len(got) == 0 {
		t.Error("body is empty, want the withdrawal member")
	}
}

// Spec: §7.3.1 — a list flag and its clear flag name opposite intents on one
// member, so the command refuses rather than picking one.
func TestLayerUpdateCmd_ClearConflictsWithValue(t *testing.T) {
	for _, args := range [][]string{
		{"--group", "eng", "--clear-groups"},
		{"--user", "alice", "--clear-users"},
	} {
		rc := layerUpdate(append([]string{"--registry", "http://example", "--id", "team"}, args...))
		if rc != 2 {
			t.Errorf("layerUpdate(%v) rc = %d, want 2 (argument error)", args, rc)
		}
	}
}
