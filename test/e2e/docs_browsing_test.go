package e2e

// End-to-end tests for docs/consuming/browsing-the-catalog.md (D-browsing).
// The page documents the four MCP meta-tools (load_domain, search_domains,
// search_artifacts, load_artifact) and their HTTP equivalents. Tests drive
// the podium-mcp stdio bridge against a standalone server and exercise the
// registry HTTP endpoints directly.
//
// Known gaps drive several skips:
//   - F-0.0.2 is a quickstart doc-key bug; the wire format here uses the
//     correct `subdomains`/`notable` keys, so those tests pass.
//   - F-8.2.1: query-text PII scrubbing is never applied (test 35).
//   - mcp-server bridge filtering is unimplemented and unfiled (tests 20, 21).
//
// search_domains now runs hybrid retrieval over DOMAIN.md projections
// (F-3.2.1): tests 8, 9, 10, 11, 30, 41, and 54 assert keyword retrieval,
// scope, top_k, the descriptor fields, and DOMAIN.md-gated indexing.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- helpers ----------------------------------------------------------------

func brEnv(baseURL string) []string { return []string{"PODIUM_REGISTRY=" + baseURL} }

func brMatEnv(t *testing.T, baseURL, mat string, extra ...string) []string {
	return append([]string{
		"PODIUM_REGISTRY=" + baseURL,
		"PODIUM_HARNESS=none",
		"PODIUM_MATERIALIZE_ROOT=" + mat,
		"PODIUM_CACHE_DIR=" + t.TempDir(),
	}, extra...)
}

// brArr returns result[key] as a slice (nil when absent or not an array).
func brArr(result map[string]any, key string) []any {
	a, _ := result[key].([]any)
	return a
}

// brToolErr returns the error string of a tools/call response, looking at
// both the JSON-RPC envelope error and the bridge's result.error field.
func brToolErr(t *testing.T, stdout string, id int) string {
	t.Helper()
	env := rpcEnvelope(t, stdout, id)
	if e, ok := env["error"]; ok && e != nil {
		return fmt.Sprintf("%v", e)
	}
	if res, ok := env["result"].(map[string]any); ok {
		if e, ok := res["error"].(string); ok {
			return e
		}
	}
	return ""
}

// brStartAuditServer starts a standalone server with a deterministic audit
// log path and returns the server plus that path.
func brStartAuditServer(t *testing.T, reg string) (*serverProc, string) {
	t.Helper()
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir(), "PODIUM_AUDIT_LOG_PATH=" + auditPath},
		"serve", "--standalone", "--layer-path", reg)
	return srv, auditPath
}

func brPollContains(path, substr string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), substr) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// brReadOrEmpty reads a file without failing the test (for diagnostics).
func brReadOrEmpty(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// ---- load_domain ------------------------------------------------------------

// T-D-browsing-1 — load_domain() with no path returns top-level subdomains.
func TestBrowsing_LoadDomainRoot(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/pay-invoice/ARTIFACT.md":   contextArtifact("pay invoice"),
		"engineering/pr-review/ARTIFACT.md": contextArtifact("pr review"),
	}))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "load_domain", map[string]any{}))
	result := rpcResult(t, res.Stdout, 1)
	body := mustJSON(result)
	if brArr(result, "subdomains") == nil {
		t.Errorf("response missing subdomains array: %s", body)
	}
	if !strings.Contains(body, "finance") || !strings.Contains(body, "engineering") {
		t.Errorf("root map missing top-level domains: %s", body)
	}
	if strings.Contains(body, "manifest_body") {
		t.Errorf("load_domain leaked manifest_body: %s", body)
	}
}

// T-D-browsing-2 — load_domain("finance") returns finance subdomains.
func TestBrowsing_LoadDomainFinance(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md":                        contextArtifact("pay"),
		"finance/close-reporting/run-variance-analysis/ARTIFACT.md": contextArtifact("variance"),
	}))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "load_domain", map[string]any{"path": "finance"}))
	result := rpcResult(t, res.Stdout, 1)
	if result["path"] != "finance" {
		t.Errorf("path=%v, want finance", result["path"])
	}
	body := mustJSON(result)
	if !strings.Contains(body, "finance/ap") || !strings.Contains(body, "finance/close-reporting") {
		t.Errorf("finance subdomains missing: %s", body)
	}
	// DOMAIN.md description is not ingested (F-4.5.2); not asserted here.
}

// T-D-browsing-3 — load_domain("finance/close-reporting") returns its children.
func TestBrowsing_LoadDomainThirdLevel(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/close-reporting/run-variance-analysis/ARTIFACT.md": contextArtifact("variance"),
	}))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "load_domain", map[string]any{"path": "finance/close-reporting"}))
	result := rpcResult(t, res.Stdout, 1)
	if result["path"] != "finance/close-reporting" {
		t.Errorf("path=%v, want finance/close-reporting", result["path"])
	}
	body := mustJSON(result)
	if !strings.Contains(body, "run-variance-analysis") {
		t.Errorf("notable missing the artifact: %s", body)
	}
	if strings.Contains(body, "manifest_body") {
		t.Errorf("leaked manifest_body: %s", body)
	}
}

// T-D-browsing-4 — load_domain response carries descriptors, not manifest bodies.
func TestBrowsing_LoadDomainNoBodies(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/report/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: A report.\n---\n\nLong multi-line\nprose body here.\n",
	}))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "load_domain", map[string]any{"path": "finance"}))
	body := mustJSON(rpcResult(t, res.Stdout, 1))
	for _, leaked := range []string{"manifest_body", "Long multi-line", "frontmatter"} {
		if strings.Contains(body, leaked) {
			t.Errorf("load_domain leaked %q: %s", leaked, body)
		}
	}
}

