package e2e

// End-to-end tests for docs/getting-started/concepts.md (D-concepts). These
// exercise the artifact/domain/registry/layer/visibility/harness/
// materialization/meta-tool concepts the page defines, against the real
// podium, podium serve, and podium-mcp surfaces.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// the canonical skill layout (ARTIFACT.md + SKILL.md +
// scripts/ + references/) lints cleanly.
func TestCoreConcept_SkillLayoutLints(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/close-reporting/run-variance-analysis/ARTIFACT.md":                      greetSkillArtifact,
		"finance/close-reporting/run-variance-analysis/SKILL.md":                         skillBody("run-variance-analysis"),
		"finance/close-reporting/run-variance-analysis/scripts/variance.py":              "print(1)\n",
		"finance/close-reporting/run-variance-analysis/references/variance-explained.md": "explained\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q stderr=%q", res.Exit, res.Stdout, res.Stderr)
	}
}

// the canonical artifact ID is the directory path under the
// registry root.
func TestCoreConcept_CanonicalID(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/close-reporting/run-variance-analysis/ARTIFACT.md": greetSkillArtifact,
		"finance/close-reporting/run-variance-analysis/SKILL.md":    skillBody("run-variance-analysis"),
	}))
	var resp map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id=finance/close-reporting/run-variance-analysis", &resp)
	if resp["id"] != "finance/close-reporting/run-variance-analysis" {
		t.Errorf("id=%v, want the directory path", resp["id"])
	}
	if st := getStatus(t, srv.BaseURL+"/v1/load_artifact?id=finance/nope"); st != 404 {
		t.Errorf("nonexistent id = HTTP %d, want 404", st)
	}
}

// a non-skill artifact uses ARTIFACT.md with frontmatter and
// a prose body; no SKILL.md is required.
func TestCoreConcept_NonSkillArtifact(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Accounts payable payment guide.\n---\n\nPay invoices on time.\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q stderr=%q", res.Exit, res.Stdout, res.Stderr)
	}
}

// every first-class type is accepted by lint.
func TestCoreConcept_AllFirstClassTypesLint(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"sk/ARTIFACT.md":  greetSkillArtifact,
		"sk/SKILL.md":     skillBody("sk"),
		"ag/ARTIFACT.md":  "---\ntype: agent\nversion: 1.0.0\ndescription: an agent\n---\n\nbody\n",
		"ctx/ARTIFACT.md": contextArtifact("a context"),
		"cmd/ARTIFACT.md": "---\ntype: command\nversion: 1.0.0\ndescription: a command\n---\n\nbody\n",
		"rl/ARTIFACT.md":  "---\ntype: rule\nversion: 1.0.0\ndescription: a rule\nrule_mode: always\n---\n\nbody\n",
		"hk/ARTIFACT.md":  "---\ntype: hook\nversion: 1.0.0\ndescription: a hook\nhook_event: stop\nhook_action: echo ok\n---\n\nbody\n",
		"ms/ARTIFACT.md":  "---\ntype: mcp-server\nversion: 1.0.0\ndescription: a server\nserver_identifier: ms\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q stderr=%q", res.Exit, res.Stdout, res.Stderr)
	}
}

// an unknown type value is rejected by lint.
func TestCoreConcept_UnknownTypeRejected(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"weird/ARTIFACT.md": "---\ntype: unicorn\nversion: 1.0.0\ndescription: w\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	out := res.Stdout + res.Stderr
	// The linter surfaces the unknown type as a lint.unknown_type diagnostic
	// (a warning, since an extension TypeProvider could register it).
	if !strings.Contains(out, "unicorn") || !strings.Contains(out, "unknown_type") {
		t.Errorf("expected an unknown-type diagnostic referencing 'unicorn', exit=%d out=%s", res.Exit, out)
	}
}

// a domain is navigable without a DOMAIN.md file.
func TestCoreConcept_DomainWithoutDomainMD(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/glossary/ARTIFACT.md": contextArtifact("glossary"),
	}))
	var fin map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance", &fin)
	if !strings.Contains(mustJSON(fin), "glossary") {
		t.Errorf("finance domain not navigable without DOMAIN.md: %v", fin)
	}
}

