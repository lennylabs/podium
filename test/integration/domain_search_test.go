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
	"github.com/lennylabs/podium/pkg/vector"
)

// bagEmbedder is a deterministic bag-of-words embedder: each lowercase
// word increments one vector dimension chosen by a stable hash. Texts
// that share words get similar vectors, which is enough to exercise the
// hybrid path without a real embedding service.
type bagEmbedder struct{ dim int }

func (bagEmbedder) ID() string        { return "bag" }
func (bagEmbedder) Model() string     { return "bag" }
func (e bagEmbedder) Dimensions() int { return e.dim }
func (e bagEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, e.dim)
		for _, f := range strings.Fields(strings.ToLower(t)) {
			h := 0
			for _, r := range f {
				h = h*31 + int(r)
			}
			if h < 0 {
				h = -h
			}
			v[h%e.dim]++
		}
		out[i] = v
	}
	return out, nil
}

// Spec: §3.2 Layer 1 / §4.7 (F-3.2.1) — search_domains hybrid retrieval
// through the real ingest walk, the metadata + vector stores, and
// core.SearchDomains. A DOMAIN.md is walked, its projection embedded at
// ingest, and a keyword-only query (the term appears in keywords, not in
// the path or description) retrieves the domain, ranked, with its
// description. A domain with no DOMAIN.md is not indexed.
func TestDomainSearch_IngestToSearchDomains(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	testharness.WriteTree(t, dir,
		testharness.WriteTreeOption{
			Path:    "finance/ap/DOMAIN.md",
			Content: "---\ndescription: \"Accounts payable operations\"\ndiscovery:\n  keywords:\n    - reconciliation\n    - remittance\n---\n\n# Accounts Payable\n\nLong-form AP context body.\n",
		},
		testharness.WriteTreeOption{Path: "finance/ap/pay-invoice/ARTIFACT.md", Content: contextArtifact},
		// ops/runner has artifacts but no DOMAIN.md anywhere in its chain.
		testharness.WriteTreeOption{Path: "ops/runner/restart/ARTIFACT.md", Content: contextArtifact},
	)

	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	v := vector.NewMemory(32)
	emb := bagEmbedder{dim: 32}
	if _, err := ingest.Ingest(ctx, st, ingest.Request{
		TenantID: "t", LayerID: "L", Files: os.DirFS(dir),
		Embedder: func(ctx context.Context, text string) ([]float32, error) {
			vs, err := emb.Embed(ctx, []string{text})
			if err != nil {
				return nil, err
			}
			return vs[0], nil
		},
		VectorPut: func(ctx context.Context, tenantID, id, ver string, vec []float32) error {
			return v.Put(ctx, tenantID, id, ver, vec)
		},
		DomainVectorPut: func(ctx context.Context, tenantID, path string, vec []float32) error {
			return v.Put(ctx, tenantID, path, core.DomainVectorVersion, vec)
		},
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	reg := core.New(st, "t", []layer.Layer{
		{ID: "L", Visibility: layer.Visibility{Public: true}, Precedence: 1},
	}).WithVectorSearch(v, emb)
	id := layer.Identity{IsPublic: true}

	res, err := reg.SearchDomains(ctx, id, core.SearchDomainsOptions{Query: "reconciliation", TopK: 10})
	if err != nil {
		t.Fatalf("SearchDomains: %v", err)
	}
	if res.Degraded {
		t.Errorf("Degraded = true; vector store and embedder are configured")
	}
	if len(res.Domains) == 0 || res.Domains[0].Path != "finance/ap" {
		t.Fatalf("domains = %+v, want finance/ap first (matched by keyword)", res.Domains)
	}
	if !strings.Contains(res.Domains[0].Description, "Accounts payable") {
		t.Errorf("finance/ap descriptor description = %q, want the DOMAIN.md description", res.Domains[0].Description)
	}
	for _, d := range res.Domains {
		if d.Path == "ops" || d.Path == "ops/runner" {
			t.Errorf("domain without a DOMAIN.md surfaced: %s", d.Path)
		}
	}
}
