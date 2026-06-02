package filesystem

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/manifest"
)

// recByID returns the walked record with the given canonical ID.
func recByID(t *testing.T, recs []ArtifactRecord, id string) ArtifactRecord {
	t.Helper()
	for _, r := range recs {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("record %q not found in %v", id, idsOf(recs))
	return ArtifactRecord{}
}

// Spec: §4.6 / §13.11.3 — a same-ID overlay child that declares
// extends: <its-own-id> is merged onto the lower-precedence parent through
// the shared MergeExtends, so the filesystem source produces the same
// resolved frontmatter the registry serves at load time.
func TestResolveExtends_SameIDOverlayMergesFrontmatter(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	parent := "---\ntype: context\nversion: 1.0.0\nname: Base\ndescription: parent desc\nsensitivity: low\ntags:\n  - shared\n---\n\nParent body.\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child desc\nsensitivity: high\nextends: x\ntags:\n  - team\n---\n\nChild body.\n"
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    ".registry-config",
			Content: "multi_layer: true\nlayer_order:\n  - team-shared\n  - personal\n",
		},
		testharness.WriteTreeOption{Path: "team-shared/x/ARTIFACT.md", Content: parent},
		testharness.WriteTreeOption{Path: "personal/x/ARTIFACT.md", Content: child},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := reg.Walk(WalkOptions{CollisionPolicy: CollisionPolicyHighestWins, ResolveExtends: true})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1 (%v)", len(got), idsOf(got))
	}
	a, err := manifest.ParseArtifact(got[0].ArtifactBytes)
	if err != nil {
		t.Fatalf("ParseArtifact(merged): %v", err)
	}
	// description: scalar child-wins; name: inherited from parent.
	if a.Description != "child desc" {
		t.Errorf("Description = %q, want child desc", a.Description)
	}
	if a.Name != "Base" {
		t.Errorf("Name = %q, want inherited parent Base", a.Name)
	}
	// sensitivity: most-restrictive (high > low).
	if string(a.Sensitivity) != "high" {
		t.Errorf("Sensitivity = %q, want high", a.Sensitivity)
	}
	// version: child's own.
	if a.Version != "2.0.0" {
		t.Errorf("Version = %q, want child's 2.0.0", a.Version)
	}
	// tags: append-unique union.
	if !(contains(a.Tags, "shared") && contains(a.Tags, "team")) {
		t.Errorf("Tags = %v, want union of shared+team", a.Tags)
	}
	// extends stripped from the served frontmatter (§4.6 hidden parent).
	if a.Extends != "" {
		t.Errorf("Extends = %q, want empty after merge", a.Extends)
	}
	if strings.Contains(string(got[0].ArtifactBytes), "extends:") {
		t.Errorf("serialized bytes still contain extends:\n%s", got[0].ArtifactBytes)
	}
}

// Spec: §4.6 — a child may extend a parent at a different canonical ID; both
// records survive dedup and the parent's structured fields are merged in.
func TestResolveExtends_DifferentIDParent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: base desc\nsensitivity: medium\ntags:\n  - base\n---\n\nBase body.\n"
	child := "---\ntype: context\nversion: 1.0.0\ndescription: derived desc\nextends: acme/base\ntags:\n  - derived\n---\n\nDerived body.\n"
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "acme/base/ARTIFACT.md", Content: parent},
		testharness.WriteTreeOption{Path: "acme/derived/ARTIFACT.md", Content: child},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := reg.Walk(WalkOptions{CollisionPolicy: CollisionPolicyHighestWins, ResolveExtends: true})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	derived := recByID(t, got, "acme/derived")
	a, err := manifest.ParseArtifact(derived.ArtifactBytes)
	if err != nil {
		t.Fatalf("ParseArtifact: %v", err)
	}
	if a.Description != "derived desc" {
		t.Errorf("Description = %q, want derived desc", a.Description)
	}
	// sensitivity inherited from the parent (child unset).
	if string(a.Sensitivity) != "medium" {
		t.Errorf("Sensitivity = %q, want inherited medium", a.Sensitivity)
	}
	if !(contains(a.Tags, "base") && contains(a.Tags, "derived")) {
		t.Errorf("Tags = %v, want union of base+derived", a.Tags)
	}
	// The parent record is untouched (no extends of its own).
	base := recByID(t, got, "acme/base")
	if pa, _ := manifest.ParseArtifact(base.ArtifactBytes); pa.Description != "base desc" {
		t.Errorf("parent Description = %q, want unchanged base desc", pa.Description)
	}
}