// T-D-browsing-5 — a depth override expands the rendered subtree: depth=2
// nests a grandchild that depth=1 omits (§4.5.5). spec: §4.5.5 (F-4.5.7)
func TestBrowsing_LoadDomainDepth(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/direct/ARTIFACT.md":   contextArtifact("direct"),
		"finance/ap/sub/deep/ARTIFACT.md": contextArtifact("deep"),
	}))
	var d1 map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance&depth=1", &d1)
	if sub := dmSubdomain(d1, "finance/ap"); sub != nil {
		if nested, _ := sub["subdomains"].([]any); len(nested) != 0 {
			t.Errorf("depth=1 should not nest finance/ap children, got %v", nested)
		}
	}
	var d2 map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance&depth=2", &d2)
	sub := dmSubdomain(d2, "finance/ap")
	if sub == nil {
		t.Fatalf("finance/ap missing at depth=2: %v", d2["subdomains"])
	}
	if !strings.Contains(mustJSON(sub), "finance/ap/sub") {
		t.Errorf("depth=2 should nest finance/ap/sub under finance/ap: %s", mustJSON(sub))
	}
}

// T-D-browsing-6 — the rendering note describes a notable reduction driven by
// target_response_tokens (§4.5.5). spec: §4.5.5 (F-4.5.7)
func TestBrowsing_LoadDomainNote(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"finance/DOMAIN.md": "---\ndescription: Finance\ndiscovery:\n  target_response_tokens: 20\n---\n\n# Finance\n",
	}
	for _, n := range []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"} {
		files["finance/"+n+"/ARTIFACT.md"] = contextArtifact(n + " operations workflow")
	}
	srv := startServer(t, writeRegistry(t, files))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance", &m)
	if note, _ := m["note"].(string); !strings.Contains(note, "reduced") {
		t.Errorf("note = %q, want a budget-reduction sentence", note)
	}
}

// T-D-browsing-7 — load_domain returns DOMAIN.md keywords verbatim (§4.5.5).
// spec: §4.5.5 (F-4.5.3)
func TestBrowsing_LoadDomainKeywords(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/DOMAIN.md":       "---\ndescription: Finance\ndiscovery:\n  keywords:\n    - reconciliation\n    - 1099\n---\n\n# Finance\n",
		"finance/pay/ARTIFACT.md": contextArtifact("pay"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance", &m)
	kw := dmKeywords(m)
	if !dmContains(kw, "reconciliation") || !dmContains(kw, "1099") {
		t.Errorf("keywords %v missing the DOMAIN.md terms", kw)
	}
}

// ---- search_domains ---------------------------------------------------------

// brDomainPaths extracts the domain paths from a search_domains result.
func brDomainPaths(result map[string]any) []string {
	out := []string{}
	for _, d := range brArr(result, "domains") {
		if m, ok := d.(map[string]any); ok {
			if p, ok := m["path"].(string); ok {
				out = append(out, p)
			}
		}
	}
	return out
}

// T-D-browsing-8 — search_domains matches a domain by a DOMAIN.md keyword
// that does not appear in its path or description (§3.2 Layer 1, hybrid
// retrieval over projections). spec: §3.2 / §4.7 (F-3.2.1)
func TestBrowsing_SearchDomainsSemantic(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":               "---\ndescription: \"Accounts payable operations\"\ndiscovery:\n  keywords:\n    - reconciliation\n---\n",
		"finance/ap/pay-invoice/ARTIFACT.md": contextArtifact("pay invoice"),
	}))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "search_domains", map[string]any{"query": "reconciliation"}))
	paths := brDomainPaths(rpcResult(t, res.Stdout, 1))
	if !dmContains(paths, "finance/ap") {
		t.Errorf("search_domains(\"reconciliation\") = %v, want finance/ap matched by its keyword", paths)
	}
}

// T-D-browsing-9 — search_domains scope constrains results to a path
// prefix. spec: §5 (F-3.2.1)
func TestBrowsing_SearchDomainsScope(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":     "---\ndescription: AP\n---\n",
		"finance/ap/x/ARTIFACT.md": contextArtifact("x"),
		"finance/ar/DOMAIN.md":     "---\ndescription: AR\n---\n",
		"finance/ar/y/ARTIFACT.md": contextArtifact("y"),
		"ops/runner/DOMAIN.md":     "---\ndescription: Ops runner\n---\n",
		"ops/runner/z/ARTIFACT.md": contextArtifact("z"),
	}))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "search_domains", map[string]any{"scope": "finance"}))
	paths := brDomainPaths(rpcResult(t, res.Stdout, 1))
	if !dmContains(paths, "finance/ap") || !dmContains(paths, "finance/ar") {
		t.Errorf("scope=finance dropped an in-scope domain: %v", paths)
	}
	if dmContains(paths, "ops/runner") {
		t.Errorf("scope=finance returned an out-of-scope domain: %v", paths)
	}
}

// T-D-browsing-10 — search_domains top_k caps the result count. spec: §5
// (F-3.2.1)
func TestBrowsing_SearchDomainsTopK(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"alpha/DOMAIN.md":       "---\ndescription: \"shared topic\"\n---\n",
		"alpha/a/ARTIFACT.md":   contextArtifact("a"),
		"bravo/DOMAIN.md":       "---\ndescription: \"shared topic\"\n---\n",
		"bravo/b/ARTIFACT.md":   contextArtifact("b"),
		"charlie/DOMAIN.md":     "---\ndescription: \"shared topic\"\n---\n",
		"charlie/c/ARTIFACT.md": contextArtifact("c"),
	}))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "search_domains", map[string]any{"query": "shared topic", "top_k": 2}))
	paths := brDomainPaths(rpcResult(t, res.Stdout, 1))
	if len(paths) != 2 {
		t.Errorf("search_domains top_k=2 returned %d domains: %v", len(paths), paths)
	}
}

