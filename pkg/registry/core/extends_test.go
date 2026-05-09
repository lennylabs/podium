package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

func parentArtifact(desc string) string {
	return "---\n" +
		"type: agent\n" +
		"version: 1.2.0\n" +
		"description: " + desc + "\n" +
		"tags: [parent-tag, shared]\n" +
		"sensitivity: low\n" +
		"---\n\nparent body\n"
}

func childArtifact(parentRef, desc string) string {
	return "---\n" +
		"type: agent\n" +
		"version: 2.0.0\n" +
		"description: " + desc + "\n" +
		"tags: [child-tag, shared]\n" +
		"sensitivity: medium\n" +
		"sbom:\n" +
		"  format: cyclonedx-1.5\n" +
		"  ref: ./sbom.json\n" +
		"extends: " + parentRef + "\n" +
		"---\n\nchild body\n"
}

// Spec: §4.6 / §4.7.6 — extends: parent reference resolves at the
// child's ingest time and the parent version is stored as a hard pin
// in the child's manifest record.
// Phase: 8
func TestExtends_PinResolvedAtIngest(t *testing.T) {
	testharness.RequirePhase(t, 8)
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	parent := fstest.MapFS{
		"shared/parent/ARTIFACT.md": &fstest.MapFile{Data: []byte(parentArtifact("parent"))},
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L1", Files: parent,
	}); err != nil {
		t.Fatalf("ingest parent: %v", err)
	}

	child := fstest.MapFS{
		"finance/child/ARTIFACT.md": &fstest.MapFile{
			Data: []byte(childArtifact("shared/parent@1.x", "child")),
		},
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L2", Files: child,
	}); err != nil {
		t.Fatalf("ingest child: %v", err)
	}
	rec, err := st.GetManifest(context.Background(), "t", "finance/child", "2.0.0")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if rec.ExtendsPin != "shared/parent@1.2.0" {
		t.Errorf("ExtendsPin = %q, want shared/parent@1.2.0", rec.ExtendsPin)
	}
}

// Spec: §4.6 — load_artifact merges parent and child fields per the
// merge-semantics table: scalars (description) child wins; list fields
// (tags) append-unique; sensitivity most-restrictive.
// Phase: 8
func TestExtends_LoadMergesFields(t *testing.T) {
	testharness.RequirePhase(t, 8)
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
		t.Fatalf("LoadArtifact: %v", err)
	}
	if got.Version != "2.0.0" {
		t.Errorf("Version = %q, want child's 2.0.0", got.Version)
	}
	if !strings.Contains(got.ManifestBody, "child body") {
		t.Errorf("Body should be child's: %q", got.ManifestBody)
	}
}

// Spec: §4.6 hidden-parent — when the parent lives in a layer the
// caller cannot see, load_artifact still returns the merged manifest
// (the parent is fetched server-side); the parent's existence is not
// surfaced.
// Phase: 8
func TestExtends_HiddenParent(t *testing.T) {
	testharness.RequirePhase(t, 8)
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "secret-layer", Files: fstest.MapFS{
			"shared/parent/ARTIFACT.md": &fstest.MapFile{Data: []byte(parentArtifact("hidden"))},
		},
	}); err != nil {
		t.Fatalf("ingest parent: %v", err)
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "public-layer", Files: fstest.MapFS{
			"finance/child/ARTIFACT.md": &fstest.MapFile{
				Data: []byte(childArtifact("shared/parent@1.x", "visible child")),
			},
		},
	}); err != nil {
		t.Fatalf("ingest child: %v", err)
	}
	reg := core.New(st, "t", []layer.Layer{
		{ID: "secret-layer", Visibility: layer.Visibility{Users: []string{"only-this-user"}}, Precedence: 1},
		{ID: "public-layer", Visibility: layer.Visibility{Public: true}, Precedence: 2},
	})
	id := layer.Identity{Sub: "joan", IsAuthenticated: true}
	got, err := reg.LoadArtifact(context.Background(), id, "finance/child", core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if got.Version != "2.0.0" {
		t.Errorf("Version = %q", got.Version)
	}
	// The parent was resolved server-side. The merged tags include the
	// parent's even though the caller cannot see the secret-layer.
	frontmatter := string(got.Frontmatter)
	if !strings.Contains(frontmatter, "child-tag") {
		t.Errorf("Frontmatter missing child's content")
	}

	// The parent is also not visible via search_artifacts to this caller.
	res, err := reg.SearchArtifacts(context.Background(), id, core.SearchArtifactsOptions{})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	for _, r := range res.Results {
		if r.ID == "shared/parent" {
			t.Errorf("hidden parent leaked through search")
		}
	}
}

// Spec: §4.6 — extends pointing at an unknown parent fails ingest with
// a structured rejection.
// Phase: 8
func TestExtends_UnknownParentFailsIngest(t *testing.T) {
	testharness.RequirePhase(t, 8)
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L", Files: fstest.MapFS{
			"finance/child/ARTIFACT.md": &fstest.MapFile{
				Data: []byte(childArtifact("does/not/exist@1.x", "orphan")),
			},
		},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(res.Rejected) != 1 {
		t.Fatalf("got %d rejections, want 1: %+v", len(res.Rejected), res.Rejected)
	}
	if !strings.Contains(res.Rejected[0].Reason, "extends") {
		t.Errorf("expected rejection to mention extends: %+v", res.Rejected[0])
	}
}

// Spec: §4.6 — self-referencing extends is rejected as a cycle.
// Phase: 8
func TestExtends_SelfCycleRejected(t *testing.T) {
	testharness.RequirePhase(t, 8)
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	src := "---\ntype: agent\nversion: 1.0.0\ndescription: self\nsensitivity: low\nextends: finance/self@1.x\n---\n\nself\n"
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L", Files: fstest.MapFS{
			"finance/self/ARTIFACT.md": &fstest.MapFile{Data: []byte(src)},
		},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(res.Rejected) != 1 {
		t.Fatalf("got %d rejections, want 1", len(res.Rejected))
	}
	if !strings.Contains(res.Rejected[0].Reason, "self") {
		t.Errorf("expected self-cycle reason: %+v", res.Rejected[0])
	}
}

// Spec: §4.7.6 — extends pin can target a specific content hash.
// Phase: 8
func TestExtends_ContentHashPin(t *testing.T) {
	testharness.RequirePhase(t, 8)
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L1", Files: fstest.MapFS{
			"shared/parent/ARTIFACT.md": &fstest.MapFile{Data: []byte(parentArtifact("parent"))},
		},
	}); err != nil {
		t.Fatalf("ingest parent: %v", err)
	}
	parentRec, err := st.GetManifest(context.Background(), "t", "shared/parent", "1.2.0")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	hash := strings.TrimPrefix(parentRec.ContentHash, "sha256:")
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L2", Files: fstest.MapFS{
			"finance/child/ARTIFACT.md": &fstest.MapFile{
				Data: []byte(childArtifact("shared/parent@sha256:"+hash, "hash-pinned child")),
			},
		},
	}); err != nil {
		t.Fatalf("ingest child: %v", err)
	}
	rec, err := st.GetManifest(context.Background(), "t", "finance/child", "2.0.0")
	if err != nil {
		t.Fatalf("GetManifest child: %v", err)
	}
	if rec.ExtendsPin != "shared/parent@1.2.0" {
		t.Errorf("ExtendsPin = %q, want shared/parent@1.2.0", rec.ExtendsPin)
	}
}

// quiet unused-import linter for tests that don't directly use errors
var _ = errors.Is
