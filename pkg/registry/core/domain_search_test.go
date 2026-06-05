package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// ---- helpers ---------------------------------------------------------------

// sdRegistry builds a registry from a set of artifacts (id → layer) and a
// set of DOMAIN.md records keyed by "layer\x00path" → raw source, over the
// given layer list. search_domains reads only the domain records, but
// artifacts let a test assert that a path with no DOMAIN.md is excluded.
func sdRegistry(t *testing.T, layers []layer.Layer, artifacts, domains map[string]string) *core.Registry {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for id, layerID := range artifacts {
		if err := st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: "t", ArtifactID: id, Version: "1.0.0",
			ContentHash: "sha256:" + id, Type: "skill", Layer: layerID,
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
	return core.New(st, "t", layers)
}

func sdHas(res *core.SearchResult, path string) bool {
	for _, d := range res.Domains {
		if d.Path == path {
			return true
		}
	}
	return false
}

func sdDescription(res *core.SearchResult, path string) string {
	for _, d := range res.Domains {
		if d.Path == path {
			return d.Description
		}
	}
	return ""
}

func sdPaths(res *core.SearchResult) []string {
	out := make([]string, 0, len(res.Domains))
	for _, d := range res.Domains {
		out = append(out, d.Path)
	}
	return out
}

// basis returns the i-th standard basis vector of length dim (a 1 at i,
// zeros elsewhere). Distinct basis vectors are orthogonal (cosine
// distance 1); identical ones have distance 0, giving deterministic
// vector-rank control in hybrid tests.
func basis(dim, i int) []float32 {
	v := make([]float32, dim)
	v[i] = 1
	return v
}

// scriptedEmbedder maps known input texts to fixed vectors for
// deterministic hybrid-retrieval tests; an unknown text gets the last
// basis dimension, far from the vectors these tests place on lower axes.
type scriptedEmbedder struct {
	dim  int
	vecs map[string][]float32
	err  error
}

func (scriptedEmbedder) ID() string        { return "scripted" }
func (scriptedEmbedder) Model() string     { return "scripted" }
func (e scriptedEmbedder) Dimensions() int { return e.dim }
func (e scriptedEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if e.err != nil {
		return nil, e.err
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		if v, ok := e.vecs[t]; ok {
			out[i] = append([]float32(nil), v...)
			continue
		}
		out[i] = basis(e.dim, e.dim-1)
	}
	return out, nil
}

var pubLayer = []layer.Layer{{ID: "pub", Visibility: layer.Visibility{Public: true}, Precedence: 1}}

// ---- search_domains: lexical retrieval over projections --------------------

// spec: §3.2 Layer 1 / §4.7 — search_domains runs hybrid retrieval over
// each domain's projection (description + keywords + body), not a
// substring match on the path. A domain matched only by a DOMAIN.md
// keyword absent from its path and description surfaces, ranked, with its
// description populated.
func TestSearchDomains_RanksByProjectionKeyword(t *testing.T) {
	t.Parallel()
	reg := sdRegistry(t, pubLayer,
		map[string]string{"finance/ap/pay-invoice": "pub"},
		map[string]string{
			"pub\x00finance/ap": "---\ndescription: \"Accounts payable invoices\"\ndiscovery:\n  keywords:\n    - reconciliation\n---\n# AP\n",
			"pub\x00finance":    "---\ndescription: \"The finance function\"\n---\n",
		})
	// "reconciliation" appears only in finance/ap's keywords, not in any
	// path or description; the old substring-on-path matcher returned nothing.
	res, err := reg.SearchDomains(context.Background(), publicID, core.SearchDomainsOptions{Query: "reconciliation", TopK: 10})
	if err != nil {
		t.Fatalf("SearchDomains: %v", err)
	}
	if len(res.Domains) == 0 || res.Domains[0].Path != "finance/ap" {
		t.Fatalf("top domain = %v, want finance/ap matched by keyword", sdPaths(res))
	}
	if got := sdDescription(res, "finance/ap"); got == "" {
		t.Errorf("finance/ap descriptor has no description; descriptors must carry it")
	}
	if !res.Degraded {
		t.Errorf("Degraded = false; expected BM25-only (no vector store configured)")
	}
}

// spec: §4.5.1 / §4.7 — a domain without a DOMAIN.md has no projection to
// embed and does not appear in search_domains; an empty query returns
// every visible domain that does have one.
func TestSearchDomains_EmptyQueryAllWithDomainMDOnly(t *testing.T) {
	t.Parallel()
	reg := sdRegistry(t, pubLayer,
		// ops/runner has artifacts but no DOMAIN.md anywhere in its chain.
		map[string]string{"ops/runner/restart": "pub"},
		map[string]string{
			"pub\x00finance/ap": "---\ndescription: AP\n---\n",
			"pub\x00finance":    "---\ndescription: Finance\n---\n",
			"pub\x00_shared":    "---\ndescription: Shared helpers\n---\n",
		})
	res, err := reg.SearchDomains(context.Background(), publicID, core.SearchDomainsOptions{TopK: 50})
	if err != nil {
		t.Fatalf("SearchDomains: %v", err)
	}
	want := []string{"_shared", "finance", "finance/ap"}
	if got := sdPaths(res); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("empty-query domains = %v, want %v (alphabetical, DOMAIN.md only)", got, want)
	}
	if sdHas(res, "ops") || sdHas(res, "ops/runner") {
		t.Errorf("domain without a DOMAIN.md surfaced in search_domains: %v", sdPaths(res))
	}
}

