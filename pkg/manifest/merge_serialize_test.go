package manifest_test

import (
	"errors"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/pkg/manifest"
)

// The helper restores the undeclared keys of every authored block in the order
// it is given them, so a key both blocks set keeps the later block's value.
// The extends resolvers pass the chain parent first, which makes that the
// child's. The served-surface claim this supports is pinned in
// pkg/registry/core/extends_merged_frontmatter_test.go.
func TestSerializeMerged_RestoresUndeclaredKeysParentFirst(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: agent\nversion: 1.0.0\ndescription: parent\n" +
		"x_review_board: platform\nx_owner: platform\n---\n\nparent body\n"
	child := "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
		"x_owner: finance\nextends: shared/parent@1.x\n---\n\nchild body\n"

	out, err := manifest.SerializeMerged(&manifest.Artifact{
		Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
		Extends: "shared/parent@1.x", Body: "child body",
	}, block(parent, ""), block(child, "shared/parent@1.x"))
	if err != nil {
		t.Fatalf("SerializeMerged: %v", err)
	}
	fm, _, err := manifest.SplitFrontmatter(out)
	if err != nil {
		t.Fatalf("SplitFrontmatter: %v", err)
	}
	got := decodeMapping(t, fm)
	for key, want := range map[string]string{
		"x_review_board": "platform",
		"x_owner":        "finance",
		"description":    "child",
	} {
		if got[key] != want {
			t.Errorf("%s = %v, want %q\n%s", key, got[key], want, fm)
		}
	}
	if _, named := got["extends"]; named {
		t.Errorf("the merged block still names the parent:\n%s", fm)
	}
}

// Spec: §4.6 omitted fields. A child that sets an undeclared key to an empty
// scalar inherits the parent's value, under both spellings of an empty entry,
// which is the rule MergeExtends applies to every declared field.
func TestSerializeMerged_EmptyChildValueInheritsTheParents(t *testing.T) {
	t.Parallel()
	for name, authored := range map[string]string{
		"null scalar":  "x_owner:",
		"empty string": `x_owner: ""`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			parent := "---\ntype: agent\nversion: 1.0.0\ndescription: parent\n" +
				"x_owner: platform\n---\n\nparent body\n"
			child := "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
				authored + "\nextends: shared/parent@1.x\n---\n\nchild body\n"

			out, err := manifest.SerializeMerged(&manifest.Artifact{
				Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
				Extends: "shared/parent@1.x", Body: "child body",
			}, block(parent, ""), block(child, "shared/parent@1.x"))
			if err != nil {
				t.Fatalf("SerializeMerged: %v", err)
			}
			fm, _, err := manifest.SplitFrontmatter(out)
			if err != nil {
				t.Fatalf("SplitFrontmatter: %v", err)
			}
			if got := decodeMapping(t, fm); got["x_owner"] != "platform" {
				t.Errorf("x_owner = %v, want %q\n%s", got["x_owner"], "platform", fm)
			}
		})
	}
}

// A chain member that carries an empty extends entry names no parent, so the
// empty string is not recorded as one and a block holding an empty value is
// still served.
func TestSerializeMerged_EmptyExtendsEntryNamesNoParent(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: agent\nversion: 1.0.0\ndescription: parent\n" +
		"extends:\nx_owner: platform\n---\n\nparent body\n"
	child := "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
		"x_note:\nextends: shared/parent@1.x\n---\n\nchild body\n"

	out, err := manifest.SerializeMerged(&manifest.Artifact{
		Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
		Extends: "shared/parent@1.x", Body: "child body",
	}, block(parent, ""), block(child, "shared/parent@1.x"))
	if err != nil {
		t.Fatalf("SerializeMerged: %v", err)
	}
	fm, _, err := manifest.SplitFrontmatter(out)
	if err != nil {
		t.Fatalf("SplitFrontmatter: %v", err)
	}
	if got := decodeMapping(t, fm); got["x_owner"] != "platform" {
		t.Errorf("x_owner = %v, want %q\n%s", got["x_owner"], "platform", fm)
	}
}

