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

// A restored value that aliases an anchor declared on a key the typed
// serialization rewrites is resolved against the block that declared it, so
// the child keeps a load it gets today. The refusal stays scoped to a block
// that names the parent or cannot be read back.
func TestSerializeMerged_RestoredAliasResolvesToItsTarget(t *testing.T) {
	t.Parallel()
	child := "---\ntype: agent\nversion: 2.0.0\ndescription: &d child\n" +
		"x_note: *d\nextends: shared/parent@1.x\n---\n\nchild body\n"

	out, err := manifest.SerializeMerged(&manifest.Artifact{
		Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
		Extends: "shared/parent@1.x", Body: "child body",
	}, block(child, "shared/parent@1.x"))
	if err != nil {
		t.Fatalf("SerializeMerged: %v", err)
	}
	fm, _, err := manifest.SplitFrontmatter(out)
	if err != nil {
		t.Fatalf("SplitFrontmatter: %v", err)
	}
	if got := decodeMapping(t, fm); got["x_note"] != "child" {
		t.Errorf("x_note = %v, want %q\n%s", got["x_note"], "child", fm)
	}
}

// A restored key that carries a chain parent's ID is refused whether the ID
// arrives through an alias or is spelled out, and whatever pin either side
// carries. §4.6's guarantee covers the parent's ID, so the pin the child
// happened to type does not decide whether the ID is served. A value restored
// from a block above the leaf is refused for mentioning the ID at all.
func TestSerializeMerged_RestoredParentIDFailsClosed(t *testing.T) {
	t.Parallel()
	const leaf = "---\ntype: agent\nversion: 2.0.0\ndescription: child\n"
	cases := map[string]struct {
		extends string
		chain   []manifest.MergedBlock
	}{
		"alias to the anchored extends": {"shared/parent@1.x", []manifest.MergedBlock{
			block(leaf+"extends: &p shared/parent@1.x\nnote: *p\n---\n\nchild body\n", "shared/parent@1.x")}},
		"literal id under another key": {"shared/parent@1.x", []manifest.MergedBlock{
			block(leaf+"x_base: shared/parent\nextends: shared/parent@1.x\n---\n\nchild body\n", "shared/parent@1.x")}},
		"merge key restoring extends": {"shared/parent@1.x", []manifest.MergedBlock{
			block(leaf+"base: &b\n  extends: shared/parent@1.x\n<<: *b\n---\n\nchild body\n", "shared/parent@1.x")}},
		"id inside a restored list": {"shared/parent@1.x", []manifest.MergedBlock{
			block(leaf+"x_bases: [shared/other, shared/parent]\nextends: shared/parent@1.x\n---\n\nchild body\n", "shared/parent@1.x")}},
		"id inside a restored mapping": {"shared/parent@1.x", []manifest.MergedBlock{
			block(leaf+"x_meta:\n  base: shared/parent\nextends: shared/parent@1.x\n---\n\nchild body\n", "shared/parent@1.x")}},
		"id under a non-string key": {"shared/parent@1.x", []manifest.MergedBlock{
			block(leaf+"x_meta:\n  1: shared/parent\nextends: shared/parent@1.x\n---\n\nchild body\n", "shared/parent@1.x")}},
		"id under a pin the extends entry does not carry": {"shared/parent@1.x", []manifest.MergedBlock{
			block(leaf+"x_base: shared/parent@2.0.0\nextends: shared/parent@1.x\n---\n\nchild body\n", "shared/parent@1.x")}},
		"pinned id against an unpinned extends": {"shared/parent", []manifest.MergedBlock{
			block(leaf+"x_base: shared/parent@1.0.0\nextends: shared/parent\n---\n\nchild body\n", "shared/parent")}},
		// The grandparent's ID reaches the served block under the middle
		// member's own key. The reference comes from the member beside its
		// block, so the refusal does not depend on the block still spelling
		// the reference out.
		"grandparent id restored from a chain block": {"shared/parent@1.0.0", []manifest.MergedBlock{
			block("---\ntype: agent\nversion: 1.0.0\ndescription: grandparent\n---\n\ngp body\n", ""),
			block("---\ntype: agent\nversion: 1.0.0\ndescription: parent\n"+
				"x_ref: shared/gp\n---\n\nparent body\n", "shared/gp@1.0.0"),
			block(leaf+"---\n\nchild body\n", "shared/parent@1.0.0")}},
		// The ID sits inside a nested value of a chain block rather than at
		// the top level, which is as much of a disclosure.
		"grandparent id nested in a chain block": {"shared/parent@1.0.0", []manifest.MergedBlock{
			block("---\ntype: agent\nversion: 1.0.0\ndescription: parent\n"+
				"x_meta:\n  bases: [shared/other, shared/gp@2.0.0]\n---\n\nparent body\n", "shared/gp@1.0.0"),
			block(leaf+"---\n\nchild body\n", "shared/parent@1.0.0")}},
		// A chain block's value carries the ID inside longer text. The leaf
		// keeps that load, but this text is a block the requester may never
		// have been able to read, so serving it discloses the hidden parent.
		"chain block quoting the id in prose": {"shared/parent@1.0.0", []manifest.MergedBlock{
			block("---\ntype: agent\nversion: 1.0.0\ndescription: parent\n"+
				"x_note: see shared/gp@1.x for details\n---\n\nparent body\n", "shared/gp@1.0.0"),
			block(leaf+"---\n\nchild body\n", "shared/parent@1.0.0")}},
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

// On the leaf's own text the parent-ID test is an equality test on the
// canonical ID, so a value that only mentions the ID inside longer text
// carries no artifact reference and is served. Refusing it would cost the
// child a load it gets today, and the leaf's authored text reaches the
// requester in raw_frontmatter regardless.
func TestSerializeMerged_TextMentioningTheIDDoesNotNameTheParent(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"longer word":                 "shared/parenthetical",
		"path under the parent":       "shared/parent/README.md",
		"prose quoting the reference": "see shared/parent@1.x for details",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			child := "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
				"x_note: " + value + "\nextends: shared/parent@1.x\n---\n\nchild body\n"

			out, err := manifest.SerializeMerged(&manifest.Artifact{
				Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
				Extends: "shared/parent@1.x", Body: "child body",
			}, block(child, "shared/parent@1.x"))
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
	}
}

// A chain block's value that spells something other than the parent's ID is
// served. The wider test the chain's own text gets is bounded to whole tokens,
// so a longer identifier that starts or ends with the ID names a different
// artifact and costs the child nothing. Spec: §4.6 hidden parents.
func TestSerializeMerged_ChainTextNearTheIDIsServed(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"identifier continuing past the id": "shared/gpx",
		"id under a longer path":            "docs/shared/gp",
		"id joined by an underscore":        "shared/gp_legacy",
		"nested value naming another id":    "shared/other",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := manifest.SerializeMerged(&manifest.Artifact{
				Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
				Extends: "shared/parent@1.0.0", Body: "child body",
			},
				block("---\ntype: agent\nversion: 1.0.0\ndescription: parent\n"+
					"x_meta:\n  base: "+value+"\n---\n\nparent body\n", "shared/gp@1.0.0"),
				block("---\ntype: agent\nversion: 2.0.0\ndescription: child\n---\n\nchild body\n",
					"shared/parent@1.0.0"))
			if err != nil {
				t.Fatalf("SerializeMerged: %v", err)
			}
			fm, _, err := manifest.SplitFrontmatter(out)
			if err != nil {
				t.Fatalf("SplitFrontmatter: %v", err)
			}
			meta, ok := decodeMapping(t, fm)["x_meta"].(map[string]any)
			if !ok || meta["base"] != value {
				t.Errorf("x_meta.base = %v, want %q\n%s", meta["base"], value, fm)
			}
		})
	}
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

// A restored value whose anchor contains itself cannot be read back as a
// mapping, so the block cannot be checked for the parent's ID. The helper
// refuses it rather than serving what it could not verify.
func TestSerializeMerged_SelfReferentialAnchorFailsClosed(t *testing.T) {
	t.Parallel()
	child := "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
		"x_self: &s\n  k: *s\nextends: shared/parent@1.x\n---\n\nchild body\n"

	out, err := manifest.SerializeMerged(&manifest.Artifact{
		Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
		Extends: "shared/parent@1.x", Body: "child body",
	}, block(child, "shared/parent@1.x"))
	if !errors.Is(err, manifest.ErrUnhidableParent) {
		t.Fatalf("err = %v, want ErrUnhidableParent", err)
	}
	if out != nil {
		t.Errorf("a block that cannot be verified must not be returned: %s", out)
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
