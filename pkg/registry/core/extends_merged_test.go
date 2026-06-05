package core_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/version"
)

// Spec: §4.6 / §6.6 step 2 — an extends child's served frontmatter is a
// re-serialization with the hidden parent stripped, so its bytes cannot
// reproduce the stored content_hash. LoadArtifact flags the result Merged and
// (see TestLoadArtifact_MergedDeliversRawFrontmatter) ships the pre-merge bytes
// so the consumer reproduces the content-hash check rather than skipping it.
func TestLoadArtifact_MergedFlagSetForExtends(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L1", Files: fstest.MapFS{
			"shared/parent/ARTIFACT.md": &fstest.MapFile{Data: []byte(parentArtifact("parent desc"))},
		},
	}); err != nil {
		t.Fatalf("ingest parent: %v", err)
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L2", Files: fstest.MapFS{
			"finance/child/ARTIFACT.md": &fstest.MapFile{
				Data: []byte(childArtifact("shared/parent@1.x", "child desc")),
			},
		},
	}); err != nil {
		t.Fatalf("ingest child: %v", err)
	}
	reg := core.New(st, "t", []layer.Layer{
		{ID: "L1", Visibility: layer.Visibility{Public: true}, Precedence: 1},
		{ID: "L2", Visibility: layer.Visibility{Public: true}, Precedence: 2},
	})
	got, err := reg.LoadArtifact(context.Background(), publicID, "finance/child", core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("LoadArtifact child: %v", err)
	}
	if !got.Merged {
		t.Error("Merged = false for an extends child, want true (consumer must skip hash check)")
	}
}

// Spec: §6.6 step 2 / §4.7.6 — a merged manifest delivers the leaf child's
// original pre-merge ARTIFACT.md bytes in RawFrontmatter, and those bytes (not
// the re-serialized served Frontmatter) reproduce the stored content_hash, so
// the consumer can run the content-hash match instead of skipping it.
func TestLoadArtifact_MergedDeliversRawFrontmatter(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L1", Files: fstest.MapFS{
			"shared/parent/ARTIFACT.md": &fstest.MapFile{Data: []byte(parentArtifact("parent desc"))},
		},
	}); err != nil {
		t.Fatalf("ingest parent: %v", err)
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L2", Files: fstest.MapFS{
			"finance/child/ARTIFACT.md": &fstest.MapFile{
				Data: []byte(childArtifact("shared/parent@1.x", "child desc")),
			},
		},
	}); err != nil {
		t.Fatalf("ingest child: %v", err)
	}
	reg := core.New(st, "t", []layer.Layer{
		{ID: "L1", Visibility: layer.Visibility{Public: true}, Precedence: 1},
		{ID: "L2", Visibility: layer.Visibility{Public: true}, Precedence: 2},
	})
	got, err := reg.LoadArtifact(context.Background(), publicID, "finance/child", core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("LoadArtifact child: %v", err)
	}
	if len(got.RawFrontmatter) == 0 {
		t.Fatal("RawFrontmatter empty for a merged manifest, consumer cannot reproduce the hash")
	}
	// The served (merged) frontmatter must not reproduce the hash...
	if "sha256:"+version.ContentHash(got.Frontmatter) == got.ContentHash {
		t.Error("served merged Frontmatter unexpectedly reproduced the content hash")
	}
	// ...but the pre-merge RawFrontmatter (no skill, no resources here) must.
	if recomputed := "sha256:" + version.ContentHash(got.RawFrontmatter); recomputed != got.ContentHash {
		t.Errorf("RawFrontmatter does not reproduce content hash: got %s, want %s", recomputed, got.ContentHash)
	}
}

// Spec: §6.6 step 2 — a plain (non-extends) artifact is served verbatim, so
// Merged is false, RawFrontmatter is empty, and the consumer recomputes and
// enforces the content_hash against the served Frontmatter.
func TestLoadArtifact_MergedFlagUnsetForPlainArtifact(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L1", Files: fstest.MapFS{
			"shared/solo/ARTIFACT.md": &fstest.MapFile{Data: []byte(parentArtifact("solo desc"))},
		},
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	reg := core.New(st, "t", []layer.Layer{
		{ID: "L1", Visibility: layer.Visibility{Public: true}, Precedence: 1},
	})
	got, err := reg.LoadArtifact(context.Background(), publicID, "shared/solo", core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if got.Merged {
		t.Error("Merged = true for a plain artifact, want false")
	}
	if len(got.RawFrontmatter) != 0 {
		t.Error("RawFrontmatter set for a plain artifact, want empty")
	}
}
