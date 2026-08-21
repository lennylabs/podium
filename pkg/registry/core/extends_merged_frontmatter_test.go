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
// key the parser resolves, not only a literal top-level `extends`. A child
// whose reference arrives through a YAML merge key keeps an operative
// `extends` inside the mapping it merges in, and that mapping is an undeclared
// key the serializer now restores, so the assembled block would name the
// parent. The load fails closed with `registry.invalid_argument` instead.
//
// This removes a load that succeeded before the change: the closed round-trip
// dropped the merge-key mapping along with every other undeclared key and
// served a clean block. The refusal is the accepted cost of preserving the
// keys §4.6 makes inheritable.
func TestExtendsFrontmatter_MergeKeyReferenceFailsClosed(t *testing.T) {
	t.Parallel()
	got, err := emfLoad(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\n---\n\nparent body\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+
			"base: &b\n  extends: shared/parent@1.x\n<<: *b\n---\n\nchild body\n")
	assertFailsClosed(t, got, err)
}

// Spec: §4.6 hidden parents — an anchored extends value carries the parent's
// ID to every alias into it, and the sibling key holding that alias is an
// undeclared key the serializer restores. §4.6 scopes its guarantee to the
// parent's existence and ID, so a served block naming the parent under `note:`
// discloses what the section forbids. The load fails closed with
// `registry.invalid_argument` instead.
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

// Spec: §4.6 hidden parents — the refusal is scoped to a block that names the
// parent or cannot be read back, and an alias into an anchor on a declared key
// is neither. The serializer resolves the alias against the block that
// declared the anchor, so the child keeps the load it gets today and the key
// arrives carrying the anchored value.
func TestExtendsFrontmatter_AliasIntoADeclaredKeyIsServed(t *testing.T) {
	t.Parallel()
	got, err := emfLoad(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\n---\n\nparent body\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: &d child\n"+
			"x_note: *d\nextends: shared/parent@1.x\n---\n\nchild body\n")
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	served := decodeServedMapping(t, got.Frontmatter)
	if served["x_note"] != "child" {
		t.Errorf("x_note = %v, want %q\n%s", served["x_note"], "child", got.Frontmatter)
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
// through the merge and the search path through the indexed columns.
func TestExtendsFrontmatter_ChildKeyMatchesTheSearchDescriptor(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: agent\nversion: 1.0.0\ndescription: parent\n---\n\nparent body\n"
	child := "---\ntype: agent\nversion: 2.0.0\ndescription: child\n" +
		"x_runbook: ops/pay.md\nextends: shared/parent@1.x\n---\n\nchild body\n"
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
	if fromSearch["x_runbook"] != "ops/pay.md" {
		t.Fatalf("the search descriptor dropped the child's own key: %q", descriptor.Frontmatter)
	}
	if fromLoad["x_runbook"] != fromSearch["x_runbook"] {
		t.Errorf("load_artifact serves x_runbook = %v, search_artifacts serves %v",
			fromLoad["x_runbook"], fromSearch["x_runbook"])
	}
}

// Spec: §4.6 same-ID overlay — a child may extend its own canonical ID, and
// the parent is then the row below it in layer order. The parent's ID is the ID
// the requester asked for, so the hidden-parent test has nothing to withhold
// and must not fire on the artifact's own identity. An inherited key holding
// that ID is served rather than costing the requester the load.
func TestExtendsFrontmatter_SameIDOverlayServesAnInheritedSelfReference(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, in := range []struct{ layerID, body string }{
		{"L1", "---\ntype: agent\nversion: 1.0.0\ndescription: base\n" +
			"x_owner: shared/base\n---\n\nbase body\n"},
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
	got, err := reg.LoadArtifact(context.Background(), publicID, "shared/base", core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	served := decodeServedMapping(t, got.Frontmatter)
	if served["x_owner"] != "shared/base" {
		t.Errorf("x_owner = %v, want %q\n%s", served["x_owner"], "shared/base", got.Frontmatter)
	}
	if _, named := served["extends"]; named {
		t.Errorf("the served frontmatter still resolves an extends value:\n%s", got.Frontmatter)
	}
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
