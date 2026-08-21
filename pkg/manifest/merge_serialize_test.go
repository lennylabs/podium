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
	parent := []byte("---\ntype: agent\nversion: 1.0.0\ndescription: parent\n" +
		"x_review_board: platform\nx_owner: platform\n---\n\nparent body\n")
	child := []byte("---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
		"x_owner: finance\nextends: shared/parent@1.x\n---\n\nchild body\n")

	out, err := manifest.SerializeMerged(&manifest.Artifact{
		Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
		Extends: "shared/parent@1.x", Body: "child body",
	}, parent, child)
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
	parent := []byte("---\ntype: agent\nversion: 1.0.0\ndescription: parent\n" +
		"extends:\nx_owner: platform\n---\n\nparent body\n")
	child := []byte("---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
		"x_note:\nextends: shared/parent@1.x\n---\n\nchild body\n")

	out, err := manifest.SerializeMerged(&manifest.Artifact{
		Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
		Extends: "shared/parent@1.x", Body: "child body",
	}, parent, child)
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
	child := []byte("---\ntype: agent\nversion: 2.0.0\ndescription: &d child\n" +
		"x_note: *d\nextends: shared/parent@1.x\n---\n\nchild body\n")

	out, err := manifest.SerializeMerged(&manifest.Artifact{
		Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
		Extends: "shared/parent@1.x", Body: "child body",
	}, child)
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
// arrives through an alias or is spelled out, and whether it carries the
// authored pin or not.
func TestSerializeMerged_RestoredParentIDFailsClosed(t *testing.T) {
	t.Parallel()
	for name, child := range map[string]string{
		"alias to the anchored extends": "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
			"extends: &p shared/parent@1.x\nnote: *p\n---\n\nchild body\n",
		"literal id under another key": "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
			"x_base: shared/parent\nextends: shared/parent@1.x\n---\n\nchild body\n",
		"merge key restoring extends": "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
			"base: &b\n  extends: shared/parent@1.x\n<<: *b\n---\n\nchild body\n",
		"id inside a restored list": "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
			"x_bases: [shared/other, shared/parent]\nextends: shared/parent@1.x\n---\n\nchild body\n",
		"id inside a restored mapping": "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
			"x_meta:\n  base: shared/parent\nextends: shared/parent@1.x\n---\n\nchild body\n",
		"id under a non-string key": "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
			"x_meta:\n  1: shared/parent\nextends: shared/parent@1.x\n---\n\nchild body\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := manifest.SerializeMerged(&manifest.Artifact{
				Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
				Extends: "shared/parent@1.x", Body: "child body",
			}, []byte(child))
			if !errors.Is(err, manifest.ErrUnhidableParent) {
				t.Fatalf("err = %v, want ErrUnhidableParent", err)
			}
			if out != nil {
				t.Errorf("a block that names the parent must not be returned: %s", out)
			}
		})
	}
}

// A restored value whose anchor contains itself cannot be read back as a
// mapping, so the block cannot be checked for the parent's ID. The helper
// refuses it rather than serving what it could not verify.
func TestSerializeMerged_SelfReferentialAnchorFailsClosed(t *testing.T) {
	t.Parallel()
	child := []byte("---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
		"x_self: &s\n  k: *s\nextends: shared/parent@1.x\n---\n\nchild body\n")

	out, err := manifest.SerializeMerged(&manifest.Artifact{
		Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
		Extends: "shared/parent@1.x", Body: "child body",
	}, child)
	if !errors.Is(err, manifest.ErrUnhidableParent) {
		t.Fatalf("err = %v, want ErrUnhidableParent", err)
	}
	if out != nil {
		t.Errorf("a block that cannot be verified must not be returned: %s", out)
	}
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
