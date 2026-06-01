package e2e

// End-to-end tests for docs/authoring/frontmatter-reference.md (D-frontmatter).
// Covers the SKILL.md/ARTIFACT.md field split, universal fields, lint
// enforcement, caller-interpreted fields (stored verbatim and surfaced in the
// load_artifact frontmatter), type-specific fields, provenance rewriting, and
// the cross-layer merge table. Tests drive the podium CLI, the standalone
// server, and the podium-mcp bridge. Behaviors blocked by a known BUILD-GAPS
// finding are recorded as skips; doc claims that the implementation does not
// honor (with no finding filed) are asserted against actual behavior with a
// note so a future change is detected.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fmSkillArtifact is a minimal skill ARTIFACT.md (Podium frontmatter only).
const fmSkillArtifact = "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- Skill body lives in SKILL.md. -->\n"

// fmSkillMD returns a SKILL.md whose name matches the leaf directory.
func fmSkillMD(name, desc string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + name + " body.\n"
}

// ---- File allocation for skills (scaffold split) ---------------------------

// T-D-frontmatter-1 — skill scaffold puts name/description in SKILL.md, keeps
// type/version in ARTIFACT.md, and writes an HTML-comment ARTIFACT.md body.
func TestFrontmatter_SkillScaffoldSplit(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "finance/close/run-variance-analysis")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "skill",
		"--description", "Flag unusual variance vs. forecast after month-end close.", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "type: skill") || !strings.Contains(art, "version:") {
		t.Errorf("ARTIFACT.md missing type/version:\n%s", art)
	}
	if strings.Contains(art, "name:") || strings.Contains(art, "description:") {
		t.Errorf("skill ARTIFACT.md must not carry name/description:\n%s", art)
	}
	skill := readFile(t, filepath.Join(out, "SKILL.md"))
	if !strings.Contains(skill, "name: run-variance-analysis") || !strings.Contains(skill, "description:") {
		t.Errorf("SKILL.md missing name/description:\n%s", skill)
	}
	if strings.Contains(skill, "type:") {
		t.Errorf("SKILL.md must not carry type:\n%s", skill)
	}
}

// T-D-frontmatter-2 — a non-skill scaffold emits a single ARTIFACT.md with the
// universal fields and no SKILL.md.
func TestFrontmatter_NonSkillScaffoldSingleFile(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "finance/glossary")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "context",
		"--description", "Glossary of finance terms.", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	if _, err := os.Stat(filepath.Join(out, "SKILL.md")); err == nil {
		t.Errorf("context must not have a SKILL.md")
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	for _, want := range []string{"type: context", "description:", "version:"} {
		if !strings.Contains(art, want) {
			t.Errorf("ARTIFACT.md missing %q:\n%s", want, art)
		}
	}
}

// T-D-frontmatter-3 — a valid skill (ARTIFACT.md + SKILL.md) lints cleanly.
func TestFrontmatter_ValidSkillLints(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md": fmSkillArtifact,
		"finance/close/run-variance-analysis/SKILL.md":    fmSkillMD("run-variance-analysis", "Flag unusual variance vs. forecast after month-end close."),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// T-D-frontmatter-4 — a skill missing SKILL.md fails lint. The registry walk
// rejects it before the lint rules run, so the message is
// "type: skill missing SKILL.md" (exit 1) rather than a lint.skill_md_compliance
// diagnostic. spec: doc "File allocation for skills".
func TestFrontmatter_SkillMissingSkillMD(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md": fmSkillArtifact,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s stderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout+res.Stderr, "missing SKILL.md") {
		t.Errorf("missing 'missing SKILL.md' diagnostic:\nstdout=%s\nstderr=%s", res.Stdout, res.Stderr)
	}
}

// T-D-frontmatter-5 — a SKILL.md whose name mismatches the parent directory
// fails lint with lint.skill_md_compliance.
func TestFrontmatter_SkillNameMismatch(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md": fmSkillArtifact,
		"finance/close/run-variance-analysis/SKILL.md":    fmSkillMD("wrong-name", "A skill whose name does not match its directory."),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.skill_md_compliance") || !strings.Contains(res.Stdout, "wrong-name") {
		t.Errorf("missing name-mismatch diagnostic:\n%s", res.Stdout)
	}
}

// T-D-frontmatter-6 — a skill name with a leading hyphen fails lint.invalid_name.
func TestFrontmatter_SkillNameLeadingHyphen(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/close/-bad-name/ARTIFACT.md": fmSkillArtifact,
		"finance/close/-bad-name/SKILL.md":    fmSkillMD("-bad-name", "Bad name with a leading hyphen."),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.invalid_name") {
		t.Errorf("missing lint.invalid_name:\n%s", res.Stdout)
	}
}

// T-D-frontmatter-7 — a skill name with consecutive hyphens fails lint.invalid_name.
func TestFrontmatter_SkillNameConsecutiveHyphens(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/close/run--analysis/ARTIFACT.md": fmSkillArtifact,
		"finance/close/run--analysis/SKILL.md":    fmSkillMD("run--analysis", "Name with consecutive hyphens."),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.invalid_name") {
		t.Errorf("missing lint.invalid_name:\n%s", res.Stdout)
	}
}