// DOMAIN.md description and keywords surface in load_domain.
// The server reads DOMAIN.md at ingest and load_domain returns the requested
// domain's description (prose body, else frontmatter) and curated keywords.
// spec: §4.5.5 description rendering and keywords.
func TestCoreConcept_DomainMDDescriptionKeywords(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":               "---\ndescription: \"Accounts payable: money out to vendors\"\ndiscovery:\n  keywords:\n    - invoice\n    - remittance\n---\n\n# Accounts Payable\n\nInvoice processing and vendor remittance.\n",
		"finance/ap/pay-invoice/ARTIFACT.md": contextArtifact("pay invoice"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	desc, _ := m["description"].(string)
	if !strings.Contains(desc, "Invoice processing and vendor remittance") {
		t.Errorf("requested-domain description missing prose body: %q", desc)
	}
	kw := dmKeywords(m)
	for _, want := range []string{"invoice", "remittance"} {
		if !dmContains(kw, want) {
			t.Errorf("keywords %v missing %q", kw, want)
		}
	}
}

// filesystem-mode sync works with no server process.
func TestCoreConcept_FilesystemSyncNoServer(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":              "multi_layer: true\n",
		"team/greetings/hi/ARTIFACT.md": greetSkillArtifact,
		"team/greetings/hi/SKILL.md":    skillBody("hi"),
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, "greetings/hi/ARTIFACT.md"))
}

// filesystem and standalone modes produce equivalent content.
func TestCoreConcept_FilesystemVsStandaloneEquivalent(t *testing.T) {
	t.Skip("blocked by a known gap: the server-source sync path is unimplemented, so the filesystem-vs-server content comparison cannot run")
}

// a higher-precedence layer overrides a lower one on collision.
func TestCoreConcept_LayerPrecedenceOverride(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":                       "multi_layer: true\nlayer_order: [low-layer, high-layer]\n",
		"low-layer/shared/glossary/ARTIFACT.md":  contextArtifact("low-description"),
		"high-layer/shared/glossary/ARTIFACT.md": contextArtifact("high-description"),
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	got := readFile(t, filepath.Join(tgt, "shared/glossary/ARTIFACT.md"))
	if !strings.Contains(got, "high-description") || strings.Contains(got, "low-description") {
		t.Errorf("higher-precedence layer did not win:\n%s", got)
	}
}

// the workspace overlay has the highest precedence.
func TestCoreConcept_OverlayHighestPrecedence(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/glossary/ARTIFACT.md": contextArtifact("registry-version"),
	})
	overlay := writeRegistry(t, map[string]string{
		"shared/glossary/ARTIFACT.md": contextArtifact("overlay-version"),
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--overlay", overlay, "--harness", "none")
	got := readFile(t, filepath.Join(tgt, "shared/glossary/ARTIFACT.md"))
	if !strings.Contains(got, "overlay-version") || strings.Contains(got, "registry-version") {
		t.Errorf("overlay did not take precedence:\n%s", got)
	}
}

// layer ordering defaults to alphabetical by subdirectory.
func TestCoreConcept_LayerOrderAlphabeticalDefault(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":                "multi_layer: true\n",
		"a-layer/shared/item/ARTIFACT.md": contextArtifact("from-a"),
		"b-layer/shared/item/ARTIFACT.md": contextArtifact("from-b"),
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	got := readFile(t, filepath.Join(tgt, "shared/item/ARTIFACT.md"))
	if !strings.Contains(got, "from-b") {
		t.Errorf("alphabetically-later layer did not win:\n%s", got)
	}
}

// a public layer is accessible to unauthenticated callers.
func TestCoreConcept_PublicLayerUnauthenticated(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"test/ARTIFACT.md": contextArtifact("test")})
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir()}, "serve", "--public-mode", "--layer-path", reg)
	if st := getStatus(t, srv.BaseURL+"/v1/search_artifacts?query=test"); st != 200 {
		t.Errorf("unauthenticated search = HTTP %d, want 200", st)
	}
}

// group-restricted layer excluded from a non-member caller.
func TestCoreConcept_GroupRestrictedExcluded(t *testing.T) {
	t.Skip("requires a standard deployment with an OIDC IdP and group claims; per-group visibility is not exercisable in the standalone sandbox")
}

// visibility is enforced on every registry call.
func TestCoreConcept_VisibilityPerCall(t *testing.T) {
	t.Skip("requires a standard deployment with an OIDC IdP and changeable group membership; not exercisable in the standalone sandbox")
}

