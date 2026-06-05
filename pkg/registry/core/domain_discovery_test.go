package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/core"
)

// dlNotableSource returns the Source tag of the notable entry with the
// given id, or "" when the entry is absent.
func dlNotableSource(res *core.LoadDomainResult, id string) string {
	for _, n := range res.Notable {
		if n.ID == id {
			return n.Source
		}
	}
	return ""
}

// spec: §4.5.5 — each notable entry carries its selection
// source: "featured" for an author-curated entry, "signal" otherwise.
func TestLoadDomain_NotableSourceTagging(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg := dlRegistry(t, []string{"finance/ap/a", "finance/ap/b"}, map[string]string{
		"L1\x00finance/ap": "---\ndescription: AP\ndiscovery:\n  featured:\n    - finance/ap/b\n---\n",
	})
	res, err := reg.LoadDomain(ctx, publicID, "finance/ap", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if got := dlNotableSource(res, "finance/ap/b"); got != "featured" {
		t.Errorf("source(finance/ap/b) = %q, want %q", got, "featured")
	}
	if got := dlNotableSource(res, "finance/ap/a"); got != "signal" {
		t.Errorf("source(finance/ap/a) = %q, want %q", got, "signal")
	}
}

// spec: §4.5.5 "Root domain" — at root there is no featured
// source, so every notable entry is tagged "signal".
func TestLoadDomain_RootNotableSourceSignalOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg := dlRegistry(t, []string{"a", "b", "c"}, nil)
	res, err := reg.LoadDomain(ctx, publicID, "", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if len(res.Notable) == 0 {
		t.Fatalf("expected notable entries at root")
	}
	for _, n := range res.Notable {
		if n.Source != "signal" {
			t.Errorf("root notable %s source = %q, want signal", n.ID, n.Source)
		}
	}
}

// dlDeepRegistry builds a branchy three-level subtree under finance plus
// a set of direct-artifact notable entries, so the §4.5.5 budget pass has
// both nested depth and a notable list to tighten.
func dlDeepRegistry(t *testing.T) *core.Registry {
	t.Helper()
	ids := []string{
		// Direct artifacts of finance → the notable candidate pool.
		"finance/n1", "finance/n2", "finance/n3", "finance/n4",
		// Branchy nesting: deptA and deptB are level 1, each with two
		// level-2 teams, each with a level-3 service. Multiple children at
		// every level keep pass-through folding from collapsing the chain.
		"finance/deptA/teamX/svcA/leaf",
		"finance/deptA/teamY/svcB/leaf",
		"finance/deptB/teamZ/svcC/leaf",
		"finance/deptB/teamW/svcD/leaf",
	}
	return dlRegistry(t, ids, nil)
}

// spec: §4.5.5 "Rendering note" — when target_response_tokens
// forces the renderer to drop nested levels and there is nothing else to
// tighten, the note reports the depth reduction in the standalone form
// ("Subtree depth reduced from X to Y to fit the response budget.").
func TestLoadDomain_BudgetReducesDepthNote(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// A two-level branchy subtree under finance with no direct artifacts of
	// finance, so the notable list is empty and only depth can be tightened.
	reg := dlRegistry(t, []string{
		"finance/dept/teamX/leaf",
		"finance/dept/teamY/leaf",
	}, nil)
	res, err := reg.LoadDomain(ctx, publicID, "finance", core.LoadDomainOptions{
		Depth:                2,
		TargetResponseTokens: 12, // forces depth 2 → 1; notable is already empty
	})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if len(res.Notable) != 0 {
		t.Fatalf("precondition: finance should have no notable entries, got %d", len(res.Notable))
	}
	want := "Subtree depth reduced from 2 to 1 to fit the response budget."
	if res.Note != want {
		t.Errorf("note = %q, want %q", res.Note, want)
	}
	if d := renderedDepthOf(res.Subdomains); d >= 2 {
		t.Errorf("rendered depth = %d, want < 2 after budget reduction", d)
	}
}

// spec: §4.5.5 "Rendering note" — a budget tight enough to
// force both reductions produces the combined sentence form.
func TestLoadDomain_BudgetReducesDepthAndNotableNote(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg := dlDeepRegistry(t)
	res, err := reg.LoadDomain(ctx, publicID, "finance", core.LoadDomainOptions{
		Depth:                3,
		TargetResponseTokens: 5, // tiny: forces depth → 1 and notable trimming
	})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if !strings.Contains(res.Note, "Notable list reduced from") {
		t.Errorf("note = %q, want a notable-reduction clause", res.Note)
	}
	if !strings.Contains(res.Note, "subtree depth reduced from") {
		t.Errorf("note = %q, want a (lowercased) subtree-depth clause in the combined sentence", res.Note)
	}
	if !strings.HasSuffix(res.Note, "to fit the response budget.") {
		t.Errorf("note = %q, want a single trailing 'to fit the response budget.'", res.Note)
	}
}

// renderedDepthOf mirrors the package's renderedDepth for assertions: the
// number of nested levels present in a wire subtree.
func renderedDepthOf(subs []core.DomainDescriptor) int {
	max := 0
	for _, s := range subs {
		if d := 1 + renderedDepthOf(s.Subdomains); d > max {
			max = d
		}
	}
	return max
}

// spec: §13.12 / §4.5.5 — the tenant registry.yaml discovery
// block overrides the package defaults when no per-domain DOMAIN.md sets
// the knob.
func TestLoadDomain_TenantDiscoveryDefaultsApplied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg := dlRegistry(t, []string{
		"finance/a", "finance/b", "finance/c", "finance/d", "finance/e",
	}, nil)
	reg.WithDiscoveryDefaults(core.DiscoveryDefaults{NotableCount: 2}, true)
	res, err := reg.LoadDomain(ctx, publicID, "finance", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if len(res.Notable) != 2 {
		t.Errorf("notable = %d, want 2 (tenant discovery default notable_count)", len(res.Notable))
	}
}

// spec: §4.5.5 — a per-domain DOMAIN.md discovery block
// overrides the tenant default when allow_per_domain_overrides is true,
// and is ignored when it is false.
func TestLoadDomain_AllowPerDomainOverridesGate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ids := []string{"finance/ap/a", "finance/ap/b", "finance/ap/c"}
	dom := map[string]string{
		"L1\x00finance/ap": "---\ndescription: AP\ndiscovery:\n  notable_count: 1\n---\n",
	}

	// Overrides allowed: the DOMAIN.md notable_count: 1 wins.
	reg := dlRegistry(t, ids, dom)
	reg.WithDiscoveryDefaults(core.DiscoveryDefaults{NotableCount: 3}, true)
	res, err := reg.LoadDomain(ctx, publicID, "finance/ap", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain(allowed): %v", err)
	}
	if len(res.Notable) != 1 {
		t.Errorf("allowed: notable = %d, want 1 (per-domain override)", len(res.Notable))
	}

	// Overrides disabled: the DOMAIN.md discovery block is ignored, so the
	// tenant default notable_count: 3 governs.
	reg = dlRegistry(t, ids, dom)
	reg.WithDiscoveryDefaults(core.DiscoveryDefaults{NotableCount: 3}, false)
	res, err = reg.LoadDomain(ctx, publicID, "finance/ap", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain(disabled): %v", err)
	}
	if len(res.Notable) != 3 {
		t.Errorf("disabled: notable = %d, want 3 (per-domain override ignored)", len(res.Notable))
	}
}

