package core_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// emfLoad ingests a parent and a child into two layers and loads the child.
func emfLoad(t *testing.T, parent, child string) (*core.LoadArtifactResult, error) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, in := range []struct {
		layerID string
		path    string
		body    string
	}{
		{"L1", "shared/parent/ARTIFACT.md", parent},
		{"L2", "finance/child/ARTIFACT.md", child},
	} {
		res, err := ingest.Ingest(context.Background(), st, ingest.Request{
			TenantID: "t", LayerID: in.layerID,
			Files: fstest.MapFS{in.path: &fstest.MapFile{Data: []byte(in.body)}},
		})
		if err != nil {
			t.Fatalf("ingest %s: %v", in.path, err)
		}
		if res.Accepted != 1 {
			t.Fatalf("ingest %s not accepted: %+v", in.path, res.Rejected)
		}
	}
	reg := core.New(st, "t", []layer.Layer{
		{ID: "L1", Visibility: layer.Visibility{Public: true}, Precedence: 1},
		{ID: "L2", Visibility: layer.Visibility{Public: true}, Precedence: 2},
	})
	return reg.LoadArtifact(context.Background(), publicID, "finance/child", core.LoadArtifactOptions{})
}

// Spec: §4.6 — the omitted-field rule names "any extension-type fields not
// declared by their `TypeProvider`" among the fields a child inherits, so an
// extension type's own frontmatter keys are part of the merge. The served
// merged frontmatter was produced by marshalling the closed `manifest.Artifact`
// struct, which drops every key it does not declare, so the load path destroyed
// keys the search path preserves.
func TestExtendsFrontmatter_UndeclaredKeysSurviveTheMerge(t *testing.T) {
	t.Parallel()
	got, err := emfLoad(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\nx_review_board: platform\n---\n\nparent body\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: child\nx_runbook: ops/pay.md\n"+
			"extends: shared/parent@1.x\n---\n\nchild body\n")
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	fm := string(got.Frontmatter)
	// Read the block back as a mapping and compare values. A key restored
	// with an empty or wrong value would satisfy a substring match on the key
	// name while leaving the §4.6 inheritance unpinned.
	served := decodeServedMapping(t, got.Frontmatter)
	for key, want := range map[string]string{
		"x_runbook":      "ops/pay.md",
		"x_review_board": "platform",
	} {
		if served[key] != want {
			t.Errorf("%s = %v, want %q\n%s", key, served[key], want, fm)
		}
	}
	if strings.Contains(fm, "extends:") {
		t.Errorf("the served frontmatter still names the hidden parent:\n%s", fm)
	}
}

// decodeServedMapping reads a served frontmatter block back as a YAML mapping,
// which is how a consumer reads it.
func decodeServedMapping(t *testing.T, served []byte) map[string]any {
	t.Helper()
	fm, _, err := manifest.SplitFrontmatter(served)
	if err != nil {
		t.Fatalf("SplitFrontmatter: %v\n%s", err, served)
	}
	var got map[string]any
	if err := yaml.Unmarshal(fm, &got); err != nil {
		t.Fatalf("decode served frontmatter: %v\n%s", err, fm)
	}
	return got
}

// Spec: §4.6 — a key both sides declare takes the child's value, so restoring
// the undeclared keys must not let a parent's value overwrite the child's.
func TestExtendsFrontmatter_ChildWinsOnASharedUndeclaredKey(t *testing.T) {
	t.Parallel()
	got, err := emfLoad(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\nx_owner: platform\n---\n\nparent body\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: child\nx_owner: finance\n"+
			"extends: shared/parent@1.x\n---\n\nchild body\n")
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	fm := string(got.Frontmatter)
	if !strings.Contains(fm, "finance") {
		t.Errorf("the child's value for a shared undeclared key was lost:\n%s", fm)
	}
	if strings.Contains(fm, "platform") {
		t.Errorf("the parent's value overwrote the child's:\n%s", fm)
	}
}

// Spec: §4.6 hidden parents — the guarantee covers the parent's ID under any
// key, and under any key the parser resolves back into an `extends` value. A
// child whose reference arrives through a YAML merge key keeps an operative
// `extends` inside the mapping it merges in. Restoring that mapping would put an
// extends entry back into the served block, so the load fails closed with
// `registry.invalid_argument`.
//
// This removes a load that succeeded before the change: the closed round-trip
// dropped the merge-key mapping along with every other undeclared key and
// served a clean block. It is the one input class preserving the keys §4.6
// makes inheritable costs its load.
func TestExtendsFrontmatter_MergeKeyReferenceFailsClosed(t *testing.T) {
	t.Parallel()
	got, err := emfLoad(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\n---\n\nparent body\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+
			"base: &b\n  extends: shared/parent@1.x\n<<: *b\n---\n\nchild body\n")
	assertFailsClosed(t, got, err)
}

