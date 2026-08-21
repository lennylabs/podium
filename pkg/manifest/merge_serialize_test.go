package manifest_test

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/pkg/manifest"
)

// childID is the canonical ID of the artifact every chain below assembles,
// which the helper needs so a same-ID overlay's parent is not mistaken for an
// ID the requester cannot already see.
const childID = "finance/child"

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
// value inherits the parent's value, under every spelling of an empty entry,
// which is the rule MergeExtends applies to every declared field. The null
// spellings are the ones a raw-text test misses: yaml.v3 keeps `null` and `~`
// as the node's value and marks them with the null tag, while ParseArtifact
// reduces all of them to a declared field's zero value.
func TestSerializeMerged_EmptyChildValueInheritsTheParents(t *testing.T) {
	t.Parallel()
	for name, authored := range map[string]string{
		"an omitted value":   "x_owner:",
		"an empty string":    `x_owner: ""`,
		"a null scalar":      "x_owner: null",
		"a tilde":            "x_owner: ~",
		"a capitalized null": "x_owner: Null",
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

// Spec: §4.6 omitted fields. §4.6 inherits the parent's value for a key the
// child omits or sets to an empty scalar, and gives every other field the
// child's value. A zero-length sequence or mapping is a value the child
// declared, so it is served and an inherited extension collection can be
// cleared.
func TestSerializeMerged_EmptyChildCollectionWins(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		parentKeys string
		authored   string
		want       any
	}{
		"an empty list": {"x_owner: [platform]", "x_owner: []", []any{}},
		"an empty mapping": {"x_owner:\n  team: platform", "x_owner: {}",
			map[string]any{}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			parent := "---\ntype: agent\nversion: 1.0.0\ndescription: parent\n" +
				tc.parentKeys + "\n---\n\nparent body\n"
			child := "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
				tc.authored + "\nextends: shared/parent@1.x\n---\n\nchild body\n"

			out, err := serializeChild([]manifest.MergedBlock{
				block(parent, ""), block(child, "shared/parent@1.x")})
			if err != nil {
				t.Fatalf("SerializeMerged: %v", err)
			}
			fm, _, err := manifest.SplitFrontmatter(out)
			if err != nil {
				t.Fatalf("SplitFrontmatter: %v", err)
			}
			if got := decodeMapping(t, fm); !reflect.DeepEqual(got["x_owner"], tc.want) {
				t.Errorf("x_owner = %#v, want %#v\n%s", got["x_owner"], tc.want, fm)
			}
		})
	}
}

// Spec: §4.6 omitted fields. An alias the child authored carries the value it
// points at, so it is a value the child set and the parent's is not restored
// over it. The restored keys keep the order the chain first saw them in, so the
// anchor is authored under a key the parent also sets and stays ahead of the
// alias into it.
func TestSerializeMerged_AliasedChildValueWins(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: agent\nversion: 1.0.0\ndescription: parent\n" +
		"x_team: platform\nx_owner: platform\n---\n\nparent body\n"
	child := "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
		"x_team: &t finance\nx_owner: *t\nextends: shared/parent@1.x\n---\n\nchild body\n"

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
	if got := decodeMapping(t, fm); got["x_owner"] != "finance" {
		t.Errorf("x_owner = %v, want %q\n%s", got["x_owner"], "finance", fm)
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

// Spec: §4.6 hidden parents. A leaf whose extends reference arrives through an
// alias or a merge key carries that reference under a second key, and the
// restore step brings it back into the assembled block. This is the one input
// class that loses a load the closed round-trip serves today, because that
// round-trip dropped every undeclared key along with the mapping or the alias
// carrying the reference.
func TestSerializeMerged_UnrewritableBlockFailsClosed(t *testing.T) {
	t.Parallel()
	const leaf = "---\ntype: agent\nversion: 2.0.0\ndescription: child\n"
	cases := map[string]string{
		// The alias expands to the value the extends entry carried, so the
		// restored key hands the requester the parent's ID.
		"alias into the anchored extends": leaf +
			"extends: &p shared/parent@1.x\nnote: *p\n---\n\nchild body\n",
		// The mapping the merge key aliases spells the parent's ID, and it
		// would otherwise restore an operative extends into the assembled
		// block, so both gates refuse it.
		"merge key restoring extends": leaf +
			"base: &b\n  extends: shared/parent@1.x\n<<: *b\n---\n\nchild body\n",
		// An anchor that contains itself cannot be expanded, so the key can be
		// neither checked against §4.6 nor inherited.
		"self-referential anchor": leaf +
			"x_self: &s\n  k: *s\nextends: shared/parent@1.x\n---\n\nchild body\n",
	}
	for name, authored := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := serializeChild([]manifest.MergedBlock{block(authored, "shared/parent@1.x")})
			assertUnhidable(t, out, err)
		})
	}
}