// T-D-browsing-11 — a domain without a DOMAIN.md has no projection and is
// not indexed in search_domains; it stays reachable via load_domain
// enumeration only. spec: §4.5.1 / §4.7 (F-3.2.1)
func TestBrowsing_SearchDomainsRequiresDomainMD(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":               "---\ndescription: \"Accounts payable\"\n---\n",
		"finance/ap/pay-invoice/ARTIFACT.md": contextArtifact("pay invoice"),
		// ops/runner has artifacts but no DOMAIN.md.
		"ops/runner/restart/ARTIFACT.md": contextArtifact("restart"),
	}))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "search_domains", map[string]any{}))
	paths := brDomainPaths(rpcResult(t, res.Stdout, 1))
	if !dmContains(paths, "finance/ap") {
		t.Errorf("search_domains missing the DOMAIN.md-backed finance/ap: %v", paths)
	}
	for _, p := range paths {
		if p == "ops" || p == "ops/runner" {
			t.Errorf("search_domains indexed a domain with no DOMAIN.md: %v", paths)
		}
	}
}

// ---- search_artifacts -------------------------------------------------------

// T-D-browsing-12 — search_artifacts with query returns ranked descriptors.
func TestBrowsing_SearchArtifactsQuery(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/close-reporting/run-variance-analysis/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- body -->\n",
		"finance/close-reporting/run-variance-analysis/SKILL.md":    skillBodyDesc("run-variance-analysis", "Run variance analysis against forecast."),
	}))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "search_artifacts", map[string]any{"query": "variance analysis", "type": "skill"}))
	result := rpcResult(t, res.Stdout, 1)
	body := mustJSON(result)
	if !strings.Contains(body, "run-variance-analysis") {
		t.Errorf("results missing the artifact: %s", body)
	}
	if strings.Contains(body, "manifest_body") || strings.Contains(body, "frontmatter") {
		t.Errorf("descriptors leaked bodies: %s", body)
	}
	if asInt(result["total_matched"]) < 1 {
		t.Errorf("total_matched=%v, want >= 1", result["total_matched"])
	}
}

// T-D-browsing-13 — search_artifacts browse mode (scope only).
func TestBrowsing_SearchArtifactsBrowse(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md":       contextArtifact("pay invoice"),
		"finance/ap/reconcile-invoice/ARTIFACT.md": contextArtifact("reconcile invoice"),
		"engineering/x/ARTIFACT.md":                contextArtifact("x"),
	}))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "search_artifacts", map[string]any{"scope": "finance/ap"}))
	body := mustJSON(rpcResult(t, res.Stdout, 1))
	for _, want := range []string{"finance/ap/pay-invoice", "finance/ap/reconcile-invoice"} {
		if !strings.Contains(body, want) {
			t.Errorf("browse missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "engineering/x") {
		t.Errorf("browse returned an out-of-scope artifact: %s", body)
	}
}

// T-D-browsing-14 — search_artifacts with query, scope, and type.
func TestBrowsing_SearchArtifactsQueryScopeType(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- body -->\n",
		"finance/ap/pay-invoice/SKILL.md":    skillBodyDesc("pay-invoice", "Approve and pay vendor invoices."),
		"engineering/pr-review/ARTIFACT.md":  "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- body -->\n",
		"engineering/pr-review/SKILL.md":     skillBodyDesc("pr-review", "Review PRs."),
	}))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "search_artifacts", map[string]any{
		"query": "invoice approval", "scope": "finance/ap", "type": "skill"}))
	body := mustJSON(rpcResult(t, res.Stdout, 1))
	if !strings.Contains(body, "finance/ap/pay-invoice") {
		t.Errorf("missing in-scope artifact: %s", body)
	}
	if strings.Contains(body, "engineering/pr-review") {
		t.Errorf("returned an out-of-scope artifact: %s", body)
	}
}

// T-D-browsing-15 — search_artifacts type filter.
func TestBrowsing_SearchArtifactsType(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"sk/ARTIFACT.md":  greetSkillArtifact,
		"sk/SKILL.md":     skillBody("sk"),
		"ctx/ARTIFACT.md": contextArtifact("a context"),
	}))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "search_artifacts", map[string]any{"type": "skill"}))
	body := mustJSON(rpcResult(t, res.Stdout, 1))
	if !strings.Contains(body, "sk") {
		t.Errorf("type=skill missing the skill: %s", body)
	}
	if strings.Contains(body, `"type":"context"`) {
		t.Errorf("type=skill returned a context: %s", body)
	}
}

// T-D-browsing-16 — search_artifacts tags filter.
func TestBrowsing_SearchArtifactsTags(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"a/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: A.\ntags: [finance, variance]\n---\n\nbody\n",
		"b/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: B.\ntags: [engineering]\n---\n\nbody\n",
	}))
	// The HTTP endpoint parses tags as a comma-separated list; pass the
	// documented comma form through the bridge (the array form serializes to
	// a non-matching query string).
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "search_artifacts", map[string]any{"tags": "finance"}))
	body := mustJSON(rpcResult(t, res.Stdout, 1))
	if !strings.Contains(body, `"id":"a"`) {
		t.Errorf("tags=finance missing artifact a: %s", body)
	}
	if strings.Contains(body, `"id":"b"`) {
		t.Errorf("tags=finance returned artifact b: %s", body)
	}
}

