package main

import (
	"context"
	"testing"
	"time"

	"net/http/httptest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §7.3.1 — `podium layer register --force-push-policy strict`
// persists the policy on the layer config.
func TestLayerRegisterCmd_ForcePushPolicy(t *testing.T) {
	const tenantID = "default"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenantID}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	ts := httptest.NewServer(server.NewLayerEndpoint(st, tenantID, server.NewModeTracker()).Handler())
	t.Cleanup(ts.Close)

	rc := layerRegister([]string{
		"--registry", ts.URL, "--id", "team",
		"--repo", "git@example/team.git", "--ref", "main",
		"--force-push-policy", "strict",
	})
	if rc != 0 {
		t.Fatalf("layerRegister rc = %d, want 0", rc)
	}
	got, err := st.GetLayerConfig(context.Background(), tenantID, "team")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if got.ForcePushPolicy != "strict" {
		t.Errorf("ForcePushPolicy = %q, want strict", got.ForcePushPolicy)
	}
}

// Spec: §4.7.2 — `--break-glass` without `--justification` is a
// client-side argument error (rc 2) and never contacts the registry.
func TestLayerReingestCmd_BreakGlassRequiresJustification(t *testing.T) {
	rc := layerReingest([]string{
		"--registry", "http://127.0.0.1:0", "--break-glass", "team",
	})
	if rc != 2 {
		t.Errorf("rc = %d, want 2", rc)
	}
}

// Spec: §4.7.2 — the break-glass flags reach the server as a request
// body the reingest runner receives (justification + approvers).
func TestLayerReingestCmd_BreakGlassBodySent(t *testing.T) {
	const tenantID = "default"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenantID}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutLayerConfig(context.Background(), store.LayerConfig{
		TenantID: tenantID, ID: "team", SourceType: "local", LocalPath: "/tmp/x",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var gotBG *server.BreakGlass
	runner := func(_ context.Context, _ store.LayerConfig, bg *server.BreakGlass) (*ingest.Result, error) {
		gotBG = bg
		return &ingest.Result{}, nil
	}
	endpoint := server.NewLayerEndpoint(st, tenantID, server.NewModeTracker()).WithReingestRunner(runner)
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	rc := layerReingest([]string{
		"--registry", ts.URL,
		"--break-glass", "--justification", "year-end hotfix",
		"--approver", "alice@acme.com", "--approver", "bob@acme.com",
		"team",
	})
	if rc != 0 {
		t.Fatalf("layerReingest rc = %d, want 0", rc)
	}
	if gotBG == nil || gotBG.Justification != "year-end hotfix" || len(gotBG.Approvers) != 2 {
		t.Errorf("break-glass grant not threaded: %+v", gotBG)
	}
}