// Spec: §4.6 hidden parents. An anchor on the extends value alone is inert. The
// merged block is rebuilt from the merged fields, which carry no anchor, and no
// other key aliases the reference back into the block, so the helper serves the
// block and it names no parent. The refusal above is bounded to a reference some
// other key resolves, and every other anchored fixture pairs its anchor with an
// alias, so this case pins that boundary from the accepted side at the helper.
// TestExtendsFrontmatter_AnchoredExtendsWithoutAnAliasIsServed in
// pkg/registry/core pins the same input on the served LoadArtifactResult.
func TestSerializeMerged_AnchoredExtendsWithoutAnAliasIsServed(t *testing.T) {
	t.Parallel()
	authored := "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
		"extends: &p shared/parent@1.x\nx_owner: finance\n---\n\nchild body\n"

	out, err := serializeChild([]manifest.MergedBlock{block(authored, "shared/parent@1.x")})
	if err != nil {
		t.Fatalf("SerializeMerged: %v", err)
	}
	assertNamesNoParent(t, out, "shared/parent")
	fm, _, err := manifest.SplitFrontmatter(out)
	if err != nil {
		t.Fatalf("SplitFrontmatter: %v", err)
	}
	if got := decodeMapping(t, fm); got["x_owner"] != "finance" {
		t.Errorf("x_owner = %v, want %q\n%s", got["x_owner"], "finance", fm)
	}
}

// Spec: §4.6 omitted fields. An alias is expanded into the value it points at
// before the block is assembled, so a key that aliases a node the merge drops or
// the typed serialization re-emits without its anchor is inherited rather than
// stranding the whole block. Neither anchor has anything to do with the extends
// reference, and constraint (a) requires both keys to survive.
func TestSerializeMerged_AliasIntoADroppedNodeIsExpanded(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		chain     []manifest.MergedBlock
		key, want string
	}{
		// The typed serialization re-emits description without the anchor it
		// carried, so the alias into it has no target left in the block.
		"an alias into a declared key": {
			[]manifest.MergedBlock{block("---\ntype: agent\nversion: 2.0.0\n"+
				"description: &d child\nx_note: *d\nextends: shared/parent@1.x\n---\n\nchild body\n",
				"shared/parent@1.x")},
			"x_note", "child"},
		// The child sets x_owner itself, so the parent's anchored node is
		// dropped and the parent's own alias into it would strand.
		"an alias into a key the child overrides": {
			[]manifest.MergedBlock{
				block("---\ntype: agent\nversion: 1.0.0\ndescription: parent\n"+
					"x_owner: &z platform\nx_team: *z\n---\n\nparent body\n", ""),
				block("---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+
					"x_owner: finance\nextends: shared/parent@1.x\n---\n\nchild body\n",
					"shared/parent@1.x")},
			"x_team", "platform"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := serializeChild(tc.chain)
			if err != nil {
				t.Fatalf("SerializeMerged: %v", err)
			}
			fm, _, err := manifest.SplitFrontmatter(out)
			if err != nil {
				t.Fatalf("SplitFrontmatter: %v", err)
			}
			if got := decodeMapping(t, fm); got[tc.key] != tc.want {
				t.Errorf("%s = %v, want %q\n%s", tc.key, got[tc.key], tc.want, fm)
			}
		})
	}
}

