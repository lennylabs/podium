package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

const ecTenant = "t"

// ecFailingStore fails GetManifest for one artifact ID with err and delegates
// every other read to the embedded store.
type ecFailingStore struct {
	store.Store
	failID string
	err    error
}

func (s ecFailingStore) GetManifest(ctx context.Context, tenantID, artifactID, version string) (store.ManifestRecord, error) {
	if artifactID == s.failID {
		return store.ManifestRecord{}, s.err
	}
	return s.Store.GetManifest(ctx, tenantID, artifactID, version)
}

// ecRow builds a stored row whose frontmatter declares the pin it carries.
func ecRow(id, ver, pin string) store.ManifestRecord {
	fm := "---\ntype: context\nversion: " + ver + "\ndescription: " + id + "\n"
	if pin != "" {
		fm += "extends: " + pin + "\n"
	}
	return store.ManifestRecord{
		TenantID: ecTenant, ArtifactID: id, Version: ver, Type: "context",
		ContentHash: id + "@" + ver, Frontmatter: []byte(fm + "---\n\nbody\n"), ExtendsPin: pin,
	}
}

func ecStore(t *testing.T, rows ...store.ManifestRecord) *store.Memory {
	t.Helper()
	st := store.NewMemory()
	for _, r := range rows {
		if err := st.PutManifest(context.Background(), r); err != nil {
			t.Fatalf("PutManifest %s: %v", r.ArtifactID, err)
		}
	}
	return st
}

// Spec: §4.6 — the chain walk separates an unresolvable chain from a store
// fault: a missing parent matches both core.ErrNotFound and store.ErrNotFound,
// any other store failure matches core.ErrNotFound and the store's own error
// but not store.ErrNotFound, and a stored cycle matches
// core.ErrInvalidArgument. Each failure falls on the grandparent, so the walk
// recurses before it fails.
func TestExtendsChain_ErrorContract(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	parent := ecRow("base/parent", "1.0.0", "base/grand@1.0.0")
	child := ecRow("team/child", "1.0.0", "base/parent@1.0.0")

	t.Run("store fault on the grandparent", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		st := ecFailingStore{Store: ecStore(t, parent), failID: "base/grand", err: boom}
		_, err := core.ExtendsChain(ctx, st, ecTenant, child)
		if !errors.Is(err, core.ErrNotFound) || !errors.Is(err, boom) {
			t.Fatalf("err = %v, want core.ErrNotFound and the injected error", err)
		}
		if errors.Is(err, store.ErrNotFound) {
			t.Errorf("err = %v matches store.ErrNotFound for a store fault", err)
		}
	})

	t.Run("missing grandparent", func(t *testing.T) {
		t.Parallel()
		_, err := core.ExtendsChain(ctx, ecStore(t, parent), ecTenant, child)
		if !errors.Is(err, core.ErrNotFound) || !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("err = %v, want core.ErrNotFound and store.ErrNotFound", err)
		}
	})

	t.Run("two-row stored cycle", func(t *testing.T) {
		t.Parallel()
		x := ecRow("base/x", "1.0.0", "base/y@1.0.0")
		y := ecRow("base/y", "1.0.0", "base/x@1.0.0")
		_, err := core.ExtendsChain(ctx, ecStore(t, x, y), ecTenant, x)
		if !errors.Is(err, core.ErrInvalidArgument) {
			t.Fatalf("err = %v, want core.ErrInvalidArgument", err)
		}
		if errors.Is(err, core.ErrNotFound) {
			t.Errorf("err = %v matches core.ErrNotFound for a cycle", err)
		}
	})
}

// Spec: §4.6 — a resolved chain is ordered root first, and ResolvedArtifact
// folds it child-wins and returns each record's authored block with its stored
// pin as the Extends reference.
func TestExtendsChain_ResolvedArtifactFoldsRootFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	grand := ecRow("base/grand", "1.0.0", "")
	parent := ecRow("base/parent", "1.0.0", "base/grand@1.0.0")
	child := ecRow("team/child", "1.0.0", "base/parent@1.0.0")
	chain, err := core.ExtendsChain(ctx, ecStore(t, grand, parent), ecTenant, child)
	if err != nil {
		t.Fatalf("ExtendsChain: %v", err)
	}
	var ids []string
	for _, r := range chain {
		ids = append(ids, r.ArtifactID)
	}
	if len(ids) != 3 || ids[0] != "base/grand" || ids[2] != "team/child" {
		t.Fatalf("chain = %v, want root first and the child last", ids)
	}

	merged, blocks := core.ResolvedArtifact(chain)
	if merged.Description != "team/child" {
		t.Errorf("merged description = %q, want the child's", merged.Description)
	}
	if len(blocks) != 3 || blocks[0].Extends != "" || blocks[2].Extends != "base/parent@1.0.0" ||
		string(blocks[1].Frontmatter) != string(parent.Frontmatter) {
		t.Errorf("blocks = %+v, want the authored blocks root first with their stored pins", blocks)
	}

	if a, b := core.ResolvedArtifact(nil); b != nil || a.Type != "" {
		t.Errorf("ResolvedArtifact(nil) = %+v, %v, want the zero artifact and no blocks", a, b)
	}
}
