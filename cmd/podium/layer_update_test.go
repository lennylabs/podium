package main

import (
	"context"
	"fmt"
	"net/http/httptest"
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
