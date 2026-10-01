package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// descChainTenant is the tenant every descriptor-chain fixture writes into.
const descChainTenant = "t"

// descChainFixture writes rows straight to store.Memory, one layer per row
// position, so a test controls each member's pin and can tombstone an ancestor
// after the chain is written.
type descChainFixture struct {
	st     *store.Memory
	layers []layer.Layer
}

func newDescChainFixture(t *testing.T) *descChainFixture {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: descChainTenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	return &descChainFixture{st: st}
}

// put writes one row in its own public layer, whose precedence follows the
// order rows are written, and whose frontmatter is header plus the extends
// line pin names.
func (f *descChainFixture) put(t *testing.T, layerID, id, ver, pin, header string) {
	t.Helper()
	ctx := context.Background()
	order := len(f.layers) + 1
	if err := f.st.PutLayerConfig(ctx, store.LayerConfig{TenantID: descChainTenant, ID: layerID, Order: order, Public: true}); err != nil {
		t.Fatalf("PutLayerConfig %s: %v", layerID, err)
	}
	f.layers = append(f.layers, layer.Layer{ID: layerID, Precedence: order, Visibility: layer.Visibility{Public: true}})
	fm := "---\ntype: agent\nversion: " + ver + "\ndescription: " + id + " desc\n" + header
	if pin != "" {
		fm += "extends: " + pin + "\n"
	}
	fm += "---\n\nbody of " + id + "\n"
	if err := f.st.PutManifest(ctx, store.ManifestRecord{
		TenantID: descChainTenant, ArtifactID: id, Version: ver, Layer: layerID,
		ContentHash: "sha256:" + strings.Repeat("0", 64), Type: "agent",
		Description: id + " desc", Frontmatter: []byte(fm), ExtendsPin: pin,
	}); err != nil {
		t.Fatalf("PutManifest %s: %v", id, err)
	}
}

func (f *descChainFixture) search(t *testing.T, id string) core.ArtifactDescriptor {
	t.Helper()
	reg := core.New(f.st, descChainTenant, f.layers)
	res, err := reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	return findResult(t, res, id)
}

// Spec: §4.6 hidden parents (withheld) — a pinned child whose block names its
// parent under a key other than extends: is served without its frontmatter
// block, the test load_artifact applies to the same child.
func TestSearch_DescriptorRefusesChildAuthoredParentSpelling(t *testing.T) {
	t.Parallel()
	f := newDescChainFixture(t)
	f.put(t, "hidden", "acme/hidden-parent", "1.0.0", "", "")
	f.put(t, "team", "team/child", "2.0.0", "acme/hidden-parent@1.0.0", "acme_base: acme/hidden-parent\n")
	got := f.search(t, "team/child")
	if got.Frontmatter != "" {
		t.Errorf("Frontmatter = %q, want no block", got.Frontmatter)
	}
}

// Spec: §4.6 hidden parents (withheld) — the control for the case above: a
// pinned child with no second reference keeps its block without extends:.
func TestSearch_DescriptorRefusesChildAuthoredParentSpelling_Control(t *testing.T) {
	t.Parallel()
	f := newDescChainFixture(t)
	f.put(t, "hidden", "acme/hidden-parent", "1.0.0", "", "")
	f.put(t, "team", "team/child", "2.0.0", "acme/hidden-parent@1.0.0", "acme_owner: platform-team\n")
	got := f.search(t, "team/child")
	if !strings.Contains(got.Frontmatter, "acme_owner: platform-team") {
		t.Errorf("Frontmatter = %q, want the child's authored block", got.Frontmatter)
	}
	if strings.Contains(got.Frontmatter, "extends") || strings.Contains(got.Frontmatter, "acme/hidden-parent") {
		t.Errorf("Frontmatter = %q still names the parent", got.Frontmatter)
	}
}

// Spec: §4.6 hidden parents (withheld) — the parent set spans the whole pinned
// chain. C names B only in its extends: key and names the grandparent A in an
// extension key, so a test against the immediate pin alone would serve A.
func TestSearch_DescriptorRefusesChildAuthoredGrandparentSpelling(t *testing.T) {
	t.Parallel()
	f := newDescChainFixture(t)
	f.put(t, "root", "acme/grand", "1.0.0", "", "")
	f.put(t, "mid", "acme/mid", "1.0.0", "acme/grand@1.0.0", "")
	f.put(t, "team", "team/leaf", "1.0.0", "acme/mid@1.0.0", "acme_origin: acme/grand\n")
	got := f.search(t, "team/leaf")
	if got.Frontmatter != "" {
		t.Errorf("Frontmatter = %q, want no block", got.Frontmatter)
	}
}

// Spec: §4.6 hidden parents (withheld) — a chain the search path cannot
// resolve serves no block and fails no search: the descriptor is still
// returned, and nothing in it names the unreachable ancestor.
func TestSearch_DescriptorWithBrokenChainCarriesNoBlock(t *testing.T) {
	t.Parallel()
	f := newDescChainFixture(t)
	f.put(t, "root", "acme/grand", "1.0.0", "", "")
	f.put(t, "mid", "acme/mid", "1.0.0", "acme/grand@1.0.0", "")
	f.put(t, "team", "team/leaf", "1.0.0", "acme/mid@1.0.0", "acme_owner: platform-team\n")
	if err := f.st.DeleteLayerConfig(context.Background(), descChainTenant, "root"); err != nil {
		t.Fatalf("DeleteLayerConfig: %v", err)
	}
	got := f.search(t, "team/leaf")
	if got.Frontmatter != "" {
		t.Errorf("Frontmatter = %q, want no block", got.Frontmatter)
	}
}

// Spec: §4.6 hidden parents (withheld) — a same-ID overlay extends its own
// canonical ID, which the load path removes from the parent set, so a block
// naming that ID is served.
func TestSearch_DescriptorSameIDOverlayKeepsBlock(t *testing.T) {
	t.Parallel()
	f := newDescChainFixture(t)
	f.put(t, "base", "team/overlay", "1.0.0", "", "")
	f.put(t, "team", "team/overlay", "2.0.0", "team/overlay@1.0.0", "acme_self: team/overlay\n")
	got := f.search(t, "team/overlay")
	if !strings.Contains(got.Frontmatter, "acme_self: team/overlay") {
		t.Errorf("Frontmatter = %q, want the overlay's authored block", got.Frontmatter)
	}
	if strings.Contains(got.Frontmatter, "extends") {
		t.Errorf("Frontmatter = %q keeps the extends key", got.Frontmatter)
	}
}
