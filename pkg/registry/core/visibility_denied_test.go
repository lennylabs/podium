package core_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §8.1 — when LoadArtifact returns not_found because the
// artifact's layer is invisible to the caller, the registry
// emits a visibility.denied audit event so SIEM can distinguish
// "missing" from "filtered."
// Phase: 7
func TestLoadArtifact_EmitsVisibilityDeniedWhenLayerInvisible(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	const tenantID = "t"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenantID}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenantID, ArtifactID: "team/secret", Version: "1.0.0",
		ContentHash: "sha256:a", Type: "context", Layer: "private",
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	rec := &recorder{}
	reg := core.New(st, tenantID, []layer.Layer{
		{ID: "private", Precedence: 1, Visibility: layer.Visibility{Users: []string{"alice"}}},
	}).WithAudit(rec.emit)

	bob := layer.Identity{Sub: "bob", IsAuthenticated: true}
	_, err := reg.LoadArtifact(context.Background(), bob, "team/secret", core.LoadArtifactOptions{})
	if err == nil {
		t.Fatalf("LoadArtifact: want not_found, got nil")
	}

	denied := false
	for _, e := range rec.events {
		if e.Type == "visibility.denied" && e.Target == "team/secret" {
			denied = true
		}
	}
	if !denied {
		t.Errorf("audit events missing visibility.denied for team/secret: %+v", rec.events)
	}
}

// Spec: §8.1 — a genuine missing artifact (no manifest in the
// store) yields a not_found result without firing
// visibility.denied.
// Phase: 7
func TestLoadArtifact_GenuineNotFoundSkipsVisibilityDenied(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	const tenantID = "t"
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: tenantID})
	rec := &recorder{}
	reg := core.New(st, tenantID, []layer.Layer{
		{ID: "private", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	}).WithAudit(rec.emit)

	bob := layer.Identity{Sub: "bob", IsAuthenticated: true}
	_, _ = reg.LoadArtifact(context.Background(), bob, "team/missing", core.LoadArtifactOptions{})

	for _, e := range rec.events {
		if e.Type == "visibility.denied" {
			t.Errorf("genuine not_found should not emit visibility.denied: %+v", e)
		}
	}
}
