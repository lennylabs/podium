package e2e

// a large generated catalog (400-plus artifacts across a deep
// domain tree with varied DOMAIN.md discovery knobs) is ingested by a standard
// standalone server, then walked through load_domain and search_artifacts.
//
// The test asserts the three §4.5.5 / §5 / §11 contracts at scale:
//   - root load_domain returns a bounded subtree within the token budget
//     (the renderer tightens depth and the notable list to fit
//     target_response_tokens, and names the reduction in the note);
//   - a nested load_domain applies folding (fold_below_artifacts collapses a
//     sparse subdomain into its parent's leaf set with a folded_from
//     annotation) and the notable cap (notable_count);
//   - search_artifacts browse mode is deterministic: a fixed query returns a
//     stable ranking across repeated calls, total_matched reflects the true
//     match count even when top_k truncates the returned results, narrowing
//     scope partitions the catalog into non-overlapping slices, and top_k > 50
//     is rejected with registry.invalid_argument.
//
// There is no pagination cursor (§4.5.5); "pagination" at this scale is scope
// narrowing plus top_k over a stable default ranking, which this test exercises.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// scaleCatalog generates a registry path map of at least 400 artifacts spread
// across at least 12 top-level domains and at least 30 subdomains, with varied
// DOMAIN.md discovery knobs. The generation is deterministic so the assertions
// are reproducible.
//
// Layout per top-level domain d{NN}:
//   - d{NN}/DOMAIN.md carries a discovery: block whose knobs vary by index.
//   - several subdomains d{NN}/s{M} each hold a handful of context artifacts.
//   - one deliberately sparse subdomain d{NN}/sparse holds a single artifact so
//     a fold_below_artifacts threshold collapses it into the parent leaf set.
//   - a third nesting level d{NN}/s0/deep/leaf exists so depth tightening and
//     max_depth have something to compress.
func scaleCatalog() (entries map[string]string, totalArtifacts int, topDomains []string) {
	entries = map[string]string{
		// multi_layer is off: the whole tree is one local-source layer rooted
		// at the registry path, which is the standalone --layer-path default.
		".registry-config": "multi_layer: false\n",
	}
	const domains = 16 // >= 12 top-level domains; 16 x 26 = 416 artifacts
	for d := 0; d < domains; d++ {
		dom := fmt.Sprintf("d%02d", d)
		topDomains = append(topDomains, dom)

		// Vary the DOMAIN.md discovery knobs across domains so the budget,
		// folding, and notable-cap paths are all exercised somewhere in the
		// tree. notable_count cycles low so the cap is observable; the budget
		// is set tight on the root via registry.yaml below, and per-domain
		// here for the nested assertion.
		notable := 2 + d%4  // 2..5
		foldBelow := 2      // a 1-artifact subdomain folds
		maxDepth := 2 + d%3 // 2..4
		dm := fmt.Sprintf(
			"---\ndescription: Domain %s operations and references.\nkeywords: [%s, scaletopic, area%d]\ndiscovery:\n  notable_count: %d\n  fold_below_artifacts: %d\n  max_depth: %d\n---\n\n# Domain %s\n\nLong-form context for domain %s. SCALEBODY-%s marker prose.\n",
			dom, dom, d, notable, foldBelow, maxDepth, dom, dom, dom,
		)
		entries[dom+"/DOMAIN.md"] = dm

		// Regular subdomains with several artifacts each (above the fold
		// threshold so they stay enumerated as their own subdomain).
		const subs = 3
		for s := 0; s < subs; s++ {
			sub := fmt.Sprintf("s%d", s)
			const perSub = 8
			for a := 0; a < perSub; a++ {
				id := fmt.Sprintf("%s/%s/a%02d", dom, sub, a)
				desc := fmt.Sprintf("Artifact %s in %s subdomain %s. scaletopic shared term.", id, dom, sub)
				entries[id+"/ARTIFACT.md"] = contextArtifact(desc)
				totalArtifacts++
			}
		}

		// A third nesting level under s0 so depth compression and max_depth
		// have a level to drop.
		deepID := fmt.Sprintf("%s/s0/deep/leaf", dom)
		entries[deepID+"/ARTIFACT.md"] = contextArtifact(
			fmt.Sprintf("Deep leaf artifact %s. scaletopic shared term.", deepID))
		totalArtifacts++

		// A deliberately sparse subdomain (single artifact) so a
		// fold_below_artifacts: 2 threshold collapses it into the parent's
		// leaf set with a folded_from annotation.
		sparseID := fmt.Sprintf("%s/sparse/only", dom)
		entries[sparseID+"/ARTIFACT.md"] = contextArtifact(
			fmt.Sprintf("Sole artifact in the sparse subdomain of %s. scaletopic shared term.", dom))
		totalArtifacts++
	}
	return entries, totalArtifacts, topDomains
}