// Spec: §4.6 hidden parents. The assembled block must resolve no extends value
// and must read back at all, and an authored block can defeat either through an
// anchor or a merge key. Each case here is a load the closed round-trip serves
// today, because it dropped every undeclared key along with the mapping that
// carried the reference; preserving the keys §4.6 makes inheritable costs this
// input class its load.
func TestSerializeMerged_UnrewritableBlockFailsClosed(t *testing.T) {
	t.Parallel()
	const leaf = "---\ntype: agent\nversion: 2.0.0\ndescription: child\n"
	cases := map[string]string{
		// The alias into the anchored extends value strands once the entry is
		// deleted, so the assembled block is not readable YAML.
		"alias into the anchored extends": leaf +
			"extends: &p shared/parent@1.x\nnote: *p\n---\n\nchild body\n",
		// The typed serialization re-emits a declared key without the anchor it
		// carried, so an alias into one strands the same way.
		"alias into an anchored declared key": "---\ntype: agent\nversion: 2.0.0\n" +
			"description: &d child\nx_note: *d\nextends: shared/parent@1.x\n---\n\nchild body\n",
		// The merge key restores an operative extends into the assembled block.
		"merge key restoring extends": leaf +
			"base: &b\n  extends: shared/parent@1.x\n<<: *b\n---\n\nchild body\n",
		// An anchor that contains itself cannot be read back as a mapping, so
		// the block cannot be verified.
		"self-referential anchor": leaf +
			"x_self: &s\n  k: *s\nextends: shared/parent@1.x\n---\n\nchild body\n",
	}
	for name, authored := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := serializeChild([]manifest.MergedBlock{block(authored, "shared/parent@1.x")})
			if !errors.Is(err, manifest.ErrUnhidableParent) {
				t.Fatalf("err = %v, want ErrUnhidableParent", err)
			}
			if out != nil {
				t.Errorf("a block that could not be verified must not be returned: %s", out)
			}
		})
	}
}

// Spec: §4.6 hidden parents. The served block carries a chain parent's ID under
// no key, so a restored key whose value references a parent is refused whichever
// block in the chain authored it. The reference is compared by canonical ID, so
// the pin either side happens to carry does not decide the outcome, and the
// value is found at any nesting depth.
func TestSerializeMerged_RestoredKeyReferencingTheParentFailsClosed(t *testing.T) {
	t.Parallel()
	const leaf = "---\ntype: agent\nversion: 2.0.0\ndescription: child\nextends: shared/parent@1.x\n---\n\nchild body\n"
	parent := func(keys string) string {
		return "---\ntype: agent\nversion: 1.0.0\ndescription: parent\n" + keys + "---\n\nparent body\n"
	}
	cases := map[string]struct {
		extends string
		chain   []manifest.MergedBlock
	}{
		"literal id under another key": {"shared/parent@1.x", []manifest.MergedBlock{
			block(parent("x_base: shared/parent\n"), ""), block(leaf, "shared/parent@1.x")}},
		// §4.6's guarantee is unconditional, so the key the leaf itself
		// authored takes the same test as one restored from a block above it.
		"literal id the leaf authored": {"shared/parent@1.x", []manifest.MergedBlock{
			block("---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+
				"x_base: shared/parent\nextends: shared/parent@1.x\n---\n\nchild body\n",
				"shared/parent@1.x")}},
		"id inside a restored list": {"shared/parent@1.x", []manifest.MergedBlock{
			block(parent("x_bases: [shared/other, shared/parent]\n"), ""), block(leaf, "shared/parent@1.x")}},
		"id inside a restored mapping": {"shared/parent@1.x", []manifest.MergedBlock{
			block(parent("x_meta:\n  base: shared/parent\n"), ""), block(leaf, "shared/parent@1.x")}},
		"id as a mapping key": {"shared/parent@1.x", []manifest.MergedBlock{
			block(parent("x_meta:\n  shared/parent: base\n"), ""), block(leaf, "shared/parent@1.x")}},
		"id under a pin the extends entry does not carry": {"shared/parent@1.x", []manifest.MergedBlock{
			block(parent("x_base: shared/parent@2.0.0\n"), ""), block(leaf, "shared/parent@1.x")}},
		"pinned id against an unpinned extends": {"shared/parent", []manifest.MergedBlock{
			block(parent("x_base: shared/parent@1.0.0\n"), ""),
			block("---\ntype: agent\nversion: 2.0.0\ndescription: child\nextends: shared/parent\n---\n\nchild body\n",
				"shared/parent")}},
		// The grandparent's ID reaches the served block under the middle
		// member's own key. The reference comes from the member beside its
		// block, so the refusal does not depend on the block still spelling
		// the reference out.
		"grandparent id restored from a chain block": {"shared/parent@1.0.0", []manifest.MergedBlock{
			block("---\ntype: agent\nversion: 1.0.0\ndescription: grandparent\n---\n\ngp body\n", ""),
			block("---\ntype: agent\nversion: 1.0.0\ndescription: parent\n"+
				"x_ref: shared/gp\n---\n\nparent body\n", "shared/gp@1.0.0"),
			block("---\ntype: agent\nversion: 2.0.0\ndescription: child\n---\n\nchild body\n", "shared/parent@1.0.0")}},
		"grandparent id nested in a chain block": {"shared/parent@1.0.0", []manifest.MergedBlock{
			block("---\ntype: agent\nversion: 1.0.0\ndescription: parent\n"+
				"x_meta:\n  bases: [shared/other, shared/gp@2.0.0]\n---\n\nparent body\n", "shared/gp@1.0.0"),
			block("---\ntype: agent\nversion: 2.0.0\ndescription: child\n---\n\nchild body\n", "shared/parent@1.0.0")}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := manifest.SerializeMerged(&manifest.Artifact{
				Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
				Extends: tc.extends, Body: "child body",
			}, tc.chain...)
			if !errors.Is(err, manifest.ErrUnhidableParent) {
				t.Fatalf("err = %v, want ErrUnhidableParent", err)
			}
			if out != nil {
				t.Errorf("a block that names the parent must not be returned: %s", out)
			}
		})
	}
}