// Spec: §4.6 hidden parents — an anchored extends value can be aliased under a
// second key, and the merge expands that alias into the value the anchor holds
// before it assembles the block, so the restored key stands as a reference to
// the chain parent. The §4.6 disclosure test over the assembled block refuses
// it, and the load fails closed with `registry.invalid_argument` rather than
// handing the requester the parent's ID under another name.
//
// This is the second half of the input class the change deliberately loses.
// The closed round-trip served these children a clean block by dropping every
// undeclared key, so the pre-change success is not the baseline.
func TestExtendsFrontmatter_AnchoredReferenceFailsClosed(t *testing.T) {
	t.Parallel()
	got, err := emfLoad(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\n---\n\nparent body\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+
			"extends: &p shared/parent@1.x\nnote: *p\n---\n\nchild body\n")
	assertFailsClosed(t, got, err)
}

// Spec: §4.6 hidden parents — the refusal above is bounded to a reference some
// other key resolves back into the assembled block. An anchor on the `extends:`
// value that no other key aliases costs no load: the merged block is rebuilt
// from the merged fields, which carry neither the reference nor the anchor. This
// pins the accepted side of that boundary at the served surface, beside the two
// refused cases, so a later change that rejects the input anywhere on the load
// path is caught here.
func TestExtendsFrontmatter_AnchoredExtendsWithoutAnAliasIsServed(t *testing.T) {
	t.Parallel()
	got, err := emfLoad(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\n---\n\nparent body\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+
			"extends: &p shared/parent@1.x\nx_owner: finance\n---\n\nchild body\n")
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	fm := string(got.Frontmatter)
	if strings.Contains(fm, "shared/parent") {
		t.Errorf("the served frontmatter names the hidden parent:\n%s", fm)
	}
	if served := decodeServedMapping(t, got.Frontmatter); served["x_owner"] != "finance" {
		t.Errorf("x_owner = %v, want %q\n%s", served["x_owner"], "finance", fm)
	}
}

// Spec: §4.6 omitted fields — the typed serialization re-emits a declared key
// without the anchor it carried, so a restored key that aliases that anchor has
// no target left in the assembled block. The merge expands the alias into the
// value it points at, so the key is inherited and the anchor costs no load.
func TestExtendsFrontmatter_AliasIntoADeclaredKeyIsExpanded(t *testing.T) {
	t.Parallel()
	got, err := emfLoad(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\n---\n\nparent body\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: &d child\n"+
			"x_note: *d\nextends: shared/parent@1.x\n---\n\nchild body\n")
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if served := decodeServedMapping(t, got.Frontmatter); served["x_note"] != "child" {
		t.Errorf("x_note = %v, want %q\n%s", served["x_note"], "child", got.Frontmatter)
	}
}

// parentNamingValues are the spellings under which a restored key stands as a
// reference to the hidden parent, handing the requester its ID together with the
// evidence that the artifact exists: the ID alone, the ID under a version pin,
// and the path spellings that differ from it only in their slashes.
var parentNamingValues = map[string]string{
	"the parent's id":          "shared/parent",
	"a pinned reference":       "shared/parent@2.0.0",
	"a rooted spelling":        "/shared/parent",
	"a doubly rooted spelling": "//shared/parent",
	"a trailing slash":         "shared/parent/",
}

// Spec: §4.6 hidden parents — a key the child inherits from the parent it hides
// carries that parent's ID to a requester who cannot see the parent's layer, so
// the load fails closed with `registry.invalid_argument`. Dropping the key
// instead is not open either, because §4.6's omitted-field rule does not allow
// serving an inheritable key as nothing.
func TestExtendsFrontmatter_InheritedValuesSpellingTheParentFailClosed(t *testing.T) {
	t.Parallel()
	for name, value := range parentNamingValues {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := emfLoad(t,
				"---\ntype: agent\nversion: 1.0.0\ndescription: parent\nx_base: '"+value+"'\n"+
					"---\n\nparent body\n",
				"---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+
					"extends: shared/parent@1.x\n---\n\nchild body\n")
			assertFailsClosed(t, got, err)
		})
	}
}