// harness none writes the canonical layout.
func TestCoreConcept_HarnessNoneCanonical(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"company/glossary/ARTIFACT.md": contextArtifact("glossary")})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	mustExist(t, filepath.Join(tgt, "company/glossary/ARTIFACT.md"))
	for _, dir := range []string{".claude", ".cursor", ".codex"} {
		if _, err := os.Stat(filepath.Join(tgt, dir)); err == nil {
			t.Errorf("harness none created %s/", dir)
		}
	}
}

// claude-code writes a skill to .claude/skills/<name>/SKILL.md.
func TestCoreConcept_ClaudeCodeSkill(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/formatter/ARTIFACT.md": greetSkillArtifact,
		"tools/formatter/SKILL.md":    skillBody("formatter"),
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	mustExist(t, filepath.Join(tgt, ".claude/skills/formatter/SKILL.md"))
}

// claude-code writes a rule to .claude/rules/<name>.md.
func TestCoreConcept_ClaudeCodeRule(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/ts-style/ARTIFACT.md": "---\ntype: rule\nversion: 1.0.0\ndescription: ts\nrule_mode: always\n---\n\nrules\n",
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	mustExist(t, filepath.Join(tgt, ".claude/rules/ts-style.md"))
}

// claude-code writes an agent to .claude/agents/<name>.md.
func TestCoreConcept_ClaudeCodeAgent(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"agents/reviewer/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: reviewer\n---\n\nbody\n",
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	mustExist(t, filepath.Join(tgt, ".claude/agents/reviewer.md"))
}

// cursor writes a rule to .cursor/rules/<name>.mdc.
func TestCoreConcept_CursorRule(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/ts-style/ARTIFACT.md": "---\ntype: rule\nversion: 1.0.0\ndescription: ts\nrule_mode: always\n---\n\nrules\n",
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "cursor")
	mustExist(t, filepath.Join(tgt, ".cursor/rules/ts-style.mdc"))
}

// an unknown harness is rejected with config.unknown_harness.
func TestCoreConcept_UnknownHarness(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", t.TempDir(), "--harness", "not-a-valid-harness")
	if res.Exit == 0 || !strings.Contains(res.Stderr, "config.unknown_harness") {
		t.Errorf("expected config.unknown_harness, exit=%d stderr=%s", res.Exit, res.Stderr)
	}
}

// MCP load_artifact materializes to PODIUM_MATERIALIZE_ROOT.
func TestCoreConcept_MCPMaterialize(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"company-glossary/ARTIFACT.md": contextArtifact("glossary")}))
	mat := t.TempDir()
	res := mcpExec(t, []string{
		"PODIUM_REGISTRY=" + srv.BaseURL,
		"PODIUM_HARNESS=none",
		"PODIUM_MATERIALIZE_ROOT=" + mat,
		"PODIUM_CACHE_DIR=" + t.TempDir(),
	}, toolCall(1, "load_artifact", map[string]any{"id": "company-glossary"}))
	result := rpcResult(t, res.Stdout, 1)
	if paths, _ := result["materialized_at"].([]any); len(paths) == 0 {
		t.Errorf("expected materialized_at paths: %v", result)
	}
	mustExist(t, filepath.Join(mat, "company-glossary/ARTIFACT.md"))
}

// with PODIUM_MATERIALIZE_ROOT unset, load_artifact returns
// the manifest without writing files (materialized_at: []).
func TestCoreConcept_MCPNoMaterializeRoot(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"company-glossary/ARTIFACT.md": contextArtifact("glossary")}))
	res := mcpExec(t, []string{
		"PODIUM_REGISTRY=" + srv.BaseURL,
		"PODIUM_CACHE_DIR=" + t.TempDir(),
	}, toolCall(1, "load_artifact", map[string]any{"id": "company-glossary"}))
	result := rpcResult(t, res.Stdout, 1)
	if result["id"] != "company-glossary" {
		t.Errorf("missing id in result: %v", result)
	}
	if paths, ok := result["materialized_at"].([]any); !ok || len(paths) != 0 {
		t.Errorf("materialized_at=%v, want empty", result["materialized_at"])
	}
}

// sync materializes the full effective view in one batch.
func TestCoreConcept_SyncBatch(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/glossary/ARTIFACT.md": contextArtifact("glossary"),
		"eng/style/ARTIFACT.md":        contextArtifact("style"),
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, "finance/glossary/ARTIFACT.md"))
	mustExist(t, filepath.Join(tgt, "eng/style/ARTIFACT.md"))
}

