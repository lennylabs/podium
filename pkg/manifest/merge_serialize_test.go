package manifest_test

import (
	"errors"
	"strings"
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
	}, childID, block(parent, ""), block(child, "shared/parent@1.x"))
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
			}, childID, block(parent, ""), block(child, "shared/parent@1.x"))
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
	}, childID, block(parent, ""), block(child, "shared/parent@1.x"))
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
// anchor or a merge key. This is the one input class that loses a load the
// closed round-trip serves today, because that round-trip dropped every
// undeclared key along with the mapping or the alias carrying the reference.
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
		// The mapping the merge key aliases spells the parent's ID, and it
		// would otherwise restore an operative extends into the assembled
		// block, so both gates refuse it.
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

// Spec: §4.6 hidden parents. A key restored from an ancestor whose name or
// value names a chain parent fails the merge, at whatever depth the ID sits.
// Dropping the key instead would serve an inheritable key as nothing, which
// §4.6's omitted-field rule does not allow and which no consumer can tell from
// a key the chain never set.
func TestSerializeMerged_InheritedKeyNamingTheParentFailsClosed(t *testing.T) {
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
		"id inside a restored list": {"shared/parent@1.x", []manifest.MergedBlock{
			block(parent("x_bases: [shared/other, shared/parent]\n"), ""), block(leaf, "shared/parent@1.x")}},
		"id inside a restored mapping": {"shared/parent@1.x", []manifest.MergedBlock{
			block(parent("x_meta:\n  base: shared/parent\n"), ""), block(leaf, "shared/parent@1.x")}},
		"id as a mapping key": {"shared/parent@1.x", []manifest.MergedBlock{
			block(parent("x_meta:\n  shared/parent: base\n"), ""), block(leaf, "shared/parent@1.x")}},
		// The restored key's own name is the one node the assembler writes
		// rather than walks, so a parent that names a frontmatter key for its
		// own canonical ID would otherwise be served verbatim.
		"id as a top-level key": {"shared/parent@1.x", []manifest.MergedBlock{
			block(parent("shared/parent: base\n"), ""), block(leaf, "shared/parent@1.x")}},
		"id under a pin the extends entry does not carry": {"shared/parent@1.x", []manifest.MergedBlock{
			block(parent("x_base: shared/parent@2.0.0\n"), ""), block(leaf, "shared/parent@1.x")}},
		"pinned id against an unpinned extends": {"shared/parent", []manifest.MergedBlock{
			block(parent("x_base: shared/parent@1.0.0\n"), ""),
			block("---\ntype: agent\nversion: 2.0.0\ndescription: child\nextends: shared/parent\n---\n\nchild body\n",
				"shared/parent")}},
		// The grandparent's ID reaches the served block under the middle
		// member's own key. The reference comes from the member beside its
		// block, so the outcome does not depend on the block still spelling
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
			}, childID, tc.chain...)
			assertUnhidable(t, out, err)
		})
	}
}

// Spec: §4.6 hidden parents. The declared fields the typed serialization writes
// are part of the served block, so a value that names a chain parent fails the
// merge under a declared key on the same terms as under a restored one. The
// deprecation pointer of a child deprecated in favour of the artifact it extends
// is the §4.4 spelling of that, and the description a child inherits from a
// parent that quotes its own ID is the spelling the child never authored.
func TestSerializeMerged_DeclaredFieldNamingTheParentFailsClosed(t *testing.T) {
	t.Parallel()
	cases := map[string]*manifest.Artifact{
		"the leaf's own replaced_by pointer": {
			Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
			Extends: "shared/parent@1.x", Deprecated: true, ReplacedBy: "shared/parent",
			Body: "child body",
		},
		"a description inherited from the parent": {
			Type: manifest.TypeAgent, Version: "2.0.0", Description: "the shared/parent baseline",
			Extends: "shared/parent@1.x", Body: "child body",
		},
	}
	for name, merged := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := manifest.SerializeMerged(merged, childID,
				block("---\ntype: agent\nversion: 1.0.0\ndescription: the shared/parent baseline\n"+
					"---\n\nparent body\n", ""),
				block("---\ntype: agent\nversion: 2.0.0\nextends: shared/parent@1.x\n---\n\nchild body\n",
					"shared/parent@1.x"))
			assertUnhidable(t, out, err)
		})
	}
}

