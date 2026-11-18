package core_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// deprecationRegistry stores team/x at 1.0.0 and 2.0.0, applying each
// overrides entry to the version it names, and returns a registry over one
// public layer.
func deprecationRegistry(t *testing.T, overrides map[string]func(*store.ManifestRecord)) *core.Registry {
	t.Helper()
	reg, _ := deprecationFixture(t, overrides)
	return reg
}

// deprecationFixture is deprecationRegistry that also returns the store.
func deprecationFixture(t *testing.T, overrides map[string]func(*store.ManifestRecord)) (*core.Registry, store.Store) {
	t.Helper()
	const tenant = "t"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenant}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"1.0.0", "2.0.0"} {
		rec := store.ManifestRecord{
			TenantID: tenant, ArtifactID: "team/x", Version: v,
			ContentHash: "sha256:" + v, Type: "skill", Layer: "L",
		}
		if f := overrides[v]; f != nil {
			f(&rec)
		}
		if err := st.PutManifest(context.Background(), storetest.Seal(t, rec, nil, nil)); err != nil {
			t.Fatalf("Put %s: %v", v, err)
		}
	}
	return core.New(st, tenant, []layer.Layer{{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}}}), st
}

func loadX(t *testing.T, reg *core.Registry, opts core.LoadArtifactOptions) *core.LoadArtifactResult {
	t.Helper()
	got, err := reg.LoadArtifact(context.Background(), layer.Identity{IsPublic: true}, "team/x", opts)
	if err != nil {
		t.Fatalf("LoadArtifact(%+v): %v", opts, err)
	}
	return got
}

func deprecated(replacedBy string) func(*store.ManifestRecord) {
	return func(r *store.ManifestRecord) {
		r.Deprecated = true
		r.ReplacedBy = replacedBy
	}
}

// Spec: §4.7.4, §4.7.6 — a latest load whose newest visible version is
// deprecated serves the newest non-deprecated version and returns a warning
// naming the deprecated version and its replaced_by, and the upgrade target
// when the served version names none. The served version is not deprecated,
// and a session-pinned latest lookup warns on the same terms.
func TestLoadArtifact_LatestWarnsWhenNewestIsDeprecated(t *testing.T) {
	t.Parallel()
	reg := deprecationRegistry(t, map[string]func(*store.ManifestRecord){"2.0.0": deprecated("team/y")})
	const want = "version 2.0.0 of team/x is deprecated; replaced_by team/y; this load serves version 1.0.0"
	for _, opts := range []core.LoadArtifactOptions{{}, {SessionID: "s1"}, {SessionID: "s1"}} {
		got := loadX(t, reg, opts)
		if got.Version != "1.0.0" || got.Deprecated || got.DeprecationWarning != want || got.ReplacedBy != "team/y" {
			t.Errorf("latest %+v: version=%s deprecated=%v warning=%q replaced_by=%q; want 1.0.0, false, %q, team/y",
				opts, got.Version, got.Deprecated, got.DeprecationWarning, got.ReplacedBy, want)
		}
	}
}

// Spec: §4.7.4 — the served version's own replaced_by wins over the
// deprecated newest version's, and a deprecated newest version without one
// warns without an upgrade target.
func TestLoadArtifact_LatestWarningKeepsTheServedUpgradeTarget(t *testing.T) {
	t.Parallel()
	reg := deprecationRegistry(t, map[string]func(*store.ManifestRecord){
		"1.0.0": func(r *store.ManifestRecord) { r.ReplacedBy = "team/z" },
		"2.0.0": deprecated(""),
	})
	got := loadX(t, reg, core.LoadArtifactOptions{})
	if got.ReplacedBy != "team/z" || got.DeprecationWarning != "version 2.0.0 of team/x is deprecated; this load serves version 1.0.0" {
		t.Errorf("replaced_by=%q warning=%q", got.ReplacedBy, got.DeprecationWarning)
	}
}

