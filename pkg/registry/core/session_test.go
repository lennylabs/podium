package core_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
	"github.com/lennylabs/podium/pkg/version"
)

func newRegistryWithStore(t *testing.T) (*core.Registry, store.Store) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	reg := core.New(st, "t", []layer.Layer{
		{ID: "L", Visibility: layer.Visibility{Public: true}, Precedence: 1},
	})
	return reg, st
}

func putVersion(t *testing.T, st store.Store, id, version string) {
	t.Helper()
	if err := st.PutManifest(context.Background(), storetest.Seal(t, store.ManifestRecord{
		TenantID: "t", ArtifactID: id, Version: version,
		ContentHash: "sha256:" + version, Type: "context", Layer: "L",
	}, nil, nil)); err != nil {
		t.Fatalf("PutManifest %s@%s: %v", id, version, err)
	}
}

// Spec: §4.7.6 — within a session, the first latest lookup pins; later
// same-id lookups in the same session see the same version even if
// newer versions land in between.
func TestLoadArtifact_SessionConsistentLatest(t *testing.T) {
	t.Parallel()
	reg, st := newRegistryWithStore(t)
	putVersion(t, st, "x", "1.0.0")

	// First call within session pins version 1.0.0.
	got, err := reg.LoadArtifact(context.Background(), publicID, "x", core.LoadArtifactOptions{
		SessionID: "session-A",
	})
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if got.Version != "1.0.0" {
		t.Errorf("first load Version = %q, want 1.0.0", got.Version)
	}

	// A newer version is ingested between calls.
	putVersion(t, st, "x", "2.0.0")

	// Second call in the same session still resolves to 1.0.0.
	got, err = reg.LoadArtifact(context.Background(), publicID, "x", core.LoadArtifactOptions{
		SessionID: "session-A",
	})
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if got.Version != "1.0.0" {
		t.Errorf("second load Version = %q, want 1.0.0 (session-pinned)", got.Version)
	}

	// A different session sees the newer 2.0.0.
	got, err = reg.LoadArtifact(context.Background(), publicID, "x", core.LoadArtifactOptions{
		SessionID: "session-B",
	})
	if err != nil {
		t.Fatalf("session-B load: %v", err)
	}
	if got.Version != "2.0.0" {
		t.Errorf("session-B Version = %q, want 2.0.0", got.Version)
	}

	// No session ID at all also sees the newer 2.0.0 (no pinning).
	got, err = reg.LoadArtifact(context.Background(), publicID, "x", core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("no-session load: %v", err)
	}
	if got.Version != "2.0.0" {
		t.Errorf("no-session Version = %q, want 2.0.0", got.Version)
	}
}

// Spec: §4.7.6 — explicit pins (1.x, 1.2.x, 1.2.3, sha256:...) are not
// affected by session consistency; they always resolve as specified.
func TestLoadArtifact_ExplicitPinIgnoresSession(t *testing.T) {
	t.Parallel()
	reg, st := newRegistryWithStore(t)
	putVersion(t, st, "x", "1.0.0")
	putVersion(t, st, "x", "2.0.0")

	got, err := reg.LoadArtifact(context.Background(), publicID, "x", core.LoadArtifactOptions{
		Version:   "1.0.0",
		SessionID: "session-A",
	})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if got.Version != "1.0.0" {
		t.Errorf("Version = %q, want 1.0.0 (explicit pin)", got.Version)
	}

	got, err = reg.LoadArtifact(context.Background(), publicID, "x", core.LoadArtifactOptions{
		Version:   "2.0.0",
		SessionID: "session-A",
	})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if got.Version != "2.0.0" {
		t.Errorf("Version = %q, want 2.0.0 (explicit pin)", got.Version)
	}
}

// Spec: §4.7.10, §4.7.6 — a session-pinned latest serves the pinned version's
// own ingest time after a newer version lands, while a new session serves the
// newer version's time.
func TestLoadArtifact_SessionPinnedLatestServesPinnedRevision(t *testing.T) {
	t.Parallel()
	reg, st := newRegistryWithStore(t)
	putAt(t, st, "x", "1.0.0", 1, false)
	pinned := core.LoadArtifactOptions{SessionID: "session-A"}
	want := version.FormatArtifactRevision(revAt(1))
	if ver, rev := loadRevision(t, reg, "x", pinned); ver != "1.0.0" || rev != want {
		t.Fatalf("first load = %s at %q, want 1.0.0 at %q", ver, rev, want)
	}
	putAt(t, st, "x", "2.0.0", 2, false)
	if ver, rev := loadRevision(t, reg, "x", pinned); ver != "1.0.0" || rev != want {
		t.Errorf("pinned load = %s at %q, want 1.0.0 at %q", ver, rev, want)
	}
	newer := version.FormatArtifactRevision(revAt(2))
	if ver, rev := loadRevision(t, reg, "x", core.LoadArtifactOptions{SessionID: "session-B"}); ver != "2.0.0" || rev != newer {
		t.Errorf("new session load = %s at %q, want 2.0.0 at %q", ver, rev, newer)
	}
}
