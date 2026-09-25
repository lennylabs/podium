package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// cedBob sees the requested artifact's layer and none of its ancestors'.
var cedBob = layer.Identity{Sub: "bob", IsAuthenticated: true}

// cedFixture is an admission fixture whose layers are store layer configs:
// "team" holds the requested artifacts and is visible to alice and bob, and
// every other layer is visible to alice alone.
type cedFixture struct{ *admFixture }

func newCEDFixture(t *testing.T, ancestorLayers ...string) cedFixture {
	t.Helper()
	f := cedFixture{newAdmFixture(t)}
	mustPutLayer(t, f.st, store.LayerConfig{TenantID: admTenant, ID: "team", Order: 10, Users: []string{"alice", "bob"}})
	for i, id := range ancestorLayers {
		mustPutLayer(t, f.st, store.LayerConfig{TenantID: admTenant, ID: id, Order: i + 1, Users: []string{"alice"}})
	}
	return f
}

// row writes a sealed, signed row in layerID whose bytes declare the extends:
// reference its pin names, so admission passes it and the walk reaches its
// parent.
func (f cedFixture) row(t *testing.T, layerID, id, ver, pin string) {
	t.Helper()
	extra := ""
	if pin != "" {
		extra = "extends: " + pin + "\n"
	}
	f.put(t, storetest.Seal(t, store.ManifestRecord{
		TenantID: admTenant, ArtifactID: id, Version: ver, Layer: layerID, Type: "context",
		Frontmatter: admManifest(ver, extra), ExtendsPin: pin,
	}, nil, f.key))
}

// deleteLayer soft-deletes a layer and every row ingested from it, so a
// GetManifest of those rows reports store.ErrNotFound.
func (f cedFixture) deleteLayer(t *testing.T, id string) {
	t.Helper()
	if err := f.st.DeleteLayerConfig(context.Background(), admTenant, id); err != nil {
		t.Fatalf("DeleteLayerConfig %s: %v", id, err)
	}
}

func (f cedFixture) load(id string) error {
	_, err := f.registry(f.key).LoadArtifact(context.Background(), cedBob, id, core.LoadArtifactOptions{})
	return err
}

// wantNamesOnly asserts err wraps sentinel, names the requested ID exactly as
// admitChain builds it, and carries none of the withheld ancestor strings.
func wantNamesOnly(t *testing.T, err, sentinel error, requestedID string, withheld ...string) {
	t.Helper()
	wantRefused(t, err, sentinel, requestedID)
	for _, w := range withheld {
		if strings.Contains(err.Error(), w) {
			t.Errorf("message %q names withheld %q", err.Error(), w)
		}
	}
}

// Spec: §4.6 hidden parents (withheld) — a failure to resolve the chain
// reports the artifact the caller requested and not the parent it could not
// reach.
func TestLoadArtifact_MissingParentErrorNamesNoParent(t *testing.T) {
	t.Parallel()

	t.Run("deleted parent", func(t *testing.T) {
		t.Parallel()
		f := newCEDFixture(t, "parent-layer")
		f.row(t, "parent-layer", "base/parent", "4.2.0", "")
		f.row(t, "team", "team/child", "1.0.0", "base/parent@4.2.0")
		f.deleteLayer(t, "parent-layer")
		wantNamesOnly(t, f.load("team/child"), core.ErrNotFound, "team/child", "base/parent", "4.2.0")
	})

	// The walk fails while it stands at B, the hidden middle ancestor, so a
	// message built from the current member would name B and one built from
	// its pin would name A.
	t.Run("three-level chain with hidden middle and deleted root", func(t *testing.T) {
		t.Parallel()
		f := newCEDFixture(t, "root-layer", "mid-layer")
		f.row(t, "root-layer", "base/grand", "3.1.4", "")
		f.row(t, "mid-layer", "base/mid", "2.7.1", "base/grand@3.1.4")
		f.row(t, "team", "team/leaf", "1.0.0", "base/mid@2.7.1")
		f.deleteLayer(t, "root-layer")
		wantNamesOnly(t, f.load("team/leaf"), core.ErrNotFound, "team/leaf",
			"base/mid", "2.7.1", "base/grand", "3.1.4")
	})
}

// Spec: §4.6 hidden parents (withheld) — a chain cycle reports only the
// requested artifact. Ingest rejects a cycle, so the rows are written to
// store.Memory directly, and the member that repeats is an ancestor rather
// than the requested artifact, so a message built from the repeating pin
// names it.
func TestLoadArtifact_ChainCycleErrorNamesNoAncestor(t *testing.T) {
	t.Parallel()
	f := newCEDFixture(t, "hidden")
	f.row(t, "hidden", "base/x", "5.0.0", "base/y@6.0.0")
	f.row(t, "hidden", "base/y", "6.0.0", "base/x@5.0.0")
	f.row(t, "team", "team/cyc", "1.0.0", "base/x@5.0.0")
	wantNamesOnly(t, f.load("team/cyc"), core.ErrInvalidArgument, "team/cyc",
		"base/x", "5.0.0", "base/y", "6.0.0")
}
