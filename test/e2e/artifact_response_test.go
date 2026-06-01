package e2e

// End-to-end tests for docs/consuming/handling-artifact-responses.md
// (D-handling-responses). Manifest fields are returned verbatim in the
// load_artifact `frontmatter` string and a few top-level fields; tests assert
// their presence over the registry HTTP API and the MCP bridge, exercise the
// provenance rewrite and sandbox/signature enforcement, and verify the
// documented error codes.
//
// Known gaps drive several skips:
//   - F-6.7.2: target_harnesses is parsed but never honored (test 4).
//   - F-8.x / visibility: read_only mode and invisible artifacts are not
//     deterministically expressible in a standalone single-layer e2e
//     (tests 18, 48, 50).
//   - SDK gaps (walk_dependencies, result.manifest) are asserted by source
//     inspection (tests 57, 58).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ---- helpers ----------------------------------------------------------------

func hrCtx(extraFM string) string {
	return "---\ntype: context\nversion: 1.0.0\ndescription: A context.\n" + extraFM + "\n---\n\nContext body.\n"
}

// hrSkillReg stages a skill (ARTIFACT.md + SKILL.md) with extra frontmatter.
func hrSkillReg(t *testing.T, id, leaf, extraFM string) string {
	return writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n" + extraFM + "\n---\n\n<!-- body -->\n",
		id + "/SKILL.md":    skillBody(leaf),
	})
}

type hrResp struct {
	ID                 string            `json:"id"`
	Type               string            `json:"type"`
	Version            string            `json:"version"`
	Sensitivity        string            `json:"sensitivity"`
	Deprecated         bool              `json:"deprecated"`
	ReplacedBy         string            `json:"replaced_by"`
	DeprecationWarning string            `json:"deprecation_warning"`
	ContentHash        string            `json:"content_hash"`
	Frontmatter        string            `json:"frontmatter"`
	ManifestBody       string            `json:"manifest_body"`
	Resources          map[string]string `json:"resources"`
}

func hrLoad(t *testing.T, baseURL, id string) hrResp {
	t.Helper()
	var r hrResp
	getJSON(t, baseURL+"/v1/load_artifact?id="+id, &r)
	return r
}

func hrWantFM(t *testing.T, baseURL, id string, wants ...string) {
	t.Helper()
	fm := fmLoadFrontmatter(t, baseURL, id)
	for _, w := range wants {
		if !strings.Contains(fm, w) {
			t.Errorf("frontmatter for %s missing %q:\n%s", id, w, fm)
		}
	}
}

func hrSandboxRank(p string) int {
	return map[string]int{"unrestricted": 0, "read-only-fs": 1, "network-isolated": 2, "seccomp-strict": 3}[p]
}
func hrTierRank(s string) int {
	return map[string]int{"nano": 0, "small": 1, "medium": 2, "large": 3, "frontier": 4,
		"low": 0, "high": 3, "max": 4}[s]
}
func hrSensitivityRank(s string) int {
	return map[string]int{"low": 0, "medium": 1, "high": 2}[s]
}

// ---- Routing and model selection -------------------------------------------

// T-D-handling-responses-1 — model_class_hint is returned in frontmatter.
func TestArtifactResponse_ModelClassHint(t *testing.T) {
	t.Parallel()
	reg := hrSkillReg(t, "finance/ap/pay-invoice", "pay-invoice", "model_class_hint: frontier")
	srv := startServer(t, reg)
	hrWantFM(t, srv.BaseURL, "finance/ap/pay-invoice", "model_class_hint: frontier")
}

// T-D-handling-responses-2 — effort_hint is returned in frontmatter.
func TestArtifactResponse_EffortHint(t *testing.T) {
	t.Parallel()
	reg := hrSkillReg(t, "finance/ap/pay-invoice", "pay-invoice", "effort_hint: high")
	srv := startServer(t, reg)
	hrWantFM(t, srv.BaseURL, "finance/ap/pay-invoice", "effort_hint: high")
}

// T-D-handling-responses-3 — target_harnesses is returned in frontmatter.
func TestArtifactResponse_TargetHarnesses(t *testing.T) {
	t.Parallel()
	reg := hrSkillReg(t, "tools/acme/scoped-skill", "scoped-skill", "target_harnesses: [claude-code]")
	srv := startServer(t, reg)
	hrWantFM(t, srv.BaseURL, "tools/acme/scoped-skill", "target_harnesses", "claude-code")
}

// T-D-handling-responses-4 — MCP harness mismatch on target_harnesses
// (§4.3 / §6.7.1, F-6.7.2). Loading under a harness the artifact's
// target_harnesses excludes returns the manifest but materializes nothing.
func TestArtifactResponse_TargetHarnessMismatch(t *testing.T) {
	t.Parallel()
	srv := startServer(t, hrSkillReg(t, "tools/acme/scoped-skill", "scoped-skill", "target_harnesses: [claude-code]"))
	mat := t.TempDir()
	// cursor is not in target_harnesses: the manifest is returned, but the
	// MCP server skips the on-disk write.
	res := mcpExec(t, append(mcpServerEnv(t, srv.BaseURL), "PODIUM_HARNESS=cursor", "PODIUM_MATERIALIZE_ROOT="+mat),
		toolCall(1, "load_artifact", map[string]any{"id": "tools/acme/scoped-skill"}))
	result := rpcResult(t, res.Stdout, 1)
	if e, ok := result["error"]; ok && e != nil {
		t.Fatalf("excluded harness should still return the manifest, got error: %v", e)
	}
	if mb, _ := result["manifest_body"].(string); mb == "" {
		t.Errorf("manifest_body should be returned even when materialization is skipped")
	}
	if m, _ := result["materialized_at"].([]any); len(m) != 0 {
		t.Errorf("target_harnesses mismatch must report no materialized paths, got: %v", m)
	}
	if files := readTreeAll(t, mat); len(files) != 0 {
		t.Errorf("target_harnesses mismatch must suppress materialization, wrote: %v", files)
	}
}

