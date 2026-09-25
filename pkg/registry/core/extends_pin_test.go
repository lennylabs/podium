package core_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

var (
	epAlice = layer.Identity{Sub: "alice", IsAuthenticated: true}
	epBob   = layer.Identity{Sub: "bob", IsAuthenticated: true}
)

// epRegistry ingests files into L1, which only alice can see, and L2, which
// every caller can see, and returns a registry over both.
func epRegistry(t *testing.T, l1, l2 fstest.MapFS) *core.Registry {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, in := range []struct {
		layer string
		files fstest.MapFS
	}{{"L1", l1}, {"L2", l2}} {
		res, err := ingest.Ingest(context.Background(), st, ingest.Request{TenantID: "t", LayerID: in.layer, Files: in.files})
		if err != nil || res.Accepted != len(in.files) {
			t.Fatalf("ingest %s: %v %+v", in.layer, err, res)
		}
	}
	return core.New(st, "t", []layer.Layer{
		{ID: "L1", Precedence: 1, Visibility: layer.Visibility{Users: []string{"alice"}}},
		{ID: "L2", Precedence: 2, Visibility: layer.Visibility{Public: true}},
	})
}

func epFile(body string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(body)} }

// epChildRegistry holds shared/parent@1.2.0 in L1 and finance/child pinned to
// it, plus finance/plain, in L2.
func epChildRegistry(t *testing.T) *core.Registry {
	t.Helper()
	return epRegistry(t,
		fstest.MapFS{"shared/parent/ARTIFACT.md": epFile(parentArtifact("parent"))},
		fstest.MapFS{
			"finance/child/ARTIFACT.md": epFile(childArtifact("shared/parent@1.x", "child")),
			"finance/plain/ARTIFACT.md": epFile("---\ntype: agent\nversion: 1.0.0\ndescription: plain\n---\n\nplain\n"),
		})
}

func epLoad(t *testing.T, r *core.Registry, id layer.Identity, artifact string) *core.LoadArtifactResult {
	t.Helper()
	res, err := r.LoadArtifact(context.Background(), id, artifact, core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("LoadArtifact %s: %v", artifact, err)
	}
	return res
}

// Spec: §4.6 hidden parents (withheld) — a caller who can see the parent
// record is served the child's pin.
func TestLoadArtifact_ExtendsPinFieldPresentWhenParentVisible(t *testing.T) {
	t.Parallel()
	if got := epLoad(t, epChildRegistry(t), epAlice, "finance/child").ExtendsPin; got != "shared/parent@1.2.0" {
		t.Errorf("ExtendsPin = %q, want shared/parent@1.2.0", got)
	}
}

// Spec: §4.6 hidden parents (withheld) — a caller who cannot see the parent
// record is served no pin.
func TestLoadArtifact_ExtendsPinFieldAbsentWhenParentHidden(t *testing.T) {
	t.Parallel()
	if got := epLoad(t, epChildRegistry(t), epBob, "finance/child").ExtendsPin; got != "" {
		t.Errorf("ExtendsPin = %q, want empty for a caller who cannot see the parent", got)
	}
}

// Spec: §4.6 hidden parents (withheld) — an artifact that extends nothing is
// served no pin, so an absent pin says nothing about a hidden parent.
func TestLoadArtifact_ExtendsPinFieldAbsentForPlainArtifact(t *testing.T) {
	t.Parallel()
	if got := epLoad(t, epChildRegistry(t), epAlice, "finance/plain").ExtendsPin; got != "" {
		t.Errorf("ExtendsPin = %q, want empty", got)
	}
}

// Spec: §4.6 hidden parents (withheld) — the served pin is keyed on the
// parent record's (ID, version). In a §4.6 same-ID overlay the child X@2.0.0
// in L2 pins X@1.0.0 in L1. A caller who sees only L2 sees the ID X and not
// the pinned version, so no pin is served; a caller who sees both is served
// X@1.0.0.
func TestLoadArtifact_ExtendsPinFieldAbsentForSameIDOverlayWhenLowerLayerHidden(t *testing.T) {
	t.Parallel()
	r := epRegistry(t,
		fstest.MapFS{"team/overlay/ARTIFACT.md": epFile("---\ntype: agent\nversion: 1.0.0\ndescription: base\n---\n\nbase\n")},
		fstest.MapFS{"team/overlay/ARTIFACT.md": epFile("---\ntype: agent\nversion: 2.0.0\ndescription: overlay\nextends: team/overlay@1.0.0\n---\n\noverlay\n")})
	if got := epLoad(t, r, epBob, "team/overlay").ExtendsPin; got != "" {
		t.Errorf("L2-only caller ExtendsPin = %q, want empty", got)
	}
	if got := epLoad(t, r, epAlice, "team/overlay").ExtendsPin; got != "team/overlay@1.0.0" {
		t.Errorf("two-layer caller ExtendsPin = %q, want team/overlay@1.0.0", got)
	}
}

// Spec: §4.6 hidden parents (withheld), §13.4 — the revalidation result the
// Revalidate predicate receives, and the one LoadArtifact returns when it
// accepts, carry the ExtendsPin the full load serves that identity, so a HEAD
// and a 304 publish the validator the full response does.
func TestLoadArtifact_RevalidationResultCarriesTheServedExtendsPin(t *testing.T) {
	t.Parallel()
	r := epChildRegistry(t)
	for id, want := range map[string]string{"alice": "shared/parent@1.2.0", "bob": ""} {
		ident := layer.Identity{Sub: id, IsAuthenticated: true}
		var seen *core.LoadArtifactResult
		res, err := r.LoadArtifact(context.Background(), ident, "finance/child", core.LoadArtifactOptions{
			Revalidate: func(res *core.LoadArtifactResult) bool {
				seen = res
				return true
			},
		})
		if err != nil {
			t.Fatalf("LoadArtifact as %s: %v", id, err)
		}
		if seen == nil || seen.ExtendsPin != want || res.ExtendsPin != want {
			t.Errorf("%s: predicate saw %+v, returned %q; want %q", id, seen, res.ExtendsPin, want)
		}
		if full := epLoad(t, r, ident, "finance/child").ExtendsPin; full != want {
			t.Errorf("%s: full load ExtendsPin = %q, want %q", id, full, want)
		}
	}
}