// Spec: §4.6 — an extends reference to a parent that is not present in the
// registry is an error rather than a silent pass-through.
func TestResolveExtends_UnknownParentErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	child := "---\ntype: context\nversion: 1.0.0\ndescription: orphan\nextends: does/not/exist\n---\n\nbody\n"
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "acme/orphan/ARTIFACT.md", Content: child},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err = reg.Walk(WalkOptions{CollisionPolicy: CollisionPolicyHighestWins, ResolveExtends: true})
	if err == nil || !strings.Contains(err.Error(), "extends.unresolved") {
		t.Fatalf("Walk err = %v, want extends.unresolved", err)
	}
}

// Spec: §4.6 — a same-ID extends with no lower-precedence layer to inherit
// from is an unresolved-parent error (it would otherwise be a self-cycle).
func TestResolveExtends_SameIDWithoutLowerLayerErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	child := "---\ntype: context\nversion: 1.0.0\ndescription: self\nextends: x\n---\n\nbody\n"
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    ".registry-config",
			Content: "multi_layer: true\nlayer_order:\n  - only\n",
		},
		testharness.WriteTreeOption{Path: "only/x/ARTIFACT.md", Content: child},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err = reg.Walk(WalkOptions{CollisionPolicy: CollisionPolicyHighestWins, ResolveExtends: true})
	if err == nil || !strings.Contains(err.Error(), "extends.unresolved") {
		t.Fatalf("Walk err = %v, want extends.unresolved", err)
	}
}

// Spec: §4.6 — "The child's type: must match the parent's; ingest rejects an
// extends: chain that crosses types." The filesystem-source materialization
// path must reject a cross-type chain rather than silently merge the parent's
// fields into a differently-typed child (F-4.6.2). Mirrors the server ingest
// rejection.
func TestResolveExtends_CrossTypeRejected(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	parent := "---\ntype: agent\nversion: 1.0.0\ndescription: parent agent\n---\n\nagent body\n"
	child := "---\ntype: context\nversion: 1.0.0\ndescription: child context\nextends: acme/base\n---\n\ncontext body\n"
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "acme/base/ARTIFACT.md", Content: parent},
		testharness.WriteTreeOption{Path: "acme/derived/ARTIFACT.md", Content: child},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err = reg.Walk(WalkOptions{CollisionPolicy: CollisionPolicyHighestWins, ResolveExtends: true})
	if err == nil || !strings.Contains(err.Error(), "extends.type_mismatch") {
		t.Fatalf("Walk err = %v, want extends.type_mismatch", err)
	}
}

// Spec: §4.6 — the cross-type rejection also fires on a same-ID overlay whose
// type differs from the lower-precedence layer's artifact at the same ID.
func TestResolveExtends_SameIDCrossTypeRejected(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	parent := "---\ntype: agent\nversion: 1.0.0\ndescription: base agent\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: overlay context\nextends: x\n---\n\nbody\n"
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    ".registry-config",
			Content: "multi_layer: true\nlayer_order:\n  - team-shared\n  - personal\n",
		},
		testharness.WriteTreeOption{Path: "team-shared/x/ARTIFACT.md", Content: parent},
		testharness.WriteTreeOption{Path: "personal/x/ARTIFACT.md", Content: child},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err = reg.Walk(WalkOptions{CollisionPolicy: CollisionPolicyHighestWins, ResolveExtends: true})
	if err == nil || !strings.Contains(err.Error(), "extends.type_mismatch") {
		t.Fatalf("Walk err = %v, want extends.type_mismatch", err)
	}
}

// Spec: §13.11.3 — with ResolveExtends disabled (the default for lint and
// conformance callers), the authored frontmatter is left unchanged.
func TestResolveExtends_DisabledLeavesBytesUnchanged(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: base\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 1.0.0\ndescription: derived\nextends: acme/base\n---\n\nbody\n"
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "acme/base/ARTIFACT.md", Content: parent},
		testharness.WriteTreeOption{Path: "acme/derived/ARTIFACT.md", Content: child},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := reg.Walk(WalkOptions{CollisionPolicy: CollisionPolicyHighestWins})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	derived := recByID(t, got, "acme/derived")
	if !strings.Contains(string(derived.ArtifactBytes), "extends: acme/base") {
		t.Errorf("ResolveExtends=false must keep the authored extends:\n%s", derived.ArtifactBytes)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