// Spec: §4.6 hidden parents — the guarantee covers the block the requester is
// served, so a key the child authored itself fails the load under those same
// spellings. The child's own bytes reach this requester through
// `raw_frontmatter` and through the search descriptor, which is a disclosure
// recorded on those surfaces and does not license the merged block to repeat
// it, and the materialized bytes pkg/sync feeds the harness adapters have
// neither surface at all.
func TestExtendsFrontmatter_ChildAuthoredValuesSpellingTheParentFailClosed(t *testing.T) {
	t.Parallel()
	for name, value := range parentNamingValues {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := emfLoad(t,
				"---\ntype: agent\nversion: 1.0.0\ndescription: parent\n---\n\nparent body\n",
				"---\ntype: agent\nversion: 2.0.0\ndescription: child\nx_base: '"+value+"'\n"+
					"extends: shared/parent@1.x\n---\n\nchild body\n")
			assertFailsClosed(t, got, err)
		})
	}
}

// Spec: §4.6 omitted fields — the disclosure test fires on a value that stands
// as a reference to the parent, so a value in which the ID appears inside a
// longer identifier, under a longer path, below the parent, or inside a
// sentence resolves to no chain parent and is inherited rather than dropped.
// That is what keeps the omitted-field rule working for the free text an
// extension key carries.
func TestExtendsFrontmatter_ValueNamingAnotherArtifactIsServed(t *testing.T) {
	t.Parallel()
	for name, value := range map[string]string{
		"longer identifier":             "shared/parent-legacy",
		"id under a longer path":        "docs/shared/parent.md",
		"id as a trailing path segment": "team/shared/parent@2.0.0",
		"a filename built from the id":  "shared/parent.md",
		"a path below the parent":       "shared/parent/CHARTER.md",
		"prose quoting the id":          "see shared/parent for details",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := emfLoad(t,
				"---\ntype: agent\nversion: 1.0.0\ndescription: parent\nx_base: "+value+"\n---\n\nparent body\n",
				"---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+
					"extends: shared/parent@1.x\n---\n\nchild body\n")
			if err != nil {
				t.Fatalf("LoadArtifact: %v", err)
			}
			if served := decodeServedMapping(t, got.Frontmatter); served["x_base"] != value {
				t.Errorf("x_base = %v, want %q\n%s", served["x_base"], value, got.Frontmatter)
			}
		})
	}
}

// Spec: §4.6 field semantics — the typed serialization is authoritative for the
// merge semantics of every declared key, because only it carries the merge
// table. `delegates_to` and `external_resources` append the parent's entries
// onto the child's, and `replaced_by` takes the child's value, and each of those
// rows reaches the served block with the value the table produced.
func TestExtendsFrontmatter_DeclaredFieldsFollowTheMergeTable(t *testing.T) {
	t.Parallel()
	for name, tc := range declaredFieldMergeCases("shared/other") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := emfLoad(t, tc.parent, tc.child)
			if err != nil {
				t.Fatalf("LoadArtifact: %v", err)
			}
			if served := decodeServedMapping(t, got.Frontmatter); served[tc.want] == nil {
				t.Errorf("the served block dropped %s:\n%s", tc.want, got.Frontmatter)
			}
			if strings.Contains(string(got.Frontmatter), "extends:") {
				t.Errorf("the served frontmatter still names the hidden parent:\n%s", got.Frontmatter)
			}
		})
	}
}

// Spec: §4.6 hidden parents — §4.6 hides the parent's ID under every key of the
// served block, so a declared field the merge table carries down fails the load
// on the same terms as a restored undeclared key when its value stands as a
// reference to a chain parent. A `delegates_to` entry and an
// `external_resources` path the child inherits are the routes by which the
// merge itself would hand the requester an ID no other surface carries.
func TestExtendsFrontmatter_DeclaredFieldNamingTheParentFailsClosed(t *testing.T) {
	t.Parallel()
	for name, tc := range declaredFieldMergeCases("shared/parent") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := emfLoad(t, tc.parent, tc.child)
			assertFailsClosed(t, got, err)
		})
	}
}

