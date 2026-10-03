package core_test

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// arRegistry ingests shared/parent@1.2.0 into L1 at parentAt and
// finance/child@2.0.0, which extends it, into L2 at childAt. Distinct ingest
// times let an assertion tell the child's revision from the parent's.
func arRegistry(t *testing.T, parentAt, childAt time.Time) *core.Registry {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, in := range []struct {
		layer string
		at    time.Time
		files fstest.MapFS
	}{
		{"L1", parentAt, fstest.MapFS{"shared/parent/ARTIFACT.md": epFile(parentArtifact("parent"))}},
		{"L2", childAt, fstest.MapFS{"finance/child/ARTIFACT.md": epFile(childArtifact("shared/parent@1.x", "child"))}},
	} {
		res, err := ingest.Ingest(context.Background(), st, ingest.Request{
			TenantID: "t", LayerID: in.layer, Files: in.files, Clock: clock.NewFrozen(in.at),
		})
		if err != nil || res.Accepted != len(in.files) {
			t.Fatalf("ingest %s: %v %+v", in.layer, err, res)
		}
	}
	return core.New(st, "t", []layer.Layer{
		{ID: "L1", Precedence: 1, Visibility: layer.Visibility{Public: true}},
		{ID: "L2", Precedence: 2, Visibility: layer.Visibility{Public: true}},
	})
}

// Spec: §4.7.10, §4.7.6 — every load result carries the resolved record's own
// ingest time as its artifact revision: a latest load, a pinned load, a
// revalidation answer, and a merged extends child, which carries the child's
// time rather than the parent's.
func TestLoadArtifact_CarriesServedArtifactRevision(t *testing.T) {
	t.Parallel()
	parentAt := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)
	childAt := time.Date(2026, 2, 3, 4, 5, 6, 654321000, time.UTC)
	reg := arRegistry(t, parentAt, childAt)
	const wantChild = "2026-02-03T04:05:06.654321Z"
	const wantParent = "2026-01-02T03:04:05.123456Z"
	id := layer.Identity{IsPublic: true}

	cases := []struct {
		name, artifact, want string
		opts                 core.LoadArtifactOptions
	}{
		{"latest extends child", "finance/child", wantChild, core.LoadArtifactOptions{}},
		{"pinned version", "finance/child", wantChild, core.LoadArtifactOptions{Version: "2.0.0"}},
		{"session first latest", "finance/child", wantChild, core.LoadArtifactOptions{SessionID: "s1"}},
		{"session pinned latest", "finance/child", wantChild, core.LoadArtifactOptions{SessionID: "s1"}},
		{"parent", "shared/parent", wantParent, core.LoadArtifactOptions{}},
		{"revalidation", "finance/child", wantChild, core.LoadArtifactOptions{
			Revalidate: func(*core.LoadArtifactResult) bool { return true },
		}},
	}
	for _, tc := range cases {
		res, err := reg.LoadArtifact(context.Background(), id, tc.artifact, tc.opts)
		if err != nil {
			t.Fatalf("%s: LoadArtifact: %v", tc.name, err)
		}
		if res.ArtifactRevision != tc.want {
			t.Errorf("%s: ArtifactRevision = %q, want %q", tc.name, res.ArtifactRevision, tc.want)
		}
	}
}