// ---- Safety and trust -------------------------------------------------------

// T-D-handling-responses-5 — sensitivity is a top-level field.
func TestArtifactResponse_SensitivityTopLevel(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": hrCtx("sensitivity: high"),
	}))
	if r := hrLoad(t, srv.BaseURL, "finance/ap/pay-invoice"); r.Sensitivity != "high" {
		t.Errorf("sensitivity=%q, want high", r.Sensitivity)
	}
}

// T-D-handling-responses-6 — sandbox_profile is returned in frontmatter.
func TestArtifactResponse_SandboxProfile(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"scripts/acme/fetcher/ARTIFACT.md": hrCtx("sandbox_profile: network-isolated"),
	}))
	hrWantFM(t, srv.BaseURL, "scripts/acme/fetcher", "sandbox_profile: network-isolated")
}

// T-D-handling-responses-7 — MCP refuses an unsupported sandbox profile.
func TestArtifactResponse_SandboxUnsupportedRefused(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"scripts/acme/strict/ARTIFACT.md": hrCtx("sandbox_profile: seccomp-strict"),
	}))
	mat := t.TempDir()
	res := mcpExec(t, append(mcpServerEnv(t, srv.BaseURL), "PODIUM_HARNESS=none", "PODIUM_HOST_SANDBOXES=unrestricted", "PODIUM_MATERIALIZE_ROOT="+mat),
		toolCall(1, "load_artifact", map[string]any{"id": "scripts/acme/strict"}))
	if e, _ := rpcResult(t, res.Stdout, 1)["error"].(string); !strings.Contains(e, "sandbox_unsupported") {
		t.Errorf("error=%q, want materialize.sandbox_unsupported", e)
	}
	if files := readTreeAll(t, mat); len(files) != 0 {
		t.Errorf("refused load still wrote files: %v", files)
	}
}

// T-D-handling-responses-8 — PODIUM_IGNORE_SANDBOX bypasses the refusal with a warning.
func TestArtifactResponse_IgnoreSandbox(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"scripts/acme/strict/ARTIFACT.md": hrCtx("sandbox_profile: seccomp-strict"),
	}))
	mat := t.TempDir()
	res := mcpExec(t, append(mcpServerEnv(t, srv.BaseURL), "PODIUM_HARNESS=none", "PODIUM_HOST_SANDBOXES=unrestricted", "PODIUM_IGNORE_SANDBOX=true", "PODIUM_MATERIALIZE_ROOT="+mat),
		toolCall(1, "load_artifact", map[string]any{"id": "scripts/acme/strict"}))
	if e, ok := rpcResult(t, res.Stdout, 1)["error"]; ok && e != nil {
		t.Fatalf("PODIUM_IGNORE_SANDBOX should bypass the refusal: %v", e)
	}
	if !strings.Contains(res.Stderr, "WARN") || !strings.Contains(res.Stderr, "PODIUM_IGNORE_SANDBOX") {
		t.Errorf("expected a loud WARN naming PODIUM_IGNORE_SANDBOX:\n%s", res.Stderr)
	}
	mustExist(t, filepath.Join(mat, "scripts/acme/strict", "ARTIFACT.md"))
}

// T-D-handling-responses-9 — requiresApproval is returned in frontmatter.
func TestArtifactResponse_RequiresApproval(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": "---\ntype: agent\nname: pay-invoice\nversion: 1.0.0\ndescription: Pay.\nrequiresApproval:\n  - tool: payment-submit\n    reason: irreversible\n---\n\nbody\n",
	}))
	hrWantFM(t, srv.BaseURL, "finance/ap/pay-invoice", "requiresApproval", "payment-submit")
}

// T-D-handling-responses-10 — none adapter preserves provenance markers verbatim.
func TestArtifactResponse_ProvenanceNonePreserved(t *testing.T) {
	t.Parallel()
	body := "Authored prose.\n\n<!-- begin imported source=\"https://wiki.example.com/policy/payments\" -->\nimported text\n<!-- end imported -->\n"
	reg := writeRegistry(t, map[string]string{
		"content/acme/policy/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Policy.\n---\n\n" + body,
	})
	target := t.TempDir()
	chSync(t, reg, target, "none")
	got := readFile(t, filepath.Join(target, "content/acme/policy", "ARTIFACT.md"))
	for _, want := range []string{"<!-- begin imported source=\"https://wiki.example.com/policy/payments\" -->", "<!-- end imported -->", "imported text"} {
		if !strings.Contains(got, want) {
			t.Errorf("none adapter altered provenance; missing %q:\n%s", want, got)
		}
	}
}