// declaredFieldMergeCases builds the parent and child blocks for each merge
// table row that can put an artifact reference on a declared field, with ref as
// the reference the row carries.
func declaredFieldMergeCases(ref string) map[string]struct{ parent, child, want string } {
	build := func(parentKeys, childKeys string) (string, string) {
		return "---\ntype: agent\nversion: 1.0.0\ndescription: parent\n" + parentKeys +
				"---\n\nparent body\n",
			"---\ntype: agent\nversion: 2.0.0\ndescription: child\n" + childKeys +
				"extends: shared/parent@1.x\n---\n\nchild body\n"
	}
	out := map[string]struct{ parent, child, want string }{}
	for name, tc := range map[string]struct{ parentKeys, childKeys, want string }{
		"a deprecation pointer the child wrote": {
			"", "deprecated: true\nreplaced_by: " + ref + "\n", "replaced_by"},
		"a delegates_to entry the child inherits": {
			"delegates_to: [" + ref + "]\n", "", "delegates_to"},
		"an external resource the child inherits": {
			"external_resources:\n  - path: " + ref + "\n    url: https://acme.example/base\n",
			"", "external_resources"},
	} {
		parent, child := build(tc.parentKeys, tc.childKeys)
		out[name] = struct{ parent, child, want string }{parent, child, tc.want}
	}
	return out
}

// Spec: §4.4, §4.6 — the disclosure test fires on a value that stands as an
// artifact reference, so the prose §4.6's merge table carries down is served. A
// child that inherits a description quoting its baseline keeps that description,
// because the sentence resolves to no artifact on the next read.
func TestExtendsFrontmatter_InheritedProseQuotingTheParentIsServed(t *testing.T) {
	t.Parallel()
	got, err := emfLoad(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: the shared/parent baseline\n---\n\nparent body\n",
		"---\ntype: agent\nversion: 2.0.0\nextends: shared/parent@1.x\n---\n\nchild body\n")
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	served := decodeServedMapping(t, got.Frontmatter)
	if served["description"] != "the shared/parent baseline" {
		t.Errorf("description = %v, want %q\n%s",
			served["description"], "the shared/parent baseline", got.Frontmatter)
	}
}

// Spec: §4.6 hidden parents — a child can author frontmatter whose anchor
// contains itself, which ingest accepts because it never decodes a key
// manifest.Artifact does not declare. A restored key whose anchor contains
// itself cannot be expanded into the values it points at, so the merge fails
// before it assembles a block, and the load fails closed rather than serving a
// key it could neither check against §4.6 nor inherit. The arm that refuses a
// chain block which does not read back as one mapping is pinned directly in
// pkg/manifest/merge_serialize_test.go.
func TestExtendsFrontmatter_SelfContainingAnchorFailsClosed(t *testing.T) {
	t.Parallel()
	got, err := emfLoad(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\n---\n\nparent body\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+
			"x_self: &s\n  k: *s\nextends: shared/parent@1.x\n---\n\nchild body\n")
	assertFailsClosed(t, got, err)
}

// Spec: §4.6 hidden parents — ingest never decodes a key manifest.Artifact does
// not declare, so a child whose frontmatter nests aliased sequences is accepted
// and yaml.v3's own alias budget is never charged for it. The merge bounds the
// expansion itself, so the read fails with `registry.invalid_argument` rather
// than expanding an alias graph for the life of the process. mergeChain is on
// the path of every read of an artifact that declares extends:, so an unbounded
// expansion here stalls the load path in both deployment modes.
func TestExtendsFrontmatter_AliasAmplificationFailsClosed(t *testing.T) {
	t.Parallel()
	type result struct {
		got *core.LoadArtifactResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		got, err := emfLoad(t,
			"---\ntype: agent\nversion: 1.0.0\ndescription: parent\n---\n\nparent body\n",
			"---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+
				amplifyingKeys(8, 9)+"extends: shared/parent@1.x\n---\n\nchild body\n")
		done <- result{got, err}
	}()
	select {
	case r := <-done:
		assertFailsClosed(t, r.got, r.err)
	case <-time.After(30 * time.Second):
		t.Fatal("the load expanded an aliased block instead of failing closed")
	}
}

// amplifyingKeys authors depth frontmatter keys, each a sequence that aliases
// the previous key width times, so expanding the last one materializes
// width^depth nodes.
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

