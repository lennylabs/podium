package e2e

// End-to-end tests for docs/authoring/domains.md (D-domains).
//
// Covers the domain model (directories as domains, with and without
// DOMAIN.md), the DOMAIN.md schema (description, keywords, featured,
// deprioritize, include/exclude imports, unlisted), the cross-layer merge
// table, discovery-rendering knobs, and the CLI/MCP surfaces (domain show,
// domain search, domain analyze, load_domain, search_domains). Tests drive the
// podium CLI, the standalone server, and the podium-mcp bridge.
//
// The filesystem server reads the directory tree to enumerate subdomains and
// direct artifacts, but it does not read DOMAIN.md frontmatter or apply the
// discovery-rendering knobs over HTTP. Behaviors blocked by a known BUILD-GAPS
// finding are recorded as skips with the finding ID:
//   - F-4.5.2: load_domain returns no DOMAIN.md description or keywords, so the
//     description/keywords/featured/deprioritize/include/exclude/unlisted/
//     cross-layer-merge surfaces are unobservable over HTTP.
//   - F-4.5.7: load_domain renders a single level and does not apply the
//     discovery rendering knobs (max_depth, fold_*, notable_count,
//     target_response_tokens).
//   - F-3.2.1: search_domains matches on the path substring only; keywords and
//     descriptions are not retrieved.
// Doc claims with no finding filed are asserted against actual behavior with a
// note so a future change is detected (T-D-domains-53).

import (
	"encoding/json"
	"strings"
	"testing"
)

// dmSkillArtifact is a minimal skill ARTIFACT.md (Podium frontmatter only); the
// name and description live in the adjacent SKILL.md (see skillBody).
const dmSkillArtifact = "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- Skill body lives in SKILL.md. -->\n"

// dmCommandArtifact is a minimal type:command ARTIFACT.md (no SKILL.md).
const dmCommandArtifact = "---\ntype: command\nversion: 1.0.0\ndescription: Check deploy configuration.\n---\n\n$ARGUMENTS\n"