// Spec: §4.6 omitted fields. An extension key that happens to nest its own
// extends entry names no chain parent and resolves nothing on the next parse of
// the served block, because ParseArtifact reads the top-level entry and the
// merge keys the block resolves. It is an inheritable key and is served, so one
// extension type's vocabulary does not end every read in the registry.
func TestSerializeMerged_NestedExtendsNamingAnotherArtifactIsServed(t *testing.T) {
	t.Parallel()
	forEachOriginKeys(t, "x_meta:\n  inner:\n    extends: other/base\n",
		func(t *testing.T, chain []manifest.MergedBlock) {
			out, err := serializeChild(chain)
			if err != nil {
				t.Fatalf("SerializeMerged: %v", err)
			}
			assertNamesNoParent(t, out, "shared/parent")
			if !strings.Contains(string(out), "other/base") {
				t.Errorf("the merged block dropped the inheritable key:\n%s", out)
			}
		})
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

// Spec: §4.6 field semantics. The typed serialization is authoritative for the
// merge semantics of every declared key, because only it carries the merge
// table: delegates_to and external_resources append the parent's entries onto
// the child's, and replaced_by takes the child's value. Each of those rows
// reaches the served block with the value the table produced.
func TestSerializeMerged_DeclaredFieldsFollowTheMergeTable(t *testing.T) {
	t.Parallel()
	for name, tc := range declaredFieldCases("shared/other") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := serializeDeclared(tc.artifact)
			if err != nil {
				t.Fatalf("SerializeMerged: %v", err)
			}
			fm, _, err := manifest.SplitFrontmatter(out)
			if err != nil {
				t.Fatalf("SplitFrontmatter: %v", err)
			}
			got := decodeMapping(t, fm)
			if !strings.Contains(encoded(t, got[tc.key]), "shared/other") {
				t.Errorf("%s = %v, want it to carry %q\n%s", tc.key, got[tc.key], "shared/other", fm)
			}
			if _, named := got["extends"]; named {
				t.Errorf("the merged block still names the parent under extends:\n%s", fm)
			}
		})
	}
}

// Spec: §4.6 hidden parents. §4.6's guarantee covers the parent's ID under
// every key of the served block, so a declared field the merge table carries
// down fails the read on the same terms as a restored one when its value stands
// as a reference to a chain parent. The disclosure test runs over the assembled
// block, so where a value came from does not decide whether it is checked.
func TestSerializeMerged_DeclaredFieldNamingTheParentFailsClosed(t *testing.T) {
	t.Parallel()
	for name, tc := range declaredFieldCases("shared/parent") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := serializeDeclared(tc.artifact)
			assertUnhidable(t, out, err)
		})
	}
}

// declaredFieldCase is one row of §4.6's merge table, authored with ref as the
// artifact reference the declared field carries.
type declaredFieldCase struct {
	artifact manifest.Artifact
	key      string
}

// declaredFieldCases builds the merge-table rows that can put an artifact
// reference on a declared field, each carrying ref.
func declaredFieldCases(ref string) map[string]declaredFieldCase {
	child := manifest.Artifact{
		Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
	}
	deprecation, delegation, resource := child, child, child
	deprecation.Deprecated, deprecation.ReplacedBy = true, ref
	delegation.DelegatesTo = []string{ref, "finance/helper"}
	resource.ExternalResources = []manifest.ExternalResource{
		{Path: ref, URL: "https://acme.example/base"},
	}
	return map[string]declaredFieldCase{
		"a deprecation pointer the leaf wrote":          {deprecation, "replaced_by"},
		"a delegates_to entry appended from the parent": {delegation, "delegates_to"},
		"an external resource path":                     {resource, "external_resources"},
	}
}

// serializeDeclared renders a merged artifact whose declared fields a case
// fixes, over the same parent-and-leaf chain every other case uses.
func serializeDeclared(a manifest.Artifact) ([]byte, error) {
	merged := a
	merged.Extends = "shared/parent@1.x"
	merged.Body = "child body"
	return manifest.SerializeMerged(&merged, childID,
		block("---\ntype: agent\nversion: 1.0.0\ndescription: parent\n---\n\nparent body\n", ""),
		block("---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+
			"extends: shared/parent@1.x\n---\n\nchild body\n", "shared/parent@1.x"))
}

