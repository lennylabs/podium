package core_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// searchFixture is one stored version of artifact x: its version, its ingest
// time as a revAt offset, and the per-version fields search filters on.
type searchFixture struct {
	ver        string
	offset     int
	deprecated bool
	directOnly bool
	tags       []string
}

// putSearchFixtures stores each fixture as x@ver in layer L of tenant t.
func putSearchFixtures(t *testing.T, st store.Store, fixtures []searchFixture) {
	t.Helper()
	for _, f := range fixtures {
		rec := store.ManifestRecord{
			TenantID: "t", ArtifactID: "x", Version: f.ver,
			ContentHash: "sha256:" + f.ver, Type: "context", Layer: "L",
			Description: "release checklist", Tags: f.tags,
			Deprecated: f.deprecated, IngestedAt: revAt(f.offset),
		}
		if f.directOnly {
			rec.SearchVisibility = string(manifest.SearchVisibilityDirectOnly)
		}
		if err := st.PutManifest(context.Background(), storetest.Seal(t, rec, nil, nil)); err != nil {
			t.Fatalf("PutManifest x@%s: %v", f.ver, err)
		}
	}
}

// searchVersionOf returns the version search reports for x, or "" when x is
// absent from the results.
func searchVersionOf(t *testing.T, reg *core.Registry, opts core.SearchArtifactsOptions) string {
	t.Helper()
	res, err := reg.SearchArtifacts(context.Background(), publicID, opts)
	if err != nil {
		t.Fatalf("SearchArtifacts(%+v): %v", opts, err)
	}
	for _, r := range res.Results {
		if r.ID == "x" {
			return r.Version
		}
	}
	return ""
}

// Spec: §4.7.6 / §4.3 / §4.5.3 / §4.7.4 — search ranks each artifact at the
// version `latest` resolves to, the version load_artifact serves for the same
// ID, and applies the deprecation, search_visibility, and tag filters to that
// version. An older version never stands in for an artifact whose latest
// version a filter excludes, and a backport ingested after a newer line is
// the version search reports.
func TestSearchArtifacts_ReportsTheVersionLoadServes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		fixtures []searchFixture
		opts     core.SearchArtifactsOptions
		// want is the version search reports for x; "" means x is absent.
		want string
		// wantLoad is the version load_artifact serves for x at latest.
		wantLoad string
	}{
		{
			name: "direct-only latest hides its indexed history",
			fixtures: []searchFixture{
				{ver: "1.0.0", offset: 1},
				{ver: "1.1.0", offset: 2, directOnly: true},
			},
			want: "", wantLoad: "1.1.0",
		},
		{
			name: "direct-only latest hides its history under a query",
			fixtures: []searchFixture{
				{ver: "1.0.0", offset: 1},
				{ver: "1.1.0", offset: 2, directOnly: true},
			},
			opts: core.SearchArtifactsOptions{Query: "release"},
			want: "", wantLoad: "1.1.0",
		},
		{
			name: "indexed latest after a direct-only version is listed",
			fixtures: []searchFixture{
				{ver: "1.0.0", offset: 1, directOnly: true},
				{ver: "1.1.0", offset: 2},
			},
			want: "1.1.0", wantLoad: "1.1.0",
		},
		{
			name: "backport at a lower semver is the listed version",
			fixtures: []searchFixture{
				{ver: "1.0.0", offset: 1},
				{ver: "2.0.0", offset: 2},
				{ver: "1.5.0", offset: 3},
			},
			want: "1.5.0", wantLoad: "1.5.0",
		},
		{
			name: "deprecated newest leaves the live version listed",
			fixtures: []searchFixture{
				{ver: "1.0.0", offset: 1},
				{ver: "2.0.0", offset: 2, deprecated: true},
			},
			want: "1.0.0", wantLoad: "1.0.0",
		},
		{
			name: "all deprecated is excluded by default",
			fixtures: []searchFixture{
				{ver: "2.0.0", offset: 1, deprecated: true},
				{ver: "1.0.0", offset: 2, deprecated: true},
			},
			want: "", wantLoad: "1.0.0",
		},
		{
			name: "all deprecated lists the newest ingest when opted in",
			fixtures: []searchFixture{
				{ver: "2.0.0", offset: 1, deprecated: true},
				{ver: "1.0.0", offset: 2, deprecated: true},
			},
			opts: core.SearchArtifactsOptions{IncludeDeprecated: true},
			want: "1.0.0", wantLoad: "1.0.0",
		},
		{
			name: "a tag the latest version dropped does not match",
			fixtures: []searchFixture{
				{ver: "1.0.0", offset: 1, tags: []string{"beta"}},
				{ver: "1.1.0", offset: 2},
			},
			opts: core.SearchArtifactsOptions{Tags: []string{"beta"}},
			want: "", wantLoad: "1.1.0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg, st := newRegistryWithStore(t)
			putSearchFixtures(t, st, tc.fixtures)
			if got := searchVersionOf(t, reg, tc.opts); got != tc.want {
				t.Errorf("search reports x at %q, want %q", got, tc.want)
			}
			got, err := reg.LoadArtifact(context.Background(), publicID, "x", core.LoadArtifactOptions{})
			if err != nil {
				t.Fatalf("LoadArtifact(x): %v", err)
			}
			if got.Version != tc.wantLoad {
				t.Errorf("load serves x at %q, want %q", got.Version, tc.wantLoad)
			}
		})
	}
}

// Spec: §4.7.6 / §7.5 — the effective view `podium sync` walks lists each
// artifact at the version `latest` resolves to, so a deprecated newer version
// or a lower-semver backport ingested later lists the version a load serves.
func TestEffectiveView_ListsTheVersionLoadServes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		fixtures []searchFixture
		want     string
	}{
		{
			name: "deprecated newest",
			fixtures: []searchFixture{
				{ver: "1.0.0", offset: 1},
				{ver: "2.0.0", offset: 2, deprecated: true},
			},
			want: "1.0.0",
		},
		{
			name: "backport at a lower semver",
			fixtures: []searchFixture{
				{ver: "2.0.0", offset: 1},
				{ver: "1.5.0", offset: 2},
			},
			want: "1.5.0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg, st := newRegistryWithStore(t)
			putSearchFixtures(t, st, tc.fixtures)
			view, err := reg.EffectiveView(context.Background(), publicID)
			if err != nil {
				t.Fatalf("EffectiveView: %v", err)
			}
			if len(view) != 1 || view[0].Version != tc.want {
				t.Errorf("EffectiveView = %+v, want x at %s", view, tc.want)
			}
		})
	}
}