// spec: §5 — scope constrains search_domains to a path prefix.
func TestSearchDomains_ScopeRestrictsSubtree(t *testing.T) {
	t.Parallel()
	reg := sdRegistry(t, pubLayer, nil, map[string]string{
		"pub\x00finance/ap": "---\ndescription: AP\n---\n",
		"pub\x00finance/ar": "---\ndescription: AR\n---\n",
		"pub\x00ops":        "---\ndescription: Ops\n---\n",
	})
	res, err := reg.SearchDomains(context.Background(), publicID, core.SearchDomainsOptions{Scope: "finance", TopK: 10})
	if err != nil {
		t.Fatalf("SearchDomains: %v", err)
	}
	if !sdHas(res, "finance/ap") || !sdHas(res, "finance/ar") {
		t.Errorf("scope=finance dropped an in-scope domain: %v", sdPaths(res))
	}
	if sdHas(res, "ops") {
		t.Errorf("scope=finance returned an out-of-scope domain: %v", sdPaths(res))
	}
}

// spec: §5 — top_k caps the returned domain count.
func TestSearchDomains_TopKCaps(t *testing.T) {
	t.Parallel()
	reg := sdRegistry(t, pubLayer, nil, map[string]string{
		"pub\x00a": "---\ndescription: a\n---\n",
		"pub\x00b": "---\ndescription: b\n---\n",
		"pub\x00c": "---\ndescription: c\n---\n",
	})
	res, err := reg.SearchDomains(context.Background(), publicID, core.SearchDomainsOptions{TopK: 2})
	if err != nil {
		t.Fatalf("SearchDomains: %v", err)
	}
	if len(res.Domains) != 2 {
		t.Errorf("len(domains) = %d, want 2 (top_k)", len(res.Domains))
	}
}

// spec: §4.7 — a domain whose DOMAIN.md was ingested only under a layer
// the caller cannot see does not surface in search_domains.
func TestSearchDomains_VisibilityFiltersByLayer(t *testing.T) {
	t.Parallel()
	layers := []layer.Layer{
		{ID: "pub", Visibility: layer.Visibility{Public: true}, Precedence: 1},
		{ID: "alice", Visibility: layer.Visibility{Users: []string{"alice@acme.com"}}, Precedence: 2},
	}
	reg := sdRegistry(t, layers, nil, map[string]string{
		"pub\x00marketing": "---\ndescription: Marketing assets\n---\n",
		"alice\x00secret":  "---\ndescription: Alice private domain\n---\n",
	})
	bob := layer.Identity{Sub: "bob@acme.com", Email: "bob@acme.com", IsAuthenticated: true}
	res, err := reg.SearchDomains(context.Background(), bob, core.SearchDomainsOptions{TopK: 10})
	if err != nil {
		t.Fatalf("SearchDomains bob: %v", err)
	}
	if !sdHas(res, "marketing") {
		t.Errorf("bob missing the public marketing domain: %v", sdPaths(res))
	}
	if sdHas(res, "secret") {
		t.Errorf("bob saw alice's private domain through search_domains: %v", sdPaths(res))
	}
	alice := layer.Identity{Sub: "alice@acme.com", Email: "alice@acme.com", IsAuthenticated: true}
	ares, err := reg.SearchDomains(context.Background(), alice, core.SearchDomainsOptions{TopK: 10})
	if err != nil {
		t.Fatalf("SearchDomains alice: %v", err)
	}
	if !sdHas(ares, "secret") {
		t.Errorf("alice missing her own domain: %v", sdPaths(ares))
	}
}