// encoded renders a decoded frontmatter value back to YAML text, so a case can
// assert on a scalar, a list, and a mapping on the same terms.
func encoded(t *testing.T, v any) string {
	t.Helper()
	out, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("re-encode %#v: %v", v, err)
	}
	return string(out)
}

// Spec: §4.4, §4.6. The disclosure test fires on a value that stands as an
// artifact reference, so the prose the merge table carries down is served. A
// description a child inherits from a parent that names its own ID in a sentence
// resolves to no artifact on the next read, and refusing it would take out every
// inherited description that happens to quote its baseline.
func TestSerializeMerged_InheritedProseQuotingTheParentIsServed(t *testing.T) {
	t.Parallel()
	out, err := manifest.SerializeMerged(&manifest.Artifact{
		Type: manifest.TypeAgent, Version: "2.0.0", Description: "the shared/parent baseline",
		Extends: "shared/parent@1.x", Body: "child body",
	}, childID,
		block("---\ntype: agent\nversion: 1.0.0\ndescription: the shared/parent baseline\n"+
			"---\n\nparent body\n", ""),
		block("---\ntype: agent\nversion: 2.0.0\nextends: shared/parent@1.x\n---\n\nchild body\n",
			"shared/parent@1.x"))
	if err != nil {
		t.Fatalf("SerializeMerged: %v", err)
	}
	fm, _, err := manifest.SplitFrontmatter(out)
	if err != nil {
		t.Fatalf("SplitFrontmatter: %v", err)
	}
	got := decodeMapping(t, fm)
	if got["description"] != "the shared/parent baseline" {
		t.Errorf("description = %v, want %q\n%s", got["description"], "the shared/parent baseline", fm)
	}
	if _, named := got["extends"]; named {
		t.Errorf("the merged block still names the parent under extends:\n%s", fm)
	}
}

// Spec: §4.6 hidden parents, §4.6 collisions. The parent of a same-ID overlay
// is the row below it in layer order, and its canonical ID is the one the
// requester asked for, so serving that ID surfaces neither an ID the requester
// lacks nor the existence of a second row. Every key the overlay inherits is
// therefore served, including one whose value spells the shared ID, which the
// disclosure test would otherwise make permanently unloadable in both modes.
func TestSerializeMerged_SameIDOverlayDisclosesNothing(t *testing.T) {
	t.Parallel()
	overlay := "---\ntype: agent\nversion: 2.0.0\ndescription: overlay\n" +
		"extends: shared/base@1.x\n---\n\noverlay body\n"
	for name, tc := range map[string]struct{ key, keys string }{
		"the bare canonical id":         {"x_owner", "x_owner: shared/base\n"},
		"a path below its own id":       {"x_owner", "x_owner: shared/base/README.md\n"},
		"prose quoting its own id":      {"x_owner", "x_owner: see shared/base for details\n"},
		"a pinned reference":            {"x_owner", "x_owner: shared/base@1.0.0\n"},
		"a pin inside a nested value":   {"x_owner", "x_owner:\n  ref: shared/base@1.0.0\n"},
		"a nested extends entry":        {"base_ref", "base_ref:\n  extends: shared/base@1.0.0\n"},
		"a nested extends naming no id": {"base_ref", "base_ref:\n  extends:\n"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			base := "---\ntype: agent\nversion: 1.0.0\ndescription: base\n" + tc.keys + "---\n\nbase body\n"
			out, err := manifest.SerializeMerged(&manifest.Artifact{
				Type: manifest.TypeAgent, Version: "2.0.0", Description: "overlay",
				Extends: "shared/base@1.x", Body: "overlay body",
			}, "shared/base", block(base, ""), block(overlay, "shared/base@1.x"))
			if err != nil {
				t.Fatalf("SerializeMerged: %v", err)
			}
			fm, _, err := manifest.SplitFrontmatter(out)
			if err != nil {
				t.Fatalf("SplitFrontmatter: %v", err)
			}
			got := decodeMapping(t, fm)
			if got[tc.key] == nil {
				t.Errorf("the overlay lost the key it inherits from the row below:\n%s", fm)
			}
			if _, named := got["extends"]; named {
				t.Errorf("the merged block still resolves an extends value:\n%s", fm)
			}
		})
	}
}