// Spec: §4.6 hidden parents, §4.6 collisions. The parent of a same-ID overlay
// is the row below it in layer order, in a layer the requester may not be able
// to see. The ID itself is the one the requester supplied, so a value spelling
// it discloses nothing and is served, while a pin on it names a version the
// requester was not served and so reports that a second row exists.
func TestSerializeMerged_SameIDOverlayHidesOnlyTheLowerRowsExtras(t *testing.T) {
	t.Parallel()
	overlay := "---\ntype: agent\nversion: 2.0.0\ndescription: overlay\n" +
		"extends: shared/base@1.x\n---\n\noverlay body\n"
	for name, tc := range map[string]struct {
		keys   string
		served bool
	}{
		"the bare canonical id":       {"x_owner: shared/base\n", true},
		"a path below its own id":     {"x_owner: shared/base/README.md\n", true},
		"prose quoting its own id":    {"x_owner: see shared/base for details\n", true},
		"a pinned reference":          {"x_owner: shared/base@1.0.0\n", false},
		"a pin inside a nested value": {"x_owner:\n  ref: shared/base@1.0.0\n", false},
		"a nested extends entry":      {"base_ref:\n  extends: shared/base@1.0.0\n", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			base := "---\ntype: agent\nversion: 1.0.0\ndescription: base\n" + tc.keys + "---\n\nbase body\n"
			out, err := manifest.SerializeMerged(&manifest.Artifact{
				Type: manifest.TypeAgent, Version: "2.0.0", Description: "overlay",
				Extends: "shared/base@1.x", Body: "overlay body",
			}, "shared/base", block(base, ""), block(overlay, "shared/base@1.x"))
			if !tc.served {
				assertUnhidable(t, out, err)
				return
			}
			if err != nil {
				t.Fatalf("SerializeMerged: %v", err)
			}
			fm, _, err := manifest.SplitFrontmatter(out)
			if err != nil {
				t.Fatalf("SplitFrontmatter: %v", err)
			}
			if got := decodeMapping(t, fm); got["x_owner"] == nil {
				t.Errorf("the overlay lost the key it inherits from the row below:\n%s", fm)
			}
		})
	}
}

// Spec: §4.6 omitted fields. The disclosure test fires at an identifier
// boundary, so an artifact whose ID continues past the parent's and an ID that
// ends with the parent's are inherited from either origin. Scoping the test
// this way is what keeps §4.6's omitted-field rule working for ordinary
// extension keys.
func TestSerializeMerged_ValuesNamingAnotherArtifactAreServed(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"longer word":                       "shared/parent-legacy",
		"identifier continuing past the id": "shared/parenthetical",
		"id under a longer path":            "docs/shared/parent.md",
		"id as a trailing path segment":     "team/shared/parent@2.0.0",
		"identifier ending with the id":     "xshared/parent",
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

// parentNamingValues are the spellings that hand the requester the parent's ID
// together with the evidence that the artifact exists, whether the ID stands
// alone, heads a path, carries a pin, or sits inside prose.
var parentNamingValues = map[string]string{
	"the bare id":                 "shared/parent",
	"a pinned reference":          "shared/parent@2.0.0",
	"path below the parent":       "shared/parent/CHARTER.md",
	"pinned prose":                "see shared/parent@1.x for details",
	"pin-free prose":              "see shared/parent for details",
	"id before a comma":           "shared/parent, and others",
	"id at the end of a sentence": "owner is shared/parent.",
	"quoted id":                   `the base is "shared/parent"`,
}

// Spec: §4.6 hidden parents. A value spelling the parent's ID fails the merge
// under every spelling the disclosure test recognizes, and from either origin.
// §4.6 constrains the block the registry serves, so a leaf that spells its
// parent's ID under a sibling key discloses what a parent that spells it
// discloses.
func TestSerializeMerged_ValuesSpellingTheParentFailClosed(t *testing.T) {
	t.Parallel()
	for name, value := range parentNamingValues {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			forEachOrigin(t, value, func(t *testing.T, chain []manifest.MergedBlock) {
				out, err := serializeChild(chain)
				assertUnhidable(t, out, err)
			})
		})
	}
}