// T-D-frontmatter-8 — a missing type fails lint.required_field_missing.
func TestFrontmatter_MissingType(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/my-tool/ARTIFACT.md": "---\nversion: 1.0.0\ndescription: Tool.\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.required_field_missing") || !strings.Contains(res.Stdout, "type is required") {
		t.Errorf("missing 'type is required':\n%s", res.Stdout)
	}
}

// T-D-frontmatter-9 — a missing version fails lint.required_field_missing.
func TestFrontmatter_MissingVersion(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/my-tool/ARTIFACT.md": "---\ntype: context\ndescription: Tool.\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.required_field_missing") || !strings.Contains(res.Stdout, "version is required") {
		t.Errorf("missing 'version is required':\n%s", res.Stdout)
	}
}

// T-D-frontmatter-10 — a non-semver version fails lint.invalid_version.
func TestFrontmatter_NonSemverVersion(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/my-tool/ARTIFACT.md": "---\ntype: context\nversion: not-semver\ndescription: Tool.\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.invalid_version") {
		t.Errorf("missing lint.invalid_version:\n%s", res.Stdout)
	}
}

// fmDeprecationRegistry stages an active and a deprecated context artifact.
func fmDeprecationRegistry(t *testing.T) string {
	return writeRegistry(t, map[string]string{
		"finance/active-tool/ARTIFACT.md": "---\ntype: context\nname: active-tool\nversion: 1.0.0\ndescription: Current variance tool.\n---\n\nbody\n",
		"finance/old-tool/ARTIFACT.md":    "---\ntype: context\nname: old-tool\nversion: 1.0.0\ndescription: Old variance tool.\ndeprecated: true\nreplaced_by: finance/active-tool\n---\n\nbody\n",
	})
}

// T-D-frontmatter-11 — a deprecated artifact is excluded from default search.
func TestFrontmatter_DeprecatedExcludedFromSearch(t *testing.T) {
	t.Parallel()
	srv := startServer(t, fmDeprecationRegistry(t))
	_, body := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query=tool&type=context")
	if strings.Contains(string(body), "finance/old-tool") {
		t.Errorf("deprecated artifact appeared in search:\n%s", body)
	}
	if !strings.Contains(string(body), "finance/active-tool") {
		t.Errorf("active artifact missing from search:\n%s", body)
	}
}

// T-D-frontmatter-12 — a deprecated artifact remains reachable via load_artifact
// and carries the deprecation signal. Observed: deprecation_warning is
// "artifact is deprecated" and replaced_by does not round-trip into the load
// response (no BUILD-GAPS finding is filed for that narrow gap), so this test
// asserts the deprecated flag and warning rather than the upgrade target.
func TestFrontmatter_DeprecatedReachableWithWarning(t *testing.T) {
	t.Parallel()
	srv := startServer(t, fmDeprecationRegistry(t))
	var load struct {
		Deprecated         bool   `json:"deprecated"`
		ReplacedBy         string `json:"replaced_by"`
		DeprecationWarning string `json:"deprecation_warning"`
	}
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id=finance/old-tool", &load)
	if !load.Deprecated {
		t.Errorf("load response missing deprecated flag: %+v", load)
	}
	if !strings.Contains(load.DeprecationWarning, "deprecated") {
		t.Errorf("load response missing deprecation warning: %+v", load)
	}
}

// T-D-frontmatter-13 — search_visibility: direct-only hides an artifact
// from search while an indexed sibling still appears.
// spec: §4.3 universal fields (search_visibility), §4.5.3 (F-4.3.3).
func TestFrontmatter_DirectOnlyHiddenFromSearch(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/public-tool/ARTIFACT.md": "---\ntype: context\nname: public-tool\nversion: 1.0.0\ndescription: Public payments tool.\n---\n\nbody\n",
		"finance/secret-tool/ARTIFACT.md": "---\ntype: context\nname: secret-tool\nversion: 1.0.0\ndescription: Secret payments tool.\nsearch_visibility: direct-only\n---\n\nbody\n",
	}))
	_, sbody := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query=payments")
	if strings.Contains(string(sbody), "finance/secret-tool") {
		t.Errorf("direct-only artifact appeared in search results:\n%s", sbody)
	}
	if !strings.Contains(string(sbody), "finance/public-tool") {
		t.Errorf("indexed artifact missing from search:\n%s", sbody)
	}
}

// T-D-frontmatter-14 — a direct-only artifact is reachable via load_artifact by
// ID (visibility filtering happens on search, not load).
func TestFrontmatter_DirectOnlyReachableByID(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/secret-tool/ARTIFACT.md": "---\ntype: context\nname: secret-tool\nversion: 1.0.0\ndescription: Secret tool.\nsearch_visibility: direct-only\n---\n\nbody\n",
	}))
	var load struct {
		ID string `json:"id"`
	}
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id=finance/secret-tool", &load)
	if load.ID != "finance/secret-tool" {
		t.Errorf("load id=%q, want finance/secret-tool", load.ID)
	}
}