// Spec: §4.6 hidden parents. A child may extend its own canonical ID, and the
// parent is then the row below it in layer order. That row is still a chain
// parent the requester may be unable to see, so the disclosure test applies to
// it as it does to every other chain reference and the load fails closed.
func TestSerializeMerged_SameIDOverlayFailsClosed(t *testing.T) {
	t.Parallel()
	base := "---\ntype: agent\nversion: 1.0.0\ndescription: base\n" +
		"x_owner: shared/base\n---\n\nbase body\n"
	overlay := "---\ntype: agent\nversion: 2.0.0\ndescription: overlay\n" +
		"extends: shared/base@1.x\n---\n\noverlay body\n"

	out, err := manifest.SerializeMerged(&manifest.Artifact{
		Type: manifest.TypeAgent, Version: "2.0.0", Description: "overlay",
		Extends: "shared/base@1.x", Body: "overlay body",
	}, block(base, ""), block(overlay, "shared/base@1.x"))
	if !errors.Is(err, manifest.ErrUnhidableParent) {
		t.Fatalf("err = %v, want ErrUnhidableParent", err)
	}
	if out != nil {
		t.Errorf("a block that names the parent must not be returned: %s", out)
	}
}

// Spec: §4.6 hidden parents. A value that is not a reference to the parent is
// served whichever block authored it. The disclosure test compares canonical
// IDs, so a longer identifier, a path below the parent, and prose that quotes
// the reference are all values §4.6's omitted-field rule makes inheritable, and
// refusing them would cost the child the whole load.
func TestSerializeMerged_ValuesThatAreNotReferencesAreServed(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"longer word":                       "shared/parent-legacy",
		"identifier continuing past the id": "shared/parenthetical",
		"path below the parent":             "shared/parent/README.md",
		"prose quoting the reference":       "see shared/parent@1.x for details",
		"id under a longer path":            "docs/shared/parent",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			forEachOrigin(t, value, func(t *testing.T, chain []manifest.MergedBlock) {
				out, err := serializeChild(chain)
				if err != nil {
					t.Fatalf("SerializeMerged: %v", err)
				}
				fm, _, err := manifest.SplitFrontmatter(out)
				if err != nil {
					t.Fatalf("SplitFrontmatter: %v", err)
				}
				if got := decodeMapping(t, fm); got["x_note"] != value {
					t.Errorf("x_note = %v, want %q\n%s", got["x_note"], value, fm)
				}
			})
		})
	}
}

