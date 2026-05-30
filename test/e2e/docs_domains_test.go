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
// The server reads DOMAIN.md at ingest and applies §4.5 domain composition at
// load_domain time: description/body/keywords, include/exclude imports,
// unlisted suppression, cross-layer merge, and the discovery-rendering knobs
// (max_depth, fold_*, notable_count, target_response_tokens, featured,
// deprioritize). search_domains runs hybrid retrieval over those DOMAIN.md
// projections (F-3.2.1): tests 9, 10, and 49 assert keyword retrieval through
// the HTTP endpoint, the CLI, and the MCP tool.
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

// dmKeywords decodes a load_domain response's keywords list.
func dmKeywords(m map[string]any) []string {
	var out []string
	kw, _ := m["keywords"].([]any)
	for _, k := range kw {
		if s, ok := k.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// dmSubdomain returns the top-level subdomain entry with the given path,
// or nil when absent.
func dmSubdomain(m map[string]any, path string) map[string]any {
	subs, _ := m["subdomains"].([]any)
	for _, s := range subs {
		if entry, ok := s.(map[string]any); ok && entry["path"] == path {
			return entry
		}
	}
	return nil
}

// dmNotableEntry returns the notable artifact entry with the given id, or
// nil when absent.
func dmNotableEntry(m map[string]any, id string) map[string]any {
	notable, _ := m["notable"].([]any)
	for _, n := range notable {
		if entry, ok := n.(map[string]any); ok && entry["id"] == id {
			return entry
		}
	}
	return nil
}

// dmContains reports whether ids contains want.
func dmContains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// dmMultiLayer stages a multi-layer filesystem registry (§4.6, §13.11.1):
// it adds a .registry-config with multi_layer: true so each top-level
// subdirectory in files becomes a layer. Layers order alphabetically, so
// a "team" layer takes precedence over an "org" layer (§4.5.4 merge).
func dmMultiLayer(t *testing.T, files map[string]string) string {
	t.Helper()
	withCfg := map[string]string{".registry-config": "multi_layer: true\n"}
	for k, v := range files {
		withCfg[k] = v
	}
	return writeRegistry(t, withCfg)
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

// T-D-domains-4 — a child domain descriptor carries its DOMAIN.md
// frontmatter description (§4.5.1, §4.5.5). spec: §4.5.5 (F-4.5.2)
func TestDomains_ChildDescriptionInLoadDomain(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":               "---\ndescription: \"AP-related operations\"\n---\n\n# AP\n",
		"finance/ap/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"finance/ap/pay-invoice/SKILL.md":    skillBody("pay-invoice"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance", &m)
	sub := dmSubdomain(m, "finance/ap")
	if sub == nil {
		t.Fatalf("finance/ap missing from subdomains: %v", m["subdomains"])
	}
	if sub["description"] != "AP-related operations" {
		t.Errorf("child description = %v, want %q", sub["description"], "AP-related operations")
	}
}

// T-D-domains-5 — load_domain on the domain itself returns the prose body
// in the description slot (§4.5.5 description rendering). spec: §4.5.5 (F-4.5.2)
func TestDomains_ProseBodyForRequestedDomain(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":               "---\ndescription: \"AP-related operations\"\n---\n\n# Accounts Payable\n\nInvoice processing and vendor remittance.\n",
		"finance/ap/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"finance/ap/pay-invoice/SKILL.md":    skillBody("pay-invoice"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	desc, _ := m["description"].(string)
	if !strings.Contains(desc, "Invoice processing and vendor remittance") {
		t.Errorf("requested-domain description missing prose body: %q", desc)
	}
}

// T-D-domains-6 — load_domain depth=2 returns child descriptions without
// their prose bodies (§4.5.5: only the requested domain gets its body).
// spec: §4.5.5 (F-4.5.7, F-4.5.2)
func TestDomains_DepthTwoChildDescriptionsOnly(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/DOMAIN.md":                  "---\ndescription: Finance\n---\n\n# Finance\n\nFINANCE-BODY-MARKER long-form prose.\n",
		"finance/ap/DOMAIN.md":               "---\ndescription: \"AP-related operations\"\n---\n\n# AP\n\nAP-BODY-MARKER should not appear for a child.\n",
		"finance/ap/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"finance/ap/pay-invoice/SKILL.md":    skillBody("pay-invoice"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance&depth=2", &m)
	if d, _ := m["description"].(string); !strings.Contains(d, "FINANCE-BODY-MARKER") {
		t.Errorf("requested domain should carry its body: %q", d)
	}
	sub := dmSubdomain(m, "finance/ap")
	if sub == nil {
		t.Fatalf("finance/ap missing: %v", m["subdomains"])
	}
	if sub["description"] != "AP-related operations" {
		t.Errorf("child description = %v, want frontmatter description", sub["description"])
	}
	if strings.Contains(mustJSON(sub), "AP-BODY-MARKER") {
		t.Errorf("child subtree leaked the prose body: %s", mustJSON(sub))
	}
}

// T-D-domains-7 — a domain without DOMAIN.md returns a synthesized fallback
// description from the directory basename (title-cased, de-slugged).
// spec: §4.5.5 (F-4.5.2)
func TestDomains_SynthesizedFallbackDescription(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/accounts-payable/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"finance/accounts-payable/pay-invoice/SKILL.md":    skillBody("pay-invoice"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance", &m)
	sub := dmSubdomain(m, "finance/accounts-payable")
	if sub == nil {
		t.Fatalf("finance/accounts-payable missing: %v", m["subdomains"])
	}
	if sub["description"] != "Accounts Payable" {
		t.Errorf("fallback description = %v, want %q", sub["description"], "Accounts Payable")
	}
}

// T-D-domains-8 — keywords are returned verbatim in load_domain output for
// the domain (§4.5.5). spec: §4.5.5 (F-4.5.3)
func TestDomains_KeywordsInLoadDomain(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":               "---\ndescription: AP\ndiscovery:\n  keywords:\n    - invoice\n    - remittance\n    - 1099\n---\n\n# AP\n",
		"finance/ap/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"finance/ap/pay-invoice/SKILL.md":    skillBody("pay-invoice"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	kw := dmKeywords(m)
	for _, want := range []string{"invoice", "remittance", "1099"} {
		if !dmContains(kw, want) {
			t.Errorf("keywords %v missing %q", kw, want)
		}
	}
}

// T-D-domains-9 — search_domains finds a domain via a DOMAIN.md keyword
// absent from its path and description (hybrid retrieval over the domain
// projection). spec: §3.2 / §4.7 (F-3.2.1)
func TestDomains_SearchDomainsByKeyword(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":               "---\ndescription: \"Money out to vendors\"\ndiscovery:\n  keywords:\n    - reconciliation\n---\n",
		"finance/ap/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"finance/ap/pay-invoice/SKILL.md":    skillBody("pay-invoice"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/search_domains?query=reconciliation&top_k=10", &m)
	if paths := brDomainPaths(m); !dmContains(paths, "finance/ap") {
		t.Errorf("search_domains(\"reconciliation\") = %v, want finance/ap via its keyword", paths)
	}
}

// T-D-domains-10 — `podium domain search` finds a domain via a keyword
// (the CLI wraps search_domains). spec: §3.2 / §4.7 (F-3.2.1)
func TestDomains_CLISearchByKeyword(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":               "---\ndescription: \"Money out to vendors\"\ndiscovery:\n  keywords:\n    - reconciliation\n---\n",
		"finance/ap/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"finance/ap/pay-invoice/SKILL.md":    skillBody("pay-invoice"),
	}))
	res := runPodium(t, "", nil, "domain", "search", "--registry", srv.BaseURL, "reconciliation")
	if res.Exit != 0 {
		t.Fatalf("domain search exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "finance/ap") {
		t.Errorf("domain search did not surface finance/ap by keyword:\n%s", res.Stdout)
	}
}