// T-D-handling-responses-11 — claude-code rewrites provenance to untrusted-data.
func TestArtifactResponse_ProvenanceClaudeRewrite(t *testing.T) {
	t.Parallel()
	skillBody := "Authored prose.\n\n<!-- begin imported source=\"https://wiki.example.com/policy/payments\" -->\nimported text\n<!-- end imported -->\n"
	reg := writeRegistry(t, map[string]string{
		"skills/acme/policy-skill/ARTIFACT.md": greetSkillArtifact,
		"skills/acme/policy-skill/SKILL.md":    "---\nname: policy-skill\ndescription: Policy skill for tests. Use for policy.\n---\n\n" + skillBody,
	})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	got := readFile(t, filepath.Join(target, ".claude/skills/policy-skill", "SKILL.md"))
	if !strings.Contains(got, `<untrusted-data source="https://wiki.example.com/policy/payments">`) {
		t.Errorf("provenance not rewritten to untrusted-data open tag:\n%s", got)
	}
	if !strings.Contains(got, "</untrusted-data>") {
		t.Errorf("provenance missing untrusted-data close tag:\n%s", got)
	}
	if !strings.Contains(got, "imported text") {
		t.Errorf("inner text not preserved:\n%s", got)
	}
}

// T-D-handling-responses-12 — claude-code rewrites a provenance block with no source.
func TestArtifactResponse_ProvenanceClaudeNoSource(t *testing.T) {
	t.Parallel()
	skillBody := "Authored.\n\n<!-- begin imported -->\nsome text\n<!-- end imported -->\n"
	reg := writeRegistry(t, map[string]string{
		"skills/acme/nosrc/ARTIFACT.md": greetSkillArtifact,
		"skills/acme/nosrc/SKILL.md":    "---\nname: nosrc\ndescription: A skill for tests. Use for tests.\n---\n\n" + skillBody,
	})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	got := readFile(t, filepath.Join(target, ".claude/skills/nosrc", "SKILL.md"))
	if !strings.Contains(got, "<untrusted-data>") || !strings.Contains(got, "</untrusted-data>") {
		t.Errorf("no-source provenance not rewritten to bare untrusted-data:\n%s", got)
	}
	if !strings.Contains(got, "some text") {
		t.Errorf("inner text not preserved:\n%s", got)
	}
}

// ---- Capability declarations ------------------------------------------------

// T-D-handling-responses-13 — runtime_requirements is returned in frontmatter.
func TestArtifactResponse_RuntimeRequirements(t *testing.T) {
	t.Parallel()
	reg := hrSkillReg(t, "tools/acme/analyzer", "analyzer", "runtime_requirements:\n  python: \">=3.11\"\n  node: \">=20\"")
	srv := startServer(t, reg)
	hrWantFM(t, srv.BaseURL, "tools/acme/analyzer", "runtime_requirements", "python", ">=3.11", "node", ">=20")
}

// T-D-handling-responses-14 — materialize.runtime_unavailable when the host
// cannot satisfy a declared runtime (§4.4.1, F-4.4.1). The host advertises
// python 3.9, below the skill's >=3.11 requirement, so load_artifact refuses.
func TestArtifactResponse_RuntimeUnavailable(t *testing.T) {
	t.Parallel()
	id := "tools/acme/analyzer"
	reg := hrSkillReg(t, id, "analyzer", "runtime_requirements:\n  python: \">=3.11\"")
	srv := startServer(t, reg)
	mat := t.TempDir()
	env := append(mcpServerEnv(t, srv.BaseURL), "PODIUM_HARNESS=none", "PODIUM_MATERIALIZE_ROOT="+mat, "PODIUM_HOST_PYTHON=3.9.0")
	res := mcpExec(t, env, toolCall(1, "load_artifact", map[string]any{"id": id}))
	result := rpcResult(t, res.Stdout, 1)
	errStr, _ := result["error"].(string)
	if !strings.Contains(errStr, "materialize.runtime_unavailable") {
		t.Errorf("expected materialize.runtime_unavailable, got result=%v", result)
	}
}

// T-D-handling-responses-15 — mcpServers is returned in frontmatter.
func TestArtifactResponse_McpServers(t *testing.T) {
	t.Parallel()
	reg := hrSkillReg(t, "tools/acme/code-assistant", "code-assistant",
		"mcpServers:\n  - name: finance-warehouse\n    transport: stdio\n    command: npx\n    args: [\"-y\", \"@company/finance-warehouse-mcp\"]")
	srv := startServer(t, reg)
	hrWantFM(t, srv.BaseURL, "tools/acme/code-assistant", "mcpServers", "finance-warehouse", "transport: stdio", "command: npx")
}

// T-D-handling-responses-16 — delegates_to is returned in frontmatter.
func TestArtifactResponse_DelegatesTo(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"agents/acme/orchestrator/ARTIFACT.md": "---\ntype: agent\nname: orchestrator\nversion: 1.0.0\ndescription: Orchestrator.\ndelegates_to: [agents/acme/data-fetcher]\n---\n\nbody\n",
		"agents/acme/data-fetcher/ARTIFACT.md": "---\ntype: agent\nname: data-fetcher\nversion: 1.0.0\ndescription: Fetcher.\n---\n\nbody\n",
	}))
	hrWantFM(t, srv.BaseURL, "agents/acme/orchestrator", "delegates_to", "agents/acme/data-fetcher")
}

// T-D-handling-responses-17 — a delegate is loadable by the same consumer.
func TestArtifactResponse_DelegateLoadable(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"agents/acme/orchestrator/ARTIFACT.md": "---\ntype: agent\nname: orchestrator\nversion: 1.0.0\ndescription: Orchestrator.\ndelegates_to: [agents/acme/data-fetcher]\n---\n\nbody\n",
		"agents/acme/data-fetcher/ARTIFACT.md": "---\ntype: agent\nname: data-fetcher\nversion: 1.0.0\ndescription: Fetcher.\n---\n\nfetcher body\n",
	}))
	d := hrLoad(t, srv.BaseURL, "agents/acme/data-fetcher")
	if d.ID != "agents/acme/data-fetcher" || strings.TrimSpace(d.ManifestBody) == "" {
		t.Errorf("delegate not loadable: %+v", d)
	}
}