// spec: §4.5.3 — an unlisted domain is removed from discovery and stays
// out of search_domains, so it is not detectable through probing.
func TestSearchDomains_UnlistedExcluded(t *testing.T) {
	t.Parallel()
	reg := sdRegistry(t, pubLayer, nil, map[string]string{
		"pub\x00finance": "---\ndescription: Finance\n---\n",
		"pub\x00hidden":  "---\nunlisted: true\ndescription: Hidden ops\n---\n",
	})
	res, err := reg.SearchDomains(context.Background(), publicID, core.SearchDomainsOptions{TopK: 10})
	if err != nil {
		t.Fatalf("SearchDomains: %v", err)
	}
	if !sdHas(res, "finance") {
		t.Errorf("listed domain finance missing: %v", sdPaths(res))
	}
	if sdHas(res, "hidden") {
		t.Errorf("unlisted domain surfaced in search_domains: %v", sdPaths(res))
	}
}

// ---- search_domains: hybrid fusion ----------------------------------------

// spec: §3.2 Layer 1 / §4.7 — with a vector store and embedder configured,
// search_domains fuses BM25 and vector ranks via RRF; a domain the vector
// ranker finds but BM25 misses (no lexical overlap with the query) still
// surfaces. Mirrors the artifact hybrid path.
func TestSearchDomains_HybridSurfacesVectorOnlyDomain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dim := 8
	v := vector.NewMemory(dim)
	// ops/widgets has no lexical overlap with "reconciliation" but its
	// stored projection vector equals the query embedding (semantic match);
	// finance/ap matches lexically via its keyword.
	if err := v.Put(ctx, "t", "ops/widgets", core.DomainVectorVersion, basis(dim, 2)); err != nil {
		t.Fatalf("Put ops/widgets: %v", err)
	}
	if err := v.Put(ctx, "t", "finance/ap", core.DomainVectorVersion, basis(dim, 0)); err != nil {
		t.Fatalf("Put finance/ap: %v", err)
	}
	e := scriptedEmbedder{dim: dim, vecs: map[string][]float32{"reconciliation": basis(dim, 2)}}
	reg := sdRegistry(t, pubLayer, nil, map[string]string{
		"pub\x00finance/ap":  "---\ndescription: AP\ndiscovery:\n  keywords:\n    - reconciliation\n---\n",
		"pub\x00ops/widgets": "---\ndescription: Gadget catalog\n---\n",
	}).WithVectorSearch(v, e)
	res, err := reg.SearchDomains(ctx, publicID, core.SearchDomainsOptions{Query: "reconciliation", TopK: 10})
	if err != nil {
		t.Fatalf("SearchDomains: %v", err)
	}
	if res.Degraded {
		t.Errorf("Degraded = true; expected hybrid path")
	}
	if !sdHas(res, "ops/widgets") {
		t.Errorf("vector-only domain ops/widgets absent; fusion dropped it: %v", sdPaths(res))
	}
	if !sdHas(res, "finance/ap") {
		t.Errorf("lexical match finance/ap absent: %v", sdPaths(res))
	}
}

// spec: §4.7 — when the embedder fails, search_domains degrades to BM25
// and reports Degraded=true rather than erroring; lexical matches remain.
func TestSearchDomains_DegradesOnEmbedderFailure(t *testing.T) {
	t.Parallel()
	e := scriptedEmbedder{dim: 8, err: errors.New("offline")}
	reg := sdRegistry(t, pubLayer, nil, map[string]string{
		"pub\x00finance/ap": "---\ndescription: AP\ndiscovery:\n  keywords:\n    - reconciliation\n---\n",
	}).WithVectorSearch(vector.NewMemory(8), e)
	res, err := reg.SearchDomains(context.Background(), publicID, core.SearchDomainsOptions{Query: "reconciliation", TopK: 10})
	if err != nil {
		t.Fatalf("SearchDomains: %v", err)
	}
	if !res.Degraded {
		t.Errorf("Degraded = false; expected true (embedder offline)")
	}
	if !sdHas(res, "finance/ap") {
		t.Errorf("BM25 fallback dropped the lexical match: %v", sdPaths(res))
	}
}

