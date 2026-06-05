package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// adminOverrideFixture seeds a tenant with one artifact in a layer visible
// only to "bob", grants "alice" the admin role, and returns a registry with
// an audit recorder attached. alice cannot see team/secret without the
// §4.7.2 diagnostic override.
func adminOverrideFixture(t *testing.T) (*core.Registry, *recorder) {
	t.Helper()
	const tenantID = "t"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenantID}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenantID, ArtifactID: "team/secret", Version: "1.0.0",
		ContentHash: "sha256:a", Type: "context", Layer: "private",
		Description: "secret context", Body: []byte("hidden body"),
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	if err := st.GrantAdmin(context.Background(), store.AdminGrant{
		UserID: "alice", OrgID: tenantID,
	}); err != nil {
		t.Fatalf("GrantAdmin: %v", err)
	}
	rec := &recorder{}
	reg := core.New(st, tenantID, []layer.Layer{
		{ID: "private", Precedence: 1, Visibility: layer.Visibility{Users: []string{"bob"}}},
	}).WithAudit(rec.emit)
	return reg, rec
}

func hasOverrideEvent(events []core.AuditEvent, target string) bool {
	for _, e := range events {
		if e.Type == "admin.visibility_override" && e.Target == target {
			return true
		}
	}
	return false
}

// Spec: §4.7.2 — an admin reading with the diagnostic override sees an
// artifact in a layer their own identity cannot otherwise see, and the
// override is itself audited (admin.visibility_override).
func TestLoadArtifact_AdminOverrideSeesInvisibleLayer(t *testing.T) {
	t.Parallel()
	reg, rec := adminOverrideFixture(t)
	alice := layer.Identity{Sub: "alice", IsAuthenticated: true}

	res, err := reg.LoadArtifact(context.Background(), alice, "team/secret", core.LoadArtifactOptions{AsAdmin: true})
	if err != nil {
		t.Fatalf("LoadArtifact with override: %v", err)
	}
	if res.ID != "team/secret" {
		t.Errorf("loaded %q, want team/secret", res.ID)
	}
	if !hasOverrideEvent(rec.snapshot(), "team/secret") {
		t.Errorf("missing admin.visibility_override audit event: %+v", rec.snapshot())
	}
}

// Spec: §4.7.2 — the override is opt-in. Without AsAdmin, even an admin
// identity sees only its own effective view, so the invisible-layer
// artifact is not_found and no override event fires.
func TestLoadArtifact_AdminWithoutOverrideStillFiltered(t *testing.T) {
	t.Parallel()
	reg, rec := adminOverrideFixture(t)
	alice := layer.Identity{Sub: "alice", IsAuthenticated: true}

	_, err := reg.LoadArtifact(context.Background(), alice, "team/secret", core.LoadArtifactOptions{})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("LoadArtifact without override: got %v, want ErrNotFound", err)
	}
	if hasOverrideEvent(rec.snapshot(), "team/secret") {
		t.Errorf("override event fired without AsAdmin: %+v", rec.snapshot())
	}
}

// Spec: §4.7.2 — the bypass requires the admin role. A non-admin caller
// that sets AsAdmin is rejected with ErrForbidden and never reaches the
// hidden artifact.
func TestLoadArtifact_NonAdminOverrideForbidden(t *testing.T) {
	t.Parallel()
	reg, rec := adminOverrideFixture(t)
	carol := layer.Identity{Sub: "carol", IsAuthenticated: true}

	_, err := reg.LoadArtifact(context.Background(), carol, "team/secret", core.LoadArtifactOptions{AsAdmin: true})
	if !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("non-admin override: got %v, want ErrForbidden", err)
	}
	if hasOverrideEvent(rec.snapshot(), "team/secret") {
		t.Errorf("override event fired for a non-admin caller: %+v", rec.snapshot())
	}
}

// Spec: §4.7.2 — a public-mode (anonymous) caller can never hold the admin
// role, so an override request is rejected even though public mode
// otherwise bypasses layer visibility.
func TestLoadArtifact_PublicModeOverrideForbidden(t *testing.T) {
	t.Parallel()
	reg, _ := adminOverrideFixture(t)

	_, err := reg.LoadArtifact(context.Background(), layer.Identity{IsPublic: true}, "team/secret", core.LoadArtifactOptions{AsAdmin: true})
	if !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("public-mode override: got %v, want ErrForbidden", err)
	}
}

// Spec: §4.7.2 — an admin search with the override enumerates artifacts in
// layers the caller cannot otherwise see; the override is audited.
func TestSearchArtifacts_AdminOverrideSeesInvisibleLayer(t *testing.T) {
	t.Parallel()
	reg, rec := adminOverrideFixture(t)
	alice := layer.Identity{Sub: "alice", IsAuthenticated: true}

	res, err := reg.SearchArtifacts(context.Background(), alice, core.SearchArtifactsOptions{AsAdmin: true})
	if err != nil {
		t.Fatalf("SearchArtifacts with override: %v", err)
	}
	found := false
	for _, a := range res.Results {
		if a.ID == "team/secret" {
			found = true
		}
	}
	if !found {
		t.Errorf("override search missing team/secret: %+v", res.Results)
	}
	if !hasOverrideEvent(rec.snapshot(), "") {
		t.Errorf("missing admin.visibility_override audit event for search: %+v", rec.snapshot())
	}
}

// Spec: §4.7.2 — without the override, the same admin's search excludes the
// invisible-layer artifact.
func TestSearchArtifacts_AdminWithoutOverrideFiltered(t *testing.T) {
	t.Parallel()
	reg, _ := adminOverrideFixture(t)
	alice := layer.Identity{Sub: "alice", IsAuthenticated: true}

	res, err := reg.SearchArtifacts(context.Background(), alice, core.SearchArtifactsOptions{})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	for _, a := range res.Results {
		if a.ID == "team/secret" {
			t.Errorf("search without override leaked team/secret: %+v", res.Results)
		}
	}
}

// Spec: §4.7.2 — a non-admin search override is rejected with ErrForbidden.
func TestSearchArtifacts_NonAdminOverrideForbidden(t *testing.T) {
	t.Parallel()
	reg, _ := adminOverrideFixture(t)
	carol := layer.Identity{Sub: "carol", IsAuthenticated: true}

	_, err := reg.SearchArtifacts(context.Background(), carol, core.SearchArtifactsOptions{AsAdmin: true})
	if !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("non-admin search override: got %v, want ErrForbidden", err)
	}
}