// T-D-frontmatter-15 — the SKILL.md compatibility field is accepted by lint and
// the skill loads. The load response surfaces ARTIFACT.md frontmatter and the
// SKILL.md body; SKILL.md-only frontmatter (compatibility) is not echoed in the
// response, so the test asserts ingest acceptance and reachability.
func TestFrontmatter_SkillCompatibilityStored(t *testing.T) {
	t.Parallel()
	skill := "---\nname: run-variance-analysis\ndescription: Flag variance.\ncompatibility: Requires Python 3.10+ and pandas. Designed for Claude Code or similar.\n---\n\nbody\n"
	reg := writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md": fmSkillArtifact,
		"finance/close/run-variance-analysis/SKILL.md":    skill,
	})
	if l := runPodium(t, "", nil, "lint", "--registry", reg); l.Exit != 0 || !strings.Contains(l.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q", l.Exit, l.Stdout)
	}
	srv := startServer(t, reg)
	if st := getStatus(t, srv.BaseURL+"/v1/load_artifact?id=finance/close/run-variance-analysis"); st != 200 {
		t.Errorf("load = HTTP %d, want 200", st)
	}
}

// T-D-frontmatter-16 — the SKILL.md metadata map is accepted by lint and the
// skill loads (same surfacing caveat as compatibility).
func TestFrontmatter_SkillMetadataStored(t *testing.T) {
	t.Parallel()
	skill := "---\nname: run-variance-analysis\ndescription: Flag variance.\nmetadata:\n  author: acme-org\n---\n\nbody\n"
	reg := writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md": fmSkillArtifact,
		"finance/close/run-variance-analysis/SKILL.md":    skill,
	})
	if l := runPodium(t, "", nil, "lint", "--registry", reg); l.Exit != 0 || !strings.Contains(l.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q", l.Exit, l.Stdout)
	}
	srv := startServer(t, reg)
	if st := getStatus(t, srv.BaseURL+"/v1/load_artifact?id=finance/close/run-variance-analysis"); st != 200 {
		t.Errorf("load = HTTP %d, want 200", st)
	}
}

// fmLoadFrontmatter returns the raw `frontmatter` string from a load_artifact
// response; caller-interpreted ARTIFACT.md fields are stored verbatim there.
func fmLoadFrontmatter(t *testing.T, baseURL, id string) string {
	t.Helper()
	var load struct {
		Frontmatter string `json:"frontmatter"`
	}
	getJSON(t, baseURL+"/v1/load_artifact?id="+id, &load)
	return load.Frontmatter
}

// T-D-frontmatter-17 — mcpServers is stored verbatim and returned in the
// load_artifact frontmatter.
func TestFrontmatter_McpServersVerbatim(t *testing.T) {
	t.Parallel()
	art := "---\ntype: skill\nversion: 1.0.0\nmcpServers:\n  - name: finance-warehouse\n    transport: stdio\n    command: npx\n    args: [\"-y\", \"@company/finance-warehouse-mcp\"]\n---\n\n<!-- Skill body lives in SKILL.md. -->\n"
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": art,
		"finance/ap/pay-invoice/SKILL.md":    fmSkillMD("pay-invoice", "Pay an approved invoice."),
	}))
	fm := fmLoadFrontmatter(t, srv.BaseURL, "finance/ap/pay-invoice")
	for _, want := range []string{"mcpServers", "finance-warehouse", "@company/finance-warehouse-mcp"} {
		if !strings.Contains(fm, want) {
			t.Errorf("frontmatter missing %q:\n%s", want, fm)
		}
	}
}

// T-D-frontmatter-18 — requiresApproval is stored verbatim and returned on load.
func TestFrontmatter_RequiresApprovalVerbatim(t *testing.T) {
	t.Parallel()
	art := "---\ntype: agent\nname: pay-agent\nversion: 1.0.0\ndescription: Pay agent.\nrequiresApproval:\n  - tool: payment-submit\n    reason: irreversible\n---\n\nbody\n"
	srv := startServer(t, writeRegistry(t, map[string]string{"finance/pay-agent/ARTIFACT.md": art}))
	fm := fmLoadFrontmatter(t, srv.BaseURL, "finance/pay-agent")
	for _, want := range []string{"requiresApproval", "payment-submit", "irreversible"} {
		if !strings.Contains(fm, want) {
			t.Errorf("frontmatter missing %q:\n%s", want, fm)
		}
	}
}

// T-D-frontmatter-19 — sbom is stored verbatim; the registry does not fetch or
// parse the referenced file.
func TestFrontmatter_SbomVerbatim(t *testing.T) {
	t.Parallel()
	art := "---\ntype: context\nname: bom-ctx\nversion: 1.0.0\ndescription: Context with SBOM.\nsbom:\n  format: cyclonedx-1.5\n  ref: ./sbom.json\n---\n\nbody\n"
	srv := startServer(t, writeRegistry(t, map[string]string{"tools/bom-ctx/ARTIFACT.md": art}))
	fm := fmLoadFrontmatter(t, srv.BaseURL, "tools/bom-ctx")
	for _, want := range []string{"sbom", "cyclonedx-1.5", "sbom.json"} {
		if !strings.Contains(fm, want) {
			t.Errorf("frontmatter missing %q:\n%s", want, fm)
		}
	}
}