// Spec: §4.6 hidden parents. An overlay of a different artifact keeps the
// disclosure test, so the exemption above is the same-ID exception rather than
// a hole in the guarantee.
func TestSerializeMerged_OverlayOfAnotherIDStillFailsClosed(t *testing.T) {
	t.Parallel()
	base := "---\ntype: agent\nversion: 1.0.0\ndescription: base\nx_owner: shared/base\n---\n\nbase body\n"
	overlay := "---\ntype: agent\nversion: 2.0.0\ndescription: overlay\n" +
		"extends: shared/base@1.x\n---\n\noverlay body\n"
	out, err := manifest.SerializeMerged(&manifest.Artifact{
		Type: manifest.TypeAgent, Version: "2.0.0", Description: "overlay",
		Extends: "shared/base@1.x", Body: "overlay body",
	}, "finance/overlay", block(base, ""), block(overlay, "shared/base@1.x"))
	assertUnhidable(t, out, err)
}

// Spec: §4.6 omitted fields. The disclosure test fires on a value that stands
// as a reference to a chain parent, so a value in which the ID appears inside a
// longer identifier, under a longer path, below the parent, or inside a
// sentence resolves to no chain parent and is inherited from either origin.
// Bounding the test this way is what keeps §4.6's omitted-field rule working
// for the free text an extension key carries.
func TestSerializeMerged_ValuesNamingAnotherArtifactAreServed(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"longer word":                       "shared/parent-legacy",
		"identifier continuing past the id": "shared/parenthetical",
		"id under a longer path":            "docs/shared/parent.md",
		"id as a trailing path segment":     "team/shared/parent@2.0.0",
		"identifier ending with the id":     "xshared/parent",
		"a filename built from the id":      "shared/parent.md",
		"a path below the parent":           "shared/parent/CHARTER.md",
		"prose quoting the id":              "see shared/parent for details",
		"the id ending a sentence":          "owner is shared/parent.",
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

// parentNamingValues are the spellings under which a value stands as a
// reference to the parent: the ID alone, the ID under a version pin, and the
// path spellings of it that differ only in the slashes or the whitespace around
// it, all of which a consumer resolves to the hidden parent.
var parentNamingValues = map[string]string{
	"the bare id":              "shared/parent",
	"a pinned reference":       "shared/parent@2.0.0",
	"a rooted spelling":        "/shared/parent",
	"a doubly rooted spelling": "//shared/parent",
	"a trailing slash":         "shared/parent/",
	"surrounding whitespace":   "  shared/parent  ",
}

// Spec: §4.6 hidden parents. A value that stands as a reference to a chain
// parent fails the merge under every spelling of the parent's ID and at
// whatever depth it sits, whoever authored it. The guarantee is a property of
// the block the requester is served, so a literal the leaf wrote hands over the
// hidden parent's ID on the same terms as a key restored from an ancestor.
// Dropping the key instead would serve a key §4.6 makes inheritable as nothing,
// which no consumer can tell from a key the chain never set.
func TestSerializeMerged_ValuesSpellingTheParentFailClosed(t *testing.T) {
	t.Parallel()
	for name, keys := range parentNamingKeys() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			forEachOriginKeys(t, keys, func(t *testing.T, chain []manifest.MergedBlock) {
				out, err := serializeChild(chain)
				assertUnhidable(t, out, err)
			})
		})
	}
}

// Spec: §4.6 hidden parents. Aliases compose, so a block whose nested aliases
// expand to more nodes than the budget allows fails the read rather than
// running for the life of the process. ParseArtifact never decodes an
// undeclared key, so yaml.v3's own alias limit is never charged for one and the
// bound has to be applied where the value is expanded. The failure is the same
// fail-closed sentinel a block that cannot be rewritten returns, which the
// resolvers report as registry.invalid_argument.
func TestSerializeMerged_AliasAmplificationIsBounded(t *testing.T) {
	t.Parallel()
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := serializeChild(leafChain(amplifyingKeys(8, 9)))
		done <- result{out, err}
	}()
	select {
	case got := <-done:
		assertUnhidable(t, got.out, got.err)
	case <-time.After(30 * time.Second):
		t.Fatal("expanding an aliased block ran past the budget instead of failing the read")
	}
}