// assertFailsClosed requires that the load returned no result, that the error
// maps to `registry.invalid_argument`, and that no served byte names the
// parent. Serving an empty frontmatter would also be a failure: the requester
// would take a block that hides the parent by hiding the artifact.
func assertFailsClosed(t *testing.T, got *core.LoadArtifactResult, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("a child whose merged frontmatter cannot be rewritten must not be served")
	}
	if !errors.Is(err, core.ErrInvalidArgument) {
		t.Errorf("error = %v, want ErrInvalidArgument so the caller maps it to registry.invalid_argument", err)
	}
	if got != nil {
		t.Fatalf("a failed load returned a result: %+v", got)
	}
}

// Spec: §4.6, §2.2 — load_artifact and search_artifacts serve the same
// artifact through two paths, and defect 3 was the two disagreeing about a
// frontmatter key the manifest.Artifact struct does not declare. The comparison
// holds for a key the child itself authored, which is what the search
// descriptor serves; a key the child inherits reaches the load path through the
// merge and the search path through the indexed columns. The cases run values
// the load path's §4.6 disclosure test inspects and admits, including several
// that carry the parent's ID without standing as a reference to it, so the
// comparison exercises the predicate rather than avoiding it.
func TestExtendsFrontmatter_ChildKeyMatchesTheSearchDescriptor(t *testing.T) {
	t.Parallel()
	for name, key := range map[string]struct{ name, value string }{
		"an ordinary key":                      {"x_runbook", "ops/pay.md"},
		"a key naming another artifact":        {"x_base", "shared/parent-legacy"},
		"a key naming a longer path":           {"x_docs", "docs/shared/parent.md"},
		"a key naming a path below the parent": {"x_charter", "shared/parent/CHARTER.md"},
		"a key quoting the parent in prose":    {"x_note", "see shared/parent for details"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertLoadMatchesSearch(t, key.name, key.value)
		})
	}
}

// assertLoadMatchesSearch ingests a child that authors key with value and
// requires that load_artifact and search_artifacts serve the same value for it.
// The value is authored as a quoted scalar so a spelling carrying a slash or a
// version pin still authors one scalar.
func assertLoadMatchesSearch(t *testing.T, key, value string) {
	t.Helper()
	parent := "---\ntype: agent\nversion: 1.0.0\ndescription: parent\n---\n\nparent body\n"
	child := "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
		key + ": '" + strings.ReplaceAll(value, "'", "''") + "'\n" +
		"extends: shared/parent@1.x\n---\n\nchild body\n"
	reg, _ := ingestPair(t, "shared/parent/ARTIFACT.md", parent, "finance/child/ARTIFACT.md", child)

	loaded, err := reg.LoadArtifact(context.Background(), publicID, "finance/child", core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	res, err := reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	descriptor := findResult(t, res, "finance/child")

	fromLoad := decodeServedMapping(t, loaded.Frontmatter)
	fromSearch := decodeServedMapping(t, []byte(descriptor.Frontmatter))
	if fromSearch[key] != value {
		t.Fatalf("the search descriptor dropped the child's own key: %q", descriptor.Frontmatter)
	}
	if fromLoad[key] != fromSearch[key] {
		t.Errorf("load_artifact serves %s = %v, search_artifacts serves %v",
			key, fromLoad[key], fromSearch[key])
	}
}

// Spec: §4.6 omitted fields — a child that sets a frontmatter key to an empty
// value inherits the parent's value, and the section states that this holds for
// every frontmatter field. manifest.MergeExtends applies the rule to every
// declared field, and the restored undeclared keys follow it, so the two halves
// of the served block do not disagree about what an empty child value means.
func TestExtendsFrontmatter_EmptyChildValueInheritsTheParents(t *testing.T) {
	t.Parallel()
	for name, authored := range map[string]string{
		"an omitted value": "x_owner:",
		"a null scalar":    "x_owner: null",
		"a tilde":          "x_owner: ~",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := emfLoad(t,
				"---\ntype: agent\nversion: 1.0.0\ndescription: parent\nx_owner: platform\n---\n\nparent body\n",
				"---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+authored+"\n"+
					"extends: shared/parent@1.x\n---\n\nchild body\n")
			if err != nil {
				t.Fatalf("LoadArtifact: %v", err)
			}
			served := decodeServedMapping(t, got.Frontmatter)
			if served["x_owner"] != "platform" {
				t.Errorf("x_owner = %v, want %q\n%s", served["x_owner"], "platform", got.Frontmatter)
			}
		})
	}
}