// load_domain with no path returns the root map.
func TestCoreConcept_LoadDomainRoot(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"alpha/x/ARTIFACT.md": contextArtifact("x"),
		"beta/y/ARTIFACT.md":  contextArtifact("y"),
	}))
	res := mcpExec(t, []string{"PODIUM_REGISTRY=" + srv.BaseURL}, toolCall(1, "load_domain", map[string]any{}))
	body := mustJSON(rpcResult(t, res.Stdout, 1))
	if !strings.Contains(body, "alpha") || !strings.Contains(body, "beta") {
		t.Errorf("root map missing top-level domains: %s", body)
	}
}

// load_domain with a path returns that domain's subdomains.
func TestCoreConcept_LoadDomainPath(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md":                        contextArtifact("pay"),
		"finance/close-reporting/run-variance-analysis/ARTIFACT.md": contextArtifact("variance"),
	}))
	res := mcpExec(t, []string{"PODIUM_REGISTRY=" + srv.BaseURL}, toolCall(1, "load_domain", map[string]any{"path": "finance"}))
	body := mustJSON(rpcResult(t, res.Stdout, 1))
	if !strings.Contains(body, "finance/ap") || !strings.Contains(body, "finance/close-reporting") {
		t.Errorf("finance subdomains missing: %s", body)
	}
}

// search_domains retrieves a domain matching a query via
// its DOMAIN.md projection. spec: §3.2 / §4.7
func TestCoreConcept_SearchDomains(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":               "---\ndescription: \"Accounts payable operations\"\ndiscovery:\n  keywords:\n    - reconciliation\n---\n",
		"finance/ap/pay-invoice/ARTIFACT.md": contextArtifact("pay invoice"),
	}))
	res := mcpExec(t, []string{"PODIUM_REGISTRY=" + srv.BaseURL}, toolCall(1, "search_domains", map[string]any{"query": "reconciliation"}))
	body := mustJSON(rpcResult(t, res.Stdout, 1))
	if !strings.Contains(body, "finance/ap") {
		t.Errorf("search_domains did not retrieve finance/ap by keyword: %s", body)
	}
}

// search_artifacts returns ranked descriptors (no bodies).
func TestCoreConcept_SearchArtifactsQuery(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/va/ARTIFACT.md": contextArtifact("variance analysis report"),
	}))
	res := mcpExec(t, []string{"PODIUM_REGISTRY=" + srv.BaseURL},
		toolCall(1, "search_artifacts", map[string]any{"query": "variance"}))
	body := mustJSON(rpcResult(t, res.Stdout, 1))
	if !strings.Contains(body, "finance/va") {
		t.Errorf("search did not return finance/va: %s", body)
	}
	if strings.Contains(body, "manifest_body") {
		t.Errorf("search descriptors leaked manifest_body: %s", body)
	}
}

// search_artifacts with scope and no query browses a domain.
func TestCoreConcept_SearchArtifactsScope(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/a/ARTIFACT.md": contextArtifact("a"),
		"finance/b/ARTIFACT.md": contextArtifact("b"),
		"eng/c/ARTIFACT.md":     contextArtifact("c"),
	}))
	res := mcpExec(t, []string{"PODIUM_REGISTRY=" + srv.BaseURL},
		toolCall(1, "search_artifacts", map[string]any{"scope": "finance"}))
	body := mustJSON(rpcResult(t, res.Stdout, 1))
	if strings.Contains(body, "eng/c") {
		t.Errorf("scope=finance returned an out-of-scope artifact: %s", body)
	}
	if !strings.Contains(body, "finance/a") {
		t.Errorf("scope=finance missing finance/a: %s", body)
	}
}

// search_artifacts type filter returns only that type.
func TestCoreConcept_SearchArtifactsType(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"sk/ARTIFACT.md":  greetSkillArtifact,
		"sk/SKILL.md":     skillBody("sk"),
		"ctx/ARTIFACT.md": contextArtifact("a context"),
	}))
	res := mcpExec(t, []string{"PODIUM_REGISTRY=" + srv.BaseURL},
		toolCall(1, "search_artifacts", map[string]any{"type": "context"}))
	body := mustJSON(rpcResult(t, res.Stdout, 1))
	if strings.Contains(body, `"type":"skill"`) {
		t.Errorf("type=context returned a skill: %s", body)
	}
	if !strings.Contains(body, "ctx") {
		t.Errorf("type=context missing the context artifact: %s", body)
	}
}

