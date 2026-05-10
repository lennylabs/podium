package core_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7.6 — `latest` resolves to the most recently
// ingested *non-deprecated* version. A higher-semver but
// deprecated version must not win latest resolution; the prior
// non-deprecated version is what callers see.
func TestLoadArtifact_LatestSkipsDeprecatedVersion(t *testing.T) {
	t.Parallel()
	const tenant = "t"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenant, ArtifactID: "team/x", Version: "1.0.0",
		ContentHash: "sha256:a", Type: "skill", Layer: "L",
	}); err != nil {
		t.Fatalf("Put 1: %v", err)
	}
	if err := st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenant, ArtifactID: "team/x", Version: "2.0.0",
		ContentHash: "sha256:b", Type: "skill", Layer: "L",
		Deprecated: true,
	}); err != nil {
		t.Fatalf("Put 2: %v", err)
	}
	reg := core.New(st, tenant, []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	got, err := reg.LoadArtifact(context.Background(), layer.Identity{IsPublic: true},
		"team/x", core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if got.Version != "1.0.0" {
		t.Errorf("Version = %q, want 1.0.0 (latest skips the 2.0.0 deprecated version)", got.Version)
	}
}

// Spec: §4.7.6 — when every version is deprecated, latest
// resolution falls back to the most recent (deprecated) version
// rather than failing with not_found. Callers see the
// deprecation warning but still get bytes.
func TestLoadArtifact_LatestFallsBackWhenAllDeprecated(t *testing.T) {
	t.Parallel()
	const tenant = "t"
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: tenant})
	for _, v := range []string{"1.0.0", "2.0.0"} {
		_ = st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: tenant, ArtifactID: "team/x", Version: v,
			ContentHash: "sha256:" + v, Type: "skill", Layer: "L",
			Deprecated: true,
		})
	}
	reg := core.New(st, tenant, []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	got, err := reg.LoadArtifact(context.Background(), layer.Identity{IsPublic: true},
		"team/x", core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if got.Version != "2.0.0" {
		t.Errorf("Version = %q, want 2.0.0 (no non-deprecated version exists)", got.Version)
	}
	if !got.Deprecated {
		t.Errorf("Deprecated = false, want true")
	}
}

// Spec: §4.7.6 — when the caller supplies an exact pin, the
// deprecation filter does not apply (callers can opt to load
// historical/deprecated versions explicitly).
func TestLoadArtifact_ExactVersionLoadsDeprecated(t *testing.T) {
	t.Parallel()
	const tenant = "t"
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: tenant})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenant, ArtifactID: "team/x", Version: "2.0.0",
		ContentHash: "sha256:b", Type: "skill", Layer: "L",
		Deprecated: true,
	})
	reg := core.New(st, tenant, []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	got, err := reg.LoadArtifact(context.Background(), layer.Identity{IsPublic: true},
		"team/x", core.LoadArtifactOptions{Version: "2.0.0"})
	if err != nil {
		t.Fatalf("LoadArtifact(2.0.0): %v", err)
	}
	if got.Version != "2.0.0" || !got.Deprecated {
		t.Errorf("got %+v, want deprecated v2.0.0", got)
	}
}