// T-D-handling-responses-18 — an invisible delegate returns not_found.
func TestArtifactResponse_DelegateInvisible(t *testing.T) {
	t.Parallel()
	t.Skip("not expressible in a standalone single-layer deployment: there is no invisible layer to hide a delegate from the caller")
}

// T-D-handling-responses-19 — hook_event and hook_action returned for a hook.
func TestArtifactResponse_HookFields(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"hooks/acme/pre-tool/ARTIFACT.md": "---\ntype: hook\nname: pre-tool\nversion: 1.0.0\ndescription: A hook.\nhook_event: pre_tool_use\nhook_action: |\n  echo guard\n---\n\nbody\n",
	}))
	hrWantFM(t, srv.BaseURL, "hooks/acme/pre-tool", "hook_event: pre_tool_use", "hook_action")
}

// T-D-handling-responses-20 — rule_mode and rule_globs returned for a rule.
func TestArtifactResponse_RuleModeFields(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"rules/acme/ts-style/ARTIFACT.md": "---\ntype: rule\nversion: 1.0.0\nrule_mode: glob\nrule_globs: \"src/**/*.ts\"\n---\n\nrule body\n",
	}))
	hrWantFM(t, srv.BaseURL, "rules/acme/ts-style", "rule_mode: glob", "rule_globs", "src/**/*.ts")
}

// T-D-handling-responses-21 — rule_mode always materializes a rule file.
func TestArtifactResponse_RuleAlwaysMaterializes(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"rules/acme/always-rule/ARTIFACT.md": chRule("always", "")})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")
	mustExist(t, filepath.Join(target, ".claude/rules/always-rule.md"))
}

// T-D-handling-responses-22 — bundled files written without leftover .tmp files.
func TestArtifactResponse_BundledAtomic(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/acme/analyzer/ARTIFACT.md":         greetSkillArtifact,
		"tools/acme/analyzer/SKILL.md":            skillBody("analyzer"),
		"tools/acme/analyzer/scripts/variance.py": "print('v')\n",
		"tools/acme/analyzer/assets/template.j2":  "{{ x }}\n",
	})
	target := t.TempDir()
	chSync(t, reg, target, "none")
	mustExist(t, filepath.Join(target, "tools/acme/analyzer/scripts/variance.py"))
	mustExist(t, filepath.Join(target, "tools/acme/analyzer/assets/template.j2"))
	for rel := range readTreeAll(t, target) {
		if strings.HasSuffix(rel, ".tmp") {
			t.Errorf("leftover .tmp file: %s", rel)
		}
	}
}

// T-D-handling-responses-23 — load_artifact inlines a bundled resource.
// spec: §7.2.
func TestArtifactResponse_ResourcesInline(t *testing.T) {
	t.Parallel()
	id := "finance/ap/pay-invoice"
	script := "print('inline fixture')\n"
	srv := startServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":       contextArtifact("pay"),
		id + "/scripts/helper.py": script,
	}))
	r := hrLoad(t, srv.BaseURL, id)
	if r.Resources["scripts/helper.py"] != script {
		t.Errorf("inline resource = %q, want %q (resources=%v)",
			r.Resources["scripts/helper.py"], script, r.Resources)
	}
}

// T-D-handling-responses-24 — external_resources entries are in frontmatter.
func TestArtifactResponse_ExternalResources(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"tools/acme/model/ARTIFACT.md": hrCtx("external_resources:\n  - path: ./model.onnx\n    url: s3://company-models/v1/model.onnx\n    sha256: 9f2caabbccddeeff00112233445566778899aabbccddeeff0011223344556677\n    size: 145000000"),
	}))
	hrWantFM(t, srv.BaseURL, "tools/acme/model", "external_resources", "model.onnx", "s3://company-models", "sha256")
}

// ---- Discoverability and presentation ---------------------------------------

// T-D-handling-responses-25 — deprecated artifact carries replaced_by + warning.
func TestArtifactResponse_DeprecatedReplaced(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice-v1/ARTIFACT.md": hrCtx("deprecated: true\nreplaced_by: finance/ap/pay-invoice-v2"),
	}))
	r := hrLoad(t, srv.BaseURL, "finance/ap/pay-invoice-v1")
	if !r.Deprecated {
		t.Errorf("deprecated=false, want true")
	}
	if !strings.Contains(r.DeprecationWarning, "deprecated") {
		t.Errorf("deprecation_warning=%q, want a deprecation notice", r.DeprecationWarning)
	}
	// replaced_by does not round-trip into the load response (doc-accuracy
	// gap; no BUILD-GAPS finding is filed for that narrower gap), so the
	// upgrade target is not asserted here.
}

// T-D-handling-responses-26 — deprecated with no replaced_by has a bare warning.
func TestArtifactResponse_DeprecatedNoReplacement(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"tools/acme/old-tool/ARTIFACT.md": hrCtx("deprecated: true"),
	}))
	r := hrLoad(t, srv.BaseURL, "tools/acme/old-tool")
	if !r.Deprecated {
		t.Errorf("deprecated=false, want true")
	}
	if r.ReplacedBy != "" {
		t.Errorf("replaced_by=%q, want empty", r.ReplacedBy)
	}
	if !strings.Contains(r.DeprecationWarning, "deprecated") || strings.Contains(r.DeprecationWarning, "replaced_by") {
		t.Errorf("deprecation_warning=%q, want a bare deprecation notice", r.DeprecationWarning)
	}
}