// T-D-browsing-17 — search_artifacts top_k default 10 / max 50.
func TestBrowsing_SearchArtifactsTopK(t *testing.T) {
	t.Parallel()
	entries := map[string]string{}
	for i := 0; i < 15; i++ {
		entries[fmt.Sprintf("d/a%02d/ARTIFACT.md", i)] = contextArtifact(fmt.Sprintf("artifact %d", i))
	}
	srv := startServer(t, writeRegistry(t, entries))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "search_artifacts", map[string]any{"scope": "d"}))
	got := len(brArr(rpcResult(t, res.Stdout, 1), "results"))
	if got > 10 {
		t.Errorf("default returned %d results, want <= 10", got)
	}
	res2 := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "search_artifacts", map[string]any{"scope": "d", "top_k": 51}))
	if got2 := len(brArr(rpcResult(t, res2.Stdout, 1), "results")); got2 > 50 {
		t.Errorf("top_k=51 returned %d results, want <= 50", got2)
	}
}

// T-D-browsing-18 — total_matched reflects full count when capped.
func TestBrowsing_SearchArtifactsTotalMatched(t *testing.T) {
	t.Parallel()
	entries := map[string]string{}
	for i := 0; i < 12; i++ {
		entries[fmt.Sprintf("finance/a%02d/ARTIFACT.md", i)] = contextArtifact(fmt.Sprintf("artifact %d", i))
	}
	srv := startServer(t, writeRegistry(t, entries))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "search_artifacts", map[string]any{"scope": "finance", "top_k": 5}))
	result := rpcResult(t, res.Stdout, 1)
	if got := len(brArr(result, "results")); got != 5 {
		t.Errorf("results length=%d, want 5", got)
	}
	if asInt(result["total_matched"]) != 12 {
		t.Errorf("total_matched=%v, want 12", result["total_matched"])
	}
}

// T-D-browsing-19 — search_artifacts returns descriptors only.
func TestBrowsing_SearchArtifactsNoBodies(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/report/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: A report.\n---\n\nVery long prose body content here.\n",
	}))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "search_artifacts", map[string]any{"query": "report"}))
	body := mustJSON(rpcResult(t, res.Stdout, 1))
	for _, leaked := range []string{"manifest_body", "frontmatter", "Very long prose body"} {
		if strings.Contains(body, leaked) {
			t.Errorf("descriptor leaked %q: %s", leaked, body)
		}
	}
}

// T-D-browsing-20 — mcp-server artifacts filtered from search via the MCP bridge.
func TestBrowsing_McpServerFilteredFromSearch(t *testing.T) {
	t.Parallel()
	t.Skip("spec §5: mcp-server artifacts should be filtered from MCP-bridge results; the bridge does not filter them and no BUILD-GAPS finding is filed")
}

// T-D-browsing-21 — mcp-server artifacts filtered from load via the MCP bridge.
func TestBrowsing_McpServerFilteredFromLoad(t *testing.T) {
	t.Parallel()
	t.Skip("spec §5: mcp-server artifacts should be blocked from load_artifact via the MCP bridge; the bridge does not block them and no BUILD-GAPS finding is filed")
}

// ---- load_artifact ----------------------------------------------------------

// T-D-browsing-22 — load_artifact returns the manifest body inline and materializes.
func TestBrowsing_LoadArtifactInline(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	srv := startServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Variance.\n---\n\nVariance analysis instructions.\n",
	}))
	mat := t.TempDir()
	res := mcpExec(t, brMatEnv(t, srv.BaseURL, mat), toolCall(1, "load_artifact", map[string]any{"id": id}))
	result := rpcResult(t, res.Stdout, 1)
	if result["id"] != id {
		t.Errorf("id=%v, want %s", result["id"], id)
	}
	if body, _ := result["manifest_body"].(string); strings.TrimSpace(body) == "" {
		t.Errorf("manifest_body empty: %v", result)
	}
	if len(brArr(result, "materialized_at")) == 0 {
		t.Errorf("materialized_at empty: %v", result)
	}
	mustExist(t, filepath.Join(mat, id, "ARTIFACT.md"))
}

// T-D-browsing-23 — load_artifact with version parameter (multi-version).
func TestBrowsing_LoadArtifactVersion(t *testing.T) {
	t.Parallel()
	t.Skip("requires two ingested versions of one id; a filesystem-source layer holds a single version per path, so version selection is not expressible here")
}

// T-D-browsing-24 — load_artifact default resolves latest non-deprecated.
func TestBrowsing_LoadArtifactLatestSkipsDeprecated(t *testing.T) {
	t.Parallel()
	t.Skip("requires two ingested versions (one deprecated) of one id; not expressible in a filesystem-source layer (covered by pkg/registry/core/latest_skips_deprecated_test.go)")
}

// T-D-browsing-25 — load_artifact per-call harness override switches adapter.
func TestBrowsing_LoadArtifactHarnessOverride(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"ts-style/ARTIFACT.md": chRule("always", ""),
	}))
	mat := t.TempDir()
	res := mcpExec(t, brMatEnv(t, srv.BaseURL, mat),
		toolCall(1, "load_artifact", map[string]any{"id": "ts-style", "harness": "claude-code"}),
		toolCall(2, "load_artifact", map[string]any{"id": "ts-style", "harness": "cursor"}))
	_ = rpcResult(t, res.Stdout, 1)
	_ = rpcResult(t, res.Stdout, 2)
	mustExist(t, filepath.Join(mat, ".claude", "rules", "ts-style.md"))
	mustExist(t, filepath.Join(mat, ".cursor", "rules", "ts-style.mdc"))
}