// spec: §4.7 — Reembed re-embeds every DOMAIN.md projection into the
// domain index so search_domains has a semantic ranker.
func TestReembed_EmbedsDomains(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dim := 16
	v := vector.NewMemory(dim)
	reg := sdRegistry(t, pubLayer, nil, map[string]string{
		"pub\x00finance/ap":  "---\ndescription: Accounts payable\ndiscovery:\n  keywords:\n    - reconciliation\n---\n",
		"pub\x00ops/widgets": "---\ndescription: Gadget catalog\n---\n",
	}).WithVectorSearch(v, fakeEmbedder{dim: dim})
	res, err := reg.Reembed(ctx, core.ReembedOptions{})
	if err != nil {
		t.Fatalf("Reembed: %v", err)
	}
	// Two domains, no artifacts: both re-embedded, none failed.
	if res.Total != 2 || res.Succeeded != 2 || len(res.Failed) != 0 {
		t.Fatalf("Reembed result = %+v, want 2 domains embedded", res)
	}
	matches, err := v.Query(ctx, "t", basis(dim, 0), 100)
	if err != nil {
		t.Fatalf("vector Query: %v", err)
	}
	got := map[string]bool{}
	for _, m := range matches {
		if m.Version == core.DomainVectorVersion {
			got[m.ArtifactID] = true
		}
	}
	if !got["finance/ap"] || !got["ops/widgets"] {
		t.Errorf("domain vectors missing after Reembed: %v", got)
	}
}

// ---- search_artifacts: vector-only matches -----------------------

// spec: §3.2 Layer 2 / §4.7 — hybrid search_artifacts must surface an
// artifact the vector ranker finds but BM25 misses (semantically related,
// no query-term overlap). Before the fix the RRF reorder rebuilt results
// from the BM25 set only and silently dropped vector-only matches.
func TestSearchArtifacts_VectorOnlyMatchSurfaces(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, m := range []store.ManifestRecord{
		{TenantID: "t", ArtifactID: "alpha", Version: "1.0.0", ContentHash: "sha256:1", Type: "skill", Description: "alpha skill", Layer: "L"},
		{TenantID: "t", ArtifactID: "beta", Version: "1.0.0", ContentHash: "sha256:2", Type: "skill", Description: "beta skill", Layer: "L"},
		{TenantID: "t", ArtifactID: "gamma", Version: "1.0.0", ContentHash: "sha256:3", Type: "skill", Description: "unrelated widget", Layer: "L"},
	} {
		if err := st.PutManifest(ctx, m); err != nil {
			t.Fatalf("PutManifest: %v", err)
		}
	}
	dim := 8
	v := vector.NewMemory(dim)
	// gamma's stored vector equals the query embedding (the semantic
	// neighbor); alpha and beta sit on orthogonal axes. gamma shares no
	// query term, so BM25 never scores it.
	_ = v.Put(ctx, "t", "gamma", "1.0.0", basis(dim, 2))
	_ = v.Put(ctx, "t", "alpha", "1.0.0", basis(dim, 0))
	_ = v.Put(ctx, "t", "beta", "1.0.0", basis(dim, 1))
	e := scriptedEmbedder{dim: dim, vecs: map[string][]float32{"alpha skill": basis(dim, 2)}}
	reg := core.New(st, "t", []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}}}).
		WithVectorSearch(v, e)
	res, err := reg.SearchArtifacts(ctx, publicID, core.SearchArtifactsOptions{Query: "alpha skill", TopK: 5})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if res.Degraded {
		t.Fatalf("Degraded = true; want hybrid path")
	}
	found := false
	for _, r := range res.Results {
		if r.ID == "gamma" {
			found = true
		}
	}
	if !found {
		ids := make([]string, 0, len(res.Results))
		for _, r := range res.Results {
			ids = append(ids, r.ID)
		}
		t.Errorf("vector-only match gamma absent from results %v; RRF dropped it", ids)
	}
}