// Spec: §4.6 omitted fields — §4.6 inherits the parent's value for a key the
// child omits or sets to an empty scalar, and gives every other field the
// child's value, naming the extension-type fields a TypeProvider does not
// declare. A child that authors an empty list has declared a value, so the
// served block carries the empty list and the child can clear what it inherits.
func TestExtendsFrontmatter_EmptyChildCollectionWins(t *testing.T) {
	t.Parallel()
	got, err := emfLoad(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\nx_owner: [platform]\n---\n\nparent body\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: child\nx_owner: []\n"+
			"extends: shared/parent@1.x\n---\n\nchild body\n")
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	served := decodeServedMapping(t, got.Frontmatter)
	owner, ok := served["x_owner"].([]any)
	if !ok || len(owner) != 0 {
		t.Errorf("x_owner = %#v, want the child's empty list\n%s", served["x_owner"], got.Frontmatter)
	}
}

// Spec: §4.6 hidden parents, §4.6 collisions — a child may extend its own
// canonical ID, and the parent is then the row below it in layer order. That
// row carries the canonical ID the requester asked for, so serving a value that
// spells it surfaces neither an ID the requester lacks nor the existence of a
// second row, and the overlay keeps every key it inherits.
func TestExtendsFrontmatter_SameIDOverlayIsServed(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ key, keys string }{
		"the bare canonical id":               {"x_owner", "x_owner: shared/base\n"},
		"a path below its own id":             {"x_owner", "x_owner: shared/base/README.md\n"},
		"a pinned reference to the lower row": {"x_owner", "x_owner: shared/base@1.0.0\n"},
		"a nested extends entry":              {"base_ref", "base_ref:\n  extends: shared/base@1.0.0\n"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := overlayLoad(t, tc.keys)
			if err != nil {
				t.Fatalf("LoadArtifact: %v", err)
			}
			served := decodeServedMapping(t, got.Frontmatter)
			if served[tc.key] == nil {
				t.Errorf("the overlay lost the key it inherits from the row below:\n%s", got.Frontmatter)
			}
			if _, named := served["extends"]; named {
				t.Errorf("the served frontmatter still resolves an extends value:\n%s", got.Frontmatter)
			}
		})
	}
}

// overlayLoad ingests a base carrying keys and a same-ID overlay extending it
// into two layers, then loads the shared canonical ID.
func overlayLoad(t *testing.T, keys string) (*core.LoadArtifactResult, error) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, in := range []struct{ layerID, body string }{
		{"L1", "---\ntype: agent\nversion: 1.0.0\ndescription: base\n" + keys + "---\n\nbase body\n"},
		{"L2", "---\ntype: agent\nversion: 2.0.0\ndescription: overlay\n" +
			"extends: shared/base@1.x\n---\n\noverlay body\n"},
	} {
		res, err := ingest.Ingest(context.Background(), st, ingest.Request{
			TenantID: "t", LayerID: in.layerID,
			Files: fstest.MapFS{"shared/base/ARTIFACT.md": &fstest.MapFile{Data: []byte(in.body)}},
		})
		if err != nil {
			t.Fatalf("ingest %s: %v", in.layerID, err)
		}
		if res.Accepted != 1 {
			t.Fatalf("ingest %s not accepted: %+v", in.layerID, res.Rejected)
		}
	}
	reg := core.New(st, "t", []layer.Layer{
		{ID: "L1", Visibility: layer.Visibility{Public: true}, Precedence: 1},
		{ID: "L2", Visibility: layer.Visibility{Public: true}, Precedence: 2},
	})
	return reg.LoadArtifact(context.Background(), publicID, "shared/base", core.LoadArtifactOptions{})
}

// Spec: §4.6 — a child that declares no undeclared key is served the typed
// serialization unchanged, so the shared helper does not perturb the common
// case. This is the arm that would break if the restore step ran
// unconditionally.
func TestExtendsFrontmatter_NoUndeclaredKeysServesTheTypedBlock(t *testing.T) {
	t.Parallel()
	got, err := emfLoad(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\ntags: [shared]\n---\n\nparent body\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: child\ntags: [team]\n"+
			"extends: shared/parent@1.x\n---\n\nchild body\n")
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	fm := string(got.Frontmatter)
	for _, want := range []string{"description: child", "shared", "team"} {
		if !strings.Contains(fm, want) {
			t.Errorf("merged frontmatter missing %q:\n%s", want, fm)
		}
	}
	if strings.Contains(fm, "extends:") {
		t.Errorf("the served frontmatter still names the hidden parent:\n%s", fm)
	}
}