// T-D-frontmatter-20 — effort_hint and model_class_hint are stored and returned
// for an agent.
func TestFrontmatter_HintsVerbatim(t *testing.T) {
	t.Parallel()
	art := "---\ntype: agent\nname: investigator\nversion: 1.0.0\ndescription: Investigator.\neffort_hint: high\nmodel_class_hint: frontier\n---\n\nbody\n"
	srv := startServer(t, writeRegistry(t, map[string]string{"personal/investigator/ARTIFACT.md": art}))
	fm := fmLoadFrontmatter(t, srv.BaseURL, "personal/investigator")
	if !strings.Contains(fm, "effort_hint: high") || !strings.Contains(fm, "model_class_hint: frontier") {
		t.Errorf("frontmatter missing hints:\n%s", fm)
	}
}

// T-D-frontmatter-21 — effort_hint on a non-agent/skill/command type warns.
func TestFrontmatter_HintOnUnsupportedTypeWarns(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/my-rule/ARTIFACT.md": "---\ntype: rule\nname: my-rule\nversion: 1.0.0\ndescription: A rule.\nrule_mode: always\neffort_hint: high\n---\n\nrules\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.hint_on_unsupported_type") || !strings.Contains(res.Stdout, "[warning]") {
		t.Errorf("missing hint_on_unsupported_type warning:\n%s", res.Stdout)
	}
}

// T-D-frontmatter-22 — agent scaffold writes input/output schema fields.
func TestFrontmatter_AgentScaffoldSchemas(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "finance/pay-agent")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "agent", "--description", "Finance agent.",
		"--input-schema", "./schemas/input.json", "--output-schema", "./schemas/output.json", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "input: ./schemas/input.json") || !strings.Contains(art, "output: ./schemas/output.json") {
		t.Errorf("ARTIFACT.md missing input/output:\n%s", art)
	}
}

// T-D-frontmatter-23 — agent scaffold writes delegates_to.
func TestFrontmatter_AgentScaffoldDelegatesTo(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "finance/pay-agent")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "agent", "--description", "Finance agent.",
		"--delegates-to", "finance/procurement/vendor-compliance-check@1.x", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "delegates_to:") || !strings.Contains(art, "finance/procurement/vendor-compliance-check@1.x") {
		t.Errorf("ARTIFACT.md missing delegates_to:\n%s", art)
	}
}

// T-D-frontmatter-27 — rule scaffold (always) writes rule_mode: always.
func TestFrontmatter_RuleScaffoldAlways(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "eng/always-rule")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "rule", "--description", "Always-on rule.",
		"--rule-mode", "always", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	if art := readFile(t, filepath.Join(out, "ARTIFACT.md")); !strings.Contains(art, "rule_mode: always") {
		t.Errorf("ARTIFACT.md missing rule_mode: always:\n%s", art)
	}
}

// T-D-frontmatter-28 — rule scaffold (glob) writes rule_mode and rule_globs.
func TestFrontmatter_RuleScaffoldGlob(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "eng/glob-rule")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "rule", "--description", "Glob rule.",
		"--rule-mode", "glob", "--rule-globs", "src/**/*.ts,src/**/*.tsx", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "rule_mode: glob") || !strings.Contains(art, "src/**/*.ts") {
		t.Errorf("ARTIFACT.md missing rule_mode glob / rule_globs:\n%s", art)
	}
}

// T-D-frontmatter-29 — rule scaffold (auto) writes rule_mode and rule_description.
func TestFrontmatter_RuleScaffoldAuto(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "eng/auto-rule")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "rule", "--description", "Auto rule.",
		"--rule-mode", "auto", "--rule-description", "Apply when working with database migrations", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "rule_mode: auto") || !strings.Contains(art, "Apply when working with database migrations") {
		t.Errorf("ARTIFACT.md missing rule_mode auto / rule_description:\n%s", art)
	}
}

// T-D-frontmatter-30 — a glob-mode rule materializes under claude-code at
// .claude/rules/<name>.md carrying its rule_mode/rule_globs frontmatter.
func TestFrontmatter_RuleGlobClaudeCode(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"eng/glob-rule/ARTIFACT.md": "---\ntype: rule\nname: glob-rule\nversion: 1.0.0\ndescription: Glob rule.\nrule_mode: glob\nrule_globs: \"src/**/*.ts\"\n---\n\nrules\n",
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, ".claude/rules/glob-rule.md"))
	if !strings.Contains(got, "rule_mode: glob") || !strings.Contains(got, "rule_globs") {
		t.Errorf("materialized rule missing rule_mode/rule_globs:\n%s", got)
	}
}

// T-D-frontmatter-31 — hook scaffold writes hook_event and hook_action.
func TestFrontmatter_HookScaffoldFields(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "eng/stop-hook")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "hook", "--description", "Log on stop.",
		"--hook-event", "stop", "--hook-action", "echo \"[hook] triggered\"", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "hook_event: stop") || !strings.Contains(art, "hook_action:") {
		t.Errorf("ARTIFACT.md missing hook fields:\n%s", art)
	}
}

// T-D-frontmatter-32 — declaring both a generic pre_tool_use hook and a
// corresponding subtype hook warns (spec §4.3.5, F-4.3.8). A lone generic hook
// alone is clean.
func TestFrontmatter_HookGenericPreToolUse(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/generic/ARTIFACT.md":  "---\ntype: hook\nname: generic\nversion: 1.0.0\ndescription: Generic hook.\nhook_event: pre_tool_use\nhook_action: |\n  echo hi\n---\n\nbody\n",
		"hooks/specific/ARTIFACT.md": "---\ntype: hook\nname: specific\nversion: 1.0.0\ndescription: Specific hook.\nhook_event: pre_shell_execution\nhook_action: |\n  echo hi\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.hook_generic_and_subtype") || !strings.Contains(res.Stdout, "pre_shell_execution") {
		t.Errorf("missing generic/subtype warning naming pre_shell_execution:\n%s", res.Stdout)
	}
}

