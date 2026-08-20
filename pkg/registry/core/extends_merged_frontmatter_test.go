package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/layer"
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
	if !strings.Contains(fm, "x_runbook") {
		t.Errorf("the child's own undeclared key was dropped:\n%s", fm)
	}
	if !strings.Contains(fm, "x_review_board") {
		t.Errorf("the parent's undeclared key was not inherited:\n%s", fm)
	}
	if strings.Contains(fm, "extends:") {
		t.Errorf("the served frontmatter still names the hidden parent:\n%s", fm)
	}
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
	_, err := emfLoad(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\n---\n\nparent body\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+
			"base: &b\n  extends: shared/parent@1.x\n<<: *b\n---\n\nchild body\n")
	if err == nil {
		t.Fatal("a child whose extends arrives through a merge key must not be served")
	}
	if !errors.Is(err, core.ErrInvalidArgument) {
		t.Errorf("error = %v, want ErrInvalidArgument so the caller maps it to registry.invalid_argument", err)
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