// dmNotableIDs decodes a load_domain response map's notable list into the set
// of canonical artifact IDs it reports.
func dmNotableIDs(m map[string]any) []string {
	var ids []string
	notable, _ := m["notable"].([]any)
	for _, n := range notable {
		if entry, ok := n.(map[string]any); ok {
			if id, ok := entry["id"].(string); ok {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// dmNoDomainRegistry stages the doc's "When you don't need DOMAIN.md" tree:
// personal/hello/greet (skill) and personal/deploy/check-config (command), with
// no DOMAIN.md anywhere.
func dmNoDomainRegistry(t *testing.T) string {
	return writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md":         dmSkillArtifact,
		"personal/hello/greet/SKILL.md":            skillBody("greet"),
		"personal/deploy/check-config/ARTIFACT.md": dmCommandArtifact,
	})
}

// T-D-domains-1 — load_domain enumerates subdomains from a directory tree with
// no DOMAIN.md.
func TestDomains_LoadDomainSubdomainsNoDomainMD(t *testing.T) {
	t.Parallel()
	srv := startServer(t, dmNoDomainRegistry(t))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=personal", &m)
	if !hasSubdomain(m, "personal/hello") {
		t.Errorf("subdomains missing personal/hello: %v", m["subdomains"])
	}
	if !hasSubdomain(m, "personal/deploy") {
		t.Errorf("subdomains missing personal/deploy: %v", m["subdomains"])
	}
}

// T-D-domains-2 — load_domain("personal/hello") lists the greet artifact in its
// notable list, with no DOMAIN.md required.
func TestDomains_LoadDomainNotableNoDomainMD(t *testing.T) {
	t.Parallel()
	srv := startServer(t, dmNoDomainRegistry(t))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=personal/hello", &m)
	found := false
	for _, id := range dmNotableIDs(m) {
		if id == "personal/hello/greet" {
			found = true
		}
	}
	if !found {
		t.Errorf("notable missing personal/hello/greet: %v", m["notable"])
	}
}

// T-D-domains-3 — `podium domain show <path>` prints subdomain paths for a tree
// with no DOMAIN.md.
func TestDomains_CLIShowNoDomainMD(t *testing.T) {
	t.Parallel()
	srv := startServer(t, dmNoDomainRegistry(t))
	res := runPodium(t, "", nil, "domain", "show", "--registry", srv.BaseURL, "personal")
	if res.Exit != 0 {
		t.Fatalf("domain show exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "personal/hello") && !strings.Contains(res.Stdout, "personal/deploy") {
		t.Errorf("domain show missing subdomain paths:\n%s", res.Stdout)
	}
}

// T-D-domains-4 — a child domain descriptor should carry its DOMAIN.md
// frontmatter description.
func TestDomains_ChildDescriptionInLoadDomain(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so a child subdomain descriptor never carries the frontmatter description")
}

// T-D-domains-5 — load_domain on the domain itself should return the prose body.
func TestDomains_ProseBodyForRequestedDomain(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so the prose body is never returned for the requested domain")
}

// T-D-domains-6 — load_domain depth=2 should return child descriptions without
// their prose bodies.
func TestDomains_DepthTwoChildDescriptionsOnly(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.7: load_domain renders a single level and does not honor depth-based multi-level subtree rendering, and F-4.5.2 means child descriptions are not read from DOMAIN.md")
}

// T-D-domains-7 — a domain without DOMAIN.md should return a synthesized
// fallback description from the directory basename.
func TestDomains_SynthesizedFallbackDescription(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not populate the description field (DOMAIN.md is not read), so the synthesized basename fallback is not observable over HTTP")
}

// T-D-domains-8 — keywords should be returned verbatim in load_domain output
// for the domain.
func TestDomains_KeywordsInLoadDomain(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so discovery.keywords are never returned in the response")
}

// T-D-domains-9 — search_domains should find a domain via a keyword absent from
// its description.
func TestDomains_SearchDomainsByKeyword(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-3.2.1: search_domains matches on the path substring only; DOMAIN.md keywords are not indexed or retrieved")
}

// T-D-domains-10 — `podium domain search` should find a domain via a keyword.
func TestDomains_CLISearchByKeyword(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-3.2.1: domain search wraps search_domains, which matches on the path substring only, so a keyword-only query does not surface the domain")
}

// T-D-domains-11 — featured artifacts should surface first in the notable list.
func TestDomains_FeaturedFirstInNotable(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.7: load_domain does not apply the discovery rendering knobs, so discovery.featured does not reorder the notable list")
}

// T-D-domains-12 — a featured list exceeding notable_count should be truncated
// in author order with no signal entries.
func TestDomains_FeaturedTruncatedAtNotableCount(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.7: load_domain does not apply discovery.featured or discovery.notable_count, so the truncation behavior is not observable over HTTP")
}

// T-D-domains-13 — a deprioritize glob should exclude matching children from
// the notable list.
func TestDomains_DeprioritizeExcludesFromNotable(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.7: load_domain does not apply discovery.deprioritize, so matching children are not ranked last or excluded from notable")
}

// T-D-domains-14 — an exact-match include should import an artifact from another
// domain.
func TestDomains_IncludeExactMatch(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so include directives are not applied and imported artifacts do not appear in the domain response")
}

// T-D-domains-15 — a one-level wildcard include should import matching
// artifacts.
func TestDomains_IncludeOneLevelWildcard(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so a `payments/*` include is not applied")
}

// T-D-domains-16 — a recursive wildcard include should import all matching
// artifacts.
func TestDomains_IncludeRecursiveWildcard(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so a `refunds/**` include is not applied")
}

// T-D-domains-17 — an alternation include should import the matching artifact
// IDs.
func TestDomains_IncludeAlternation(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so a `{ssn,iban,routing-number}` alternation include is not applied")
}

// T-D-domains-18 — imported artifacts should keep their canonical IDs in the
// load_domain response.
func TestDomains_ImportsKeepCanonicalIDs(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so include directives are not applied and no imported artifacts appear in the domain response to inspect their IDs")
}

// dmRoutingValidatorRegistry stages a single skill at the unlisted-style
// _shared path; it is the only artifact, so the server boots cleanly.
func dmRoutingValidatorRegistry(t *testing.T) string {
	return writeRegistry(t, map[string]string{
		"_shared/payment-helpers/routing-validator/ARTIFACT.md": dmSkillArtifact,
		"_shared/payment-helpers/routing-validator/SKILL.md":    skillBody("routing-validator"),
	})
}

// T-D-domains-19 — search_artifacts returns an imported artifact once, under its
// canonical path. The artifact has exactly one canonical home, so its ID
// appears a single time in the search payload regardless of imports.
func TestDomains_SearchReturnsImportedOnce(t *testing.T) {
	t.Parallel()
	srv := startServer(t, dmRoutingValidatorRegistry(t))
	_, body := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query=routing+validator&top_k=10")
	if n := strings.Count(string(body), "_shared/payment-helpers/routing-validator"); n != 1 {
		t.Errorf("canonical id appears %d times, want 1:\n%s", n, body)
	}
}

// T-D-domains-20 — exclude should remove paths from the include set.
func TestDomains_ExcludeRemovesFromInclude(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so neither include nor exclude directives are applied")
}

// T-D-domains-21 — unlisted: true should remove a domain from load_domain
// enumeration.
func TestDomains_UnlistedRemovedFromEnumeration(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so unlisted: true is not honored and the subtree still appears in enumeration")
}

// dmUnlistedSharedRegistry stages an unlisted _shared/DOMAIN.md (ignored over
// HTTP) alongside the routing-validator skill.
func dmUnlistedSharedRegistry(t *testing.T) string {
	return writeRegistry(t, map[string]string{
		"_shared/DOMAIN.md": "---\nunlisted: true\n---\n",
		"_shared/payment-helpers/routing-validator/ARTIFACT.md": dmSkillArtifact,
		"_shared/payment-helpers/routing-validator/SKILL.md":    skillBody("routing-validator"),
	})
}

// T-D-domains-22 — an artifact inside an unlisted domain stays reachable via
// load_artifact. The DOMAIN.md unlisted flag is not read over HTTP, but the
// load path is independent of enumeration either way.
func TestDomains_UnlistedArtifactReachableByLoad(t *testing.T) {
	t.Parallel()
	srv := startServer(t, dmUnlistedSharedRegistry(t))
	st, body := getRaw(t, srv.BaseURL+"/v1/load_artifact?id=_shared/payment-helpers/routing-validator")
	if st != 200 {
		t.Fatalf("load = HTTP %d, want 200\n%s", st, body)
	}
}

// T-D-domains-23 — an artifact inside an unlisted domain appears in
// search_artifacts.
func TestDomains_UnlistedArtifactInSearch(t *testing.T) {
	t.Parallel()
	srv := startServer(t, dmUnlistedSharedRegistry(t))
	_, body := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query=routing+validator&top_k=10")
	if !strings.Contains(string(body), "_shared/payment-helpers/routing-validator") {
		t.Errorf("search results missing the unlisted-domain artifact:\n%s", body)
	}
}

// T-D-domains-24 — an artifact inside an unlisted domain should be importable
// into another domain via include.
func TestDomains_UnlistedArtifactImportable(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so the include directive that would surface the unlisted artifact under finance/ap is not applied")
}

// T-D-domains-25 — unlisted: true should propagate to the whole subtree.
func TestDomains_UnlistedPropagatesToSubtree(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so unlisted is not honored and the subtree is not removed from enumeration")
}

// T-D-domains-26 — cross-layer DOMAIN.md description should use last-layer-wins.
func TestDomains_LayerMergeDescriptionLastWins(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so the cross-layer description merge is not observable in the response")
}

// T-D-domains-27 — cross-layer DOMAIN.md include should be additive.
func TestDomains_LayerMergeIncludeAdditive(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so include directives from either layer are not applied")
}

// T-D-domains-28 — cross-layer DOMAIN.md unlisted should use most-restrictive.
func TestDomains_LayerMergeUnlistedMostRestrictive(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so unlisted is not honored and the most-restrictive merge is not observable")
}

// T-D-domains-29 — cross-layer DOMAIN.md max_depth should use the lowest value.
func TestDomains_LayerMergeMaxDepthLowest(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.7: load_domain does not apply discovery.max_depth, so the cross-layer most-restrictive merge of max_depth is not observable")
}

// T-D-domains-30 — cross-layer fold_below_artifacts should use the highest
// value.
func TestDomains_LayerMergeFoldBelowHighest(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.7: load_domain does not apply discovery.fold_below_artifacts, so the cross-layer most-restrictive merge is not observable")
}

// T-D-domains-31 — cross-layer DOMAIN.md keywords should be append-unique.
func TestDomains_LayerMergeKeywordsAppendUnique(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so the appended-unique keyword merge is not returned in the response")
}

// T-D-domains-32 — the max_depth knob should cap the rendered subtree depth.
func TestDomains_MaxDepthCapsSubtree(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.7: load_domain renders a single level and does not apply discovery.max_depth")
}

// T-D-domains-33 — fold_below_artifacts should collapse sparse subdomains into
// the parent notable list.
func TestDomains_FoldBelowArtifactsCollapses(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.7: load_domain does not apply discovery.fold_below_artifacts, so sparse subdomains are not folded and no folded_from annotation appears")
}

// T-D-domains-34 — fold_passthrough_chains should collapse single-child
// intermediate domains.
func TestDomains_FoldPassthroughChains(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.7: load_domain does not apply discovery.fold_passthrough_chains, so single-child intermediate domains are not collapsed")
}

// T-D-domains-35 — the notable_count knob should cap the notable list size.
func TestDomains_NotableCountCap(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.7: load_domain does not apply discovery.notable_count, so the default cap of 10 is not enforced over HTTP")
}

// T-D-domains-36 — target_response_tokens should tighten depth and notable
// count.
func TestDomains_TargetResponseTokensTightens(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.7: load_domain does not apply discovery.target_response_tokens, so the response is not tightened and no budget note is emitted")
}

// T-D-domains-37 — `podium domain show <path>` should surface the DOMAIN.md
// description or keywords.
func TestDomains_CLIShowSurfacesDomainMD(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: domain show wraps load_domain, which does not read DOMAIN.md, so the description/keywords are not present in the output")
}

// T-D-domains-38 — `podium domain show --json` emits valid JSON.
func TestDomains_CLIShowJSON(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("x")}))
	res := runPodium(t, "", nil, "domain", "show", "--registry", srv.BaseURL, "--json")
	if res.Exit != 0 {
		t.Fatalf("domain show --json exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !json.Valid([]byte(res.Stdout)) {
		t.Errorf("domain show --json output is not valid JSON:\n%s", res.Stdout)
	}
}

// dmFinanceOpsRegistry stages finance/ap and ops/restart domains, each with a
// single skill, for scope-filtering checks.
func dmFinanceOpsRegistry(t *testing.T) string {
	return writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"finance/ap/pay-invoice/SKILL.md":    skillBody("pay-invoice"),
		"ops/restart/do-it/ARTIFACT.md":      dmSkillArtifact,
		"ops/restart/do-it/SKILL.md":         skillBody("do-it"),
	})
}