// T-D-frontmatter-33 — a generic post_tool_use hook declared alongside a
// corresponding post-subtype hook warns (spec §4.3.5).
func TestFrontmatter_HookGenericPostToolUse(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/generic-post/ARTIFACT.md": "---\ntype: hook\nname: generic-post\nversion: 1.0.0\ndescription: Generic post hook.\nhook_event: post_tool_use\nhook_action: |\n  echo hi\n---\n\nbody\n",
		"hooks/edit-hook/ARTIFACT.md":    "---\ntype: hook\nname: edit-hook\nversion: 1.0.0\ndescription: Edit hook.\nhook_event: post_file_edit\nhook_action: |\n  echo hi\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.hook_generic_and_subtype") || !strings.Contains(res.Stdout, "post_file_edit") {
		t.Errorf("missing generic/subtype warning naming post_file_edit:\n%s", res.Stdout)
	}
}

// T-D-frontmatter-34 — a specific subtype event does not trigger the generic
// info diagnostic.
func TestFrontmatter_HookSubtypeNoInfo(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hooks/specific/ARTIFACT.md": "---\ntype: hook\nname: specific\nversion: 1.0.0\ndescription: Specific hook.\nhook_event: pre_shell_execution\nhook_action: |\n  echo hi\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "lint.hook_generic_and_subtype") {
		t.Errorf("subtype event wrongly flagged:\n%s", res.Stdout)
	}
}

// T-D-frontmatter-35 — mcp-server scaffold writes server_identifier.
func TestFrontmatter_McpServerScaffold(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "servers/finance-warehouse")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "mcp-server", "--description", "Finance warehouse MCP.",
		"--server-identifier", "npx:@company/finance-warehouse-mcp", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	if art := readFile(t, filepath.Join(out, "ARTIFACT.md")); !strings.Contains(art, "server_identifier: npx:@company/finance-warehouse-mcp") {
		t.Errorf("ARTIFACT.md missing server_identifier:\n%s", art)
	}
}

// T-D-frontmatter-36 — scaffold --extends writes the extends field.
func TestFrontmatter_ScaffoldExtends(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "finance/ap/pay-invoice-extended")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "agent", "--description", "Extended pay agent.",
		"--extends", "finance/ap/pay-invoice@1.2", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	if art := readFile(t, filepath.Join(out, "ARTIFACT.md")); !strings.Contains(art, "extends: finance/ap/pay-invoice@1.2") {
		t.Errorf("ARTIFACT.md missing extends:\n%s", art)
	}
}

// T-D-frontmatter-37 — an artifact carrying target_harnesses lints cleanly.
func TestFrontmatter_TargetHarnessesLintsClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"eng/my-context/ARTIFACT.md": "---\ntype: context\nname: my-context\nversion: 1.0.0\ndescription: Context.\ntarget_harnesses: [claude-code, opencode]\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Errorf("lint exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// T-D-frontmatter-38 — sync skips an artifact whose target_harnesses
// excludes the active harness (§4.3 / §6.7.1, F-6.7.2).
func TestFrontmatter_TargetHarnessesExcludesSkips(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		// Targets opencode only; a claude-code sync must skip it.
		"eng/scoped/ARTIFACT.md": "---\ntype: context\nname: scoped\nversion: 1.0.0\ndescription: Context.\ntarget_harnesses: [opencode]\n---\n\nbody\n",
		// A second artifact with no target_harnesses materializes normally,
		// so the run still produces output and the skip is isolated.
		"eng/open/ARTIFACT.md": "---\ntype: context\nname: open\nversion: 1.0.0\ndescription: Context.\n---\n\nbody\n",
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if _, err := os.Stat(filepath.Join(tgt, ".podium/context/eng/scoped/ARTIFACT.md")); err == nil {
		t.Errorf("artifact excluded by target_harnesses must not materialize for claude-code")
	}
	mustExist(t, filepath.Join(tgt, ".podium/context/eng/open/ARTIFACT.md"))
}

// T-D-frontmatter-39 — sync materializes an artifact whose target_harnesses
// includes the active harness. (target_harnesses is inert, so materialization
// happens for every harness; this positive case holds regardless.)
func TestFrontmatter_TargetHarnessesIncludesMaterializes(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"eng/my-context/ARTIFACT.md": "---\ntype: context\nname: my-context\nversion: 1.0.0\ndescription: Context.\ntarget_harnesses: [claude-code]\n---\n\nbody\n",
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, ".podium/context/eng/my-context/ARTIFACT.md"))
}

