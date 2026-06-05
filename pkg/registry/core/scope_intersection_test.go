package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// scopeRegistry seeds a public layer with finance (two versions) and hr
// artifacts so a caller's OAuth scope is the only thing that narrows the
// surface (§6.3.1). It returns the registry and an audit recorder.
func scopeRegistry(t *testing.T) (*core.Registry, *recorder) {
	t.Helper()
	const tenantID = "t"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenantID}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	put := func(id, ver string) {
		if err := st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: tenantID, ArtifactID: id, Version: ver,
			ContentHash: "sha256:" + id + ver, Type: "context",
			Description: id, Layer: "shared",
		}); err != nil {
			t.Fatalf("PutManifest(%s@%s): %v", id, ver, err)
		}
	}
	put("finance/ap/pay-invoice", "1.0.0")
	put("finance/ap/pay-invoice", "2.0.0")
	put("hr/policies", "1.0.0")
	rec := &recorder{}
	reg := core.New(st, tenantID, []layer.Layer{
		{ID: "shared", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	}).WithAudit(rec.emit)
	return reg, rec
}

// Spec: §6.3.1 — a "podium:read:finance/*" scope narrows the discovery
// surface to the finance subtree; hr is excluded even though the layer is
// visible. The smaller surface wins.
func TestSearchArtifacts_ReadScopeNarrows(t *testing.T) {
	t.Parallel()
	reg, _ := scopeRegistry(t)
	id := layer.Identity{Sub: "alice", IsAuthenticated: true, Scopes: []string{"podium:read:finance/*"}}
	res, err := reg.SearchArtifacts(context.Background(), id, core.SearchArtifactsOptions{})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	var sawFinance, sawHR bool
	for _, r := range res.Results {
		switch r.ID {
		case "finance/ap/pay-invoice":
			sawFinance = true
		case "hr/policies":
			sawHR = true
		}
	}
	if !sawFinance {
		t.Errorf("finance artifact missing under read:finance/* scope: %+v", res.Results)
	}
	if sawHR {
		t.Errorf("hr artifact leaked past read:finance/* scope: %+v", res.Results)
	}
}

// Spec: §6.3.1 — a read scope does not authorize a load; loading a
// finance artifact with only "podium:read:finance/*" is denied and emits
// visibility.denied (no leak).
func TestLoadArtifact_ReadScopeDoesNotAuthorizeLoad(t *testing.T) {
	t.Parallel()
	reg, rec := scopeRegistry(t)
	id := layer.Identity{Sub: "alice", IsAuthenticated: true, Scopes: []string{"podium:read:finance/*"}}
	_, err := reg.LoadArtifact(context.Background(), id, "finance/ap/pay-invoice", core.LoadArtifactOptions{})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("LoadArtifact: got %v, want ErrNotFound", err)
	}
	denied := false
	for _, e := range rec.snapshot() {
		if e.Type == "visibility.denied" && e.Target == "finance/ap/pay-invoice" {
			denied = true
		}
	}
	if !denied {
		t.Errorf("expected visibility.denied for an out-of-scope load")
	}
}

// Spec: §6.3.1 — a "podium:load:finance/*" scope authorizes loading any
// finance artifact, and the load implies read visibility.
func TestLoadArtifact_LoadScopeAllowsSubtree(t *testing.T) {
	t.Parallel()
	reg, _ := scopeRegistry(t)
	id := layer.Identity{Sub: "alice", IsAuthenticated: true, Scopes: []string{"podium:load:finance/*"}}
	res, err := reg.LoadArtifact(context.Background(), id, "finance/ap/pay-invoice", core.LoadArtifactOptions{Version: "1.0.0"})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if res == nil {
		t.Fatalf("LoadArtifact returned nil result")
	}
	// An artifact outside the granted subtree is denied.
	if _, err := reg.LoadArtifact(context.Background(), id, "hr/policies", core.LoadArtifactOptions{}); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("hr load: got %v, want ErrNotFound", err)
	}
}

// Spec: §6.3.1 — a version-pinned load scope ("@1.x") permits matching
// versions and denies non-matching ones; the smaller surface wins.
func TestLoadArtifact_LoadScopeVersionPin(t *testing.T) {
	t.Parallel()
	reg, _ := scopeRegistry(t)
	id := layer.Identity{Sub: "alice", IsAuthenticated: true, Scopes: []string{"podium:load:finance/ap/pay-invoice@1.x"}}
	if _, err := reg.LoadArtifact(context.Background(), id, "finance/ap/pay-invoice", core.LoadArtifactOptions{Version: "1.0.0"}); err != nil {
		t.Errorf("load 1.0.0 under @1.x: %v", err)
	}
	if _, err := reg.LoadArtifact(context.Background(), id, "finance/ap/pay-invoice", core.LoadArtifactOptions{Version: "2.0.0"}); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("load 2.0.0 under @1.x: got %v, want ErrNotFound", err)
	}
}

// Spec: §6.3.1 — a caller with no "podium:*" scope keeps full layer
// visibility (no narrowing).
func TestLoadArtifact_NoScopeFullAccess(t *testing.T) {
	t.Parallel()
	reg, _ := scopeRegistry(t)
	id := layer.Identity{Sub: "alice", IsAuthenticated: true}
	for _, target := range []string{"finance/ap/pay-invoice", "hr/policies"} {
		if _, err := reg.LoadArtifact(context.Background(), id, target, core.LoadArtifactOptions{}); err != nil {
			t.Errorf("load %s with no scope: %v", target, err)
		}
	}
}