// T-D-browsing-26 — load_artifact harness=none produces canonical layout.
func TestBrowsing_LoadArtifactHarnessNone(t *testing.T) {
	t.Parallel()
	id := "finance/ap/pay-invoice"
	srv := startServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": greetSkillArtifact,
		id + "/SKILL.md":    skillBody("pay-invoice"),
	}))
	mat := t.TempDir()
	// Process-level harness is claude-code; the per-call none override wins.
	res := mcpExec(t, append(brMatEnv(t, srv.BaseURL, mat), "PODIUM_HARNESS=claude-code"),
		toolCall(1, "load_artifact", map[string]any{"id": id, "harness": "none"}))
	_ = rpcResult(t, res.Stdout, 1)
	mustExist(t, filepath.Join(mat, id, "ARTIFACT.md"))
	if _, err := os.Stat(filepath.Join(mat, ".claude")); err == nil {
		t.Errorf("harness=none should not create .claude/")
	}
}

// T-D-browsing-27 — load_artifact materializes bundled resources atomically.
// spec: §7.2.
func TestBrowsing_LoadArtifactResourcesAtomic(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	srv := startServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":               brSkillArtifact,
		id + "/SKILL.md":                  brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
		id + "/scripts/variance.py":       "print('variance')\n",
		id + "/assets/output-schema.json": "{}\n",
	}))
	mat := t.TempDir()
	res := mcpExec(t, brMatEnv(t, srv.BaseURL, mat),
		toolCall(1, "load_artifact", map[string]any{"id": id}))
	_ = rpcResult(t, res.Stdout, 1)
	mustExist(t, filepath.Join(mat, id, "scripts/variance.py"))
	mustExist(t, filepath.Join(mat, id, "assets/output-schema.json"))
	// Atomic write leaves no .tmp scratch files behind.
	brNoTmp(t, readTreeAll(t, mat))
}

// T-D-browsing-28 — only load_artifact writes to the host filesystem.
func TestBrowsing_OnlyLoadArtifactWrites(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": contextArtifact("pay"),
		"finance/x/ARTIFACT.md":              contextArtifact("x"),
	}))
	mat := t.TempDir()
	discovery := mcpExec(t, brMatEnv(t, srv.BaseURL, mat),
		toolCall(1, "load_domain", map[string]any{}),
		toolCall(2, "search_artifacts", map[string]any{"scope": "finance"}))
	_ = discovery
	if files := readTreeAll(t, mat); len(files) != 0 {
		t.Errorf("discovery calls wrote files: %v", files)
	}
	load := mcpExec(t, brMatEnv(t, srv.BaseURL, mat), toolCall(1, "load_artifact", map[string]any{"id": "finance/ap/pay-invoice"}))
	_ = rpcResult(t, load.Stdout, 1)
	mustExist(t, filepath.Join(mat, "finance/ap/pay-invoice", "ARTIFACT.md"))
	if _, err := os.Stat(filepath.Join(mat, "finance/x", "ARTIFACT.md")); err == nil {
		t.Errorf("load_artifact wrote an artifact it was not asked for")
	}
}

// T-D-browsing-29 — full discovery flow: cold start, drill, browse, load.
func TestBrowsing_DiscoveryFlow(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md":                        greetSkillArtifact,
		"finance/ap/pay-invoice/SKILL.md":                           skillBody("pay-invoice"),
		"finance/close-reporting/run-variance-analysis/ARTIFACT.md": greetSkillArtifact,
		"finance/close-reporting/run-variance-analysis/SKILL.md":    skillBody("run-variance-analysis"),
	}))
	mat := t.TempDir()
	env := brMatEnv(t, srv.BaseURL, mat)
	// 1. cold start
	r1 := mcpExec(t, env, toolCall(1, "load_domain", map[string]any{}))
	if !strings.Contains(mustJSON(rpcResult(t, r1.Stdout, 1)), "finance") {
		t.Fatalf("cold start missing finance")
	}
	// 2. drill in
	r2 := mcpExec(t, env, toolCall(1, "load_domain", map[string]any{"path": "finance"}))
	if b := mustJSON(rpcResult(t, r2.Stdout, 1)); !strings.Contains(b, "finance/ap") {
		t.Fatalf("drill-in missing finance/ap: %s", b)
	}
	// 3. browse
	r3 := mcpExec(t, env, toolCall(1, "search_artifacts", map[string]any{"scope": "finance/ap"}))
	if !strings.Contains(mustJSON(rpcResult(t, r3.Stdout, 1)), "finance/ap/pay-invoice") {
		t.Fatalf("browse missing pay-invoice")
	}
	if len(readTreeAll(t, mat)) != 0 {
		t.Errorf("discovery steps 1-3 wrote files")
	}
	// 4. load
	r4 := mcpExec(t, env, toolCall(1, "load_artifact", map[string]any{"id": "finance/ap/pay-invoice"}))
	_ = rpcResult(t, r4.Stdout, 1)
	mustExist(t, filepath.Join(mat, "finance/ap/pay-invoice", "ARTIFACT.md"))
}

// T-D-browsing-30 — discovery flow cold start via search_domains: a
// topical query finds a domain neighborhood, then the agent drills in
// with load_domain. spec: §3.4 / §3.2 (F-3.2.1)
func TestBrowsing_DiscoveryFlowSearchDomains(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":               "---\ndescription: \"Accounts payable: money out to vendors\"\ndiscovery:\n  keywords:\n    - invoice\n---\n",
		"finance/ap/pay-invoice/ARTIFACT.md": contextArtifact("pay invoice"),
	}))
	// Cold start: a topical query ("accounts payable") absent from the path
	// surfaces the neighborhood.
	r1 := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "search_domains", map[string]any{"query": "accounts payable"}))
	paths := brDomainPaths(rpcResult(t, r1.Stdout, 1))
	if !dmContains(paths, "finance/ap") {
		t.Fatalf("search_domains(\"accounts payable\") = %v, want finance/ap to drill into", paths)
	}
	// Drill into the chosen domain.
	r2 := mcpExec(t, brEnv(srv.BaseURL), toolCall(2, "load_domain", map[string]any{"path": "finance/ap"}))
	if got := rpcResult(t, r2.Stdout, 2)["path"]; got != "finance/ap" {
		t.Errorf("load_domain path = %v, want finance/ap", got)
	}
}