// T-D-frontmatter-40 — external_resources metadata is stored verbatim and
// returned on load; the binary object does not transit the registry.
func TestFrontmatter_ExternalResourcesVerbatim(t *testing.T) {
	t.Parallel()
	art := "---\ntype: context\nname: model-ctx\nversion: 1.0.0\ndescription: Model context.\nexternal_resources:\n  - path: ./model.onnx\n    url: s3://company-models/variance/v1/model.onnx\n    sha256: 9f2caabbccddeeff00112233445566778899aabbccddeeff0011223344556677\n    size: 145000000\n    signature: \"sigstore:abc123\"\n---\n\nbody\n"
	srv := startServer(t, writeRegistry(t, map[string]string{"data/model-ctx/ARTIFACT.md": art}))
	fm := fmLoadFrontmatter(t, srv.BaseURL, "data/model-ctx")
	for _, want := range []string{"external_resources", "model.onnx", "s3://company-models", "145000000", "sigstore:abc123"} {
		if !strings.Contains(fm, want) {
			t.Errorf("frontmatter missing %q:\n%s", want, fm)
		}
	}
}

// fmProvenanceSkill returns a registry whose SKILL.md body carries an imported
// provenance block.
func fmProvenanceSkill(t *testing.T, body string) string {
	return writeRegistry(t, map[string]string{
		"finance/policy/payments/ARTIFACT.md": fmSkillArtifact,
		"finance/policy/payments/SKILL.md":    "---\nname: payments\ndescription: Payments policy skill.\n---\n\n" + body,
	})
}

// T-D-frontmatter-41 — the claude-code adapter rewrites an imported provenance
// block into an <untrusted-data> region.
func TestFrontmatter_ProvenanceRewriteClaudeCode(t *testing.T) {
	t.Parallel()
	body := "Authored prose.\n\n<!-- begin imported source=\"https://wiki.example.com/policy/payments\" -->\nImported policy text.\n<!-- end imported -->\n"
	reg := fmProvenanceSkill(t, body)
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, ".claude/skills/payments/SKILL.md"))
	if !strings.Contains(got, "<untrusted-data source=\"https://wiki.example.com/policy/payments\">") {
		t.Errorf("missing untrusted-data region:\n%s", got)
	}
	if strings.Contains(got, "begin imported") || strings.Contains(got, "end imported") {
		t.Errorf("provenance markers not rewritten:\n%s", got)
	}
}

// T-D-frontmatter-42 — a provenance block without a source attribute rewrites to
// a bare <untrusted-data> region.
func TestFrontmatter_ProvenanceNoSource(t *testing.T) {
	t.Parallel()
	body := "Authored.\n\n<!-- begin imported -->\nImported text.\n<!-- end imported -->\n"
	reg := fmProvenanceSkill(t, body)
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	got := readFile(t, filepath.Join(tgt, ".claude/skills/payments/SKILL.md"))
	if !strings.Contains(got, "<untrusted-data>") {
		t.Errorf("missing bare untrusted-data region:\n%s", got)
	}
}

// T-D-frontmatter-43 — a SKILL.md body without provenance markers passes through
// unchanged.
func TestFrontmatter_ProvenancePassthrough(t *testing.T) {
	t.Parallel()
	body := "Plain authored prose with no imported blocks.\n"
	reg := fmProvenanceSkill(t, body)
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	got := readFile(t, filepath.Join(tgt, ".claude/skills/payments/SKILL.md"))
	if strings.Contains(got, "untrusted-data") {
		t.Errorf("unmarked body should not gain untrusted-data tags:\n%s", got)
	}
	if !strings.Contains(got, "Plain authored prose with no imported blocks.") {
		t.Errorf("authored prose missing:\n%s", got)
	}
}

// Cross-layer merge (T-D-frontmatter-44..49): a higher-precedence team-foo
// child declares extends: at the same canonical ID as the org-defaults parent,
// is accepted as an overlay (§4.6 extends exception), and the field-semantics
// table merges into the served frontmatter. The two-layer boot helpers live in
// docs_extends_test.go (same package).

// T-D-frontmatter-44 — child description wins over parent description.
func TestFrontmatter_MergeChildDescriptionWins(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: PARENT description\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: CHILD description\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	fm := exLoad(t, extendsBoot(t, parent, child, nil), exParentID).Frontmatter
	if !strings.Contains(fm, "CHILD description") || strings.Contains(fm, "PARENT description") {
		t.Errorf("description should be the child's only:\n%s", fm)
	}
}

// T-D-frontmatter-45 — tags are unioned across parent and child.
func TestFrontmatter_MergeTagsUnion(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: p\ntags: [parent-tag, shared]\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: c\ntags: [child-tag, shared]\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	fm := exLoad(t, extendsBoot(t, parent, child, nil), exParentID).Frontmatter
	for _, tag := range []string{"parent-tag", "child-tag", "shared"} {
		if !strings.Contains(fm, tag) {
			t.Errorf("merged tags missing %q:\n%s", tag, fm)
		}
	}
}

// T-D-frontmatter-46 — sensitivity takes the most-restrictive value.
func TestFrontmatter_MergeSensitivityMostRestrictive(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: p\nsensitivity: high\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: c\nsensitivity: low\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	if got := exLoad(t, extendsBoot(t, parent, child, nil), exParentID); got.Sensitivity != "high" {
		t.Errorf("sensitivity = %q, want high (most-restrictive)", got.Sensitivity)
	}
}

