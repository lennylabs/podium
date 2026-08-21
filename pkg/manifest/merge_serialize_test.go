package manifest_test

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/pkg/manifest"
)

// Spec: §4.6 — the omitted-field rule makes an extension type's own
// frontmatter keys inheritable, so the merged block carries the parent's
// undeclared keys alongside the child's, and a key both blocks set keeps the
// child's value because the child's block is restored last.
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

// Spec: §4.6 hidden parents — a restored key can carry the parent's ID under a
// name of its own. A YAML merge key is the case the parser acts on, so the
// assembled block resolves an extends value again and the helper refuses it.
func TestSerializeMerged_MergeKeyRestoresTheParentAndFailsClosed(t *testing.T) {
	t.Parallel()
	child := []byte("---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
		"base: &b\n  extends: shared/parent@1.x\n<<: *b\n---\n\nchild body\n")

	out, err := manifest.SerializeMerged(&manifest.Artifact{
		Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
		Extends: "shared/parent@1.x", Body: "child body",
	}, child)
	if !errors.Is(err, manifest.ErrUnhidableParent) {
		t.Fatalf("err = %v, want ErrUnhidableParent", err)
	}
	if out != nil {
		t.Errorf("a block that names the parent must not be returned: %s", out)
	}
}

// Spec: §4.6 hidden parents — a restored value that aliases an anchor the
// typed serialization does not reproduce leaves a dangling alias, so the
// assembled block does not read back as a mapping. The helper fails closed on
// a block it cannot verify rather than serving it.
func TestSerializeMerged_DanglingAliasFailsClosed(t *testing.T) {
	t.Parallel()
	child := []byte("---\ntype: agent\nversion: 2.0.0\ndescription: &d child\n" +
		"x_note: *d\nextends: shared/parent@1.x\n---\n\nchild body\n")

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

// An authored block the helper cannot read contributes no keys, which leaves
// the typed serialization as the whole answer rather than failing the render.
func TestSerializeMerged_UnreadableAuthoredBlockContributesNothing(t *testing.T) {
	t.Parallel()
	out, err := manifest.SerializeMerged(&manifest.Artifact{
		Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
		Extends: "shared/parent@1.x", Body: "child body",
	},
		[]byte("no frontmatter at all\n"),
		[]byte("---\n- a\n- b\n---\n\nsequence, not a mapping\n"),
		[]byte("---\nkey: [unterminated\n---\n\nbroken yaml\n"),
	)
	if err != nil {
		t.Fatalf("SerializeMerged: %v", err)
	}
	fm, body, err := manifest.SplitFrontmatter(out)
	if err != nil {
		t.Fatalf("SplitFrontmatter: %v", err)
	}
	got := decodeMapping(t, fm)
	if got["description"] != "child" {
		t.Errorf("description = %v, want %q\n%s", got["description"], "child", fm)
	}
	if strings.TrimSpace(string(body)) != "child body" {
		t.Errorf("body = %q, want the child's own", body)
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