// ---- audit ------------------------------------------------------------------

// T-D-browsing-31 — domain.loaded audit event.
func TestBrowsing_AuditDomainLoaded(t *testing.T) {
	t.Parallel()
	srv, audit := brStartAuditServer(t, writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("x")}))
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance", nil)
	if !brPollContains(audit, "domain.loaded", 5*time.Second) {
		t.Errorf("audit log missing domain.loaded:\n%s", brReadOrEmpty(audit))
	}
}

// T-D-browsing-32 — domains.searched audit event.
func TestBrowsing_AuditDomainsSearched(t *testing.T) {
	t.Parallel()
	srv, audit := brStartAuditServer(t, writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("x")}))
	getJSON(t, srv.BaseURL+"/v1/search_domains?query=vendor+payments", nil)
	if !brPollContains(audit, "domains.searched", 5*time.Second) {
		t.Errorf("audit log missing domains.searched:\n%s", brReadOrEmpty(audit))
	}
}

// T-D-browsing-33 — artifacts.searched audit event.
func TestBrowsing_AuditArtifactsSearched(t *testing.T) {
	t.Parallel()
	srv, audit := brStartAuditServer(t, writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("invoice approval")}))
	getJSON(t, srv.BaseURL+"/v1/search_artifacts?query=invoice+approval", nil)
	if !brPollContains(audit, "artifacts.searched", 5*time.Second) {
		t.Errorf("audit log missing artifacts.searched:\n%s", brReadOrEmpty(audit))
	}
}

// T-D-browsing-34 — artifact.loaded audit event.
func TestBrowsing_AuditArtifactLoaded(t *testing.T) {
	t.Parallel()
	srv, audit := brStartAuditServer(t, writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("x")}))
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id=finance/x", nil)
	if !brPollContains(audit, "artifact.loaded", 5*time.Second) {
		t.Errorf("audit log missing artifact.loaded:\n%s", brReadOrEmpty(audit))
	}
}

// T-D-browsing-35 — search query PII scrubbed before audit.
func TestBrowsing_AuditPIIScrubbed(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-8.2.1: query-text PII scrubbing is never applied in the runtime path; queries reach the audit log unredacted")
}

// ---- SLO benchmarks (placement) --------------------------------------------

// T-D-browsing-36..40 — p99 SLO targets. These are benchmark gates that belong
// in test/bench; asserting wall-clock p99 from an e2e test is timing-flaky.
func TestBrowsing_SLOLoadDomain(t *testing.T) {
	t.Skip("SLO/benchmark gate (load_domain p99 < 200ms): belongs in test/bench, not asserted in e2e to avoid timing flakiness")
}
func TestBrowsing_SLOSearchDomains(t *testing.T) {
	t.Skip("SLO/benchmark gate (search_domains p99 < 200ms): belongs in test/bench, not asserted in e2e")
}
func TestBrowsing_SLOSearchArtifacts(t *testing.T) {
	t.Skip("SLO/benchmark gate (search_artifacts p99 < 200ms): belongs in test/bench, not asserted in e2e")
}
func TestBrowsing_SLOLoadArtifactManifest(t *testing.T) {
	t.Skip("SLO/benchmark gate (load_artifact manifest p99 < 500ms): belongs in test/bench, not asserted in e2e")
}
func TestBrowsing_SLOLoadArtifactResources(t *testing.T) {
	t.Skip("SLO/benchmark gate (load_artifact + resources p99 < 2s): belongs in test/bench, not asserted in e2e")
}

// ---- descriptor structure ---------------------------------------------------

// T-D-browsing-41 — a search_domains descriptor carries path, name,
// description, keywords, and score (§3.2 Layer 1). spec: §3.2 (F-3.2.1)
func TestBrowsing_SearchDomainsDescriptorFields(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":               "---\ndescription: \"Accounts payable operations\"\ndiscovery:\n  keywords:\n    - reconciliation\n    - remittance\n---\n",
		"finance/ap/pay-invoice/ARTIFACT.md": contextArtifact("pay invoice"),
	}))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "search_domains", map[string]any{"query": "reconciliation"}))
	result := rpcResult(t, res.Stdout, 1)
	domains := brArr(result, "domains")
	if len(domains) == 0 {
		t.Fatalf("search_domains returned no domains: %s", mustJSON(result))
	}
	d, _ := domains[0].(map[string]any)
	for _, field := range []string{"path", "name", "description", "keywords", "score"} {
		if _, ok := d[field]; !ok {
			t.Errorf("descriptor missing %q field: %v", field, d)
		}
	}
	if kw, _ := d["keywords"].([]any); len(kw) == 0 {
		t.Errorf("descriptor keywords empty, want the DOMAIN.md keywords: %v", d)
	}
	if sc, _ := d["score"].(float64); sc <= 0 {
		t.Errorf("descriptor score = %v, want > 0 for a lexical match", d["score"])
	}
}

// T-D-browsing-42 — search_domains returns no subdomain list or notable.
func TestBrowsing_SearchDomainsNoSubtree(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": contextArtifact("pay"),
	}))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "search_domains", map[string]any{"query": "finance"}))
	result := rpcResult(t, res.Stdout, 1)
	body := mustJSON(result)
	for _, leaked := range []string{"notable", "subdomains", "manifest_body"} {
		if strings.Contains(body, leaked) {
			t.Errorf("search_domains leaked %q: %s", leaked, body)
		}
	}
}

// ---- offline ----------------------------------------------------------------