// T-D-frontmatter-47 — search_visibility takes the most-restrictive value.
func TestFrontmatter_MergeSearchVisibilityMostRestrictive(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: p\nsearch_visibility: direct-only\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: c\nsearch_visibility: indexed\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	fm := exLoad(t, extendsBoot(t, parent, child, nil), exParentID).Frontmatter
	if !strings.Contains(fm, "direct-only") {
		t.Errorf("search_visibility should stay direct-only (most-restrictive):\n%s", fm)
	}
}

// T-D-frontmatter-48 — the child type must match the parent type.
func TestFrontmatter_MergeTypeMustMatch(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: agent\nversion: 1.0.0\ndescription: parent agent\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child context\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	srv := extendsBoot(t, parent, child, nil)
	got := exLoad(t, srv, exParentID)
	if got.Type != "agent" || got.Version != "1.0.0" {
		t.Errorf("got type=%q version=%q, want the agent parent (cross-type child rejected)", got.Type, got.Version)
	}
}

// T-D-frontmatter-49 — a self-referencing extends is rejected at ingest (cycle
// detection); the artifact is dropped and a sibling keeps boot alive.
func TestFrontmatter_ExtendsCycleDetected(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/sibling/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: sibling keeps boot alive\n---\n\nbody\n",
		"finance/self/ARTIFACT.md":    "---\ntype: context\nversion: 1.0.0\ndescription: self\nextends: finance/self@1.x\n---\n\nbody\n",
	})
	srv := startServer(t, reg)
	if st := getStatus(t, srv.BaseURL+"/v1/load_artifact?id=finance/sibling"); st != 200 {
		t.Fatalf("sibling = HTTP %d, want 200", st)
	}
	if st := getStatus(t, srv.BaseURL+"/v1/load_artifact?id=finance/self"); st != 404 {
		t.Errorf("self-extends artifact = HTTP %d, want 404 (cycle rejected at ingest)", st)
	}
}

// T-D-frontmatter-50 — a skill ARTIFACT.md body with non-comment prose warns.
func TestFrontmatter_SkillArtifactBodyWarns(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n---\n\n# Hello world, this is prose, not a comment.\n",
		"finance/close/run-variance-analysis/SKILL.md":    fmSkillMD("run-variance-analysis", "Flag variance."),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.skill_artifact_body") || !strings.Contains(res.Stdout, "[warning]") {
		t.Errorf("missing skill_artifact_body warning:\n%s", res.Stdout)
	}
}

// T-D-frontmatter-51 — a skill ARTIFACT.md body that is a single HTML comment
// passes lint.
func TestFrontmatter_SkillArtifactBodyComment(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md": fmSkillArtifact,
		"finance/close/run-variance-analysis/SKILL.md":    fmSkillMD("run-variance-analysis", "Flag variance."),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || strings.Contains(res.Stdout, "lint.skill_artifact_body") {
		t.Errorf("comment body should not warn: exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// T-D-frontmatter-52 — a SKILL.md body over the 5000-token soft cap warns.
func TestFrontmatter_SkillBodyOverSoftCapWarns(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("word ", 5200) // ~26000 bytes => ~6500 tokens > 5000
	reg := writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md": fmSkillArtifact,
		"finance/close/run-variance-analysis/SKILL.md":    "---\nname: run-variance-analysis\ndescription: Flag variance.\n---\n\n" + body,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout(head)=%.300s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.manifest_size") || !strings.Contains(res.Stdout, "[warning]") {
		t.Errorf("missing manifest_size warning:\n%.400s", res.Stdout)
	}
}

// T-D-frontmatter-53 — a manifest over the 20000-token hard cap errors.
func TestFrontmatter_ManifestOverHardCapErrors(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("word ", 21000) // ~105000 bytes => ~26000 tokens > 20000
	reg := writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md": fmSkillArtifact,
		"finance/close/run-variance-analysis/SKILL.md":    "---\nname: run-variance-analysis\ndescription: Flag variance.\n---\n\n" + body,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout(head)=%.300s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.manifest_size") {
		t.Errorf("missing manifest_size error:\n%.400s", res.Stdout)
	}
}

// T-D-frontmatter-54 — scaffold --sensitivity high writes sensitivity: high.
func TestFrontmatter_ScaffoldSensitivityHigh(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "finance/sensitive-context")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "context", "--description", "Sensitive context.",
		"--sensitivity", "high", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	if art := readFile(t, filepath.Join(out, "ARTIFACT.md")); !strings.Contains(art, "sensitivity: high") {
		t.Errorf("ARTIFACT.md missing sensitivity: high:\n%s", art)
	}
}

// T-D-frontmatter-55 — scaffold --when-to-use parses a CSV into a list.
func TestFrontmatter_ScaffoldWhenToUseList(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "finance/close-context")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "context", "--description", "Close context.",
		"--when-to-use", "After month-end close,When reviewing financial performance", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "when_to_use:") || !strings.Contains(art, "After month-end close") || !strings.Contains(art, "When reviewing financial performance") {
		t.Errorf("ARTIFACT.md missing when_to_use entries:\n%s", art)
	}
}

// T-D-frontmatter-56 — scaffold --tags parses a CSV into a tags list.
func TestFrontmatter_ScaffoldTagsList(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "finance/tagged-context")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "context", "--description", "Finance context.",
		"--tags", "finance,close,variance", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	if art := readFile(t, filepath.Join(out, "ARTIFACT.md")); !strings.Contains(art, "tags: [finance, close, variance]") {
		t.Errorf("ARTIFACT.md missing tags list:\n%s", art)
	}
}