// spec: §4.5.4 — last-layer-wins for description means the last
// layer that *supplies* a value wins; a higher-precedence DOMAIN.md that
// omits description does not clear a lower layer's value.
func TestLoadDomain_CrossLayerDescriptionNotClearedByOmission(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg := dlRegistry(t, []string{"finance/ap/x", "_shared/regex/ssn"}, map[string]string{
		// Org layer supplies the description; the team layer only adds an
		// include and omits description.
		"L1\x00finance/ap": "---\ndescription: Org AP\n---\n",
		"L2\x00finance/ap": "---\ninclude:\n  - _shared/regex/**\n---\n",
	})
	res, err := reg.LoadDomain(ctx, publicID, "finance/ap", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if res.Description != "Org AP" {
		t.Errorf("description = %q, want %q (higher layer omitting description does not clear it)", res.Description, "Org AP")
	}
	// The team layer's include still merges additively.
	if !dlContains(dlNotableIDs(res), "_shared/regex/ssn") {
		t.Errorf("notable %v missing the additively-merged import", dlNotableIDs(res))
	}
}

// spec: §4.5.5 — a single-child intermediate domain whose only
// members arrive through DOMAIN.md include: is not a bare pass-through and
// must not be collapsed away.
func TestLoadDomain_ImportedMembersPreventPassthroughCollapse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg := dlRegistry(t, []string{
		"finance/hub/sub/leaf", // makes finance/hub a single-child chain
		"_shared/regex/ssn",    // the imported member
	}, map[string]string{
		// finance/hub has no direct artifacts of its own; its only members
		// are imported. Without counting imports it looks like a bare
		// pass-through and would collapse to finance/hub/sub.
		"L1\x00finance/hub": "---\ninclude:\n  - _shared/regex/**\n---\n",
	})
	res, err := reg.LoadDomain(ctx, publicID, "finance", core.LoadDomainOptions{Depth: 1})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if findSub(res.Subdomains, "finance/hub") == nil {
		paths := make([]string, 0, len(res.Subdomains))
		for _, s := range res.Subdomains {
			paths = append(paths, s.Path)
		}
		t.Errorf("finance/hub collapsed away despite imported members; subdomains = %v", paths)
	}
}

// spec: §4.5.5 "Visibility-aware counts" — imported members
// count toward the fold_below_artifacts recursive count, so a domain that
// is sparse in canonical children but rich in imports is not folded.
func TestLoadDomain_ImportedMembersCountTowardFold(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg := dlRegistry(t, []string{
		"finance/sparse/only", // one canonical child
		"_shared/lib/a", "_shared/lib/b", "_shared/lib/c",
		"finance/dense/a", "finance/dense/b", "finance/dense/c",
	}, map[string]string{
		"L1\x00finance/sparse": "---\ninclude:\n  - _shared/lib/**\n---\n",
	})
	res, err := reg.LoadDomain(ctx, publicID, "finance", core.LoadDomainOptions{
		FoldBelowArtifacts: 3,
	})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	// finance/sparse has 1 canonical child + 3 imports = 4 ≥ 3, so it must
	// not fold; the count-only logic (1 < 3) would have folded it.
	if findSub(res.Subdomains, "finance/sparse") == nil {
		t.Errorf("finance/sparse folded despite 3 imported members raising its count above the threshold")
	}
	if findSub(res.Subdomains, "finance/dense") == nil {
		t.Errorf("finance/dense (3 canonical children) should remain a subdomain")
	}
}
