package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
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
func TestExtends_PinResolvedAtIngest(t *testing.T) {
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
func TestExtends_LoadMergesFields(t *testing.T) {
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

// Spec: §4.6 field-semantics table — the FULL merge must reach the
// served frontmatter, not only the indexed Description/Tags/Sensitivity
// record fields. The parent's when_to_use and mcpServers are inherited,
// tags are unioned, sensitivity is most-restrictive, description is the
// child's, and the extends: reference is stripped (hidden-parent privacy).
func TestExtends_MergedFrontmatterServed(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	parent := "---\ntype: agent\nversion: 1.0.0\ndescription: parent desc\n" +
		"when_to_use:\n  - when parenting\n" +
		"mcpServers:\n  - name: github\n    command: gh\n" +
		"tags: [parent-tag]\nsensitivity: high\n---\n\nparent body\n"
	child := "---\ntype: agent\nversion: 2.0.0\ndescription: child desc\n" +
		"when_to_use:\n  - when childing\n" +
		"tags: [child-tag]\nsensitivity: low\nextends: shared/parent@1.x\n---\n\nchild body\n"
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L1", Files: fstest.MapFS{
			"shared/parent/ARTIFACT.md": &fstest.MapFile{Data: []byte(parent)},
		},
	}); err != nil {
		t.Fatalf("ingest parent: %v", err)
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L2", Files: fstest.MapFS{
			"finance/child/ARTIFACT.md": &fstest.MapFile{Data: []byte(child)},
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
	merged, err := manifest.ParseArtifact(got.Frontmatter)
	if err != nil {
		t.Fatalf("ParseArtifact(served frontmatter): %v\n%s", err, got.Frontmatter)
	}
	if merged.Description != "child desc" {
		t.Errorf("served description = %q, want child desc", merged.Description)
	}
	if !containsStr(merged.WhenToUse, "when parenting") || !containsStr(merged.WhenToUse, "when childing") {
		t.Errorf("served when_to_use = %v, want both parent and child entries (append)", merged.WhenToUse)
	}
	if len(merged.MCPServers) != 1 || merged.MCPServers[0].Name != "github" {
		t.Errorf("parent mcpServers not inherited into served frontmatter: %v", merged.MCPServers)
	}
	if !containsStr(merged.Tags, "parent-tag") || !containsStr(merged.Tags, "child-tag") {
		t.Errorf("served tags = %v, want union", merged.Tags)
	}
	if merged.Sensitivity != manifest.SensitivityHigh {
		t.Errorf("served sensitivity = %q, want high (most-restrictive)", merged.Sensitivity)
	}
	if got.Sensitivity != "high" {
		t.Errorf("record sensitivity = %q, want high", got.Sensitivity)
	}
	// §4.6 hidden parents: the parent's ID must not be surfaced.
	if merged.Extends != "" || strings.Contains(string(got.Frontmatter), "shared/parent") {
		t.Errorf("served frontmatter leaks the extends parent: %s", got.Frontmatter)
	}
}

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// Spec: §4.6 hidden-parent — when the parent lives in a layer the
// caller cannot see, load_artifact still returns the merged manifest
// (the parent is fetched server-side); the parent's existence is not
// surfaced.
func TestExtends_HiddenParent(t *testing.T) {
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
func TestExtends_UnknownParentFailsIngest(t *testing.T) {
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
func TestExtends_SelfCycleRejected(t *testing.T) {
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
func TestExtends_ContentHashPin(t *testing.T) {
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

// Spec: §4.7.4 + §4.6 — search_artifacts surfaces the sensitivity
// label, and for an extends: child it reflects the most-restrictive value
// across the chain so search and load_artifact agree. A child that declares
// `low` while its pinned parent is `high` searches as `high`.
func TestSearchArtifacts_SensitivityMergesAcrossExtends(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	highParent := "---\ntype: agent\nversion: 1.0.0\ndescription: parent\nsensitivity: high\n---\n\nparent body\n"
	lowChild := "---\ntype: agent\nversion: 2.0.0\ndescription: child\nsensitivity: low\nextends: shared/parent@1.x\n---\n\nchild body\n"
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L1", Files: fstest.MapFS{
			"shared/parent/ARTIFACT.md": &fstest.MapFile{Data: []byte(highParent)},
		},
	}); err != nil {
		t.Fatalf("ingest parent: %v", err)
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L2", Files: fstest.MapFS{
			"finance/child/ARTIFACT.md": &fstest.MapFile{Data: []byte(lowChild)},
		},
	}); err != nil {
		t.Fatalf("ingest child: %v", err)
	}
	reg := core.New(st, "t", []layer.Layer{
		{ID: "L1", Visibility: layer.Visibility{Public: true}, Precedence: 1},
		{ID: "L2", Visibility: layer.Visibility{Public: true}, Precedence: 2},
	})
	res, err := reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	var child *core.ArtifactDescriptor
	for i := range res.Results {
		if res.Results[i].ID == "finance/child" {
			child = &res.Results[i]
		}
	}
	if child == nil {
		t.Fatalf("search did not return finance/child: %+v", res.Results)
	}
	if child.Sensitivity != "high" {
		t.Errorf("child search Sensitivity = %q, want high (most-restrictive across extends; the child cannot relax to low)", child.Sensitivity)
	}
	// The search and load_artifact surfaces must report the same value.
	got, err := reg.LoadArtifact(context.Background(), publicID, "finance/child", core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if got.Sensitivity != child.Sensitivity {
		t.Errorf("search Sensitivity %q disagrees with load Sensitivity %q", child.Sensitivity, got.Sensitivity)
	}
}

// Spec: §4.7.4 — a non-extends artifact surfaces its own declared sensitivity
// in search results (no chain to resolve).
func TestSearchArtifacts_SensitivityOwnValueNoExtends(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	body := "---\ntype: context\nversion: 1.0.0\ndescription: solo\nsensitivity: medium\n---\n\nbody\n"
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L1", Files: fstest.MapFS{
			"finance/solo/ARTIFACT.md": &fstest.MapFile{Data: []byte(body)},
		},
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	reg := core.New(st, "t", []layer.Layer{{ID: "L1", Visibility: layer.Visibility{Public: true}, Precedence: 1}})
	res, err := reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	found := false
	for _, r := range res.Results {
		if r.ID == "finance/solo" {
			found = true
			if r.Sensitivity != "medium" {
				t.Errorf("Sensitivity = %q, want medium", r.Sensitivity)
			}
		}
	}
	if !found {
		t.Fatalf("search did not return finance/solo: %+v", res.Results)
	}
}

// quiet unused-import linter for tests that don't directly use errors
var _ = errors.Is