// amplifyingKeys authors depth keys, each a sequence that aliases the previous
// key width times, so expanding the last one materializes width^depth nodes.
func amplifyingKeys(depth, width int) string {
	var b strings.Builder
	b.WriteString("x_a0: &a0 [seed]\n")
	for i := 1; i <= depth; i++ {
		b.WriteString("x_a" + strconv.Itoa(i) + ": &a" + strconv.Itoa(i) + " [")
		for j := 0; j < width; j++ {
			if j > 0 {
				b.WriteString(", ")
			}
			b.WriteString("*a" + strconv.Itoa(i-1))
		}
		b.WriteString("]\n")
	}
	return b.String()
}

// parentNamingKeys authors each spelling of the parent's ID under x_note,
// together with the depths at which the ID can sit.
func parentNamingKeys() map[string]string {
	keys := map[string]string{
		"a nested extends entry": "x_note:\n  inner:\n    extends: shared/parent\n",
		"an id inside a list":    "x_note: [shared/other, shared/parent]\n",
	}
	for name, value := range parentNamingValues {
		keys[name] = noteKeys(value)
	}
	return keys
}

// Spec: §4.6 hidden parents. A restored node comes out of the decoder carrying
// the anchor its author wrote, and yaml.Marshal re-emits it, so an anchor name
// spelling the parent's ID would reach the requester under a key whose value
// carries nothing of the sort. The assembler expands the aliases and clears the
// anchors, so neither reaches the served block. An anchor name cannot hold a
// slash, so the parent here has a single-segment canonical ID.
func TestSerializeMerged_RestoredKeysCarryNoAnchors(t *testing.T) {
	t.Parallel()
	for name, keys := range map[string]string{
		"the id as an anchor name":             "x_owner: &parent platform\n",
		"the id as an anchor on a nested node": "x_meta:\n  team: &parent platform\n",
		"the id as an alias name":              "x_owner: &parent platform\nx_team: *parent\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := manifest.SerializeMerged(&manifest.Artifact{
				Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
				Extends: "parent@1.x", Body: "child body",
			}, childID,
				block("---\ntype: agent\nversion: 1.0.0\ndescription: parent\n"+keys+
					"---\n\nparent body\n", ""),
				block("---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+
					"extends: parent@1.x\n---\n\nchild body\n", "parent@1.x"))
			if err != nil {
				t.Fatalf("SerializeMerged: %v", err)
			}
			assertNamesNoParent(t, out, "parent")
			if strings.ContainsAny(string(out), "&*") {
				t.Errorf("the merged block carries an author's anchor:\n%s", out)
			}
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

// Spec: §4.6 omitted fields. A chain block the helper cannot read back as a
// mapping holds keys that can be neither checked against §4.6's hidden-parent
// rule nor inherited, so the merge fails rather than serving a block that
// silently lost them. Neither extends resolver is expected to reach this arm,
// because both pass blocks a parser has already accepted, and the filesystem
// resolver additionally passes blocks it has rewritten itself.
func TestSerializeMerged_UnreadableChainBlockFailsClosed(t *testing.T) {
	t.Parallel()
	for name, authored := range map[string]string{
		"no frontmatter delimiters": "parent body\n",
		"header that is not YAML":   "---\n\tx: [\n---\n\nparent body\n",
		"header that is a sequence": "---\n- one\n- two\n---\n\nparent body\n",
		"header that is a scalar":   "---\njust a string\n---\n\nparent body\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			child := "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
				"extends: shared/parent@1.x\n---\n\nchild body\n"
			out, err := serializeChild([]manifest.MergedBlock{
				block(authored, ""), block(child, "shared/parent@1.x")})
			if err == nil {
				t.Fatalf("a chain block that cannot be read back must fail the merge:\n%s", out)
			}
			if out != nil {
				t.Errorf("a block that could not be verified must not be returned:\n%s", out)
			}
		})
	}
}