// A chain block the helper cannot read back as a mapping contributes no keys,
// and the merged artifact is still served through the typed serialization.
// Neither extends resolver reaches this arm, because both pass blocks a parser
// has already accepted.
func TestSerializeMerged_UnreadableChainBlockContributesNoKeys(t *testing.T) {
	t.Parallel()
	for name, authored := range map[string]string{
		"no frontmatter delimiters": "parent body\n",
		"header that is not YAML":   "---\n\tx: [\n---\n\nparent body\n",
		"header that is a sequence": "---\n- one\n- two\n---\n\nparent body\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			child := "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
				"extends: shared/parent@1.x\n---\n\nchild body\n"
			out, err := serializeChild([]manifest.MergedBlock{
				block(authored, ""), block(child, "shared/parent@1.x")})
			if err != nil {
				t.Fatalf("SerializeMerged: %v", err)
			}
			fm, _, err := manifest.SplitFrontmatter(out)
			if err != nil {
				t.Fatalf("SplitFrontmatter: %v", err)
			}
			if got := decodeMapping(t, fm); got["description"] != "child" {
				t.Errorf("description = %v, want %q\n%s", got["description"], "child", fm)
			}
		})
	}
}

// forEachOrigin runs check over the two chains that put value on x_note: one
// where the leaf authored the key, and one where the key is restored from the
// parent's block. A value that is no reference to the parent is served from
// either origin, so every case that states it runs on both.
func forEachOrigin(t *testing.T, value string, check func(*testing.T, []manifest.MergedBlock)) {
	t.Helper()
	for origin, chain := range map[string][]manifest.MergedBlock{
		"leaf authored the key": {block("---\ntype: agent\nversion: 2.0.0\n"+
			"description: child\nx_note: "+value+"\nextends: shared/parent@1.x\n"+
			"---\n\nchild body\n", "shared/parent@1.x")},
		"restored from the parent": {
			block("---\ntype: agent\nversion: 1.0.0\ndescription: parent\n"+
				"x_note: "+value+"\n---\n\nparent body\n", ""),
			block("---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+
				"extends: shared/parent@1.x\n---\n\nchild body\n", "shared/parent@1.x"),
		},
	} {
		t.Run(origin, func(t *testing.T) {
			t.Parallel()
			check(t, chain)
		})
	}
}

// serializeChild renders the merged artifact for the child the origin chains
// describe, which is the same artifact in every case.
func serializeChild(chain []manifest.MergedBlock) ([]byte, error) {
	return manifest.SerializeMerged(&manifest.Artifact{
		Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
		Extends: "shared/parent@1.x", Body: "child body",
	}, chain...)
}

// FrontmatterHidingParent rewrites the record's own authored block and merges
// nothing, so the parent-ID test the merge needs does not apply to it. A
// sibling key the parser never resolves is the child's own text and survives
// with the rest of the block. Spec: §4.6 hidden parents. The served-surface
// claim is pinned in pkg/registry/core/extends_test.go.
func TestFrontmatterHidingParent_SiblingKeyNamingTheParentSurvives(t *testing.T) {
	t.Parallel()
	child := "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
		"x_base: shared/parent\nextends: shared/parent@1.x\n---\n\nchild body\n"

	fm, _, err := manifest.SplitFrontmatter([]byte(manifest.FrontmatterHidingParent([]byte(child))))
	if err != nil {
		t.Fatalf("SplitFrontmatter: %v", err)
	}
	got := decodeMapping(t, fm)
	if got["x_base"] != "shared/parent" {
		t.Errorf("x_base = %v, want %q", got["x_base"], "shared/parent")
	}
	if _, named := got["extends"]; named {
		t.Errorf("the descriptor block still names the parent under extends: %+v", got)
	}
}

// block pairs an authored frontmatter block with the extends reference its
// manifest declares, which is what SerializeMerged reads a chain member as.
func block(frontmatter, extends string) manifest.MergedBlock {
	return manifest.MergedBlock{Frontmatter: []byte(frontmatter), Extends: extends}
}

// decodeMapping reads a frontmatter block back as a YAML mapping, which is how
// a consumer reads the served block.
func decodeMapping(t *testing.T, fm []byte) map[string]any {
	t.Helper()
	var got map[string]any
	if err := yaml.Unmarshal(fm, &got); err != nil {
		t.Fatalf("decode frontmatter: %v\n%s", err, fm)
	}
	return got
}
