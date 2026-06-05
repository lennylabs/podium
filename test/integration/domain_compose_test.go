package integration

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.5 — DOMAIN.md composition end to end through the
// real ingest walk, the metadata store, and core.LoadDomain. This covers
// the path the unit tests bypass (they PutDomain directly): a DOMAIN.md on
// disk is walked, persisted, and applied at load_domain time for
// description, keywords, include imports, and unlisted suppression.
func TestDomainComposition_IngestToLoadDomain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	testharness.WriteTree(t, dir,
		testharness.WriteTreeOption{
			Path:    "finance/ap/DOMAIN.md",
			Content: "---\ndescription: \"AP-related operations\"\ndiscovery:\n  keywords:\n    - invoice\n    - 1099\ninclude:\n  - _shared/regex/**\nexclude:\n  - _shared/regex/iban\n---\n\n# Accounts Payable\n\nLong-form AP context body.\n",
		},
		testharness.WriteTreeOption{Path: "finance/ap/pay-invoice/ARTIFACT.md", Content: contextArtifact},
		testharness.WriteTreeOption{Path: "_shared/regex/ssn/ARTIFACT.md", Content: contextArtifact},
		testharness.WriteTreeOption{Path: "_shared/regex/iban/ARTIFACT.md", Content: contextArtifact},
		// An unlisted helper subtree that stays out of enumeration.
		testharness.WriteTreeOption{Path: "_archive/DOMAIN.md", Content: "---\nunlisted: true\n---\n"},
		testharness.WriteTreeOption{Path: "_archive/old/ARTIFACT.md", Content: contextArtifact},
	)

	st := store.NewMemory()
	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if _, err := ingest.Ingest(ctx, st, ingest.Request{
		TenantID: "t", LayerID: "L", Files: os.DirFS(dir),
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	reg := core.New(st, "t", []layer.Layer{
		{ID: "L", Visibility: layer.Visibility{Public: true}, Precedence: 1},
	})
	id := layer.Identity{IsPublic: true}

	// Requested domain: prose body in the description, keywords verbatim,
	// imported artifacts after exclude.
	res, err := reg.LoadDomain(ctx, id, "finance/ap", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain(finance/ap): %v", err)
	}
	if want := "Long-form AP context body."; !strings.Contains(res.Description, want) {
		t.Errorf("description = %q, want the prose body containing %q", res.Description, want)
	}
	if !hasString(res.Keywords, "invoice") || !hasString(res.Keywords, "1099") {
		t.Errorf("keywords = %v, want invoice and 1099", res.Keywords)
	}
	notable := notableIDs(res)
	if !hasString(notable, "_shared/regex/ssn") {
		t.Errorf("notable %v missing imported _shared/regex/ssn", notable)
	}
	if hasString(notable, "_shared/regex/iban") {
		t.Errorf("exclude did not drop _shared/regex/iban from notable: %v", notable)
	}

	// Unlisted subtree is absent from root enumeration and not loadable as a
	// domain, while a normal domain is enumerable.
	root, err := reg.LoadDomain(ctx, id, "", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain(root): %v", err)
	}
	var sawFinance, sawArchive bool
	for _, s := range root.Subdomains {
		if strings.HasPrefix(s.Path, "finance") {
			sawFinance = true
		}
		if strings.HasPrefix(s.Path, "_archive") {
			sawArchive = true
		}
	}
	if !sawFinance {
		t.Errorf("root enumeration missing finance: %+v", root.Subdomains)
	}
	if sawArchive {
		t.Errorf("unlisted _archive leaked into root enumeration: %+v", root.Subdomains)
	}
	if _, err := reg.LoadDomain(ctx, id, "_archive", core.LoadDomainOptions{}); err == nil {
		t.Error("load_domain(_archive) should return domain.not_found")
	}
}

// Spec: §13.12 / §4.5.5 — through the real ingest walk, a
// per-domain DOMAIN.md discovery override is applied when
// allow_per_domain_overrides is true and ignored when false, while the
// tenant registry.yaml discovery default governs the rest.
func TestDomainComposition_AllowPerDomainOverridesGate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	testharness.WriteTree(t, dir,
		testharness.WriteTreeOption{
			Path:    "finance/ap/DOMAIN.md",
			Content: "---\ndescription: AP\ndiscovery:\n  notable_count: 1\n---\n",
		},
		testharness.WriteTreeOption{Path: "finance/ap/a/ARTIFACT.md", Content: contextArtifact},
		testharness.WriteTreeOption{Path: "finance/ap/b/ARTIFACT.md", Content: contextArtifact},
		testharness.WriteTreeOption{Path: "finance/ap/c/ARTIFACT.md", Content: contextArtifact},
	)
	st := store.NewMemory()
	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if _, err := ingest.Ingest(ctx, st, ingest.Request{TenantID: "t", LayerID: "L", Files: os.DirFS(dir)}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	layers := []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}, Precedence: 1}}
	id := layer.Identity{IsPublic: true}
	tenant := core.DiscoveryDefaults{NotableCount: 3}

	// Overrides allowed: the DOMAIN.md notable_count: 1 wins.
	reg := core.New(st, "t", layers).WithDiscoveryDefaults(tenant, true)
	res, err := reg.LoadDomain(ctx, id, "finance/ap", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain(allowed): %v", err)
	}
	if len(res.Notable) != 1 {
		t.Errorf("allowed: notable = %d, want 1 (per-domain override)", len(res.Notable))
	}

	// Overrides disabled: the DOMAIN.md discovery block is ignored, so the
	// tenant default notable_count: 3 governs.
	reg = core.New(st, "t", layers).WithDiscoveryDefaults(tenant, false)
	res, err = reg.LoadDomain(ctx, id, "finance/ap", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain(disabled): %v", err)
	}
	if len(res.Notable) != 3 {
		t.Errorf("disabled: notable = %d, want 3 (per-domain override ignored)", len(res.Notable))
	}
}