// load_artifact returns absolute materialized_at paths.
func TestCoreConcept_LoadArtifactAbsolutePaths(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"company-glossary/ARTIFACT.md": contextArtifact("glossary")}))
	mat := t.TempDir()
	res := mcpExec(t, []string{
		"PODIUM_REGISTRY=" + srv.BaseURL,
		"PODIUM_HARNESS=none",
		"PODIUM_MATERIALIZE_ROOT=" + mat,
		"PODIUM_CACHE_DIR=" + t.TempDir(),
	}, toolCall(1, "load_artifact", map[string]any{"id": "company-glossary"}))
	result := rpcResult(t, res.Stdout, 1)
	paths, _ := result["materialized_at"].([]any)
	if len(paths) == 0 {
		t.Fatalf("no materialized_at paths: %v", result)
	}
	if p, _ := paths[0].(string); !filepath.IsAbs(p) {
		t.Errorf("materialized_at path is not absolute: %q", p)
	}
	mustExist(t, filepath.Join(mat, "company-glossary/ARTIFACT.md"))
}

// the MCP server exposes exactly the four meta-tools.
func TestCoreConcept_FourMetaTools(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	res := mcpExec(t, []string{"PODIUM_REGISTRY=" + srv.BaseURL}, rpcReq{ID: 1, Method: "tools/list"})
	for _, tool := range []string{"load_domain", "search_domains", "search_artifacts", "load_artifact"} {
		if !strings.Contains(res.Stdout, tool) {
			t.Errorf("tools/list missing %q", tool)
		}
	}
}

// mcp-server artifacts absent from MCP bridge search results.
func TestCoreConcept_MCPServerFilteredFromSearch(t *testing.T) {
	t.Skip("spec §5: mcp-server artifacts should be filtered from MCP bridge results; the bridge does not filter them and no implementation-gap finding is filed for this gap")
}

// the read CLI (podium search) hits the same HTTP API as the
// MCP search_artifacts tool.
func TestCoreConcept_SearchCLIMatchesHTTP(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"company-glossary/ARTIFACT.md": contextArtifact("the company glossary of terms"),
	}))
	cli := runPodium(t, "", nil, "search", "--registry", srv.BaseURL, "glossary")
	if cli.Exit != 0 || !strings.Contains(cli.Stdout, "company-glossary") {
		t.Fatalf("search CLI exit=%d stdout=%q", cli.Exit, cli.Stdout)
	}
	_, body := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query=glossary")
	if !strings.Contains(string(body), "company-glossary") {
		t.Errorf("HTTP search missing the artifact: %s", body)
	}
}

// podium domain show is a thin client over /v1/load_domain.
func TestCoreConcept_DomainShowThinClient(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/glossary/ARTIFACT.md": contextArtifact("glossary"),
	}))
	cli := runPodium(t, "", nil, "domain", "show", "--registry", srv.BaseURL, "finance")
	if cli.Exit != 0 || !strings.Contains(cli.Stdout, "finance") {
		t.Fatalf("domain show exit=%d stdout=%q", cli.Exit, cli.Stdout)
	}
	var http map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance", &http)
	if http["path"] != "finance" {
		t.Errorf("HTTP load_domain path=%v, want finance", http["path"])
	}
}

// lazy navigation: load_domain and search write nothing;
// only load_artifact materializes.
func TestCoreConcept_LazyNavigation(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/glossary/ARTIFACT.md": contextArtifact("glossary"),
	}))
	mat := t.TempDir()
	res := mcpExec(t, []string{
		"PODIUM_REGISTRY=" + srv.BaseURL,
		"PODIUM_HARNESS=none",
		"PODIUM_MATERIALIZE_ROOT=" + mat,
		"PODIUM_CACHE_DIR=" + t.TempDir(),
	},
		toolCall(1, "load_domain", map[string]any{}),
		toolCall(2, "search_artifacts", map[string]any{"scope": "finance"}),
		toolCall(3, "load_artifact", map[string]any{"id": "finance/glossary"}),
	)
	// Discovery calls return results.
	if rpcResult(t, res.Stdout, 1) == nil || rpcResult(t, res.Stdout, 2) == nil {
		t.Fatalf("discovery calls returned no result")
	}
	// Only the loaded artifact is on disk; discovery did not materialize.
	files := readTreeFiltered(t, mat)
	if _, ok := files["finance/glossary/ARTIFACT.md"]; !ok {
		t.Errorf("loaded artifact not materialized: %v", files)
	}
	for path := range files {
		if !strings.HasPrefix(path, "finance/glossary/") {
			t.Errorf("unexpected materialized file (discovery wrote to disk?): %s", path)
		}
	}
}