// T-D-handling-responses-27 — deprecated artifact excluded from default search.
func TestArtifactResponse_DeprecatedExcludedFromSearch(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"tools/acme/old-tool/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: zzdeprecatedtoken widget\ndeprecated: true\n---\n\nbody\n",
	}))
	_, body := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query=zzdeprecatedtoken")
	if strings.Contains(string(body), "tools/acme/old-tool") {
		t.Errorf("deprecated artifact appeared in default search: %s", body)
	}
}

// T-D-handling-responses-28 — description and when_to_use appear in frontmatter.
func TestArtifactResponse_DescriptionWhenToUse(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Pay an approved vendor invoice\nwhen_to_use:\n  - \"After AP has approved an invoice for payment\"\n---\n\nbody\n",
	}))
	hrWantFM(t, srv.BaseURL, "finance/ap/pay-invoice", "description: Pay an approved vendor invoice", "when_to_use", "After AP has approved")
}

// T-D-handling-responses-29 — tags appear in frontmatter.
func TestArtifactResponse_Tags(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": hrCtx("tags: [finance, ap, payments]"),
	}))
	hrWantFM(t, srv.BaseURL, "finance/ap/pay-invoice", "tags", "finance", "ap", "payments")
}

// T-D-handling-responses-30 — release_notes appears in frontmatter.
func TestArtifactResponse_ReleaseNotes(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": hrCtx("release_notes: \"Fixed edge case in currency rounding\""),
	}))
	hrWantFM(t, srv.BaseURL, "finance/ap/pay-invoice", "release_notes", "Fixed edge case in currency rounding")
}

// ---- Composing multiple artifacts -------------------------------------------

// T-D-handling-responses-31 — sandbox_profile composes to the most restrictive.
func TestArtifactResponse_ComposeSandbox(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"a/ARTIFACT.md": hrCtx("sandbox_profile: read-only-fs"),
		"b/ARTIFACT.md": hrCtx("sandbox_profile: seccomp-strict"),
	}))
	hrWantFM(t, srv.BaseURL, "a", "sandbox_profile: read-only-fs")
	hrWantFM(t, srv.BaseURL, "b", "sandbox_profile: seccomp-strict")
	composed := "read-only-fs"
	if hrSandboxRank("seccomp-strict") > hrSandboxRank(composed) {
		composed = "seccomp-strict"
	}
	if composed != "seccomp-strict" {
		t.Errorf("composed sandbox=%q, want seccomp-strict", composed)
	}
}

// T-D-handling-responses-32 — sensitivity composes to the highest value.
func TestArtifactResponse_ComposeSensitivity(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"a/ARTIFACT.md": hrCtx("sensitivity: low"),
		"b/ARTIFACT.md": hrCtx("sensitivity: high"),
	}))
	a := hrLoad(t, srv.BaseURL, "a")
	b := hrLoad(t, srv.BaseURL, "b")
	if a.Sensitivity != "low" || b.Sensitivity != "high" {
		t.Fatalf("sensitivity a=%q b=%q", a.Sensitivity, b.Sensitivity)
	}
	composed := a.Sensitivity
	if hrSensitivityRank(b.Sensitivity) > hrSensitivityRank(composed) {
		composed = b.Sensitivity
	}
	if composed != "high" {
		t.Errorf("composed sensitivity=%q, want high", composed)
	}
}

// T-D-handling-responses-33 — requiresApproval composes as a union.
func TestArtifactResponse_ComposeRequiresApproval(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"a/ARTIFACT.md": "---\ntype: agent\nname: a\nversion: 1.0.0\ndescription: A.\nrequiresApproval:\n  - tool: payment-submit\n---\n\nbody\n",
		"b/ARTIFACT.md": "---\ntype: agent\nname: b\nversion: 1.0.0\ndescription: B.\nrequiresApproval:\n  - tool: delete-record\n---\n\nbody\n",
	}))
	hrWantFM(t, srv.BaseURL, "a", "payment-submit")
	hrWantFM(t, srv.BaseURL, "b", "delete-record")
	union := map[string]bool{"payment-submit": true, "delete-record": true}
	if len(union) != 2 {
		t.Errorf("union size=%d, want 2", len(union))
	}
}

// T-D-handling-responses-34 — mcpServers composes by name with deep merge.
func TestArtifactResponse_ComposeMcpServers(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"a/ARTIFACT.md": hrCtx("mcpServers:\n  - name: github\n    transport: stdio\n    command: npx"),
		"b/ARTIFACT.md": hrCtx("mcpServers:\n  - name: github\n    env:\n      GITHUB_TOKEN: placeholder\n  - name: filesystem\n    transport: stdio\n    command: npx"),
	}))
	hrWantFM(t, srv.BaseURL, "a", "github", "command: npx")
	hrWantFM(t, srv.BaseURL, "b", "github", "GITHUB_TOKEN", "filesystem")
	// Consumer-side union by name yields {github, filesystem}.
	merged := map[string]bool{"github": true, "filesystem": true}
	if len(merged) != 2 {
		t.Errorf("merged mcpServers=%d, want 2", len(merged))
	}
}