// scaleResultIDs decodes a SearchResponse result list into its artifact IDs in
// returned order.
func scaleResultIDs(m map[string]any) []string {
	var ids []string
	results, _ := m["results"].([]any)
	for _, r := range results {
		if entry, ok := r.(map[string]any); ok {
			if id, ok := entry["id"].(string); ok {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// scaleSubdomainPaths flattens a load_domain subdomains tree into the set of
// every path it contains, at any nesting level.
func scaleSubdomainPaths(m map[string]any) map[string]bool {
	out := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		arr, _ := v.([]any)
		for _, e := range arr {
			sub, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if p, ok := sub["path"].(string); ok {
				out[p] = true
			}
			walk(sub["subdomains"])
		}
	}
	walk(m["subdomains"])
	return out
}

// scaleRenderedDepth returns the deepest nesting level present in a load_domain
// subdomains tree relative to the requested path: a flat list of immediate
// children is depth 1.
func scaleRenderedDepth(m map[string]any) int {
	var depth func(any) int
	depth = func(v any) int {
		arr, _ := v.([]any)
		max := 0
		for _, e := range arr {
			sub, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if d := 1 + depth(sub["subdomains"]); d > max {
				max = d
			}
		}
		return max
	}
	return depth(m["subdomains"])
}

// startScaleServer boots a standard standalone server over the generated
// catalog with a tight tenant-scope response budget so the §4.5.5 budget pass
// fires on the root load_domain. The discovery block lives in a registry.yaml
// config file (the §13.12 tenant-scope `discovery:` block is config-file-only),
// supplied via PODIUM_CONFIG_FILE alongside the generated --layer-path.
func startScaleServer(t testing.TB, entries map[string]string) *serverProc {
	t.Helper()
	reg := writeRegistry(t, entries)
	cfgDir := t.TempDir()
	cfgFile := filepath.Join(cfgDir, "registry.yaml")
	// §13.12 nests every server-side key under `registry:`. A tight
	// target_response_tokens forces the root load_domain to tighten depth and
	// the notable list to fit (§4.5.5).
	cfg := "registry:\n  layer_path: " + reg + "\n  discovery:\n    target_response_tokens: 700\n    max_depth: 3\n"
	if err := os.WriteFile(cfgFile, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}
	return startServerArgs(t,
		[]string{"HOME=" + t.TempDir(), "PODIUM_CONFIG_FILE=" + cfgFile},
		"serve", "--standalone")
}

// TestScale_LargeCatalogIngestAndWalk is the journey.
func TestScale_LargeCatalogIngestAndWalk(t *testing.T) {
	t.Parallel()
	entries, total, topDomains := scaleCatalog()
	if total < 400 {
		t.Fatalf("generator produced %d artifacts, want >= 400", total)
	}
	srv := startScaleServer(t, entries)

	// ---- 1. root load_domain stays within the token budget ----------------
	var root map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain", &root)

	// Every top-level domain is enumerated at the root.
	rootSubs := scaleSubdomainPaths(root)
	for _, dom := range topDomains {
		if !rootSubs[dom] {
			t.Errorf("root load_domain missing top-level domain %q; subdomains=%v", dom, root["subdomains"])
		}
	}

	// The renderer tightened depth or notable to fit the tight tenant budget,
	// so the response carries a §4.5.5 rendering note naming the reduction.
	note, _ := root["note"].(string)
	if note == "" {
		t.Errorf("root load_domain produced no rendering note despite a 700-token budget over a 400+ artifact tree; subtree depth=%d", scaleRenderedDepth(root))
	} else if !strings.Contains(note, "to fit the response budget") {
		t.Errorf("rendering note does not describe a budget reduction: %q", note)
	}

	// The token-budget pass compressed the rendered subtree below the
	// configured max_depth ceiling of 3 (the directory tree nests three levels
	// under root: d{NN}, d{NN}/s0, d{NN}/s0/deep). The §4.5.5 pass tightens
	// depth first, stopping at the shallowest level that fits, and never drops
	// the immediate children. The exact stopping depth is an internal of the
	// estimator; the contract this asserts is that compression happened and the
	// immediate children survived.
	if d := scaleRenderedDepth(root); d >= 3 {
		t.Errorf("root subtree depth=%d, want < the max_depth ceiling of 3 (the budget pass should have compressed the deepest levels)", d)
	}
	if d := scaleRenderedDepth(root); d < 1 {
		t.Errorf("root subtree depth=%d: the budget pass must not drop the immediate children", d)
	}

	// The estimated rendered size must respect the budget. The estimator is
	// bytes/4 over the subdomain tree plus the notable descriptors (§4.5.5);
	// reproduce it from the JSON so the assertion is independent of internals.
	if est := scaleEstimateTokens(t, root); est > 700 {
		t.Errorf("root rendered response estimate=%d tokens, want <= 700 budget; note=%q", est, note)
	}

	// ---- 2. nested load_domain folds and caps -----------------------------
	// d00 has notable_count: 2, fold_below_artifacts: 2. Its sparse subdomain
	// (one artifact) must fold into d00's leaf set, and its notable list must
	// not exceed the cap.
	var d00 map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=d00", &d00)

	// fold_below_artifacts: 2 collapses d00/sparse (one artifact) into d00's
	// leaf set; the folded artifact carries a folded_from annotation and the
	// sparse subdomain is gone from the enumerated subtree.
	d00Subs := scaleSubdomainPaths(d00)
	if d00Subs["d00/sparse"] {
		t.Errorf("d00/sparse should have folded below fold_below_artifacts=2, but it is still enumerated: %v", d00["subdomains"])
	}
	foundFolded := false
	for _, n := range d00NotableEntries(d00) {
		if ff, _ := n["folded_from"].(string); ff != "" {
			foundFolded = true
		}
	}
	if !foundFolded {
		t.Errorf("d00 notable carries no folded_from entry; fold_below_artifacts should have lifted the sparse artifact into the leaf set: %v", d00["notable"])
	}

	// notable_count: 2 caps the notable list.
	if n := len(d00NotableEntries(d00)); n > 2 {
		t.Errorf("d00 notable length=%d, want <= notable_count=2", n)
	}

	// ---- 3. search_artifacts paginates deterministically ------------------
	// Stable ranking: the identical browse query over the whole catalog
	// returns the same ordered result IDs on repeated calls.
	first := scaleSearch(t, srv, "scaletopic", "", 50)
	second := scaleSearch(t, srv, "scaletopic", "", 50)
	if !equalStrings(scaleResultIDs(first), scaleResultIDs(second)) {
		t.Errorf("search_artifacts ranking is not stable across identical calls:\n first=%v\nsecond=%v", scaleResultIDs(first), scaleResultIDs(second))
	}

	// total_matched reflects the true match count even when top_k truncates
	// the returned results. Every generated artifact carries the "scaletopic"
	// term, so the whole catalog matches.
	if got := len(scaleResultIDs(first)); got > 50 {
		t.Errorf("top_k=50 returned %d results, want <= 50", got)
	}
	if tm := asInt(first["total_matched"]); tm != total {
		t.Errorf("total_matched=%d, want %d (the full match count must survive top_k truncation)", tm, total)
	}

	// Narrowing scope partitions the catalog into non-overlapping slices: the
	// per-domain browse counts sum to the full total, and no artifact appears
	// under two distinct top-level scopes. This is how a caller "pages" a
	// large catalog without a cursor (§4.5.5).
	sumScoped := 0
	seen := map[string]bool{}
	for _, dom := range topDomains {
		scoped := scaleSearch(t, srv, "", dom, 50)
		tm := asInt(scoped["total_matched"])
		sumScoped += tm
		for _, id := range scaleResultIDs(scoped) {
			if !strings.HasPrefix(id, dom+"/") {
				t.Errorf("scope=%q returned an out-of-scope artifact %q", dom, id)
			}
			if seen[id] {
				t.Errorf("artifact %q appeared under two distinct scopes; the partition is not disjoint", id)
			}
			seen[id] = true
		}
		// Each top-level domain holds the same generated count.
		if want := total / len(topDomains); tm != want {
			t.Errorf("scope=%q total_matched=%d, want %d per-domain", dom, tm, want)
		}
	}
	if sumScoped != total {
		t.Errorf("per-domain scope counts sum to %d, want the full catalog total %d", sumScoped, total)
	}

	// Browse-mode order within a single scope is the deterministic §4.7
	// alphabetical-by-id listing; assert it directly so a future ranking
	// change to browse mode is caught.
	browse := scaleSearch(t, srv, "", "d00", 50)
	ids := scaleResultIDs(browse)
	if !sort.StringsAreSorted(ids) {
		t.Errorf("scope=d00 browse results are not in deterministic ascending-id order: %v", ids)
	}

	// top_k > 50 is rejected with a structured registry.invalid_argument
	// error at the registry, even at scale.
	st, body := getRaw(t, srv.BaseURL+"/v1/search_artifacts?scope=d00&top_k=51")
	if st != 400 {
		t.Errorf("top_k=51 returned HTTP %d, want 400; body=%s", st, body)
	}
	if !strings.Contains(string(body), "registry.invalid_argument") {
		t.Errorf("top_k=51 error body missing registry.invalid_argument: %s", body)
	}
}

// d00NotableEntries decodes a load_domain notable list into its entry maps.
func d00NotableEntries(m map[string]any) []map[string]any {
	var out []map[string]any
	arr, _ := m["notable"].([]any)
	for _, e := range arr {
		if entry, ok := e.(map[string]any); ok {
			out = append(out, entry)
		}
	}
	return out
}

// scaleSearch issues one search_artifacts call and decodes the response.
func scaleSearch(t testing.TB, srv *serverProc, query, scope string, topK int) map[string]any {
	t.Helper()
	u := fmt.Sprintf("%s/v1/search_artifacts?top_k=%d", srv.BaseURL, topK)
	if query != "" {
		u += "&query=" + query
	}
	if scope != "" {
		u += "&scope=" + scope
	}
	var m map[string]any
	getJSON(t, u, &m)
	return m
}

// scaleEstimateTokens reproduces the §4.5.5 response-token estimator over the
// JSON load_domain response: bytes/4 over the subdomain tree (path + name +
// description + 16 per node) plus the notable descriptors (id + description +
// type + 32, plus tags). It mirrors estimateResponseTokens so the budget
// assertion does not reach into unexported internals.
func scaleEstimateTokens(t testing.TB, m map[string]any) int {
	t.Helper()
	bytes := scaleSubdomainBytes(m["subdomains"])
	for _, n := range d00NotableEntries(m) {
		bytes += len(strFromAny(n["id"])) + len(strFromAny(n["description"])) + len(strFromAny(n["type"])) + 32
		if tags, ok := n["tags"].([]any); ok {
			for _, tg := range tags {
				bytes += len(strFromAny(tg)) + 4
			}
		}
	}
	return bytes / 4
}

func scaleSubdomainBytes(v any) int {
	arr, _ := v.([]any)
	total := 0
	for _, e := range arr {
		sub, ok := e.(map[string]any)
		if !ok {
			continue
		}
		total += len(strFromAny(sub["path"])) + len(strFromAny(sub["name"])) + len(strFromAny(sub["description"])) + 16
		total += scaleSubdomainBytes(sub["subdomains"])
	}
	return total
}

func strFromAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// (json import kept for symmetry with other e2e helpers that decode envelopes;
// the scale test decodes via getJSON which already uses encoding/json.)
var _ = json.Marshal