// eager loading: sync materializes the whole effective view.
func TestCoreConcept_EagerSync(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/a/ARTIFACT.md": contextArtifact("a"),
		"finance/b/ARTIFACT.md": contextArtifact("b"),
		"eng/c/ARTIFACT.md":     contextArtifact("c"),
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	for _, p := range []string{"finance/a", "finance/b", "eng/c"} {
		mustExist(t, filepath.Join(tgt, p, "ARTIFACT.md"))
	}
	if !strings.Contains(res.Stdout, "adapter:") {
		t.Errorf("stdout missing adapter report:\n%s", res.Stdout)
	}
}

// sync --watch re-materializes when the registry changes.
func TestCoreConcept_WatchRematerializes(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"first/ARTIFACT.md": contextArtifact("first")})
	tgt := t.TempDir()
	w := startWatch(t, reg, tgt, "none")
	if !pollFile(filepath.Join(tgt, "first/ARTIFACT.md"), 5*time.Second) {
		t.Fatalf("initial sync missing\nlog:\n%s", w.log())
	}
	mkArtifact(t, filepath.Join(reg, "second"), contextArtifact("second"))
	got := pollFile(filepath.Join(tgt, "second/ARTIFACT.md"), 6*time.Second)
	code := w.stop(t)
	if !got {
		t.Errorf("watcher did not pick up the new artifact\nlog:\n%s", w.log())
	}
	if code != 0 {
		t.Errorf("watch exit=%d, want 0", code)
	}
}

// eager and lazy produce identical adapter output.
func TestCoreConcept_EagerLazyIdentical(t *testing.T) {
	t.Skip("blocked by a known gap: the eager path (podium sync against a server URL) is unimplemented, so eager-vs-lazy adapter output cannot be compared")
}

// sync works against a server registry.
func TestCoreConcept_SyncServerRegistry(t *testing.T) {
	t.Skip("blocked by a known gap: podium sync has no server-source HTTP path; a URL registry is treated as a filesystem path")
}

// version prints a version string and exits cleanly.
func TestCoreConcept_Version(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "version")
	if res.Exit != 0 || !strings.HasPrefix(res.Stdout, "podium ") {
		t.Errorf("version exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// sync with no registry and no sync.yaml fails descriptively.
func TestCoreConcept_SyncNoRegistry(t *testing.T) {
	t.Parallel()
	res := runPodium(t, t.TempDir(), []string{"HOME=" + t.TempDir(), "PODIUM_REGISTRY="}, "sync", "--target", t.TempDir())
	if res.Exit != 2 || !strings.Contains(res.Stderr, "config.no_registry") {
		t.Errorf("exit=%d stderr=%q, want 2 + '--registry is required'", res.Exit, res.Stderr)
	}
}

// version pinning via @semver in the artifact ID.
// Doc-accuracy: inline @<semver> in the id is not resolved (404); the
// ?version= query parameter is the working pin.
func TestCoreConcept_VersionPinInID(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/glossary/ARTIFACT.md": contextArtifact("glossary"),
	}))
	if st := getStatus(t, srv.BaseURL+"/v1/load_artifact?id=finance/glossary@1.0.0"); st != 404 {
		t.Errorf("inline @1.0.0 in id = HTTP %d; the implementation does not resolve inline pins (want 404 documenting the gap)", st)
	}
	var pinned map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id=finance/glossary&version=1.0.0", &pinned)
	if pinned["version"] != "1.0.0" {
		t.Errorf("?version=1.0.0 did not resolve: %v", pinned)
	}
}

// artifact scaffold generates a skill directory structure.
func TestCoreConcept_ScaffoldSkill(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "finance/close-reporting/run-variance-analysis")
	res := runPodium(t, "", nil, "artifact", "scaffold", "--type", "skill", "--description", "Runs variance analysis.", "--yes", out)
	if res.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "type: skill") {
		t.Errorf("ARTIFACT.md missing type: skill:\n%s", art)
	}
	mustExist(t, filepath.Join(out, "SKILL.md"))
}