// Spec: §4.7.4 — a load by an exact version or a range warns only when the
// version it serves is deprecated, and a latest load whose newest version is
// live warns not at all.
func TestLoadArtifact_PinnedAndLiveLoadsCarryNoNewerWarning(t *testing.T) {
	t.Parallel()
	reg := deprecationRegistry(t, map[string]func(*store.ManifestRecord){"2.0.0": deprecated("team/y")})
	for _, v := range []string{"1.0.0", "1.x"} {
		if got := loadX(t, reg, core.LoadArtifactOptions{Version: v}); got.DeprecationWarning != "" {
			t.Errorf("pin %s: warning %q, want none", v, got.DeprecationWarning)
		}
	}
	if got := loadX(t, reg, core.LoadArtifactOptions{Version: "2.0.0"}); !got.Deprecated || got.DeprecationWarning != "artifact is deprecated; replaced_by team/y" {
		t.Errorf("pin 2.0.0: deprecated=%v warning=%q, want the served version's own warning", got.Deprecated, got.DeprecationWarning)
	}

	live := deprecationRegistry(t, map[string]func(*store.ManifestRecord){"1.0.0": deprecated("")})
	if got := loadX(t, live, core.LoadArtifactOptions{}); got.Version != "2.0.0" || got.DeprecationWarning != "" {
		t.Errorf("newest live: version=%s warning=%q, want 2.0.0 and none", got.Version, got.DeprecationWarning)
	}
}

// Spec: §4.7.4, §13.4 — the upgrade target of the deprecated newest version is
// read from its admitted bytes, so a row whose bytes no longer match its
// content hash warns with no upgrade target rather than one an edited row
// names.
func TestLoadArtifact_NewerWarningOmitsTheTargetOfAnUnadmittedRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatal(err)
	}
	live := storetest.Seal(t, store.ManifestRecord{TenantID: "t", ArtifactID: "team/x", Version: "1.0.0", Type: "skill", Layer: "L"}, nil, nil)
	newer := storetest.Seal(t, store.ManifestRecord{
		TenantID: "t", ArtifactID: "team/x", Version: "2.0.0", Type: "skill", Layer: "L", Deprecated: true,
		Frontmatter: []byte("---\ntype: skill\nversion: 2.0.0\ndeprecated: true\nreplaced_by: team/y\n---\n"),
	}, nil, nil)
	// The stored bytes now differ from the bytes the content hash covers.
	newer.Frontmatter = []byte("---\ntype: skill\nversion: 2.0.0\ndeprecated: true\nreplaced_by: team/evil\n---\n")
	for _, rec := range []store.ManifestRecord{live, newer} {
		if err := st.PutManifest(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	reg := core.New(st, "t", []layer.Layer{{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}}})
	got := loadX(t, reg, core.LoadArtifactOptions{})
	if got.ReplacedBy != "" || got.DeprecationWarning != "version 2.0.0 of team/x is deprecated; this load serves version 1.0.0" {
		t.Errorf("replaced_by=%q warning=%q, want no upgrade target from the unadmitted row", got.ReplacedBy, got.DeprecationWarning)
	}
}

// Spec: §7.2 (Deprecation members) — the revalidation result the registry's
// 304 decision reads carries the warning, for the newest-deprecated case and
// for a served version that is itself deprecated.
func TestLoadArtifact_RevalidationResultCarriesTheWarning(t *testing.T) {
	t.Parallel()
	reg := deprecationRegistry(t, map[string]func(*store.ManifestRecord){"2.0.0": deprecated("team/y")})
	var seen []string
	revalidate := func(res *core.LoadArtifactResult) bool {
		seen = append(seen, res.DeprecationWarning)
		return true
	}
	loadX(t, reg, core.LoadArtifactOptions{Revalidate: revalidate})
	loadX(t, reg, core.LoadArtifactOptions{Version: "2.0.0", Revalidate: revalidate})
	if len(seen) != 2 || seen[0] == "" || seen[1] != "artifact is deprecated; replaced_by team/y" {
		t.Errorf("revalidation warnings = %q, want the newer-version warning and the served version's own", seen)
	}
}