// dmFeaturedRegistry stages finance/ap with a DOMAIN.md whose discovery
// block sets featured/deprioritize/notable_count per the named values, plus
// the listed direct-child skills.
func dmFeaturedRegistry(t *testing.T, domainMD string, children ...string) string {
	t.Helper()
	files := map[string]string{"finance/ap/DOMAIN.md": domainMD}
	for _, c := range children {
		files["finance/ap/"+c+"/ARTIFACT.md"] = dmSkillArtifact
		files["finance/ap/"+c+"/SKILL.md"] = skillBody(c)
	}
	return writeRegistry(t, files)
}

// T-D-domains-11 — featured artifacts surface first in the notable list, in
// author-supplied order (§4.5.5 notable selection). spec: §4.5.5 (F-4.5.7)
func TestDomains_FeaturedFirstInNotable(t *testing.T) {
	t.Parallel()
	md := "---\ndescription: AP\ndiscovery:\n  featured:\n    - finance/ap/zebra\n---\n\n# AP\n"
	srv := startServer(t, dmFeaturedRegistry(t, md, "alpha", "zebra"))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	ids := dmNotableIDs(m)
	if len(ids) == 0 || ids[0] != "finance/ap/zebra" {
		t.Errorf("notable order = %v, want finance/ap/zebra first", ids)
	}
}

// T-D-domains-12 — a featured list exceeding notable_count is truncated in
// author order (§4.5.5 cap). spec: §4.5.5 (F-4.5.7)
func TestDomains_FeaturedTruncatedAtNotableCount(t *testing.T) {
	t.Parallel()
	md := "---\ndescription: AP\ndiscovery:\n  notable_count: 2\n  featured:\n    - finance/ap/a\n    - finance/ap/b\n    - finance/ap/c\n---\n\n# AP\n"
	srv := startServer(t, dmFeaturedRegistry(t, md, "a", "b", "c"))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	ids := dmNotableIDs(m)
	want := []string{"finance/ap/a", "finance/ap/b"}
	if len(ids) != 2 || ids[0] != want[0] || ids[1] != want[1] {
		t.Errorf("notable = %v, want %v (truncated in author order)", ids, want)
	}
}

