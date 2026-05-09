package core_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.6 + §6.3.1 — when a GroupResolver is wired via
// WithGroupResolver, the registry expands a layer's `groups:`
// filter into a SCIM-managed user list before deciding visibility.
// A user without the group claim in their JWT but present in the
// resolved member set sees the layer.
// Phase: 7
func TestRegistry_WithGroupResolver_ExpandsGroupMembership(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	const tenant = "t"
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: tenant})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenant, ArtifactID: "team-x", Version: "1.0.0",
		ContentHash: "sha256:a", Type: "context", Layer: "team",
	})

	resolver := func(g string) []string {
		if g == "engineering" {
			return []string{"alice", "bob"}
		}
		return nil
	}
	reg := core.New(st, tenant, []layer.Layer{
		{ID: "team", Precedence: 1, Visibility: layer.Visibility{Groups: []string{"engineering"}}},
	}).WithGroupResolver(resolver)

	// Alice has no JWT group claim; the resolver matches her sub.
	alice := layer.Identity{Sub: "alice", IsAuthenticated: true}
	res, err := reg.SearchArtifacts(context.Background(), alice, core.SearchArtifactsOptions{})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if len(res.Results) != 1 || res.Results[0].ID != "team-x" {
		t.Errorf("alice results = %+v, want [team-x]", res.Results)
	}

	// Carol is not in the resolved set: she sees nothing.
	carol := layer.Identity{Sub: "carol", IsAuthenticated: true}
	res, err = reg.SearchArtifacts(context.Background(), carol, core.SearchArtifactsOptions{})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if len(res.Results) != 0 {
		t.Errorf("carol results = %+v, want []", res.Results)
	}
}

// Spec: §4.6 — without a GroupResolver, the visibility evaluator
// falls back to JWT-claim groups (back-compat).
// Phase: 7
func TestRegistry_NoGroupResolver_FallsBackToJWTClaims(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	const tenant = "t"
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: tenant})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenant, ArtifactID: "team-x", Version: "1.0.0",
		ContentHash: "sha256:a", Type: "context", Layer: "team",
	})
	reg := core.New(st, tenant, []layer.Layer{
		{ID: "team", Precedence: 1, Visibility: layer.Visibility{Groups: []string{"engineering"}}},
	})

	jwt := layer.Identity{Sub: "alice", IsAuthenticated: true, Groups: []string{"engineering"}}
	res, err := reg.SearchArtifacts(context.Background(), jwt, core.SearchArtifactsOptions{})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if len(res.Results) != 1 {
		t.Errorf("results = %+v, want 1", res.Results)
	}
}