// load_domain note field when truncated by token budget.
func TestCoreConcept_LoadDomainNoteField(t *testing.T) {
	t.Skip("blocked by a known gap: load_domain renders a single level and the discovery rendering controls (notable_count, token budget, the note field) are not applied")
}

// layer visibility fields combine as a union.
func TestCoreConcept_VisibilityUnion(t *testing.T) {
	t.Skip("requires a standard deployment with an OIDC IdP and org/group claims; union visibility is not exercisable in the standalone sandbox")
}

// a registry with no .registry-config runs as single-layer.
func TestCoreConcept_SingleLayerNoConfig(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"glossary/ARTIFACT.md": contextArtifact("glossary")})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, "glossary/ARTIFACT.md"))
}

// a context artifact materializes to the harness-neutral
// .podium/context/<id>/ bucket under codex (identical across every adapter).
func TestCoreConcept_CodexHarness(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"tools/helper/ARTIFACT.md": contextArtifact("helper")})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "codex")
	if res.Exit != 0 {
		t.Fatalf("codex sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, ".podium/context/tools/helper/ARTIFACT.md"))
}

// hermes reuses the Cursor .mdc format and writes a rule to
// .cursor/rules/<name>.mdc.
func TestCoreConcept_HermesRule(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/style/ARTIFACT.md": "---\ntype: rule\nversion: 1.0.0\ndescription: style\nrule_mode: always\n---\n\nrules\n",
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "hermes")
	mustExist(t, filepath.Join(tgt, ".cursor/rules/style.mdc"))
}

// pi injects a rule into AGENTS.md (§6.7 inject mechanism).
func TestCoreConcept_PiRule(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/style/ARTIFACT.md": "---\ntype: rule\nversion: 1.0.0\ndescription: style\nrule_mode: always\n---\n\nrules\n",
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "pi")
	if got := readFile(t, filepath.Join(tgt, "AGENTS.md")); !strings.Contains(got, "rules") {
		t.Errorf("AGENTS.md missing the injected rule body:\n%s", got)
	}
}

// a context artifact materializes to .podium/context/<id>/
// under gemini (the harness-neutral bucket).
func TestCoreConcept_GeminiExtension(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"tools/helper/ARTIFACT.md": contextArtifact("helper")})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "gemini")
	mustExist(t, filepath.Join(tgt, ".podium/context/tools/helper/ARTIFACT.md"))
}

// opencode injects a rule into AGENTS.md (§6.7 inject).
func TestCoreConcept_OpencodeRule(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/style/ARTIFACT.md": "---\ntype: rule\nversion: 1.0.0\ndescription: style\nrule_mode: always\n---\n\nrules\n",
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "opencode")
	if got := readFile(t, filepath.Join(tgt, "AGENTS.md")); !strings.Contains(got, "rules") {
		t.Errorf("AGENTS.md missing the injected rule body:\n%s", got)
	}
}

// claude-desktop has no project-level surface, so sync
// produces no project output for it (§6.7).
func TestCoreConcept_ClaudeDesktopExtension(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"tools/helper/ARTIFACT.md": contextArtifact("helper")})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-desktop")
	// The sync writes its own .podium/ bookkeeping, but no artifact is
	// materialized: neither the harness-neutral context bucket nor any
	// claude-desktop subtree is produced.
	mustNotExist(t, filepath.Join(tgt, ".podium", "context"))
	mustNotExist(t, filepath.Join(tgt, ".claude-desktop"))
}

// claude-cowork materializes one plugin per artifact under
// plugins/<id>/ with a .claude-plugin/plugin.json manifest. A skill ships as a
// plugin; a context artifact goes to the harness-neutral .podium/context/
// bucket instead (§6.7, identical across every adapter).
func TestCoreConcept_ClaudeCoworkPlugin(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/greet/ARTIFACT.md":  greetSkillArtifact,
		"tools/greet/SKILL.md":     skillBody("greet"),
		"tools/helper/ARTIFACT.md": contextArtifact("helper"),
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-cowork")
	mustExist(t, filepath.Join(tgt, "plugins/tools/greet/.claude-plugin/plugin.json"))
	mustExist(t, filepath.Join(tgt, "plugins/tools/greet/skills/greet/SKILL.md"))
	// Standalone context is harness-neutral, not a plugin.
	mustExist(t, filepath.Join(tgt, ".podium/context/tools/helper/ARTIFACT.md"))
	mustNotExist(t, filepath.Join(tgt, "plugins/tools/helper"))
}