// Spec: §4.6 omitted fields. A chain member whose record stored no frontmatter
// at all holds no key to inherit, so it contributes none and the merge proceeds.
// A store record built outside ingest, such as the pinned extends parent the
// §8.4 retention test writes, is the case that reaches this arm.
func TestSerializeMerged_EmptyChainBlockContributesNoKeys(t *testing.T) {
	t.Parallel()
	child := "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
		"x_owner: finance\nextends: shared/parent@1.x\n---\n\nchild body\n"
	out, err := serializeChild([]manifest.MergedBlock{
		{Extends: ""}, block(child, "shared/parent@1.x")})
	if err != nil {
		t.Fatalf("SerializeMerged: %v", err)
	}
	fm, _, err := manifest.SplitFrontmatter(out)
	if err != nil {
		t.Fatalf("SplitFrontmatter: %v", err)
	}
	if got := decodeMapping(t, fm); got["x_owner"] != "finance" {
		t.Errorf("x_owner = %v, want %q\n%s", got["x_owner"], "finance", fm)
	}
}

// Spec: §4.6 hidden parents. The disclosure test reads the extends reference
// the merged artifact itself carries, so a chain whose members declare none is
// checked like any other. A leaf whose record stored no frontmatter is that
// case, which is what a store record built outside ingest holds.
func TestSerializeMerged_ChainWithoutALeafBlockIsStillTested(t *testing.T) {
	t.Parallel()
	out, err := manifest.SerializeMerged(&manifest.Artifact{
		Type: manifest.TypeAgent, Version: "2.0.0", Description: "child",
		Extends: "shared/parent@1.x", Body: "child body",
	}, childID,
		block("---\ntype: agent\nversion: 1.0.0\ndescription: parent\n"+
			"x_base: shared/parent\n---\n\nparent body\n", ""),
		manifest.MergedBlock{})
	assertUnhidable(t, out, err)
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
// parent's block. A value that names no chain parent is served from either
// origin, so every case it runs is checked on both.
func forEachOrigin(t *testing.T, value string, check func(*testing.T, []manifest.MergedBlock)) {
	t.Helper()
	forEachOriginKeys(t, "x_note: "+yamlScalar(value)+"\n", check)
}

// forEachOriginKeys runs check over the two chains that put the authored keys
// block on the leaf and on the parent. The disclosure test is a property of the
// served block, so every case runs from both origins.
func forEachOriginKeys(t *testing.T, keys string, check func(*testing.T, []manifest.MergedBlock)) {
	t.Helper()
	for origin, chain := range map[string][]manifest.MergedBlock{
		"leaf authored the key":    leafChain(keys),
		"restored from the parent": inheritedChain(keys),
	} {
		t.Run(origin, func(t *testing.T) {
			t.Parallel()
			check(t, chain)
		})
	}
}

// leafChain puts the authored keys in the leaf's own block.
func leafChain(keys string) []manifest.MergedBlock {
	return []manifest.MergedBlock{block("---\ntype: agent\nversion: 2.0.0\n"+
		"description: child\n"+keys+"extends: shared/parent@1.x\n"+
		"---\n\nchild body\n", "shared/parent@1.x")}
}

// inheritedChain puts the authored keys in the parent's block, which the leaf
// inherits under §4.6's omitted-field rule.
func inheritedChain(keys string) []manifest.MergedBlock {
	return []manifest.MergedBlock{
		block("---\ntype: agent\nversion: 1.0.0\ndescription: parent\n"+
			keys+"---\n\nparent body\n", ""),
		block("---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+
			"extends: shared/parent@1.x\n---\n\nchild body\n", "shared/parent@1.x"),
	}
}

// noteKeys authors x_note with value, quoted so a case carrying a comma, a
// quote, or a leading indicator still authors one scalar.
func noteKeys(value string) string {
	return "x_note: " + yamlScalar(value) + "\n"
}

// yamlScalar quotes value so a case that carries a comma, a quote, or a
// leading indicator still authors one scalar.
func yamlScalar(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

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
