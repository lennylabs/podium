package filesystem

import (
	"errors"
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
// fields into a differently-typed child. Mirrors the server ingest
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

// recordOf builds a walked record from an authored manifest.
func recordOf(t *testing.T, id, src string) ArtifactRecord {
	t.Helper()
	a, err := manifest.ParseArtifact([]byte(src))
	if err != nil {
		t.Fatalf("ParseArtifact(%s): %v", id, err)
	}
	return ArtifactRecord{ID: id, Artifact: a, ArtifactBytes: []byte(src)}
}

// Spec: §4.6 hidden parents / §11 — the chain the hidden-parent test checks
// comes from each record's parsed manifest, so a child inheriting a key that
// names the grandparent fails closed whichever record this resolver reached
// first. Read back out of the record bytes, the grandparent's ID would go
// missing once the middle record had been rewritten in place, and the
// filesystem mode would serve a block the server mode refuses.
func TestResolveExtends_GrandparentIDFailsClosedInAnyProcessingOrder(t *testing.T) {
	t.Parallel()
	gp := recordOf(t, "shared/gp",
		"---\ntype: context\nversion: 1.0.0\ndescription: grandparent\n---\n\ngp body\n")
	parent := recordOf(t, "shared/parent",
		"---\ntype: context\nversion: 1.0.0\ndescription: parent\n"+
			"x_base: shared/gp\nextends: shared/gp\n---\n\nparent body\n")
	child := recordOf(t, "team/child",
		"---\ntype: context\nversion: 2.0.0\ndescription: child\nextends: shared/parent\n---\n\nchild body\n")
	other := recordOf(t, "team/other",
		"---\ntype: context\nversion: 1.0.0\ndescription: unrelated\n---\n\nother body\n")

	for name, order := range map[string][]ArtifactRecord{
		"parent before child": {gp, parent, child, other},
		"child before parent": {other, child, parent, gp},
	} {
		order := order
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			deduped := append([]ArtifactRecord(nil), order...)
			err := resolveExtends(deduped, append([]ArtifactRecord(nil), order...))
			if !errors.Is(err, manifest.ErrUnhidableParent) {
				t.Fatalf("resolveExtends err = %v, want ErrUnhidableParent", err)
			}
		})
	}
}

// Spec: §4.6 hidden parents, §11 — the walk a consumer drives fails closed
// when a child inherits a key naming the hidden grandparent, because serving
// the key would name the grandparent and dropping it would serve an inheritable
// key as nothing. The server mode refuses the same child, so neither deployment
// mode materializes a tree the other refuses (§11, §2.2).
func TestWalk_ResolveExtendsFailsOnAnInheritedKeyNamingTheGrandparent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    "shared/gp/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: grandparent\n---\n\ngp body\n",
		},
		testharness.WriteTreeOption{
			Path: "shared/parent/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: parent\n" +
				"x_base: shared/gp\nextends: shared/gp\n---\n\nparent body\n",
		},
		testharness.WriteTreeOption{
			Path:    "team/child/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 2.0.0\ndescription: child\nextends: shared/parent\n---\n\nchild body\n",
		},
		testharness.WriteTreeOption{
			Path:    "team/other/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: unrelated\n---\n\nother body\n",
		},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := reg.Walk(WalkOptions{CollisionPolicy: CollisionPolicyHighestWins, ResolveExtends: true})
	if !errors.Is(err, manifest.ErrUnhidableParent) {
		t.Fatalf("Walk err = %v, want ErrUnhidableParent", err)
	}
	if got != nil {
		t.Errorf("a failed walk returned records: %v", idsOf(got))
	}
}

// Spec: §4.6 hidden parents, §11 — §4.6 constrains the merged block the
// registry serves and says nothing about which member of the chain wrote the
// text in it, so a key the child authored that names its own parent fails the
// walk exactly as an inherited one does. The server mode refuses the same
// artifact, so the two deployment modes materialize the same tree (§11, §2.2).
func TestWalk_ResolveExtendsFailsOnTheChildsOwnKeyNamingItsParent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    "shared/parent/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: parent\n---\n\nparent body\n",
		},
		testharness.WriteTreeOption{
			Path: "team/child/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 2.0.0\ndescription: child\n" +
				"x_base: shared/parent\nextends: shared/parent\n---\n\nchild body\n",
		},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := reg.Walk(WalkOptions{CollisionPolicy: CollisionPolicyHighestWins, ResolveExtends: true})
	if !errors.Is(err, manifest.ErrUnhidableParent) {
		t.Fatalf("Walk err = %v, want ErrUnhidableParent", err)
	}
	if got != nil {
		t.Errorf("a failed walk returned records: %v", idsOf(got))
	}
}

// Spec: §4.6 hidden parents, §11 — a child whose extends reference is carried
// by an anchor leaves the merged block with an alias into a value that is gone,
// so the block cannot be read back and its contents cannot be checked. The walk
// ends with the sentinel the server mode reports as registry.invalid_argument,
// so neither mode materializes a tree the other refuses (§11, §2.2).
func TestWalk_ResolveExtendsFailsClosedOnAnAnchoredReference(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    "shared/parent/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: parent\n---\n\nparent body\n",
		},
		testharness.WriteTreeOption{
			Path: "team/child/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 2.0.0\ndescription: child\n" +
				"extends: &p shared/parent\nnote: *p\n---\n\nchild body\n",
		},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := reg.Walk(WalkOptions{CollisionPolicy: CollisionPolicyHighestWins, ResolveExtends: true})
	if !errors.Is(err, manifest.ErrUnhidableParent) {
		t.Fatalf("Walk err = %v, want ErrUnhidableParent", err)
	}
	if got != nil {
		t.Errorf("a failed walk returned records: %v", idsOf(got))
	}
}