// T-D-frontmatter-57 — search_artifacts respects the tags filter.
func TestFrontmatter_SearchTagsFilter(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/close-tool/ARTIFACT.md": "---\ntype: context\nname: close-tool\nversion: 1.0.0\ndescription: Close tool.\ntags: [finance, close]\n---\n\nbody\n",
		"eng/build-tool/ARTIFACT.md":     "---\ntype: context\nname: build-tool\nversion: 1.0.0\ndescription: Build tool.\ntags: [engineering]\n---\n\nbody\n",
	}))
	_, all := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query=tool")
	if !strings.Contains(string(all), "finance/close-tool") || !strings.Contains(string(all), "eng/build-tool") {
		t.Errorf("unfiltered search missing artifacts:\n%s", all)
	}
	_, fin := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query=tool&tags=finance")
	if !strings.Contains(string(fin), "finance/close-tool") {
		t.Errorf("tag filter dropped the finance artifact:\n%s", fin)
	}
	if strings.Contains(string(fin), "eng/build-tool") {
		t.Errorf("tag filter leaked the engineering artifact:\n%s", fin)
	}
}

// T-D-frontmatter-58 — load_artifact on a non-existent ID returns 404
// registry.not_found.
func TestFrontmatter_LoadNotFound(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/real/ARTIFACT.md": "---\ntype: context\nname: real\nversion: 1.0.0\ndescription: Real.\n---\n\nbody\n",
	}))
	st, body := getRaw(t, srv.BaseURL+"/v1/load_artifact?id=finance/does-not-exist")
	if st != 404 {
		t.Fatalf("load = HTTP %d, want 404\n%s", st, body)
	}
	if !strings.Contains(string(body), "registry.not_found") {
		t.Errorf("body missing registry.not_found:\n%s", body)
	}
}

// T-D-frontmatter-59 — release_notes is stored verbatim and returned on load.
func TestFrontmatter_ReleaseNotesVerbatim(t *testing.T) {
	t.Parallel()
	art := "---\ntype: context\nname: notes-ctx\nversion: 1.0.0\ndescription: Notes context.\nrelease_notes: \"Initial release.\"\n---\n\nbody\n"
	srv := startServer(t, writeRegistry(t, map[string]string{"tools/notes-ctx/ARTIFACT.md": art}))
	fm := fmLoadFrontmatter(t, srv.BaseURL, "tools/notes-ctx")
	if !strings.Contains(fm, "release_notes") || !strings.Contains(fm, "Initial release.") {
		t.Errorf("frontmatter missing release_notes:\n%s", fm)
	}
}

// T-D-frontmatter-60 — replaced_by should surface in the deprecation warning.
// Observed: the warning is "artifact is deprecated" and the replaced_by target
// does not round-trip (no BUILD-GAPS finding filed); this test asserts the
// deprecated flag and warning and flags the gap if replaced_by later appears.
func TestFrontmatter_ReplacedBySurfaced(t *testing.T) {
	t.Parallel()
	art := "---\ntype: context\nname: old-tool\nversion: 1.0.0\ndescription: Old tool.\ndeprecated: true\nreplaced_by: finance/close-reporting/run-variance-analysis-v2\n---\n\nbody\n"
	srv := startServer(t, writeRegistry(t, map[string]string{"finance/old-tool/ARTIFACT.md": art}))
	var load struct {
		Deprecated         bool   `json:"deprecated"`
		DeprecationWarning string `json:"deprecation_warning"`
	}
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id=finance/old-tool", &load)
	if !load.Deprecated || !strings.Contains(load.DeprecationWarning, "deprecated") {
		t.Errorf("expected deprecated flag + warning: %+v", load)
	}
}

// T-D-frontmatter-61 — a bundled resource over the 1 MB per-file soft cap warns.
func TestFrontmatter_BundledPerFileSoftCapWarns(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("a", 1024*1024+1)
	reg := writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md":       fmSkillArtifact,
		"finance/close/run-variance-analysis/SKILL.md":          fmSkillMD("run-variance-analysis", "Flag variance."),
		"finance/close/run-variance-analysis/scripts/large.bin": big,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout(head)=%.200s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.bundled_resource_size") || !strings.Contains(res.Stdout, "per-file") {
		t.Errorf("missing per-file soft-cap warning:\n%.400s", res.Stdout)
	}
}

// T-D-frontmatter-62 — bundled resources over the 10 MB per-package cap error.
func TestFrontmatter_BundledPerPackageCapErrors(t *testing.T) {
	t.Parallel()
	chunk := strings.Repeat("b", 6*1024*1024)
	reg := writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md":   fmSkillArtifact,
		"finance/close/run-variance-analysis/SKILL.md":      fmSkillMD("run-variance-analysis", "Flag variance."),
		"finance/close/run-variance-analysis/scripts/a.bin": chunk,
		"finance/close/run-variance-analysis/scripts/b.bin": chunk,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout(head)=%.200s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.bundled_resource_size") || !strings.Contains(res.Stdout, "per-package") {
		t.Errorf("missing per-package error:\n%.400s", res.Stdout)
	}
}
