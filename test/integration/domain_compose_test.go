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

// Spec: §4.5 (F-4.5.1..7) — DOMAIN.md composition end to end through the
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