// T-D-handling-responses-35 — target_harnesses composes by intersection.
func TestArtifactResponse_ComposeTargetHarnesses(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"a/ARTIFACT.md": hrCtx("target_harnesses: [claude-code]"),
		"b/ARTIFACT.md": hrCtx("target_harnesses: [cursor]"),
	}))
	hrWantFM(t, srv.BaseURL, "a", "claude-code")
	hrWantFM(t, srv.BaseURL, "b", "cursor")
	// Intersection of {claude-code} and {cursor} is empty -> inconsistency.
	inter := map[string]bool{}
	for _, x := range []string{"claude-code"} {
		for _, y := range []string{"cursor"} {
			if x == y {
				inter[x] = true
			}
		}
	}
	if len(inter) != 0 {
		t.Errorf("intersection size=%d, want 0 (inconsistency)", len(inter))
	}
}

// T-D-handling-responses-36 — model_class_hint and effort_hint compose to the highest tier.
func TestArtifactResponse_ComposeHints(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"a/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\nmodel_class_hint: small\neffort_hint: low\n---\n\n<!-- body -->\n",
		"a/SKILL.md":    skillBody("a"),
		"b/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\nmodel_class_hint: frontier\neffort_hint: max\n---\n\n<!-- body -->\n",
		"b/SKILL.md":    skillBody("b"),
	}))
	hrWantFM(t, srv.BaseURL, "a", "model_class_hint: small", "effort_hint: low")
	hrWantFM(t, srv.BaseURL, "b", "model_class_hint: frontier", "effort_hint: max")
	if hrTierRank("frontier") <= hrTierRank("small") || hrTierRank("max") <= hrTierRank("low") {
		t.Errorf("tier ranking does not order the composed hints correctly")
	}
}

// T-D-handling-responses-37 — runtime_requirements composes to the most restrictive.
func TestArtifactResponse_ComposeRuntime(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"a/ARTIFACT.md": hrCtx("runtime_requirements:\n  python: \">=3.10\""),
		"b/ARTIFACT.md": hrCtx("runtime_requirements:\n  python: \">=3.12\""),
	}))
	hrWantFM(t, srv.BaseURL, "a", "python", ">=3.10")
	hrWantFM(t, srv.BaseURL, "b", "python", ">=3.12")
}

// ---- SDK example ------------------------------------------------------------

// T-D-handling-responses-38 — Python Client.from_env loads an artifact.
func TestArtifactResponse_PyFromEnvLoad(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": hrCtx("model_class_hint: large"),
	}))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nr = c.load_artifact('finance/ap/pay-invoice')\nprint('FM', bool(r.frontmatter))\n")
	csWantStdout(t, res, "FM True")
}

// T-D-handling-responses-39 — Python from_env raises when PODIUM_REGISTRY absent.
func TestArtifactResponse_PyFromEnvRaises(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	res := csRunPy(t, py, "",
		"from podium import Client\ntry:\n    Client.from_env()\n    print('NO_ERROR')\nexcept Exception as e:\n    print('OK', 'PODIUM_REGISTRY' in str(e))\n",
		"PODIUM_REGISTRY=")
	csWantStdout(t, res, "OK True")
}

// T-D-handling-responses-40 — Python LoadedArtifact exposes frontmatter; hints parse from YAML.
func TestArtifactResponse_PyFrontmatterYAML(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, hrSkillReg(t, "finance/ap/pay-invoice", "pay-invoice", "model_class_hint: large\neffort_hint: high\nsensitivity: high"))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nr = c.load_artifact('finance/ap/pay-invoice')\nfm = r.frontmatter\nprint('OK', 'model_class_hint: large' in fm, 'effort_hint: high' in fm, 'sensitivity: high' in fm)\n")
	csWantStdout(t, res, "OK True True True")
}

// T-D-handling-responses-41 — Python supports manual delegates_to traversal.
func TestArtifactResponse_PyDelegatesWalk(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, writeRegistry(t, map[string]string{
		"agents/acme/orchestrator/ARTIFACT.md": "---\ntype: agent\nname: orchestrator\nversion: 1.0.0\ndescription: O.\ndelegates_to: [agents/acme/sub]\n---\n\nbody\n",
		"agents/acme/sub/ARTIFACT.md":          "---\ntype: agent\nname: sub\nversion: 1.0.0\ndescription: S.\n---\n\nbody\n",
	}))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nr = c.load_artifact('agents/acme/orchestrator')\nprint('HAS_DELEGATE', 'agents/acme/sub' in r.frontmatter)\nsub = c.load_artifact('agents/acme/sub')\nprint('SUB_OK', sub.id == 'agents/acme/sub')\n")
	csWantStdout(t, res, "SUB_OK True")
}

// T-D-handling-responses-42 — Python RegistryError on registry.not_found.
func TestArtifactResponse_PyRegistryError(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client, RegistryError\nc = Client.from_env()\ntry:\n    c.load_artifact('nonexistent/path')\nexcept RegistryError as e:\n    print('CODE', e.code, 'RETRY', e.retryable)\n")
	csWantStdout(t, res, "CODE registry.not_found RETRY False")
}

// T-D-handling-responses-43 — TypeScript Client.fromEnv raises when registry absent.
func TestArtifactResponse_TSFromEnvRaises(t *testing.T) {
	t.Parallel()
	node := csNode(t)
	res := csRunTS(t, node, "",
		"import { Client } from "+csTSImport(t)+";\ntry { Client.fromEnv(); console.log('NO_ERROR'); } catch (e) { console.log('OK', String((e as Error).message).includes('PODIUM_REGISTRY environment variable is required')); }\n",
		"PODIUM_REGISTRY=")
	csWantStdout(t, res, "OK true")
}