// T-D-domains-13 — a deprioritize entry ranks a matching child last and
// excludes it when the notable cap leaves no room (§4.5.5). spec: §4.5.5 (F-4.5.7)
func TestDomains_DeprioritizeExcludesFromNotable(t *testing.T) {
	t.Parallel()
	md := "---\ndescription: AP\ndiscovery:\n  notable_count: 1\n  deprioritize:\n    - finance/ap/old-invoice\n---\n\n# AP\n"
	srv := startServer(t, dmFeaturedRegistry(t, md, "old-invoice", "pay-invoice"))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	ids := dmNotableIDs(m)
	// Alphabetically old-invoice precedes pay-invoice; deprioritize ranks it
	// last, so the single notable slot goes to pay-invoice instead.
	if len(ids) != 1 || ids[0] != "finance/ap/pay-invoice" {
		t.Errorf("notable = %v, want only finance/ap/pay-invoice (old-invoice deprioritized out)", ids)
	}
}

// dmImportRegistry stages an importing finance/ap/DOMAIN.md plus the named
// imported-artifact paths (each a skill).
func dmImportRegistry(t *testing.T, domainMD string, importedPaths ...string) string {
	t.Helper()
	files := map[string]string{
		"finance/ap/DOMAIN.md":               domainMD,
		"finance/ap/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"finance/ap/pay-invoice/SKILL.md":    skillBody("pay-invoice"),
	}
	for _, p := range importedPaths {
		files[p+"/ARTIFACT.md"] = dmSkillArtifact
		files[p+"/SKILL.md"] = skillBody(lastSeg(p))
	}
	return writeRegistry(t, files)
}

// lastSeg returns the final slash-separated segment of p.
func lastSeg(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// T-D-domains-14 — an exact-match include imports an artifact from another
// domain (§4.5.2). spec: §4.5.2 (F-4.5.6)
func TestDomains_IncludeExactMatch(t *testing.T) {
	t.Parallel()
	md := "---\ndescription: AP\ninclude:\n  - _shared/payment-helpers/routing-validator\n---\n\n# AP\n"
	srv := startServer(t, dmImportRegistry(t, md, "_shared/payment-helpers/routing-validator"))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	if !dmContains(dmNotableIDs(m), "_shared/payment-helpers/routing-validator") {
		t.Errorf("notable %v missing the exact-include import", dmNotableIDs(m))
	}
}

// T-D-domains-15 — a one-level wildcard include imports matching artifacts and
// not deeper ones (§4.5.2). spec: §4.5.2 (F-4.5.6)
func TestDomains_IncludeOneLevelWildcard(t *testing.T) {
	t.Parallel()
	md := "---\ndescription: AP\ninclude:\n  - finance/payments/*\n---\n\n# AP\n"
	srv := startServer(t, dmImportRegistry(t, md,
		"finance/payments/ach", "finance/payments/deep/nested"))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	ids := dmNotableIDs(m)
	if !dmContains(ids, "finance/payments/ach") {
		t.Errorf("notable %v missing finance/payments/ach", ids)
	}
	if dmContains(ids, "finance/payments/deep/nested") {
		t.Errorf("one-level wildcard wrongly imported a two-level-deep artifact: %v", ids)
	}
}

// T-D-domains-16 — a recursive wildcard include imports all matching artifacts
// at any depth (§4.5.2). spec: §4.5.2 (F-4.5.6)
func TestDomains_IncludeRecursiveWildcard(t *testing.T) {
	t.Parallel()
	md := "---\ndescription: AP\ninclude:\n  - finance/refunds/**\n---\n\n# AP\n"
	srv := startServer(t, dmImportRegistry(t, md,
		"finance/refunds/partial", "finance/refunds/full/deep"))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	ids := dmNotableIDs(m)
	for _, want := range []string{"finance/refunds/partial", "finance/refunds/full/deep"} {
		if !dmContains(ids, want) {
			t.Errorf("recursive include notable %v missing %q", ids, want)
		}
	}
}

// T-D-domains-17 — an alternation include imports the matching artifact IDs
// (§4.5.2). spec: §4.5.2 (F-4.5.6)
func TestDomains_IncludeAlternation(t *testing.T) {
	t.Parallel()
	md := "---\ndescription: AP\ninclude:\n  - _shared/regex/{ssn,iban,routing-number}\n---\n\n# AP\n"
	srv := startServer(t, dmImportRegistry(t, md,
		"_shared/regex/ssn", "_shared/regex/iban", "_shared/regex/routing-number", "_shared/regex/other"))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	ids := dmNotableIDs(m)
	for _, want := range []string{"_shared/regex/ssn", "_shared/regex/iban", "_shared/regex/routing-number"} {
		if !dmContains(ids, want) {
			t.Errorf("alternation include notable %v missing %q", ids, want)
		}
	}
	if dmContains(ids, "_shared/regex/other") {
		t.Errorf("alternation include wrongly imported _shared/regex/other: %v", ids)
	}
}

// T-D-domains-18 — imported artifacts keep their canonical IDs in the
// load_domain response (§4.5.2 imports do not change canonical paths).
// spec: §4.5.2 (F-4.5.6)
func TestDomains_ImportsKeepCanonicalIDs(t *testing.T) {
	t.Parallel()
	md := "---\ndescription: AP\ninclude:\n  - _shared/payment-helpers/routing-validator\n---\n\n# AP\n"
	srv := startServer(t, dmImportRegistry(t, md, "_shared/payment-helpers/routing-validator"))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	entry := dmNotableEntry(m, "_shared/payment-helpers/routing-validator")
	if entry == nil {
		t.Fatalf("imported artifact missing under its canonical id: %v", dmNotableIDs(m))
	}
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

// T-D-domains-20 — exclude removes paths from the include set (§4.5.2,
// applied after include). spec: §4.5.2 (F-4.5.6)
func TestDomains_ExcludeRemovesFromInclude(t *testing.T) {
	t.Parallel()
	md := "---\ndescription: AP\ninclude:\n  - _shared/regex/**\nexclude:\n  - _shared/regex/iban\n---\n\n# AP\n"
	srv := startServer(t, dmImportRegistry(t, md, "_shared/regex/ssn", "_shared/regex/iban"))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	ids := dmNotableIDs(m)
	if !dmContains(ids, "_shared/regex/ssn") {
		t.Errorf("notable %v missing included _shared/regex/ssn", ids)
	}
	if dmContains(ids, "_shared/regex/iban") {
		t.Errorf("exclude did not remove _shared/regex/iban: %v", ids)
	}
}

// T-D-domains-21 — unlisted: true removes a folder and its subtree from
// load_domain enumeration (§4.5.3) while a sibling stays visible.
// spec: §4.5.3 / §4.5.5 (F-4.5.5)
func TestDomains_UnlistedRemovedFromEnumeration(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/x/ARTIFACT.md": contextArtifact("x"),
		"_shared/DOMAIN.md":     "---\nunlisted: true\n---\n",
		"_shared/payment-helpers/routing-validator/ARTIFACT.md": dmSkillArtifact,
		"_shared/payment-helpers/routing-validator/SKILL.md":    skillBody("routing-validator"),
	}))
	var root map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain", &root)
	if !hasSubdomain(root, "finance") {
		t.Errorf("visible sibling finance dropped from root: %v", root["subdomains"])
	}
	if hasSubdomain(root, "_shared") {
		t.Errorf("unlisted _shared appears in root enumeration: %v", root["subdomains"])
	}
	// §4.5.5 unknown paths: an unlisted path returns domain.not_found.
	st, body := getRaw(t, srv.BaseURL+"/v1/load_domain?path=_shared")
	if st != 404 || !strings.Contains(string(body), "domain.not_found") {
		t.Errorf("load_domain(_shared) = HTTP %d (%s), want 404 domain.not_found", st, body)
	}
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

