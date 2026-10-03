package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
	"github.com/lennylabs/podium/pkg/version"
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
	if err := st.PutManifest(context.Background(), storetest.Seal(t, store.ManifestRecord{
		TenantID: tenant, ArtifactID: "team/x", Version: "1.0.0",
		ContentHash: "sha256:a", Type: "skill", Layer: "L",
	}, nil, nil)); err != nil {
		t.Fatalf("Put 1: %v", err)
	}
	if err := st.PutManifest(context.Background(), storetest.Seal(t, store.ManifestRecord{
		TenantID: tenant, ArtifactID: "team/x", Version: "2.0.0",
		ContentHash: "sha256:b", Type: "skill", Layer: "L",
		Deprecated: true,
	}, nil, nil)); err != nil {
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
		_ = st.PutManifest(context.Background(), storetest.Seal(t, store.ManifestRecord{
			TenantID: tenant, ArtifactID: "team/x", Version: v,
			ContentHash: "sha256:" + v, Type: "skill", Layer: "L",
			Deprecated: true,
		}, nil, nil))
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
	_ = st.PutManifest(context.Background(), storetest.Seal(t, store.ManifestRecord{
		TenantID: tenant, ArtifactID: "team/x", Version: "2.0.0",
		ContentHash: "sha256:b", Type: "skill", Layer: "L",
		Deprecated: true,
	}, nil, nil))
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

// revBase anchors the ingest times of the revision fixtures. Each fixture
// version is stored at revBase plus its own offset, so versions carry strictly
// increasing ingest times and a served revision names exactly one version. A
// record written through PutManifest without an IngestedAt serves the epoch,
// which would make every version's revision equal.
var revBase = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

// revAt is the ingest time of the fixture version stored at offset minutes.
func revAt(offset int) time.Time { return revBase.Add(time.Duration(offset) * time.Minute) }

// putAt stores id@ver in layer L of tenant t with the ingest time revAt(offset).
func putAt(t *testing.T, st store.Store, id, ver string, offset int, deprecated bool) {
	t.Helper()
	if err := st.PutManifest(context.Background(), storetest.Seal(t, store.ManifestRecord{
		TenantID: "t", ArtifactID: id, Version: ver,
		ContentHash: "sha256:" + ver, Type: "context", Layer: "L",
		Deprecated: deprecated, IngestedAt: revAt(offset),
	}, nil, nil)); err != nil {
		t.Fatalf("PutManifest %s@%s: %v", id, ver, err)
	}
}

// loadRevision loads id with opts as a public caller and returns the served
// version and artifact revision.
func loadRevision(t *testing.T, reg *core.Registry, id string, opts core.LoadArtifactOptions) (string, string) {
	t.Helper()
	got, err := reg.LoadArtifact(context.Background(), publicID, id, opts)
	if err != nil {
		t.Fatalf("LoadArtifact(%s, %+v): %v", id, opts, err)
	}
	return got.Version, got.ArtifactRevision
}

// Spec: §4.7.10, §4.7.6 — the served artifact revision is the resolved
// record's own ingest time, and it moves only when latest resolves to a
// different stored version. A version born deprecated leaves both unchanged,
// a backport at a lower semver raises it, and an exact pin serves the pinned
// version's own time.
func TestLoadArtifact_LatestRevisionFollowsTheResolvedVersion(t *testing.T) {
	t.Parallel()
	reg, st := newRegistryWithStore(t)
	steps := []struct {
		name          string
		ver           string
		offset        int
		deprecated    bool
		wantVer       string
		wantRevOffset int
	}{
		{"first ingest", "1.0.0", 1, false, "1.0.0", 1},
		{"newer ingest", "2.0.0", 2, false, "2.0.0", 2},
		{"born deprecated", "3.0.0", 3, true, "2.0.0", 2},
		{"backport at lower semver", "1.5.0", 4, false, "1.5.0", 4},
	}
	for _, s := range steps {
		putAt(t, st, "x", s.ver, s.offset, s.deprecated)
		ver, rev := loadRevision(t, reg, "x", core.LoadArtifactOptions{})
		if want := version.FormatArtifactRevision(revAt(s.wantRevOffset)); ver != s.wantVer || rev != want {
			t.Errorf("%s: latest = %s at %q, want %s at %q", s.name, ver, rev, s.wantVer, want)
		}
	}
	ver, rev := loadRevision(t, reg, "x", core.LoadArtifactOptions{Version: "1.0.0"})
	if want := version.FormatArtifactRevision(revAt(1)); ver != "1.0.0" || rev != want {
		t.Errorf("exact 1.0.0 = %s at %q, want 1.0.0 at %q", ver, rev, want)
	}
}

// Spec: §4.7.10, §4.7.6 — when every version is deprecated, latest falls back
// to the newest-ingested version and serves that version's ingest time.
func TestLoadArtifact_AllDeprecatedFallbackServesNewestRevision(t *testing.T) {
	t.Parallel()
	reg, st := newRegistryWithStore(t)
	putAt(t, st, "x", "2.0.0", 1, true)
	putAt(t, st, "x", "1.0.0", 2, true)
	ver, rev := loadRevision(t, reg, "x", core.LoadArtifactOptions{})
	if want := version.FormatArtifactRevision(revAt(2)); ver != "1.0.0" || rev != want {
		t.Errorf("all-deprecated latest = %s at %q, want 1.0.0 at %q", ver, rev, want)
	}
}

// Spec: §4.7.10, §4.7.6, §8.4 — purging a deprecated version while a
// non-deprecated one exists leaves the latest version and its revision
// unchanged, because latest never resolved to the purged version.
func TestLoadArtifact_PurgeOfDeprecatedKeepsLatestRevision(t *testing.T) {
	t.Parallel()
	reg, st := newRegistryWithStore(t)
	putAt(t, st, "x", "1.0.0", 1, false)
	putAt(t, st, "x", "2.0.0", 2, true)
	want := version.FormatArtifactRevision(revAt(1))
	if ver, rev := loadRevision(t, reg, "x", core.LoadArtifactOptions{}); ver != "1.0.0" || rev != want {
		t.Fatalf("before purge latest = %s at %q, want 1.0.0 at %q", ver, rev, want)
	}
	n, err := st.PurgeDeprecatedManifests(context.Background(), revAt(3))
	if err != nil || n != 1 {
		t.Fatalf("PurgeDeprecatedManifests = %d, %v; want 1 purged", n, err)
	}
	if ver, rev := loadRevision(t, reg, "x", core.LoadArtifactOptions{}); ver != "1.0.0" || rev != want {
		t.Errorf("after purge latest = %s at %q, want 1.0.0 at %q", ver, rev, want)
	}
}