// Spec: §4.5.5 — through the real ingest walk, a domain whose
// only members arrive via DOMAIN.md include: counts them toward the
// fold_below threshold and is preserved as a pass-through stop, rather
// than being folded or collapsed away.
func TestDomainComposition_ImportedMembersPreserveDomain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	testharness.WriteTree(t, dir,
		// finance/hub has no canonical artifacts of its own; its members
		// are imported. A single canonical descendant makes it a one-child
		// chain that the count-only logic would collapse.
		// No description/keywords: curation comes purely from the resolved
		// include: members, isolating the import path.
		testharness.WriteTreeOption{
			Path:    "finance/hub/DOMAIN.md",
			Content: "---\ninclude:\n  - _shared/lib/**\n---\n",
		},
		testharness.WriteTreeOption{Path: "finance/hub/sub/leaf/ARTIFACT.md", Content: contextArtifact},
		testharness.WriteTreeOption{Path: "_shared/lib/a/ARTIFACT.md", Content: contextArtifact},
		testharness.WriteTreeOption{Path: "_shared/lib/b/ARTIFACT.md", Content: contextArtifact},
	)
	st := store.NewMemory()
	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if _, err := ingest.Ingest(ctx, st, ingest.Request{TenantID: "t", LayerID: "L", Files: os.DirFS(dir)}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	reg := core.New(st, "t", []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}, Precedence: 1}})
	res, err := reg.LoadDomain(ctx, layer.Identity{IsPublic: true}, "finance", core.LoadDomainOptions{Depth: 1})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	var got []string
	for _, s := range res.Subdomains {
		got = append(got, s.Path)
	}
	found := false
	for _, p := range got {
		if p == "finance/hub" {
			found = true
		}
	}
	if !found {
		t.Errorf("finance/hub collapsed past despite imported members; subdomains = %v", got)
	}
}

func hasString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func notableIDs(res *core.LoadDomainResult) []string {
	out := make([]string, 0, len(res.Notable))
	for _, n := range res.Notable {
		out = append(out, n.ID)
	}
	return out
}