// T-D-domains-39 — `podium domain search --scope finance <query>` constrains
// results to the finance prefix and excludes ops.
func TestDomains_CLISearchScope(t *testing.T) {
	t.Parallel()
	srv := startServer(t, dmFinanceOpsRegistry(t))
	res := runPodium(t, "", nil, "domain", "search", "--registry", srv.BaseURL, "--scope", "finance", "ap")
	if res.Exit != 0 {
		t.Fatalf("domain search exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if strings.Contains(res.Stdout, "ops/restart") {
		t.Errorf("scope=finance leaked an ops domain:\n%s", res.Stdout)
	}
}

// T-D-domains-40 — `podium domain analyze --path <path>` reports metrics for the
// subtree.
func TestDomains_CLIAnalyzeSubtree(t *testing.T) {
	t.Parallel()
	srv := startServer(t, dmFinanceOpsRegistry(t))
	res := runPodium(t, "", nil, "domain", "analyze", "--registry", srv.BaseURL, "--path", "finance")
	if res.Exit != 0 {
		t.Fatalf("domain analyze exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "finance") {
		t.Errorf("analyze output missing finance:\n%s", res.Stdout)
	}
}

// T-D-domains-41 — `podium domain analyze` with no --path analyzes the root and
// returns a non-empty report.
func TestDomains_CLIAnalyzeRoot(t *testing.T) {
	t.Parallel()
	srv := startServer(t, dmFinanceOpsRegistry(t))
	res := runPodium(t, "", nil, "domain", "analyze", "--registry", srv.BaseURL)
	if res.Exit != 0 {
		t.Fatalf("domain analyze exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) == "" {
		t.Errorf("analyze (root) produced an empty report")
	}
}

// T-D-domains-42 — `podium domain show` without a registry errors (exit 2).
func TestDomains_CLIShowMissingRegistry(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", []string{"PODIUM_REGISTRY="}, "domain", "show", "finance")
	if res.Exit != 2 {
		t.Fatalf("domain show exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--registry is required") {
		t.Errorf("stderr missing '--registry is required':\n%s", res.Stderr)
	}
}

// T-D-domains-43 — `podium domain search` without a query argument errors with
// usage guidance (exit 2).
func TestDomains_CLISearchMissingQuery(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("x")}))
	res := runPodium(t, "", nil, "domain", "search", "--registry", srv.BaseURL)
	if res.Exit != 2 {
		t.Fatalf("domain search exit=%d, want 2\nstdout=%s stderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "usage: podium domain search <query>") {
		t.Errorf("stderr missing usage guidance:\n%s", res.Stderr)
	}
}

// T-D-domains-44 — lint warns (exit 0) on a DOMAIN.md include pattern that
// matches no artifact, naming the unresolved pattern.
func TestDomains_LintImportUnresolved(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":               "---\ninclude:\n  - nonexistent/path/*\n---\n\n# AP\n",
		"finance/ap/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"finance/ap/pay-invoice/SKILL.md":    skillBody("pay-invoice"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.domain_import_unresolved") || !strings.Contains(res.Stdout, "nonexistent/path/*") {
		t.Errorf("missing unresolved-import warning naming the pattern:\n%s", res.Stdout)
	}
}

// T-D-domains-45 — lint warns (exit 0) on a DOMAIN.md import cycle between two
// domains, naming both participants.
func TestDomains_LintImportCycle(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/DOMAIN.md":      "---\ninclude:\n  - treasury/**\n---\n\n# Finance\n",
		"treasury/DOMAIN.md":     "---\ninclude:\n  - finance/**\n---\n\n# Treasury\n",
		"finance/x/ARTIFACT.md":  contextArtifact("finance context"),
		"treasury/y/ARTIFACT.md": contextArtifact("treasury context"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.domain_import_cycle") {
		t.Fatalf("missing lint.domain_import_cycle:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "finance") || !strings.Contains(res.Stdout, "treasury") {
		t.Errorf("cycle warning does not name both participants:\n%s", res.Stdout)
	}
}

// dmFullDomainMD is the full DOMAIN.md example from the doc's schema section.
const dmFullDomainMD = `---
unlisted: false
description: "AP-related operations"

discovery:
  max_depth: 4
  fold_below_artifacts: 5
  featured:
    - finance/ap/pay-invoice
  deprioritize:
    - finance/ap/_archive/**
  keywords:
    - invoice
    - remittance
    - reconciliation
    - 1099
    - vendor master

include:
  - finance/ap/pay-invoice
  - finance/ap/payments/*
  - finance/refunds/**
  - _shared/payment-helpers/*
  - _shared/regex/{ssn,iban,routing-number}

exclude:
  - finance/ap/internal/**
---

# Accounts Payable

Operations and artifacts for the AP function: invoice processing,
vendor remittance, payment reconciliation, and 1099 reporting. This
domain applies to tasks involving money flowing out of the company to
vendors.

For inbound payments and AR, see ` + "`finance/ar/`" + `.
`

// T-D-domains-46 — the full DOMAIN.md schema example lints cleanly when every
// include and featured pattern resolves to an existing artifact.
func TestDomains_FullSchemaLintsClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md": dmFullDomainMD,

		"finance/ap/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"finance/ap/pay-invoice/SKILL.md":    skillBody("pay-invoice"),

		"finance/ap/payments/ach/ARTIFACT.md": dmSkillArtifact,
		"finance/ap/payments/ach/SKILL.md":    skillBody("ach"),

		"finance/refunds/partial/ARTIFACT.md": dmSkillArtifact,
		"finance/refunds/partial/SKILL.md":    skillBody("partial"),

		"_shared/payment-helpers/routing-validator/ARTIFACT.md": dmSkillArtifact,
		"_shared/payment-helpers/routing-validator/SKILL.md":    skillBody("routing-validator"),

		"_shared/regex/ssn/ARTIFACT.md": dmSkillArtifact,
		"_shared/regex/ssn/SKILL.md":    skillBody("ssn"),

		"_shared/regex/iban/ARTIFACT.md": dmSkillArtifact,
		"_shared/regex/iban/SKILL.md":    skillBody("iban"),

		"_shared/regex/routing-number/ARTIFACT.md": dmSkillArtifact,
		"_shared/regex/routing-number/SKILL.md":    skillBody("routing-number"),

		"finance/ap/_archive/old-invoice/ARTIFACT.md": dmSkillArtifact,
		"finance/ap/_archive/old-invoice/SKILL.md":    skillBody("old-invoice"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s\nstderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
	if strings.Contains(res.Stdout, "[error]") {
		t.Errorf("full schema produced error diagnostics:\n%s", res.Stdout)
	}
}

// T-D-domains-47 — the MCP load_domain tool navigates the domain hierarchy and
// reports the subdomains.
func TestDomains_MCPLoadDomain(t *testing.T) {
	t.Parallel()
	srv := startServer(t, dmNoDomainRegistry(t))
	res := mcpExec(t, mcpServerEnv(t, srv.BaseURL), toolCall(1, "load_domain", map[string]any{"path": "personal"}))
	body := mustJSON(rpcResult(t, res.Stdout, 1))
	if !strings.Contains(body, "personal/hello") || !strings.Contains(body, "personal/deploy") {
		t.Errorf("MCP load_domain missing subdomains:\n%s", body)
	}
}

// T-D-domains-48 — the MCP load_domain tool should return the DOMAIN.md
// description and keywords.
func TestDomains_MCPLoadDomainMetadata(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so the MCP load_domain result carries no description or keywords")
}

// T-D-domains-49 — the MCP search_domains tool should return domains matching a
// keyword query.
func TestDomains_MCPSearchDomains(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-3.2.1: search_domains matches on the path substring only, so a keyword query routed through the MCP tool does not surface the domain")
}

// T-D-domains-50 — load_domain on an unknown path returns 404 domain.not_found.
func TestDomains_LoadDomainNotFound(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("x")}))
	st, body := getRaw(t, srv.BaseURL+"/v1/load_domain?path=nonexistent/path")
	if st != 404 {
		t.Fatalf("load_domain = HTTP %d, want 404\n%s", st, body)
	}
	if !strings.Contains(string(body), "domain.not_found") {
		t.Errorf("body missing domain.not_found:\n%s", body)
	}
}

// T-D-domains-51 — POST /v1/domain/analyze returns 405 (GET only).
func TestDomains_AnalyzePostNotAllowed(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("x")}))
	st, _ := postJSON(t, srv.BaseURL+"/v1/domain/analyze", map[string]any{})
	if st != 405 {
		t.Errorf("POST /v1/domain/analyze = HTTP %d, want 405", st)
	}
}

// T-D-domains-52 — a frontmatter-only DOMAIN.md should resolve the description
// to its frontmatter value.
func TestDomains_FrontmatterOnlyDescriptionFallback(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so the frontmatter description fallback is not returned in the response")
}

// T-D-domains-53 — the doc states lint warns when a DOMAIN.md prose body exceeds
// 2000 tokens, but no such rule exists (no BUILD-GAPS finding is filed for this
// gap). This change-detector asserts the actual behavior: a long DOMAIN.md body
// lints cleanly with no manifest_size diagnostic and no error. If a
// DOMAIN.md body-length lint is later added, this test flags the change.
func TestDomains_DomainBodyLengthNoLintWarning(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("word ", 1800) // ~9000 bytes of prose
	reg := writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":               "---\ndescription: \"AP-related operations\"\n---\n\n# Accounts Payable\n\n" + body,
		"finance/ap/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"finance/ap/pay-invoice/SKILL.md":    skillBody("pay-invoice"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout(head)=%.300s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "manifest_size") {
		t.Errorf("unexpected manifest_size diagnostic on a DOMAIN.md body (no such rule should exist):\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "[error]") {
		t.Errorf("long DOMAIN.md body produced an error diagnostic:\n%s", res.Stdout)
	}
}

// T-D-domains-54 — exclude applied after include should remove the specified
// paths.
func TestDomains_ExcludeAfterIncludePipeline(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so the include-then-exclude pipeline is not applied over HTTP")
}

// T-D-domains-55 — a cross-domain import from an unlisted _shared into finance/ap
// should surface the helpers under finance/ap while keeping _shared hidden.
func TestDomains_CrossDomainImportFromUnlisted(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.5.2: load_domain does not read DOMAIN.md, so neither the include directive nor the unlisted flag is applied; the imported helpers do not appear under finance/ap")
}