// Spec: §4.6 hidden parents. A restored node comes out of the decoder with the
// comments its author wrote, and yaml.Marshal re-emits them, so the assembler
// clears them. A parent's comment naming the parent would otherwise reach a
// requester who cannot see the parent's layer, under a key whose value carries
// nothing of the sort.
func TestSerializeMerged_RestoredKeysCarryNoComments(t *testing.T) {
	t.Parallel()
	for name, keys := range map[string]string{
		"line comment": "x_owner: platform # inherited from shared/parent\n",
		"head comment": "# owned by shared/parent\nx_owner: platform\n",
		"comment inside a nested value": "x_meta:\n" +
			"  team: platform # see shared/parent\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := serializeChild([]manifest.MergedBlock{
				block("---\ntype: agent\nversion: 1.0.0\ndescription: parent\n"+keys+
					"---\n\nparent body\n", ""),
				block("---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+
					"extends: shared/parent@1.x\n---\n\nchild body\n", "shared/parent@1.x")})
			if err != nil {
				t.Fatalf("SerializeMerged: %v", err)
			}
			assertNamesNoParent(t, out, "shared/parent")
			if strings.Contains(string(out), "#") {
				t.Errorf("the merged block carries an author's comment:\n%s", out)
			}
		})
	}
}

// assertUnhidable requires that the merge failed with ErrUnhidableParent and
// returned no block. A caller maps the error to registry.invalid_argument, and
// returning a block beside the error would let a caller that ignores it serve
// what §4.6 hides.
func assertUnhidable(t *testing.T, out []byte, err error) {
	t.Helper()
	if !errors.Is(err, manifest.ErrUnhidableParent) {
		t.Fatalf("err = %v, want ErrUnhidableParent\n%s", err, out)
	}
	if out != nil {
		t.Errorf("a block that names the parent must not be returned:\n%s", out)
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

// assertNamesNoParent requires that out reads back as a frontmatter block whose
// bytes carry no mention of hidden, which is the property §4.6 states about the
// block the registry serves.
func assertNamesNoParent(t *testing.T, out []byte, hidden string) {
	t.Helper()
	fm, _, err := manifest.SplitFrontmatter(out)
	if err != nil {
		t.Fatalf("SplitFrontmatter: %v\n%s", err, out)
	}
	got := decodeMapping(t, fm)
	if _, named := got["extends"]; named {
		t.Errorf("the merged block still names the parent under extends:\n%s", fm)
	}
	if strings.Contains(string(fm), hidden) {
		t.Errorf("the merged block names the hidden parent %q:\n%s", hidden, fm)
	}
}

// forEachOrigin runs check over the two chains that put value on x_note: one
// where the leaf authored the key, and one where the key is restored from the
// parent's block. §4.6 constrains what the served block carries and says
// nothing about which member of the chain wrote it, so both origins reach the
// same outcome and every case runs on both.
func forEachOrigin(t *testing.T, value string, check func(*testing.T, []manifest.MergedBlock)) {
	t.Helper()
	for origin, chain := range map[string][]manifest.MergedBlock{
		"leaf authored the key":    leafChain(value),
		"restored from the parent": inheritedChain(value),
	} {
		t.Run(origin, func(t *testing.T) {
			t.Parallel()
			check(t, chain)
		})
	}
}

// leafChain puts value on x_note in the leaf's own block.
func leafChain(value string) []manifest.MergedBlock {
	return []manifest.MergedBlock{block("---\ntype: agent\nversion: 2.0.0\n"+
		"description: child\nx_note: "+yamlScalar(value)+"\nextends: shared/parent@1.x\n"+
		"---\n\nchild body\n", "shared/parent@1.x")}
}

// inheritedChain puts value on x_note in the parent's block, which the leaf
// inherits under §4.6's omitted-field rule.
func inheritedChain(value string) []manifest.MergedBlock {
	return []manifest.MergedBlock{
		block("---\ntype: agent\nversion: 1.0.0\ndescription: parent\n"+
			"x_note: "+yamlScalar(value)+"\n---\n\nparent body\n", ""),
		block("---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+
			"extends: shared/parent@1.x\n---\n\nchild body\n", "shared/parent@1.x"),
	}
}

// yamlScalar quotes value so a case that carries a comma, a quote, or a
// leading indicator still authors one scalar.
func yamlScalar(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// childID is the canonical ID of the child every chain in this file describes,
// which is the ID the requester supplied to read it.
const childID = "finance/child"

// serializeChild renders the merged artifact for the child the origin chains
// describe, which is the same artifact in every case.
func serializeChild(chain []manifest.MergedBlock) ([]byte, error) {
	return manifest.SerializeMerged(&manifest.Artifact{
		Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
		Extends: "shared/parent@1.x", Body: "child body",
	}, childID, chain...)
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
