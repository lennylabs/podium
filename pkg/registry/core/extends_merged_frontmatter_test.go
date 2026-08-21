package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

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

// Spec: §4.6 hidden parents — deleting an anchored extends value strands every
// alias into it, so the assembled block is not readable YAML and its contents
// cannot be checked against §4.6. The load fails closed with
// `registry.invalid_argument` rather than serving what it could not verify.
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

// Spec: §4.6 hidden parents — the typed serialization re-emits a declared key
// without the anchor it carried, so a restored key that aliases that anchor
// strands and the assembled block is not readable YAML. It is the same input
// class as the anchored extends value, and the load fails closed rather than
// serving a block whose contents were never verified.
func TestExtendsFrontmatter_AliasIntoADeclaredKeyFailsClosed(t *testing.T) {
	t.Parallel()
	got, err := emfLoad(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\n---\n\nparent body\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: &d child\n"+
			"x_note: *d\nextends: shared/parent@1.x\n---\n\nchild body\n")
	assertFailsClosed(t, got, err)
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

// Spec: §4.6 hidden parents — the guarantee covers the block the registry
// serves and says nothing about who wrote a value, so a key the child authored
// itself fails the load on the same terms as one it inherits. The child's own
// bytes still reach this requester as `raw_frontmatter` and through the search
// descriptor, which are separate surfaces with their own §4.6 questions and are
// out of scope for the merged block.
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

// Spec: §4.6 hidden parents — the guarantee covers the block the registry
// serves, so a declared field is held to it like a restored key. §4.6's own
// merge table carries artifact references down a chain: `delegates_to` and
// `external_resources` append the parent's entries onto the child's, and
// `replaced_by` is the child's or the parent's. Each of those hands the parent's
// canonical ID to a requester who cannot see the parent's layer, so the load
// fails closed with `registry.invalid_argument`.
func TestExtendsFrontmatter_DeclaredFieldsNamingTheParentFailClosed(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ parentKeys, childKeys string }{
		"a deprecation pointer the child wrote": {
			"", "deprecated: true\nreplaced_by: shared/parent\n"},
		"a delegates_to entry the child inherits": {
			"delegates_to: [shared/parent]\n", ""},
		"an external resource the child inherits": {
			"external_resources:\n  - path: shared/parent\n    url: https://acme.example/parent\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := emfLoad(t,
				"---\ntype: agent\nversion: 1.0.0\ndescription: parent\n"+tc.parentKeys+
					"---\n\nparent body\n",
				"---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+tc.childKeys+
					"extends: shared/parent@1.x\n---\n\nchild body\n")
			assertFailsClosed(t, got, err)
		})
	}
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
// manifest.Artifact does not declare. The assembled block carrying that key
// does not read back as a mapping, so it cannot be checked for the parent's
// ID, and the load fails closed rather than serving what it could not verify.
func TestExtendsFrontmatter_UndecodableMergedBlockFailsClosed(t *testing.T) {
	t.Parallel()
	got, err := emfLoad(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\n---\n\nparent body\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+
			"x_self: &s\n  k: *s\nextends: shared/parent@1.x\n---\n\nchild body\n")
	assertFailsClosed(t, got, err)
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
// frontmatter key the manifest.Artifact struct does not declare. The
// comparison holds for a key the child itself authored, which is what the
// search descriptor serves; a key the child inherits reaches the load path
// through the merge and the search path through the indexed columns. The cases
// run values the load path's §4.6 disclosure test inspects and admits,
// including two that carry the parent's ID without standing as a reference to
// it, so the comparison exercises the predicate rather than avoiding it. The one
// class the two paths still answer differently is a child-authored value that is
// a reference to the hidden parent, which §4.6 requires the merged block to
// refuse.
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
			parent := "---\ntype: agent\nversion: 1.0.0\ndescription: parent\n---\n\nparent body\n"
			child := "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
				key.name + ": " + key.value + "\nextends: shared/parent@1.x\n---\n\nchild body\n"
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
			if fromSearch[key.name] != key.value {
				t.Fatalf("the search descriptor dropped the child's own key: %q", descriptor.Frontmatter)
			}
			if fromLoad[key.name] != fromSearch[key.name] {
				t.Errorf("load_artifact serves %s = %v, search_artifacts serves %v",
					key.name, fromLoad[key.name], fromSearch[key.name])
			}
		})
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

// Spec: §4.6 omitted fields — §4.6 makes a child inherit when it omits a field
// or sets an empty scalar, and for a field outside its merge table a value both
// sides declare takes the child's. An extension-type key has no table row, so a
// child that empties a list is served its own empty list rather than the
// parent's contents.
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
	if owner, ok := served["x_owner"].([]any); !ok || len(owner) != 0 {
		t.Errorf("x_owner = %#v, want an empty list\n%s", served["x_owner"], got.Frontmatter)
	}
}

// Spec: §4.6 hidden parents, §4.6 collisions — a child may extend its own
// canonical ID, and the parent is then the row below it in layer order, in a
// layer the requester may not be able to see. The ID is the one the requester
// supplied to read the artifact, so an inherited key that spells it discloses
// nothing and the overlay loads. A pin on it names a version the requester was
// not served, which reports that a second row exists, so that spelling fails the
// load like any other chain parent. A nested extends entry follows the same
// rule, because ParseArtifact resolves the top-level entry and the block's merge
// keys, so a nested one is inherited text rather than a reference the next read
// acts on.
func TestExtendsFrontmatter_SameIDOverlayHidesOnlyTheLowerRowsExtras(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		key    string
		keys   string
		served bool
	}{
		"the bare canonical id":               {"x_owner", "x_owner: shared/base\n", true},
		"a path below its own id":             {"x_owner", "x_owner: shared/base/README.md\n", true},
		"a pinned reference to the lower row": {"x_owner", "x_owner: shared/base@1.0.0\n", false},
		"a nested extends entry":              {"base_ref", "base_ref:\n  extends: shared/base@1.0.0\n", false},
		"an unpinned nested extends entry":    {"base_ref", "base_ref:\n  extends: shared/base\n", true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := overlayLoad(t, tc.keys)
			if !tc.served {
				assertFailsClosed(t, got, err)
				return
			}
			if err != nil {
				t.Fatalf("LoadArtifact: %v", err)
			}
			if served := decodeServedMapping(t, got.Frontmatter); served[tc.key] == nil {
				t.Errorf("the overlay lost the key it inherits from the row below:\n%s", got.Frontmatter)
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