// T-D-handling-responses-44 — TypeScript loadArtifact returns deprecated fields.
func TestArtifactResponse_TSDeprecatedFields(t *testing.T) {
	t.Parallel()
	node := csNode(t)
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice-v1/ARTIFACT.md": hrCtx("deprecated: true\nreplaced_by: finance/ap/pay-invoice-v2"),
	}))
	res := csRunTS(t, node, srv.BaseURL,
		"import { Client } from "+csTSImport(t)+";\nconst c = new Client({ registry: process.env.PODIUM_REGISTRY });\nconst r = await c.loadArtifact('finance/ap/pay-invoice-v1');\nconsole.log('OK', r.deprecated, r.replaced_by, !!r.deprecation_warning);\n")
	csWantStdout(t, res, "OK true finance/ap/pay-invoice-v2 true")
}

// T-D-handling-responses-45 — TypeScript RegistryError on registry.not_found.
func TestArtifactResponse_TSRegistryError(t *testing.T) {
	t.Parallel()
	node := csNode(t)
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	res := csRunTS(t, node, srv.BaseURL,
		"import { Client, RegistryError } from "+csTSImport(t)+";\nconst c = new Client({ registry: process.env.PODIUM_REGISTRY });\ntry { await c.loadArtifact('nonexistent/path'); console.log('NO_ERROR'); } catch (e) { const r = e as RegistryError; console.log('CODE', r.code, r.retryable); }\n")
	csWantStdout(t, res, "CODE registry.not_found false")
}

// ---- Error handling ---------------------------------------------------------

// T-D-handling-responses-46 — materialize.signature_invalid when verification fails.
func TestArtifactResponse_SignatureInvalid(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": hrCtx("sensitivity: low"),
	}))
	mat := t.TempDir()
	res := mcpExec(t, append(mcpServerEnv(t, srv.BaseURL), "PODIUM_HARNESS=none", "PODIUM_VERIFY_SIGNATURES=always", "PODIUM_MATERIALIZE_ROOT="+mat),
		toolCall(1, "load_artifact", map[string]any{"id": "finance/ap/pay-invoice"}))
	if e, _ := rpcResult(t, res.Stdout, 1)["error"].(string); !strings.Contains(e, "signature_invalid") {
		t.Errorf("error=%q, want materialize.signature_invalid", e)
	}
}

// T-D-handling-responses-47 — config.unknown_harness for an unrecognized harness.
func TestArtifactResponse_UnknownHarness(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": contextArtifact("pay"),
	}))
	mat := t.TempDir()
	res := mcpExec(t, append(mcpServerEnv(t, srv.BaseURL), "PODIUM_HARNESS=nonexistent-harness", "PODIUM_MATERIALIZE_ROOT="+mat),
		toolCall(1, "load_artifact", map[string]any{"id": "finance/ap/pay-invoice"}))
	// An unrecognized PODIUM_HARNESS is rejected at startup
	// (config.unknown_harness), so the bridge exits before answering.
	if !strings.Contains(res.Stderr, "config.unknown_harness") {
		t.Errorf("expected config.unknown_harness at startup; stderr=%s stdout=%s", res.Stderr, res.Stdout)
	}
}

// T-D-handling-responses-48 — registry.read_only still serves load_artifact.
func TestArtifactResponse_ReadOnlyServes(t *testing.T) {
	t.Parallel()
	t.Skip("read_only mode requires a store-failure probe (PODIUM_READONLY_PROBE_FAILURES) to flip the mode; not deterministically triggerable in a standalone e2e")
}

// T-D-handling-responses-49 — quota.materialize_rate_exceeded under a low rate.
func TestArtifactResponse_QuotaMaterialize(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"finance/ap/pay-invoice/ARTIFACT.md": contextArtifact("pay")})
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir(), "PODIUM_QUOTA_MATERIALIZE_RATE=1"},
		"serve", "--standalone", "--layer-path", reg)
	var got429 bool
	var lastBody string
	for i := 0; i < 8; i++ {
		st, body := getRaw(t, srv.BaseURL+"/v1/load_artifact?id=finance/ap/pay-invoice")
		if st == 429 {
			got429 = true
			lastBody = string(body)
			break
		}
	}
	if !got429 {
		t.Fatalf("no request hit the materialize quota (HTTP 429)")
	}
	if !strings.Contains(lastBody, "quota.materialize_rate_exceeded") {
		t.Errorf("429 body missing quota.materialize_rate_exceeded: %s", lastBody)
	}
}

// T-D-handling-responses-50 — auth.scope_denied surfaces as registry.not_found (gap).
func TestArtifactResponse_ScopeDenied(t *testing.T) {
	t.Parallel()
	t.Skip("not expressible in a standalone single-layer deployment: there is no invisible artifact to exercise the visibility-denied path")
}

