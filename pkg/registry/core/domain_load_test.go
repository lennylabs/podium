package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// dlRegistry builds a registry from a set of artifact IDs (one version
// each) and a set of DOMAIN.md records keyed by "layer\x00path" → raw
// source. Layers L1 (precedence 1) and L2 (precedence 2) are available
// for cross-layer merge tests; every artifact lands in L1 unless its ID
// is prefixed by the layer in artifactLayers.
func dlRegistry(t *testing.T, ids []string, domains map[string]string) *core.Registry {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, id := range ids {
		if err := st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: "t", ArtifactID: id, Version: "1.0.0",
			ContentHash: "sha256:" + id, Type: "skill", Layer: "L1",
		}); err != nil {
			t.Fatalf("PutManifest %s: %v", id, err)
		}
	}
	for key, raw := range domains {
		layerID, path, _ := strings.Cut(key, "\x00")
		if err := st.PutDomain(context.Background(), store.DomainRecord{
			TenantID: "t", Layer: layerID, Path: path, Raw: []byte(raw),
		}); err != nil {
			t.Fatalf("PutDomain %s: %v", key, err)
		}
	}
	return core.New(st, "t", []layer.Layer{
		{ID: "L1", Visibility: layer.Visibility{Public: true}, Precedence: 1},
		{ID: "L2", Visibility: layer.Visibility{Public: true}, Precedence: 2},
	})
}

func dlNotableIDs(res *core.LoadDomainResult) []string {
	out := make([]string, 0, len(res.Notable))
	for _, n := range res.Notable {
		out = append(out, n.ID)
	}
	return out
}

func dlContains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// spec: §4.5.5 (F-4.5.2) — the requested domain's description resolves
// to the prose body, then frontmatter description, then the basename
// fallback.
func TestLoadDomain_RequestedDescriptionResolution(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Body wins.
	reg := dlRegistry(t, []string{"finance/ap/x"}, map[string]string{
		"L1\x00finance/ap": "---\ndescription: AP fm\n---\n\n# AP body text\n",
	})
	res, err := reg.LoadDomain(ctx, publicID, "finance/ap", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if !strings.Contains(res.Description, "AP body text") {
		t.Errorf("description = %q, want the prose body", res.Description)
	}

	// Frontmatter description when no body.
	reg = dlRegistry(t, []string{"finance/ap/x"}, map[string]string{
		"L1\x00finance/ap": "---\ndescription: AP fm\n---\n",
	})
	res, _ = reg.LoadDomain(ctx, publicID, "finance/ap", core.LoadDomainOptions{})
	if res.Description != "AP fm" {
		t.Errorf("description = %q, want the frontmatter value", res.Description)
	}

	// Fallback when no DOMAIN.md.
	reg = dlRegistry(t, []string{"finance/accounts-payable/x"}, nil)
	res, _ = reg.LoadDomain(ctx, publicID, "finance/accounts-payable", core.LoadDomainOptions{})
	if res.Description != "Accounts Payable" {
		t.Errorf("description = %q, want the synthesized fallback", res.Description)
	}
}

// spec: §4.5.5 (F-4.5.3) — keywords are returned verbatim for the
// requested domain; the root returns an empty list.
func TestLoadDomain_Keywords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg := dlRegistry(t, []string{"finance/ap/x"}, map[string]string{
		"L1\x00finance/ap": "---\ndescription: AP\ndiscovery:\n  keywords:\n    - invoice\n    - 1099\n---\n",
	})
	res, err := reg.LoadDomain(ctx, publicID, "finance/ap", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if !dlContains(res.Keywords, "invoice") || !dlContains(res.Keywords, "1099") {
		t.Errorf("keywords = %v, want invoice and 1099", res.Keywords)
	}
	root, _ := reg.LoadDomain(ctx, publicID, "", core.LoadDomainOptions{})
	if root.Keywords == nil || len(root.Keywords) != 0 {
		t.Errorf("root keywords = %v, want an empty list", root.Keywords)
	}
}

// spec: §4.5.3 / §4.5.5 (F-4.5.5) — an unlisted folder and its subtree
// are removed from enumeration and return domain.not_found, while a
// sibling stays enumerable.
func TestLoadDomain_Unlisted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg := dlRegistry(t, []string{"finance/x", "_shared/helpers/v"}, map[string]string{
		"L1\x00_shared": "---\nunlisted: true\n---\n",
	})
	root, err := reg.LoadDomain(ctx, publicID, "", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain root: %v", err)
	}
	for _, s := range root.Subdomains {
		if s.Path == "_shared" {
			t.Errorf("unlisted _shared appears in enumeration: %+v", root.Subdomains)
		}
	}
	if _, err := reg.LoadDomain(ctx, publicID, "_shared", core.LoadDomainOptions{}); err == nil {
		t.Error("load_domain(_shared) should return domain.not_found")
	}
	if _, err := reg.LoadDomain(ctx, publicID, "_shared/helpers", core.LoadDomainOptions{}); err == nil {
		t.Error("unlisted propagates to subtree: _shared/helpers should be not_found")
	}
}