// T-D-domains-24 — an artifact inside an unlisted domain is importable into
// another domain via include (§4.5.3). spec: §4.5.3 (F-4.5.5, F-4.5.6)
func TestDomains_UnlistedArtifactImportable(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":                                  "---\ndescription: AP\ninclude:\n  - _shared/payment-helpers/*\n---\n\n# AP\n",
		"finance/ap/pay-invoice/ARTIFACT.md":                    dmSkillArtifact,
		"finance/ap/pay-invoice/SKILL.md":                       skillBody("pay-invoice"),
		"_shared/DOMAIN.md":                                     "---\nunlisted: true\n---\n",
		"_shared/payment-helpers/routing-validator/ARTIFACT.md": dmSkillArtifact,
		"_shared/payment-helpers/routing-validator/SKILL.md":    skillBody("routing-validator"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	if !dmContains(dmNotableIDs(m), "_shared/payment-helpers/routing-validator") {
		t.Errorf("unlisted-domain artifact was not importable into finance/ap: %v", dmNotableIDs(m))
	}
}

// T-D-domains-25 — unlisted: true propagates to the whole subtree, so a path
// below an unlisted folder also returns domain.not_found (§4.5.3).
// spec: §4.5.3 / §4.5.5 (F-4.5.5)
func TestDomains_UnlistedPropagatesToSubtree(t *testing.T) {
	t.Parallel()
	srv := startServer(t, dmUnlistedSharedRegistry(t))
	st, body := getRaw(t, srv.BaseURL+"/v1/load_domain?path=_shared/payment-helpers")
	if st != 404 || !strings.Contains(string(body), "domain.not_found") {
		t.Errorf("load_domain(_shared/payment-helpers) = HTTP %d (%s), want 404 domain.not_found", st, body)
	}
}

// T-D-domains-26 — cross-layer DOMAIN.md description uses last-layer-wins; the
// higher-precedence "team" layer wins over "org" (§4.5.4). spec: §4.5.4 (F-4.5.2)
func TestDomains_LayerMergeDescriptionLastWins(t *testing.T) {
	t.Parallel()
	srv := startServer(t, dmMultiLayer(t, map[string]string{
		"org/finance/ap/DOMAIN.md":               "---\ndescription: \"Org AP\"\n---\n",
		"team/finance/ap/DOMAIN.md":              "---\ndescription: \"Team AP\"\n---\n",
		"org/finance/ap/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"org/finance/ap/pay-invoice/SKILL.md":    skillBody("pay-invoice"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	// Frontmatter-only DOMAIN.md (no body), so the description slot resolves to
	// the merged frontmatter description, last-layer-wins (team over org).
	if d, _ := m["description"].(string); d != "Team AP" {
		t.Errorf("merged description = %q, want %q (last-layer-wins)", d, "Team AP")
	}
}

// T-D-domains-27 — cross-layer DOMAIN.md include is additive across layers
// (§4.5.4). spec: §4.5.4 (F-4.5.6)
func TestDomains_LayerMergeIncludeAdditive(t *testing.T) {
	t.Parallel()
	srv := startServer(t, dmMultiLayer(t, map[string]string{
		"org/finance/ap/DOMAIN.md":               "---\ndescription: AP\ninclude:\n  - _shared/a/*\n---\n\n# AP\n",
		"team/finance/ap/DOMAIN.md":              "---\ninclude:\n  - _shared/b/*\n---\n\n# AP\n",
		"org/finance/ap/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"org/finance/ap/pay-invoice/SKILL.md":    skillBody("pay-invoice"),
		"org/_shared/a/one/ARTIFACT.md":          dmSkillArtifact,
		"org/_shared/a/one/SKILL.md":             skillBody("one"),
		"org/_shared/b/two/ARTIFACT.md":          dmSkillArtifact,
		"org/_shared/b/two/SKILL.md":             skillBody("two"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	ids := dmNotableIDs(m)
	for _, want := range []string{"_shared/a/one", "_shared/b/two"} {
		if !dmContains(ids, want) {
			t.Errorf("additive include notable %v missing %q", ids, want)
		}
	}
}

// T-D-domains-28 — cross-layer unlisted uses most-restrictive-wins: one layer
// setting unlisted: true hides the subtree (§4.5.4). spec: §4.5.4 (F-4.5.5)
func TestDomains_LayerMergeUnlistedMostRestrictive(t *testing.T) {
	t.Parallel()
	srv := startServer(t, dmMultiLayer(t, map[string]string{
		"org/finance/ap/DOMAIN.md":               "---\nunlisted: false\ndescription: AP\n---\n\n# AP\n",
		"team/finance/ap/DOMAIN.md":              "---\nunlisted: true\n---\n",
		"org/finance/ap/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"org/finance/ap/pay-invoice/SKILL.md":    skillBody("pay-invoice"),
	}))
	st, body := getRaw(t, srv.BaseURL+"/v1/load_domain?path=finance/ap")
	if st != 404 || !strings.Contains(string(body), "domain.not_found") {
		t.Errorf("load_domain(finance/ap) = HTTP %d (%s), want 404 (most-restrictive unlisted)", st, body)
	}
}

// T-D-domains-29 — cross-layer max_depth uses the lowest value; a caller depth
// above the merged ceiling is capped and noted (§4.5.4). spec: §4.5.4 (F-4.5.7)
func TestDomains_LayerMergeMaxDepthLowest(t *testing.T) {
	t.Parallel()
	srv := startServer(t, dmMultiLayer(t, map[string]string{
		"org/finance/DOMAIN.md":        "---\ndescription: Finance\ndiscovery:\n  max_depth: 3\n---\n\n# Finance\n",
		"team/finance/DOMAIN.md":       "---\ndiscovery:\n  max_depth: 1\n---\n\n# Finance\n",
		"org/finance/ap/x/ARTIFACT.md": dmSkillArtifact,
		"org/finance/ap/x/SKILL.md":    skillBody("x"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance&depth=3", &m)
	if note, _ := m["note"].(string); !strings.Contains(note, "ceiling of 1") {
		t.Errorf("note = %q, want depth capped at the merged ceiling of 1", note)
	}
}

// T-D-domains-30 — cross-layer fold_below_artifacts uses the highest value, so
// a sparse subdomain folds into the parent (§4.5.4). spec: §4.5.4 (F-4.5.7)
func TestDomains_LayerMergeFoldBelowHighest(t *testing.T) {
	t.Parallel()
	srv := startServer(t, dmMultiLayer(t, map[string]string{
		"org/finance/DOMAIN.md":                 "---\ndescription: Finance\ndiscovery:\n  fold_below_artifacts: 0\n---\n\n# Finance\n",
		"team/finance/DOMAIN.md":                "---\ndiscovery:\n  fold_below_artifacts: 5\n---\n\n# Finance\n",
		"org/finance/sparse/lonely/ARTIFACT.md": dmSkillArtifact,
		"org/finance/sparse/lonely/SKILL.md":    skillBody("lonely"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance", &m)
	if hasSubdomain(m, "finance/sparse") {
		t.Errorf("finance/sparse should have folded (merged fold_below=5): %v", m["subdomains"])
	}
	entry := dmNotableEntry(m, "finance/sparse/lonely")
	if entry == nil {
		t.Fatalf("folded artifact missing from notable: %v", dmNotableIDs(m))
	}
	if entry["folded_from"] != "sparse" {
		t.Errorf("folded_from = %v, want %q", entry["folded_from"], "sparse")
	}
}

// T-D-domains-31 — cross-layer keywords are append-unique (§4.5.4).
// spec: §4.5.4 (F-4.5.3)
func TestDomains_LayerMergeKeywordsAppendUnique(t *testing.T) {
	t.Parallel()
	srv := startServer(t, dmMultiLayer(t, map[string]string{
		"org/finance/ap/DOMAIN.md":               "---\ndescription: AP\ndiscovery:\n  keywords:\n    - invoice\n    - remittance\n---\n\n# AP\n",
		"team/finance/ap/DOMAIN.md":              "---\ndiscovery:\n  keywords:\n    - remittance\n    - vendor\n---\n\n# AP\n",
		"org/finance/ap/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"org/finance/ap/pay-invoice/SKILL.md":    skillBody("pay-invoice"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	kw := dmKeywords(m)
	for _, want := range []string{"invoice", "remittance", "vendor"} {
		if !dmContains(kw, want) {
			t.Errorf("merged keywords %v missing %q", kw, want)
		}
	}
	n := 0
	for _, k := range kw {
		if k == "remittance" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("keyword remittance appears %d times, want 1 (append-unique)", n)
	}
}

// T-D-domains-32 — the max_depth knob caps the rendered subtree depth: at
// max_depth 1, an immediate child carries no nested subdomains (§4.5.5).
// spec: §4.5.5 (F-4.5.7)
func TestDomains_MaxDepthCapsSubtree(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/DOMAIN.md":               "---\ndescription: Finance\ndiscovery:\n  max_depth: 1\n---\n\n# Finance\n",
		"finance/ap/direct/ARTIFACT.md":   dmSkillArtifact,
		"finance/ap/direct/SKILL.md":      skillBody("direct"),
		"finance/ap/sub/deep/ARTIFACT.md": dmSkillArtifact,
		"finance/ap/sub/deep/SKILL.md":    skillBody("deep"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance", &m)
	sub := dmSubdomain(m, "finance/ap")
	if sub == nil {
		t.Fatalf("finance/ap missing: %v", m["subdomains"])
	}
	if nested, _ := sub["subdomains"].([]any); len(nested) != 0 {
		t.Errorf("max_depth 1 should render no nested subdomains under finance/ap, got %v", nested)
	}
}

// T-D-domains-33 — fold_below_artifacts collapses a sparse subdomain into the
// parent notable list with a folded_from annotation (§4.5.5). spec: §4.5.5 (F-4.5.7)
func TestDomains_FoldBelowArtifactsCollapses(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/DOMAIN.md":                 "---\ndescription: Finance\ndiscovery:\n  fold_below_artifacts: 3\n---\n\n# Finance\n",
		"finance/sparse/lonely/ARTIFACT.md": dmSkillArtifact,
		"finance/sparse/lonely/SKILL.md":    skillBody("lonely"),
		"finance/dense/a/ARTIFACT.md":       dmSkillArtifact,
		"finance/dense/a/SKILL.md":          skillBody("a"),
		"finance/dense/b/ARTIFACT.md":       dmSkillArtifact,
		"finance/dense/b/SKILL.md":          skillBody("b"),
		"finance/dense/c/ARTIFACT.md":       dmSkillArtifact,
		"finance/dense/c/SKILL.md":          skillBody("c"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance", &m)
	if hasSubdomain(m, "finance/sparse") {
		t.Errorf("sparse subdomain should have folded away: %v", m["subdomains"])
	}
	if !hasSubdomain(m, "finance/dense") {
		t.Errorf("dense subdomain (3 >= threshold) should remain: %v", m["subdomains"])
	}
	entry := dmNotableEntry(m, "finance/sparse/lonely")
	if entry == nil {
		t.Fatalf("folded artifact missing from notable: %v", dmNotableIDs(m))
	}
	if entry["folded_from"] != "sparse" {
		t.Errorf("folded_from = %v, want %q", entry["folded_from"], "sparse")
	}
}

// T-D-domains-34 — fold_passthrough_chains collapses single-child intermediate
// domains into the deepest non-passthrough descendant (§4.5.5). spec: §4.5.5 (F-4.5.7)
func TestDomains_FoldPassthroughChains(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"a/b/c/d/x/ARTIFACT.md": dmSkillArtifact,
		"a/b/c/d/x/SKILL.md":    skillBody("x"),
		"a/b/c/d/y/ARTIFACT.md": dmSkillArtifact,
		"a/b/c/d/y/SKILL.md":    skillBody("y"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=a", &m)
	if !hasSubdomain(m, "a/b/c/d") {
		t.Errorf("passthrough chain a/b/c should collapse to a/b/c/d: %v", m["subdomains"])
	}
	if hasSubdomain(m, "a/b") {
		t.Errorf("intermediate a/b should not appear as its own subdomain: %v", m["subdomains"])
	}
}

// T-D-domains-35 — the notable_count knob caps the notable list size (§4.5.5).
// spec: §4.5.5 (F-4.5.7)
func TestDomains_NotableCountCap(t *testing.T) {
	t.Parallel()
	srv := startServer(t, dmFeaturedRegistry(t,
		"---\ndescription: AP\ndiscovery:\n  notable_count: 3\n---\n\n# AP\n",
		"a", "b", "c", "d", "e"))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	if ids := dmNotableIDs(m); len(ids) != 3 {
		t.Errorf("notable count = %d, want 3 (discovery.notable_count): %v", len(ids), ids)
	}
}

// T-D-domains-36 — target_response_tokens tightens the notable list to fit the
// soft budget and surfaces a rendering note (§4.5.5). spec: §4.5.5 (F-4.5.7)
func TestDomains_TargetResponseTokensTightens(t *testing.T) {
	t.Parallel()
	srv := startServer(t, dmFeaturedRegistry(t,
		"---\ndescription: AP\ndiscovery:\n  target_response_tokens: 20\n---\n\n# AP\n",
		"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	ids := dmNotableIDs(m)
	if len(ids) >= 6 {
		t.Errorf("notable not tightened under the budget: %d entries", len(ids))
	}
	if note, _ := m["note"].(string); !strings.Contains(note, "reduced") {
		t.Errorf("note = %q, want a budget-reduction sentence", note)
	}
}

// T-D-domains-37 — `podium domain show <path>` surfaces the DOMAIN.md
// description and keywords (it prints the load_domain payload).
// spec: §4.5.5 (F-4.5.2, F-4.5.3)
func TestDomains_CLIShowSurfacesDomainMD(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":               "---\ndescription: \"AP-related operations\"\ndiscovery:\n  keywords:\n    - remittance\n---\n",
		"finance/ap/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"finance/ap/pay-invoice/SKILL.md":    skillBody("pay-invoice"),
	}))
	res := runPodium(t, "", nil, "domain", "show", "--registry", srv.BaseURL, "finance/ap")
	if res.Exit != 0 {
		t.Fatalf("domain show exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "AP-related operations") || !strings.Contains(res.Stdout, "remittance") {
		t.Errorf("domain show missing DOMAIN.md description/keywords:\n%s", res.Stdout)
	}
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

// T-D-domains-48 — the MCP load_domain tool returns the DOMAIN.md description
// and keywords for the requested domain (§4.5.5). spec: §4.5.5 (F-4.5.2, F-4.5.3)
func TestDomains_MCPLoadDomainMetadata(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":               "---\ndescription: \"AP-related operations\"\ndiscovery:\n  keywords:\n    - remittance\n---\n",
		"finance/ap/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"finance/ap/pay-invoice/SKILL.md":    skillBody("pay-invoice"),
	}))
	res := mcpExec(t, mcpServerEnv(t, srv.BaseURL), toolCall(1, "load_domain", map[string]any{"path": "finance/ap"}))
	body := mustJSON(rpcResult(t, res.Stdout, 1))
	if !strings.Contains(body, "AP-related operations") || !strings.Contains(body, "remittance") {
		t.Errorf("MCP load_domain missing description/keywords:\n%s", body)
	}
}

// T-D-domains-49 — the MCP search_domains tool returns a domain matched by
// a DOMAIN.md keyword routed through the bridge. spec: §3.2 / §4.7 (F-3.2.1)
func TestDomains_MCPSearchDomains(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":               "---\ndescription: \"Accounts payable operations\"\ndiscovery:\n  keywords:\n    - remittance\n---\n",
		"finance/ap/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"finance/ap/pay-invoice/SKILL.md":    skillBody("pay-invoice"),
	}))
	res := mcpExec(t, mcpServerEnv(t, srv.BaseURL), toolCall(1, "search_domains", map[string]any{"query": "remittance"}))
	body := mustJSON(rpcResult(t, res.Stdout, 1))
	if !strings.Contains(body, "finance/ap") {
		t.Errorf("MCP search_domains did not surface finance/ap by keyword:\n%s", body)
	}
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

// T-D-domains-52 — a frontmatter-only DOMAIN.md (no prose body) resolves the
// requested domain's description to its frontmatter value (§4.5.5).
// spec: §4.5.5 (F-4.5.2)
func TestDomains_FrontmatterOnlyDescriptionFallback(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":               "---\ndescription: \"AP-related operations\"\n---\n",
		"finance/ap/pay-invoice/ARTIFACT.md": dmSkillArtifact,
		"finance/ap/pay-invoice/SKILL.md":    skillBody("pay-invoice"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	if d, _ := m["description"].(string); d != "AP-related operations" {
		t.Errorf("description = %q, want the frontmatter value (no body present)", d)
	}
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

// T-D-domains-54 — exclude is applied after include, removing the specified
// paths from the imported set (§4.5.2). spec: §4.5.2 (F-4.5.6)
func TestDomains_ExcludeAfterIncludePipeline(t *testing.T) {
	t.Parallel()
	md := "---\ndescription: AP\ninclude:\n  - _shared/**\nexclude:\n  - _shared/regex/iban\n---\n\n# AP\n"
	srv := startServer(t, dmImportRegistry(t, md,
		"_shared/regex/ssn", "_shared/regex/iban", "_shared/other/thing"))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	ids := dmNotableIDs(m)
	for _, want := range []string{"_shared/regex/ssn", "_shared/other/thing"} {
		if !dmContains(ids, want) {
			t.Errorf("include _shared/** notable %v missing %q", ids, want)
		}
	}
	if dmContains(ids, "_shared/regex/iban") {
		t.Errorf("exclude (applied after include) did not remove _shared/regex/iban: %v", ids)
	}
}

// T-D-domains-56 — load_domain tags each notable entry with its §4.5.5
// selection source: "featured" for an author-curated entry, "signal"
// otherwise. spec: §4.5.5 (F-4.5.9)
func TestDomains_NotableSourceTag(t *testing.T) {
	t.Parallel()
	md := "---\ndescription: AP\ndiscovery:\n  featured:\n    - finance/ap/zebra\n---\n\n# AP\n"
	srv := startServer(t, dmFeaturedRegistry(t, md, "alpha", "zebra"))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &m)
	if e := dmNotableEntry(m, "finance/ap/zebra"); e == nil || e["source"] != "featured" {
		t.Errorf("featured entry source = %v, want %q", entrySource(e), "featured")
	}
	if e := dmNotableEntry(m, "finance/ap/alpha"); e == nil || e["source"] != "signal" {
		t.Errorf("non-featured entry source = %v, want %q", entrySource(e), "signal")
	}
}

// entrySource returns the "source" field of a notable entry, or "<nil>".
func entrySource(e map[string]any) any {
	if e == nil {
		return "<nil>"
	}
	return e["source"]
}

// T-D-domains-57 — target_response_tokens tightens the rendered subtree
// depth and the note reports it ("Subtree depth reduced from X to Y ...").
// spec: §4.5.5 (F-4.5.10)
func TestDomains_BudgetReducesSubtreeDepthNote(t *testing.T) {
	t.Parallel()
	// finance has no direct artifacts (so the notable list is empty and
	// only depth can be tightened) and a two-level branchy subtree.
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/DOMAIN.md":                "---\ndescription: Finance\ndiscovery:\n  target_response_tokens: 5\n---\n\n# Finance\n",
		"finance/dept/teamA/x/ARTIFACT.md": dmSkillArtifact,
		"finance/dept/teamA/x/SKILL.md":    skillBody("x"),
		"finance/dept/teamB/y/ARTIFACT.md": dmSkillArtifact,
		"finance/dept/teamB/y/SKILL.md":    skillBody("y"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance&depth=2", &m)
	note, _ := m["note"].(string)
	if !strings.Contains(strings.ToLower(note), "subtree depth reduced from") {
		t.Errorf("note = %q, want a subtree-depth-reduction sentence", note)
	}
}

// T-D-domains-58 — a single-child intermediate domain whose only members
// arrive through DOMAIN.md include: is not collapsed away as a bare
// pass-through (§4.5.5). spec: §4.5.5 (F-4.5.13)
func TestDomains_ImportedMembersPreventCollapse(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		// finance/hub has no canonical artifacts; only imported members.
		"finance/hub/DOMAIN.md":            "---\ninclude:\n  - _shared/lib/**\n---\n",
		"finance/hub/sub/leaf/ARTIFACT.md": dmSkillArtifact,
		"finance/hub/sub/leaf/SKILL.md":    skillBody("leaf"),
		"_shared/lib/routing/ARTIFACT.md":  dmSkillArtifact,
		"_shared/lib/routing/SKILL.md":     skillBody("routing"),
	}))
	var m map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance", &m)
	if !hasSubdomain(m, "finance/hub") {
		t.Errorf("finance/hub collapsed away despite imported members: %v", m["subdomains"])
	}
}

// T-D-domains-55 — a cross-domain import from an unlisted _shared into
// finance/ap surfaces the helpers under finance/ap while _shared stays hidden
// from enumeration (§4.5.2, §4.5.3). spec: §4.5.2 / §4.5.3 (F-4.5.5, F-4.5.6)
func TestDomains_CrossDomainImportFromUnlisted(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/DOMAIN.md":                                  "---\ndescription: AP\ninclude:\n  - _shared/payment-helpers/*\n---\n\n# AP\n",
		"finance/ap/pay-invoice/ARTIFACT.md":                    dmSkillArtifact,
		"finance/ap/pay-invoice/SKILL.md":                       skillBody("pay-invoice"),
		"_shared/DOMAIN.md":                                     "---\nunlisted: true\n---\n",
		"_shared/payment-helpers/routing-validator/ARTIFACT.md": dmSkillArtifact,
		"_shared/payment-helpers/routing-validator/SKILL.md":    skillBody("routing-validator"),
	}))
	var ap map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &ap)
	if !dmContains(dmNotableIDs(ap), "_shared/payment-helpers/routing-validator") {
		t.Errorf("imported helper missing under finance/ap: %v", dmNotableIDs(ap))
	}
	var root map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain", &root)
	if hasSubdomain(root, "_shared") {
		t.Errorf("unlisted _shared leaked into root enumeration: %v", root["subdomains"])
	}
}