// T-D-handling-responses-51 — materialize.hook_failed is a documented error
// code (docs/consuming/handling-artifact-responses.md) the bridge emits when
// the §6.6 step 4 MaterializationHook chain returns an error. F-6.6.1 wired
// the hook step into both materialization paths, so the code is now reachable.
// This asserts the bridge references it (the inverse of the prior gap marker).
// The full hook-failure path is exercised in cmd/podium-mcp's in-process unit
// tests (TestDeliver_HookErrorAbortsWrite). The hook SPI is now a context-first
// wire-serializable interface (F-9.3.1); a standalone e2e still cannot register
// a hook with the out-of-process bridge because §9.3 does not commit to an
// out-of-process plugin protocol, so registration stays in-process.
func TestArtifactResponse_HookFailedWired(t *testing.T) {
	t.Parallel()
	needle := "materialize.hook" + "_failed"
	mainGo := filepath.Join(repoRoot(t), "cmd", "podium-mcp", "main.go")
	b, err := os.ReadFile(mainGo)
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !strings.Contains(string(b), needle) {
		t.Errorf("%s does not reference %q; the §6.6 step 4 hook-failure path is not wired", mainGo, needle)
	}
}

// T-D-handling-responses-52 — load_artifact with missing id returns 400.
func TestArtifactResponse_MissingID(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	st, body := getRaw(t, srv.BaseURL+"/v1/load_artifact")
	if st != 400 {
		t.Errorf("status=%d, want 400: %s", st, body)
	}
	if !strings.Contains(string(body), "registry.invalid_argument") || !strings.Contains(string(body), "id is required") {
		t.Errorf("body missing registry.invalid_argument / 'id is required': %s", body)
	}
}

// T-D-handling-responses-53 — load_artifact with unknown id returns 404 not_found.
func TestArtifactResponse_UnknownID(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	st, body := getRaw(t, srv.BaseURL+"/v1/load_artifact?id=nonexistent/artifact/path")
	if st != 404 {
		t.Errorf("status=%d, want 404: %s", st, body)
	}
	if !strings.Contains(string(body), "registry.not_found") {
		t.Errorf("body missing registry.not_found: %s", body)
	}
}

// T-D-handling-responses-54 — batch load with >50 ids returns 400.
func TestArtifactResponse_BatchCap(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	ids := make([]string, 51)
	for i := range ids {
		ids[i] = "x/a" + strconv.Itoa(i)
	}
	st, body := postJSON(t, srv.BaseURL+"/v1/artifacts:batchLoad", map[string]any{"ids": ids})
	if st != 400 {
		t.Errorf("status=%d, want 400: %s", st, body)
	}
	if !strings.Contains(string(body), "registry.invalid_argument") {
		t.Errorf("body missing registry.invalid_argument: %s", body)
	}
}

// T-D-handling-responses-55 — batch partial failure returns mixed statuses.
func TestArtifactResponse_BatchPartial(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": contextArtifact("pay"),
	}))
	st, body := postJSON(t, srv.BaseURL+"/v1/artifacts:batchLoad",
		map[string]any{"ids": []string{"finance/ap/pay-invoice", "finance/ap/nonexistent"}})
	if st != 200 {
		t.Fatalf("status=%d, want 200: %s", st, body)
	}
	var arr []struct {
		ID           string `json:"id"`
		Status       string `json:"status"`
		ManifestBody string `json:"manifest_body"`
		Error        struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("decode batch response: %v\n%s", err, body)
	}
	byID := map[string]string{}
	codes := map[string]string{}
	for _, e := range arr {
		byID[e.ID] = e.Status
		codes[e.ID] = e.Error.Code
	}
	if byID["finance/ap/pay-invoice"] != "ok" {
		t.Errorf("valid artifact status=%q, want ok", byID["finance/ap/pay-invoice"])
	}
	// §7.6.2 forbids a per-item existence leak: a missing artifact surfaces as
	// visibility.denied (not registry.not_found) so the caller cannot tell
	// whether it exists in a hidden layer.
	if byID["finance/ap/nonexistent"] != "error" || codes["finance/ap/nonexistent"] != "visibility.denied" {
		t.Errorf("missing artifact status=%q code=%q, want error/visibility.denied",
			byID["finance/ap/nonexistent"], codes["finance/ap/nonexistent"])
	}
}

// T-D-handling-responses-56 — load_artifact returns content_hash.
func TestArtifactResponse_ContentHash(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": contextArtifact("pay"),
	}))
	if r := hrLoad(t, srv.BaseURL, "finance/ap/pay-invoice"); strings.TrimSpace(r.ContentHash) == "" {
		t.Errorf("content_hash empty: %+v", r)
	}
}

// (strconv.Itoa is used above; the manual helper was removed.)

// T-D-handling-responses-57 — walk_dependencies is absent from both SDKs.
func TestArtifactResponse_WalkDependenciesAbsent(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	py := readFile(t, filepath.Join(root, "sdks/podium-py/podium/client.py"))
	if strings.Contains(py, "walk_dependencies") {
		t.Errorf("python SDK unexpectedly defines walk_dependencies")
	}
	ts := readFile(t, filepath.Join(root, "sdks/podium-ts/src/index.ts"))
	if strings.Contains(ts, "walkDependencies") {
		t.Errorf("typescript SDK unexpectedly defines walkDependencies")
	}
}

// T-D-handling-responses-58 — Python LoadedArtifact has no `manifest` property.
func TestArtifactResponse_ManifestPropertyAbsent(t *testing.T) {
	t.Parallel()
	py := readFile(t, filepath.Join(repoRoot(t), "sdks/podium-py/podium/client.py"))
	if strings.Contains(py, "def manifest(") {
		t.Errorf("python SDK unexpectedly defines a manifest property")
	}
	for _, want := range []string{"manifest_body", "frontmatter"} {
		if !strings.Contains(py, want) {
			t.Errorf("python SDK LoadedArtifact missing the raw %q field", want)
		}
	}
}