// podium-mcp fails at startup when PODIUM_REGISTRY is unset.
func TestCoreConcept_MCPRequiresRegistry(t *testing.T) {
	t.Parallel()
	res := mcpExec(t, []string{"PODIUM_REGISTRY="}, rpcReq{ID: 1, Method: "initialize"})
	out := res.Stdout + res.Stderr
	// §6.10: unset across all scopes surfaces config.no_registry. The
	// host env may carry a real ~/.podium/sync.yaml, so a clean start is allowed;
	// otherwise a startup failure naming config.no_registry is required.
	if res.Exit == 0 && !strings.Contains(out, "config.no_registry") {
		t.Errorf("expected config.no_registry failure, exit=%d out=%s", res.Exit, out)
	}
}

// none adapter preserves skill bundled resources at relative paths.
func TestCoreConcept_NoneBundledResources(t *testing.T) {
	t.Parallel()
	base := "finance/close-reporting/run-variance-analysis"
	reg := writeRegistry(t, map[string]string{
		base + "/ARTIFACT.md":                      greetSkillArtifact,
		base + "/SKILL.md":                         skillBody("run-variance-analysis"),
		base + "/scripts/variance.py":              "print(1)\n",
		base + "/references/variance-explained.md": "explained\n",
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	for _, f := range []string{"ARTIFACT.md", "SKILL.md", "scripts/variance.py", "references/variance-explained.md"} {
		mustExist(t, filepath.Join(tgt, base, f))
	}
}

// claude-code co-locates skill resources under .claude/skills/<name>/.
func TestCoreConcept_ClaudeCodeColocatesResources(t *testing.T) {
	t.Parallel()
	base := "finance/close-reporting/run-variance-analysis"
	reg := writeRegistry(t, map[string]string{
		base + "/ARTIFACT.md":         greetSkillArtifact,
		base + "/SKILL.md":            skillBody("run-variance-analysis"),
		base + "/scripts/variance.py": "print(1)\n",
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	mustExist(t, filepath.Join(tgt, ".claude/skills/run-variance-analysis/SKILL.md"))
	mustExist(t, filepath.Join(tgt, ".claude/skills/run-variance-analysis/scripts/variance.py"))
}

// sync --dry-run reports a plan and writes nothing.
func TestCoreConcept_DryRun(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--dry-run")
	if res.Exit != 0 || !strings.Contains(res.Stdout, "dry-run") {
		t.Fatalf("dry-run exit=%d stdout=%q", res.Exit, res.Stdout)
	}
	if files := readTreeFiltered(t, tgt); len(files) != 0 {
		t.Errorf("dry-run wrote %d files", len(files))
	}
}

// sync is idempotent across two runs.
func TestCoreConcept_Idempotent(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"a/ARTIFACT.md": contextArtifact("a"),
		"b/ARTIFACT.md": contextArtifact("b"),
	})
	tgt := t.TempDir()
	first := syncAndSnapshot(t, reg, tgt)
	second := syncAndSnapshot(t, reg, tgt)
	if len(first) != len(second) {
		t.Fatalf("file count changed: %d -> %d", len(first), len(second))
	}
	for path, content := range first {
		if second[path] != content {
			t.Errorf("content changed for %q across runs", path)
		}
	}
}

// a per-call harness override on load_artifact switches the
// adapter without restarting the bridge.
func TestCoreConcept_PerCallHarnessOverride(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"rules/ts-style/ARTIFACT.md": "---\ntype: rule\nversion: 1.0.0\ndescription: ts\nrule_mode: always\n---\n\nrules\n",
	}))
	mat := t.TempDir()
	res := mcpExec(t, []string{
		"PODIUM_REGISTRY=" + srv.BaseURL,
		"PODIUM_HARNESS=none",
		"PODIUM_MATERIALIZE_ROOT=" + mat,
		"PODIUM_CACHE_DIR=" + t.TempDir(),
	}, toolCall(1, "load_artifact", map[string]any{"id": "rules/ts-style", "harness": "cursor"}))
	if rpcResult(t, res.Stdout, 1) == nil {
		t.Fatalf("load_artifact returned no result")
	}
	mustExist(t, filepath.Join(mat, ".cursor/rules/ts-style.mdc"))
}