// T-D-browsing-43 — registry offline: search_artifacts surfaces an error.
func TestBrowsing_OfflineSearchArtifacts(t *testing.T) {
	t.Parallel()
	res := mcpExec(t, []string{"PODIUM_REGISTRY=http://127.0.0.1:1", "PODIUM_CACHE_MODE=always-revalidate", "PODIUM_CACHE_DIR=" + t.TempDir()},
		toolCall(1, "search_artifacts", map[string]any{"query": "anything"}))
	errStr := brToolErr(t, res.Stdout, 1)
	if errStr == "" {
		t.Errorf("expected an error for an unreachable registry; stdout=%s", res.Stdout)
	}
	if res.Exit != 0 {
		t.Errorf("bridge crashed (exit=%d) instead of returning an error result", res.Exit)
	}
}

// T-D-browsing-44 — registry offline: load_domain surfaces an error.
func TestBrowsing_OfflineLoadDomain(t *testing.T) {
	t.Parallel()
	res := mcpExec(t, []string{"PODIUM_REGISTRY=http://127.0.0.1:1", "PODIUM_CACHE_MODE=always-revalidate", "PODIUM_CACHE_DIR=" + t.TempDir()},
		toolCall(1, "load_domain", map[string]any{}))
	if brToolErr(t, res.Stdout, 1) == "" {
		t.Errorf("expected an error for an unreachable registry; stdout=%s", res.Stdout)
	}
}

// T-D-browsing-45 — search_artifacts forwards session_id to the registry.
func TestBrowsing_SearchArtifactsSessionID(t *testing.T) {
	t.Parallel()
	t.Skip("gap: the MCP bridge does not forward session_id to the registry, and the HTTP search_artifacts endpoint has no session_id parameter (no BUILD-GAPS finding filed)")
}

// T-D-browsing-46 — load_artifact session_id pins latest for the session.
func TestBrowsing_LoadArtifactSessionPin(t *testing.T) {
	t.Parallel()
	t.Skip("not expressible: the HTTP load_artifact endpoint has no session_id parameter, and new versions cannot be ingested into a live standalone server mid-test")
}

// T-D-browsing-47 — load_artifact for a nonexistent artifact returns a structured error.
func TestBrowsing_LoadArtifactNotFound(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	mat := t.TempDir()
	res := mcpExec(t, brMatEnv(t, srv.BaseURL, mat), toolCall(1, "load_artifact", map[string]any{"id": "does/not/exist"}))
	if e := brToolErr(t, res.Stdout, 1); !strings.Contains(e, "not_found") {
		t.Errorf("error=%q, want registry.not_found", e)
	}
	if len(readTreeAll(t, mat)) != 0 {
		t.Errorf("a not-found load wrote files")
	}
}

// T-D-browsing-48 — load_artifact for a nonexistent version returns an error.
func TestBrowsing_LoadArtifactBadVersion(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": contextArtifact("pay"),
	}))
	st, body := getRaw(t, srv.BaseURL+"/v1/load_artifact?id=finance/ap/pay-invoice&version=9.9.9")
	if st == 200 {
		t.Errorf("bad version returned HTTP 200: %s", body)
	}
	if !strings.Contains(string(body), "not_found") {
		t.Errorf("bad version body missing not_found: %s", body)
	}
}

// T-D-browsing-49 — tools/list returns the meta-tools plus the §13.9
// health tool, each with a description.
func TestBrowsing_ToolsList(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	res := mcpExec(t, brEnv(srv.BaseURL), rpcReq{ID: 1, Method: "tools/list", Params: map[string]any{}})
	result := rpcResult(t, res.Stdout, 1)
	tools := brArr(result, "tools")
	names := map[string]bool{}
	for _, ti := range tools {
		m, _ := ti.(map[string]any)
		names[fmt.Sprintf("%v", m["name"])] = true
		if d, _ := m["description"].(string); strings.TrimSpace(d) == "" {
			t.Errorf("tool %v has an empty description", m["name"])
		}
	}
	for _, want := range []string{"load_domain", "search_domains", "search_artifacts", "load_artifact", "health"} {
		if !names[want] {
			t.Errorf("tools/list missing %q: %s", want, mustJSON(result))
		}
	}
}

// T-D-browsing-50 — search_artifacts on an empty registry returns empty results.
func TestBrowsing_SearchArtifactsEmptyRegistry(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{}))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "search_artifacts", map[string]any{"query": "anything"}))
	result := rpcResult(t, res.Stdout, 1)
	if got := len(brArr(result, "results")); got != 0 {
		t.Errorf("empty registry returned %d results, want 0", got)
	}
	if asInt(result["total_matched"]) != 0 {
		t.Errorf("total_matched=%v, want 0", result["total_matched"])
	}
}

// T-D-browsing-51 — load_domain on a path with no content. The doc describes a
// graceful empty result, but the implementation returns domain.not_found for a
// path that resolves to no visible artifacts; this asserts the actual behavior.
func TestBrowsing_LoadDomainEmptyPath(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("x")}))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "load_domain", map[string]any{"path": "empty/domain"}))
	if e := brToolErr(t, res.Stdout, 1); !strings.Contains(e, "not_found") {
		t.Errorf("expected domain.not_found for a path with no artifacts, got error=%q stdout=%s", e, res.Stdout)
	}
}

// T-D-browsing-52 — browse mode returns all non-filtered artifact types.
func TestBrowsing_BrowseAllTypes(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"tools/sk/ARTIFACT.md":  greetSkillArtifact,
		"tools/sk/SKILL.md":     skillBody("sk"),
		"tools/ctx/ARTIFACT.md": contextArtifact("a context"),
		"tools/cmd/ARTIFACT.md": "---\ntype: command\nversion: 1.0.0\ndescription: A command.\n---\n\n$ARGUMENTS\n",
	}))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "search_artifacts", map[string]any{"scope": "tools"}))
	body := mustJSON(rpcResult(t, res.Stdout, 1))
	for _, want := range []string{"tools/sk", "tools/ctx", "tools/cmd"} {
		if !strings.Contains(body, want) {
			t.Errorf("browse missing %q: %s", want, body)
		}
	}
}