// spec: §4.5.2 (F-4.5.6) — include globs pull artifacts from other
// prefixes into the notable list; exclude is applied after.
func TestLoadDomain_Imports(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg := dlRegistry(t, []string{
		"finance/ap/pay-invoice",
		"_shared/regex/ssn",
		"_shared/regex/iban",
	}, map[string]string{
		"L1\x00finance/ap": "---\ndescription: AP\ninclude:\n  - _shared/regex/**\nexclude:\n  - _shared/regex/iban\n---\n",
	})
	res, err := reg.LoadDomain(ctx, publicID, "finance/ap", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	ids := dlNotableIDs(res)
	if !dlContains(ids, "_shared/regex/ssn") {
		t.Errorf("notable %v missing imported _shared/regex/ssn", ids)
	}
	if dlContains(ids, "_shared/regex/iban") {
		t.Errorf("exclude did not drop _shared/regex/iban: %v", ids)
	}
	// Imported artifacts keep their canonical IDs.
	if !dlContains(ids, "finance/ap/pay-invoice") {
		t.Errorf("notable %v missing the canonical child", ids)
	}
}

// spec: §4.5.5 (F-4.5.7) — depth expands the rendered subtree; the
// resolved max_depth ceiling caps it and the cap is noted.
func TestLoadDomain_DepthAndMaxDepth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// finance/ap has a direct artifact (not a passthrough) and a sub.
	reg := dlRegistry(t, []string{"finance/ap/direct", "finance/ap/sub/deep"}, nil)

	d1, err := reg.LoadDomain(ctx, publicID, "finance", core.LoadDomainOptions{Depth: 1})
	if err != nil {
		t.Fatalf("LoadDomain depth=1: %v", err)
	}
	ap := findSub(d1.Subdomains, "finance/ap")
	if ap == nil {
		t.Fatalf("finance/ap missing: %+v", d1.Subdomains)
	}
	if len(ap.Subdomains) != 0 {
		t.Errorf("depth=1 should not nest finance/ap children: %+v", ap.Subdomains)
	}

	d2, _ := reg.LoadDomain(ctx, publicID, "finance", core.LoadDomainOptions{Depth: 2})
	ap = findSub(d2.Subdomains, "finance/ap")
	if ap == nil || findSub(ap.Subdomains, "finance/ap/sub") == nil {
		t.Errorf("depth=2 should nest finance/ap/sub under finance/ap: %+v", d2.Subdomains)
	}

	// max_depth ceiling from DOMAIN.md caps a larger caller depth.
	reg = dlRegistry(t, []string{"finance/ap/x"}, map[string]string{
		"L1\x00finance": "---\ndescription: Finance\ndiscovery:\n  max_depth: 1\n---\n",
	})
	capped, _ := reg.LoadDomain(ctx, publicID, "finance", core.LoadDomainOptions{Depth: 5})
	if !strings.Contains(capped.Note, "ceiling of 1") {
		t.Errorf("note = %q, want the depth-cap sentence", capped.Note)
	}
}

// spec: §4.5.4 (F-4.5.2..7) — DOMAIN.md candidates merge across layers:
// description last-layer-wins, keywords append-unique, unlisted
// most-restrictive.
func TestLoadDomain_CrossLayerMerge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg := dlRegistry(t, []string{"finance/ap/x"}, map[string]string{
		"L1\x00finance/ap": "---\ndescription: Org AP\ndiscovery:\n  keywords:\n    - invoice\n---\n",
		"L2\x00finance/ap": "---\ndescription: Team AP\ndiscovery:\n  keywords:\n    - vendor\n---\n",
	})
	res, err := reg.LoadDomain(ctx, publicID, "finance/ap", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if res.Description != "Team AP" {
		t.Errorf("merged description = %q, want Team AP (last-layer-wins)", res.Description)
	}
	if !dlContains(res.Keywords, "invoice") || !dlContains(res.Keywords, "vendor") {
		t.Errorf("merged keywords = %v, want both invoice and vendor", res.Keywords)
	}

	// Most-restrictive unlisted: L2 sets unlisted true → not_found.
	reg = dlRegistry(t, []string{"finance/ap/x"}, map[string]string{
		"L1\x00finance/ap": "---\nunlisted: false\ndescription: AP\n---\n",
		"L2\x00finance/ap": "---\nunlisted: true\n---\n",
	})
	if _, err := reg.LoadDomain(ctx, publicID, "finance/ap", core.LoadDomainOptions{}); err == nil {
		t.Error("merged unlisted (most-restrictive) should yield domain.not_found")
	}
}

// spec: §4.5.5 (F-4.5.7) — deprioritize ranks matching children last and
// excludes them when the notable cap leaves no room.
func TestLoadDomain_Deprioritize(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg := dlRegistry(t, []string{"finance/ap/old-invoice", "finance/ap/pay-invoice"}, map[string]string{
		"L1\x00finance/ap": "---\ndescription: AP\ndiscovery:\n  notable_count: 1\n  deprioritize:\n    - finance/ap/old-invoice\n---\n",
	})
	res, err := reg.LoadDomain(ctx, publicID, "finance/ap", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	ids := dlNotableIDs(res)
	if len(ids) != 1 || ids[0] != "finance/ap/pay-invoice" {
		t.Errorf("notable = %v, want only finance/ap/pay-invoice (old-invoice deprioritized out)", ids)
	}
}

func findSub(subs []core.DomainDescriptor, path string) *core.DomainDescriptor {
	for i := range subs {
		if subs[i].Path == path {
			return &subs[i]
		}
	}
	return nil
}