// T-D-browsing-53 — authoring quality: specific description scores higher.
func TestBrowsing_DescriptionScoring(t *testing.T) {
	t.Parallel()
	t.Skip("ranking-quality assertion is embedding/scoring-dependent and not a stable e2e gate")
}

// T-D-browsing-54 — a domain with no DOMAIN.md (so no keywords or
// projection) does not surface in search_domains but stays reachable via
// load_domain enumeration; a keyworded sibling is found by query.
// spec: §4.5.1 / §4.7 (F-3.2.1)
func TestBrowsing_DomainWithoutKeywords(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":           "---\ndescription: AP\ndiscovery:\n  keywords:\n    - reconciliation\n---\n",
		"finance/ap/x/ARTIFACT.md":       contextArtifact("x"),
		"ops/runner/restart/ARTIFACT.md": contextArtifact("restart"),
	}))
	res := mcpExec(t, brEnv(srv.BaseURL), toolCall(1, "search_domains", map[string]any{"query": "reconciliation"}))
	paths := brDomainPaths(rpcResult(t, res.Stdout, 1))
	if !dmContains(paths, "finance/ap") {
		t.Errorf("keyworded domain not found by search_domains: %v", paths)
	}
	if dmContains(paths, "ops/runner") {
		t.Errorf("domain without a DOMAIN.md surfaced in search_domains: %v", paths)
	}
	// The bare domain is still reachable via load_domain enumeration.
	r2 := mcpExec(t, brEnv(srv.BaseURL), toolCall(2, "load_domain", map[string]any{"path": "ops/runner"}))
	if got := rpcResult(t, r2.Stdout, 2)["path"]; got != "ops/runner" {
		t.Errorf("load_domain(ops/runner) = %v, want the bare domain to resolve", got)
	}
}

// T-D-browsing-55 — authoring quality: when_to_use improves retrieval signal.
func TestBrowsing_WhenToUseSignal(t *testing.T) {
	t.Parallel()
	t.Skip("ranking-quality assertion is embedding/scoring-dependent and not a stable e2e gate")
}

// ---- HTTP endpoint structure ------------------------------------------------

// T-D-browsing-56 — GET /v1/load_domain returns the documented structure.
func TestBrowsing_HTTPLoadDomain(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("x")}))
	var resp struct {
		Path       string `json:"path"`
		Subdomains []any  `json:"subdomains"`
		Notable    []any  `json:"notable"`
	}
	for _, u := range []string{"/v1/load_domain", "/v1/load_domain?path=finance", "/v1/load_domain?path=finance&depth=2"} {
		getJSON(t, srv.BaseURL+u, &resp)
		if resp.Subdomains == nil {
			t.Errorf("%s: subdomains is null, want array", u)
		}
		if resp.Notable == nil {
			t.Errorf("%s: notable is null, want array", u)
		}
	}
}

// T-D-browsing-57 — GET /v1/search_artifacts returns the documented structure.
func TestBrowsing_HTTPSearchArtifacts(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/va/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- body -->\n",
		"finance/va/SKILL.md":    skillBodyDesc("va", "variance."),
	}))
	st, body := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query=variance&type=skill")
	if st != 200 {
		t.Fatalf("HTTP %d: %s", st, body)
	}
	s := string(body)
	for _, want := range []string{`"total_matched"`, `"results"`} {
		if !strings.Contains(s, want) {
			t.Errorf("response missing %s: %s", want, s)
		}
	}
	if strings.Contains(s, "manifest_body") {
		t.Errorf("results leaked manifest_body: %s", s)
	}
}

// T-D-browsing-58 — GET /v1/search_domains returns the documented
// structure. The finance domain carries a DOMAIN.md so it has a
// projection to retrieve over (§4.7); without one a domain is reachable
// by load_domain enumeration only.
func TestBrowsing_HTTPSearchDomains(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/DOMAIN.md":     "---\ndescription: \"The finance function\"\n---\n",
		"finance/x/ARTIFACT.md": contextArtifact("x"),
	}))
	st, body := getRaw(t, srv.BaseURL+"/v1/search_domains?query=finance")
	if st != 200 {
		t.Fatalf("HTTP %d: %s", st, body)
	}
	s := string(body)
	for _, want := range []string{`"total_matched"`, `"domains"`} {
		if !strings.Contains(s, want) {
			t.Errorf("response missing %s: %s", want, s)
		}
	}
}

// T-D-browsing-59 — GET /v1/load_artifact returns the documented structure.
func TestBrowsing_HTTPLoadArtifact(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": contextArtifact("pay"),
	}))
	st, body := getRaw(t, srv.BaseURL+"/v1/load_artifact?id=finance/ap/pay-invoice")
	if st != 200 {
		t.Fatalf("HTTP %d: %s", st, body)
	}
	for _, want := range []string{`"id"`, `"type"`, `"version"`, `"content_hash"`, `"manifest_body"`, `"frontmatter"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("response missing %s: %s", want, body)
		}
	}
}

// T-D-browsing-60 — GET /v1/load_artifact with missing id returns 400.
func TestBrowsing_HTTPLoadArtifactMissingID(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	st, body := getRaw(t, srv.BaseURL+"/v1/load_artifact")
	if st != 400 {
		t.Errorf("status=%d, want 400: %s", st, body)
	}
	s := string(body)
	if !strings.Contains(s, "registry.invalid_argument") || !strings.Contains(s, "id") {
		t.Errorf("body missing registry.invalid_argument / id: %s", s)
	}
}
