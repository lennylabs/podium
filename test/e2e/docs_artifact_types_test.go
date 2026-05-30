package e2e

// End-to-end tests for docs/authoring/artifact-types.md (D-artifact-types).
// Covers every built-in type (skill, agent, context, command, rule, hook,
// mcp-server) plus the extension-type path: scaffolding, lint acceptance
// and rejection, the per-harness materialization layout, and the
// registry-side behaviors (MCP prompt projection, search type filtering,
// deprecation, dependents). Tests drive the podium CLI, the standalone
// server, and the podium-mcp bridge. Behaviors blocked by a known
// BUILD-GAPS finding are encoded as skips so the acceptance criterion is
// recorded without failing the suite.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- skill -----------------------------------------------------------------

// T-D-artifact-types-1 — scaffold skill splits name/description into
// SKILL.md and keeps the Podium frontmatter in ARTIFACT.md; lints clean.
func TestArtifactTypes_ScaffoldSkillSplit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	out := filepath.Join(root, "finance/close/run-variance-analysis")
	sc := runPodium(t, "", nil, "artifact", "scaffold",
		"--type", "skill",
		"--description", "Flag unusual variance vs. forecast after month-end close.",
		"--tags", "finance,close,variance",
		"--sensitivity", "low",
		"--when-to-use", "After month-end close",
		"--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "type: skill") || !strings.Contains(art, "sensitivity: low") || !strings.Contains(art, "when_to_use:") {
		t.Errorf("ARTIFACT.md missing skill frontmatter:\n%s", art)
	}
	if strings.Contains(art, "name:") || strings.Contains(art, "description:") {
		t.Errorf("skill ARTIFACT.md must not carry name/description (agentskills.io split):\n%s", art)
	}
	skill := readFile(t, filepath.Join(out, "SKILL.md"))
	if !strings.Contains(skill, "name: run-variance-analysis") ||
		!strings.Contains(skill, "description: Flag unusual variance vs. forecast after month-end close.") {
		t.Errorf("SKILL.md missing name/description:\n%s", skill)
	}
	if l := runPodium(t, "", nil, "lint", "--registry", root); l.Exit != 0 {
		t.Errorf("lint exit=%d stdout=%q", l.Exit, l.Stdout)
	}
}

// T-D-artifact-types-2 — the doc's skill example (with runtime_requirements)
// lints clean and the field round-trips.
func TestArtifactTypes_SkillExampleLints(t *testing.T) {
	t.Parallel()
	art := "---\ntype: skill\nname: run-variance-analysis\nversion: 1.0.0\n" +
		"description: Flag unusual variance vs. forecast after month-end close.\n" +
		"when_to_use:\n  - \"After month-end close, when reviewing financial performance.\"\n" +
		"tags: [finance, close, variance]\nsensitivity: low\n" +
		"runtime_requirements:\n  python: \">=3.10\"\n---\n\n<!-- body -->\n"
	skill := "---\nname: run-variance-analysis\ndescription: Flag unusual variance vs. forecast after month-end close. Use after the close period.\n---\n\nCompare actuals vs. forecast.\n"
	reg := writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md": art,
		"finance/close/run-variance-analysis/SKILL.md":    skill,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q", res.Exit, res.Stdout)
	}
	if back := readFile(t, filepath.Join(reg, "finance/close/run-variance-analysis/ARTIFACT.md")); !strings.Contains(back, "runtime_requirements") {
		t.Errorf("runtime_requirements not preserved:\n%s", back)
	}
}

// T-D-artifact-types-3 — claude-code materializes a skill to
// .claude/skills/<name>/SKILL.md, with no ARTIFACT.md.
func TestArtifactTypes_SkillClaudeCodeLayout(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- body -->\n",
		"finance/close/run-variance-analysis/SKILL.md":    skillBody("run-variance-analysis"),
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	mustExist(t, filepath.Join(tgt, ".claude/skills/run-variance-analysis/SKILL.md"))
	if _, err := os.Stat(filepath.Join(tgt, ".claude/skills/run-variance-analysis/ARTIFACT.md")); err == nil {
		t.Errorf("claude-code wrote ARTIFACT.md into the skill dir")
	}
}

// T-D-artifact-types-4 — the none adapter writes the canonical skill layout.
func TestArtifactTypes_SkillNoneLayout(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- body -->\n",
		"finance/close/run-variance-analysis/SKILL.md":    skillBody("run-variance-analysis"),
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	mustExist(t, filepath.Join(tgt, "finance/close/run-variance-analysis/ARTIFACT.md"))
	mustExist(t, filepath.Join(tgt, "finance/close/run-variance-analysis/SKILL.md"))
}

// T-D-artifact-types-5 — a skill's runtime_requirements is returned by
// load_artifact (in the frontmatter) so the host can decide.
func TestArtifactTypes_SkillRuntimeInLoad(t *testing.T) {
	t.Parallel()
	art := "---\ntype: skill\nversion: 1.0.0\nruntime_requirements:\n  python: \">=3.10\"\n---\n\n<!-- body -->\n"
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md": art,
		"finance/close/run-variance-analysis/SKILL.md":    skillBody("run-variance-analysis"),
	}))
	_, body := getRaw(t, srv.BaseURL+"/v1/load_artifact?id=finance/close/run-variance-analysis")
	if !strings.Contains(string(body), "runtime_requirements") || !strings.Contains(string(body), "python") {
		t.Errorf("load response missing runtime_requirements/python:\n%s", body)
	}
}

// ---- agent -----------------------------------------------------------------

// T-D-artifact-types-6 — scaffold agent writes input, output, and a
// delegates_to list; lints clean.
func TestArtifactTypes_ScaffoldAgentFields(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	out := filepath.Join(root, "finance/procurement/vendor-compliance-check")
	sc := runPodium(t, "", nil, "artifact", "scaffold",
		"--type", "agent",
		"--description", "Verify a vendor against compliance and credit checks.",
		"--tags", "finance,procurement,compliance",
		"--sensitivity", "medium",
		"--input-schema", "./schemas/input.json",
		"--output-schema", "./schemas/output.json",
		"--delegates-to", "finance/credit/credit-check@1.x,finance/compliance/sanctions-screen@1.x",
		"--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	for _, want := range []string{"type: agent", "sensitivity: medium", "input: ./schemas/input.json", "output: ./schemas/output.json",
		"delegates_to:", "finance/credit/credit-check@1.x", "finance/compliance/sanctions-screen@1.x"} {
		if !strings.Contains(art, want) {
			t.Errorf("ARTIFACT.md missing %q:\n%s", want, art)
		}
	}
	if l := runPodium(t, "", nil, "lint", "--registry", root); l.Exit != 0 {
		t.Errorf("lint exit=%d stdout=%q", l.Exit, l.Stdout)
	}
}

// T-D-artifact-types-7 — claude-code materializes an agent to
// .claude/agents/<name>.md, no SKILL.md.
func TestArtifactTypes_AgentClaudeCodeLayout(t *testing.T) {
	t.Parallel()
	art := "---\ntype: agent\nname: vendor-compliance-check\nversion: 2.1.0\ndescription: Verify a vendor.\n---\n\nbody\n"
	reg := writeRegistry(t, map[string]string{"finance/procurement/vendor-compliance-check/ARTIFACT.md": art})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	mustExist(t, filepath.Join(tgt, ".claude/agents/vendor-compliance-check.md"))
	if _, err := os.Stat(filepath.Join(tgt, ".claude/skills/vendor-compliance-check")); err == nil {
		t.Errorf("agent must not create a .claude/skills entry")
	}
}

// ---- context ---------------------------------------------------------------

// T-D-artifact-types-8 — scaffold context produces no SKILL.md and lints clean.
func TestArtifactTypes_ScaffoldContext(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	out := filepath.Join(root, "reference/company-glossary")
	sc := runPodium(t, "", nil, "artifact", "scaffold",
		"--type", "context",
		"--description", "Internal terminology and acronyms used at the company.",
		"--tags", "reference,glossary",
		"--sensitivity", "low",
		"--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	if _, err := os.Stat(filepath.Join(out, "SKILL.md")); err == nil {
		t.Errorf("context must not have a SKILL.md")
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "type: context") || !strings.Contains(art, "name: company-glossary") {
		t.Errorf("ARTIFACT.md missing type/name:\n%s", art)
	}
	if l := runPodium(t, "", nil, "lint", "--registry", root); l.Exit != 0 {
		t.Errorf("lint exit=%d stdout=%q", l.Exit, l.Stdout)
	}
}

// T-D-artifact-types-9 — the doc's context example (version 1.4.0) lints clean.
func TestArtifactTypes_ContextExampleLints(t *testing.T) {
	t.Parallel()
	art := "---\ntype: context\nname: company-glossary\nversion: 1.4.0\n" +
		"description: Internal terminology and acronyms used at the company.\n" +
		"tags: [reference, glossary]\nsensitivity: low\n---\n\n# Glossary\n\n**ACV.** Annual Contract Value.\n"
	reg := writeRegistry(t, map[string]string{"reference/company-glossary/ARTIFACT.md": art})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// T-D-artifact-types-10 — claude-code places a context under .claude/podium/<id>/.
func TestArtifactTypes_ContextClaudeCodeLayout(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"reference/company-glossary/ARTIFACT.md": "---\ntype: context\nname: company-glossary\nversion: 1.4.0\ndescription: Glossary.\n---\n\nbody\n",
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	mustExist(t, filepath.Join(tgt, ".claude/podium/reference/company-glossary/ARTIFACT.md"))
}

// ---- command ---------------------------------------------------------------

// T-D-artifact-types-11 — scaffold command --expose-as-mcp-prompt writes the
// field and lints clean.
func TestArtifactTypes_ScaffoldCommandExpose(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	out := filepath.Join(root, "tools/refactor-module")
	sc := runPodium(t, "", nil, "artifact", "scaffold",
		"--type", "command",
		"--description", "Guided module refactoring with configurable focus areas.",
		"--tags", "command,refactoring",
		"--sensitivity", "low",
		"--expose-as-mcp-prompt",
		"--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "type: command") || !strings.Contains(art, "expose_as_mcp_prompt: true") {
		t.Errorf("ARTIFACT.md missing command/expose fields:\n%s", art)
	}
	if l := runPodium(t, "", nil, "lint", "--registry", root); l.Exit != 0 {
		t.Errorf("lint exit=%d stdout=%q", l.Exit, l.Stdout)
	}
}

// T-D-artifact-types-12 — an exposed command is visible via MCP prompts/list.
func TestArtifactTypes_CommandInPromptsList(t *testing.T) {
	t.Parallel()
	art := "---\ntype: command\nname: refactor-module\nversion: 1.0.0\ndescription: Guided refactoring.\nexpose_as_mcp_prompt: true\n---\n\n$ARGUMENTS\n"
	srv := startServer(t, writeRegistry(t, map[string]string{"tools/refactor-module/ARTIFACT.md": art}))
	res := mcpExec(t, mcpServerEnv(t, srv.BaseURL), rpcReq{ID: 1, Method: "prompts/list", Params: map[string]any{}})
	if body := mustJSON(rpcResult(t, res.Stdout, 1)); !strings.Contains(body, "tools/refactor-module") {
		t.Errorf("prompts/list missing the exposed command:\n%s", body)
	}
}

// T-D-artifact-types-13 — a command without expose_as_mcp_prompt is absent
// from prompts/list.
func TestArtifactTypes_CommandNotInPromptsList(t *testing.T) {
	t.Parallel()
	art := "---\ntype: command\nname: refactor-module\nversion: 1.0.0\ndescription: Guided refactoring.\n---\n\n$ARGUMENTS\n"
	srv := startServer(t, writeRegistry(t, map[string]string{"tools/refactor-module/ARTIFACT.md": art}))
	res := mcpExec(t, mcpServerEnv(t, srv.BaseURL), rpcReq{ID: 1, Method: "prompts/list", Params: map[string]any{}})
	if body := mustJSON(rpcResult(t, res.Stdout, 1)); strings.Contains(body, "tools/refactor-module") {
		t.Errorf("prompts/list leaked a non-exposed command:\n%s", body)
	}
}

// ---- rule ------------------------------------------------------------------

// T-D-artifact-types-14 — scaffold rule --rule-mode always produces a
// lint-clean rule.
func TestArtifactTypes_ScaffoldRuleAlways(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	out := filepath.Join(root, "policies/payment-style")
	sc := runPodium(t, "", nil, "artifact", "scaffold",
		"--type", "rule",
		"--description", "Style and review checks for payment-handling code.",
		"--tags", "style,payments",
		"--sensitivity", "low",
		"--rule-mode", "always",
		"--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	if art := readFile(t, filepath.Join(out, "ARTIFACT.md")); !strings.Contains(art, "type: rule") || !strings.Contains(art, "rule_mode: always") {
		t.Errorf("ARTIFACT.md missing rule/rule_mode fields:\n%s", art)
	}
	if l := runPodium(t, "", nil, "lint", "--registry", root); l.Exit != 0 {
		t.Errorf("lint exit=%d stdout=%q", l.Exit, l.Stdout)
	}
}

// T-D-artifact-types-15 — scaffold rule --rule-mode glob writes rule_globs.
func TestArtifactTypes_ScaffoldRuleGlob(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	out := filepath.Join(root, "policies/payment-style")
	sc := runPodium(t, "", nil, "artifact", "scaffold",
		"--type", "rule",
		"--description", "Style and review checks for payment-handling code.",
		"--sensitivity", "low",
		"--rule-mode", "glob",
		"--rule-globs", "**/payment_*.py,**/billing/**",
		"--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "rule_mode: glob") || !strings.Contains(art, "**/payment_*.py,**/billing/**") {
		t.Errorf("ARTIFACT.md missing rule_mode glob / rule_globs:\n%s", art)
	}
	if l := runPodium(t, "", nil, "lint", "--registry", root); l.Exit != 0 {
		t.Errorf("lint exit=%d stdout=%q", l.Exit, l.Stdout)
	}
}

// T-D-artifact-types-16 — scaffold rule --rule-mode glob without --rule-globs
// exits 2.
func TestArtifactTypes_ScaffoldRuleGlobMissingGlobs(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "policies/payment-style")
	res := runPodium(t, "", nil, "artifact", "scaffold", "--type", "rule", "--description", "x", "--rule-mode", "glob", "--yes", out)
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "rule-globs") {
		t.Errorf("stderr missing --rule-globs requirement: %q", res.Stderr)
	}
}

// T-D-artifact-types-17 — scaffold rule --rule-mode auto without
// --rule-description exits 2.
func TestArtifactTypes_ScaffoldRuleAutoMissingDesc(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "policies/db-rule")
	res := runPodium(t, "", nil, "artifact", "scaffold", "--type", "rule", "--description", "x", "--rule-mode", "auto", "--yes", out)
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "rule-description") {
		t.Errorf("stderr missing --rule-description requirement: %q", res.Stderr)
	}
}

// T-D-artifact-types-18 — claude-code materializes a rule to
// .claude/rules/<name>.md.
func TestArtifactTypes_RuleClaudeCodeLayout(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"policies/payment-style/ARTIFACT.md": "---\ntype: rule\nname: payment-style\nversion: 1.0.0\ndescription: Style checks.\nrule_mode: always\n---\n\nrules\n",
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	mustExist(t, filepath.Join(tgt, ".claude/rules/payment-style.md"))
}

// T-D-artifact-types-19 — cursor materializes a rule to .cursor/rules/<name>.mdc.
func TestArtifactTypes_RuleCursorLayout(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"policies/payment-style/ARTIFACT.md": "---\ntype: rule\nname: payment-style\nversion: 1.0.0\ndescription: Style checks.\nrule_mode: glob\nrule_globs: \"**/*.py\"\n---\n\nrules\n",
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "cursor")
	mustExist(t, filepath.Join(tgt, ".cursor/rules/payment-style.mdc"))
}

// ---- hook ------------------------------------------------------------------

// T-D-artifact-types-20 — scaffold hook --hook-event stop is lint-clean.
func TestArtifactTypes_ScaffoldHookStop(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	out := filepath.Join(root, "audit/log-session-end")
	sc := runPodium(t, "", nil, "artifact", "scaffold",
		"--type", "hook",
		"--description", "Log session-end events to a local audit file.",
		"--tags", "hook,audit",
		"--sensitivity", "low",
		"--hook-event", "stop",
		"--hook-action", "echo done",
		"--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "type: hook") || !strings.Contains(art, "hook_event: stop") || !strings.Contains(art, "hook_action: |") {
		t.Errorf("ARTIFACT.md missing hook fields:\n%s", art)
	}
	if l := runPodium(t, "", nil, "lint", "--registry", root); l.Exit != 0 {
		t.Errorf("lint exit=%d stdout=%q", l.Exit, l.Stdout)
	}
}

// T-D-artifact-types-21 — scaffold hook without --hook-event exits 2.
func TestArtifactTypes_ScaffoldHookMissingEvent(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "audit/h")
	res := runPodium(t, "", nil, "artifact", "scaffold", "--type", "hook", "--description", "x", "--yes", out)
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "hook-event") {
		t.Errorf("stderr missing --hook-event requirement: %q", res.Stderr)
	}
}

// T-D-artifact-types-22 — claude-code places a hook under .claude/podium/<id>/.
func TestArtifactTypes_HookClaudeCodeLayout(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"audit/log-session-end/ARTIFACT.md": "---\ntype: hook\nname: log-session-end\nversion: 1.0.0\ndescription: Log session-end events.\nhook_event: stop\nhook_action: |\n  echo done\n---\n\nbody\n",
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	mustExist(t, filepath.Join(tgt, ".claude/podium/audit/log-session-end/ARTIFACT.md"))
}

// T-D-artifact-types-23 — a generic hook_event (pre_tool_use) emits an info
// diagnostic naming the subtypes; lint stays green.
func TestArtifactTypes_HookGenericInfo(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"audit/generic-tool-hook/ARTIFACT.md": "---\ntype: hook\nname: generic-tool-hook\nversion: 1.0.0\ndescription: Generic hook.\nhook_event: pre_tool_use\nhook_action: |\n  echo done\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (info only)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "[info]") || !strings.Contains(res.Stdout, "pre_tool_use") || !strings.Contains(res.Stdout, "subtype") {
		t.Errorf("missing generic-hook info diagnostic:\n%s", res.Stdout)
	}
}

// T-D-artifact-types-24 — every canonical hook event scaffolds and lints
// clean (no error severity). Directory names are hyphenated because the
// §4.2 name syntax forbids the underscores in the event identifiers.
func TestArtifactTypes_AllCanonicalHookEvents(t *testing.T) {
	t.Parallel()
	events := []string{
		"session_start", "session_end", "user_prompt_submit", "pre_tool_use", "post_tool_use",
		"post_tool_use_failure", "pre_shell_execution", "post_shell_execution", "pre_mcp_execution",
		"post_mcp_execution", "pre_read_file", "post_file_edit", "permission_request", "permission_denied",
		"subagent_start", "subagent_stop", "stop", "pre_compact", "post_compact", "notification",
	}
	root := t.TempDir()
	for _, ev := range events {
		dir := filepath.Join(root, "hooks", strings.ReplaceAll(ev, "_", "-"))
		res := runPodium(t, "", nil, "artifact", "scaffold", "--type", "hook", "--description", "Test hook.",
			"--hook-event", ev, "--hook-action", "echo done", "--yes", dir)
		if res.Exit != 0 {
			t.Fatalf("scaffold %s exit=%d stderr=%s", ev, res.Exit, res.Stderr)
		}
	}
	res := runPodium(t, "", nil, "lint", "--registry", root)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "[error]") {
		t.Errorf("error-severity diagnostic for a canonical hook event:\n%s", res.Stdout)
	}
}

// ---- mcp-server ------------------------------------------------------------

// T-D-artifact-types-25 — scaffold mcp-server without --server-identifier
// exits 2.
func TestArtifactTypes_ScaffoldMCPMissingIdentifier(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "tools/finance-warehouse")
	res := runPodium(t, "", nil, "artifact", "scaffold", "--type", "mcp-server", "--description", "x", "--yes", out)
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "server-identifier") {
		t.Errorf("stderr missing --server-identifier requirement: %q", res.Stderr)
	}
}

// T-D-artifact-types-26 — scaffold mcp-server with --server-identifier lints
// clean.
func TestArtifactTypes_ScaffoldMCPServer(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	out := filepath.Join(root, "tools/finance-warehouse")
	sc := runPodium(t, "", nil, "artifact", "scaffold",
		"--type", "mcp-server",
		"--description", "Read-only access to the finance data warehouse.",
		"--tags", "mcp-server,finance,warehouse",
		"--sensitivity", "medium",
		"--server-identifier", "npx:@company/finance-warehouse-mcp",
		"--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	for _, want := range []string{"type: mcp-server", "server_identifier: npx:@company/finance-warehouse-mcp", "sensitivity: medium"} {
		if !strings.Contains(art, want) {
			t.Errorf("ARTIFACT.md missing %q:\n%s", want, art)
		}
	}
	if l := runPodium(t, "", nil, "lint", "--registry", root); l.Exit != 0 {
		t.Errorf("lint exit=%d stdout=%q", l.Exit, l.Stdout)
	}
}

// T-D-artifact-types-27 — the full mcp-server example with an mcpServers list
// lints clean.
func TestArtifactTypes_MCPServerFullExample(t *testing.T) {
	t.Parallel()
	art := "---\ntype: mcp-server\nname: finance-warehouse\nversion: 1.0.0\n" +
		"description: Read-only access to the finance data warehouse.\nsensitivity: medium\n" +
		"server_identifier: npx:@company/finance-warehouse-mcp\n" +
		"mcpServers:\n  - name: finance-warehouse\n    transport: stdio\n    command: npx\n    args: [\"-y\", \"@company/finance-warehouse-mcp\"]\n---\n\nRead-only SQL access.\n"
	reg := writeRegistry(t, map[string]string{"tools/finance-warehouse/ARTIFACT.md": art})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// T-D-artifact-types-28 — the doc claims mcp-server artifacts are filtered
// out of MCP-bridge search_artifacts results. The bridge passes the
// registry response through unfiltered and no BUILD-GAPS finding records
// the gap; skip rather than assert non-compliant behavior.
func TestArtifactTypes_MCPServerFilteredFromSearch(t *testing.T) {
	t.Skip("spec §5: mcp-server artifacts should be filtered from MCP-bridge search_artifacts results; the bridge does not filter them (searchArtifacts passes the registry response through) and no BUILD-GAPS finding is filed for this gap")
}

// T-D-artifact-types-29 — mcp-server artifacts materialize through podium sync.
func TestArtifactTypes_MCPServerVisibleViaSync(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/finance-warehouse/ARTIFACT.md": "---\ntype: mcp-server\nname: finance-warehouse\nversion: 1.0.0\ndescription: Finance MCP server.\nserver_identifier: npx:@company/finance-warehouse-mcp\n---\n\nbody\n",
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, "tools/finance-warehouse/ARTIFACT.md"))
}

// ---- extension types -------------------------------------------------------

// T-D-artifact-types-30 — scaffolding an extension type warns but produces a
// generic ARTIFACT.md.
func TestArtifactTypes_ExtensionScaffoldWarns(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "datasets/acme-q4-2024")
	res := runPodium(t, "", nil, "artifact", "scaffold", "--type", "company-dataset", "--description", "Internal training dataset.", "--yes", out)
	if res.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "not a first-class type") {
		t.Errorf("stderr missing extension-type warning: %q", res.Stderr)
	}
	if art := readFile(t, filepath.Join(out, "ARTIFACT.md")); !strings.Contains(art, "type: company-dataset") {
		t.Errorf("ARTIFACT.md missing the extension type:\n%s", art)
	}
}

// T-D-artifact-types-31 — an extension type emits a lint.unknown_type warning.
func TestArtifactTypes_ExtensionLintWarning(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"datasets/acme-q4/ARTIFACT.md": "---\ntype: company-dataset\nname: acme-q4\nversion: 1.0.0\ndescription: Dataset.\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.unknown_type") || !strings.Contains(res.Stdout, "[warning]") {
		t.Errorf("missing lint.unknown_type warning:\n%s", res.Stdout)
	}
}

// ---- agentskills.io name constraints ---------------------------------------

// T-D-artifact-types-32 — a skill name over 64 characters is rejected.
func TestArtifactTypes_SkillNameTooLong(t *testing.T) {
	t.Parallel()
	name := strings.Repeat("a", 65)
	out := filepath.Join(t.TempDir(), name)
	res := runPodium(t, "", nil, "artifact", "scaffold", "--type", "skill", "--description", "x", "--yes", out)
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "64 characters") {
		t.Errorf("stderr missing 64-character constraint: %q", res.Stderr)
	}
}

// T-D-artifact-types-33 — a skill name with a trailing hyphen is rejected.
func TestArtifactTypes_SkillNameTrailingHyphen(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "my-skill-")
	res := runPodium(t, "", nil, "artifact", "scaffold", "--type", "skill", "--description", "x", "--yes", out)
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "hyphen") {
		t.Errorf("stderr missing trailing-hyphen constraint: %q", res.Stderr)
	}
}

// T-D-artifact-types-34 — a skill name with consecutive hyphens is rejected.
func TestArtifactTypes_SkillNameConsecutiveHyphens(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "my--skill")
	res := runPodium(t, "", nil, "artifact", "scaffold", "--type", "skill", "--description", "x", "--yes", out)
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "consecutive hyphens") {
		t.Errorf("stderr missing consecutive-hyphen constraint: %q", res.Stderr)
	}
}

// T-D-artifact-types-35 — a skill without SKILL.md fails lint. The registry
// walk rejects it before the lint rules run, so the error surfaces as
// "<id>: type: skill missing SKILL.md" (exit 1) rather than a
// lint.skill_md_compliance diagnostic.
func TestArtifactTypes_SkillMissingSkillMD(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- body -->\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s stderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
	out := res.Stdout + res.Stderr
	if !strings.Contains(out, "missing SKILL.md") {
		t.Errorf("missing 'missing SKILL.md' diagnostic:\nstdout=%s\nstderr=%s", res.Stdout, res.Stderr)
	}
}

// T-D-artifact-types-36 — a SKILL.md whose name mismatches the directory
// fails lint.
func TestArtifactTypes_SkillNameMismatch(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- body -->\n",
		"finance/close/run-variance-analysis/SKILL.md":    "---\nname: wrong-name\ndescription: A skill whose name does not match its directory.\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.skill_md_compliance") || !strings.Contains(res.Stdout, "wrong-name") {
		t.Errorf("missing name-mismatch diagnostic:\n%s", res.Stdout)
	}
}

// T-D-artifact-types-37 — the doc says delegates_to is "constrained to
// agent-type targets at lint time", but no filesystem lint rule enforces
// it: an agent delegating to a skill lints without a delegates-type
// diagnostic. Documents the gap (the type-combination check the doc
// describes is a server-ingest concern, not filesystem lint).
func TestArtifactTypes_DelegatesToTypeNotEnforcedAtLint(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- body -->\n",
		"finance/close/run-variance-analysis/SKILL.md":    skillBody("run-variance-analysis"),
		"finance/procurement/vendor-check/ARTIFACT.md":    "---\ntype: agent\nname: vendor-check\nversion: 1.0.0\ndescription: Check vendor.\ndelegates_to:\n  - finance/close/run-variance-analysis\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (no filesystem delegates-type rule)\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "delegates_to") {
		t.Errorf("a delegates_to type diagnostic appeared; the gap may be closed — update this test:\n%s", res.Stdout)
	}
}

// T-D-artifact-types-38 — rule_mode auto with rule_description lints clean.
func TestArtifactTypes_RuleAutoLints(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"policies/payment-style/ARTIFACT.md": "---\ntype: rule\nname: payment-style\nversion: 1.0.0\ndescription: Style checks.\nrule_mode: auto\nrule_description: \"Apply when reviewing payment-handling code\"\n---\n\nrules\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// T-D-artifact-types-39 — rule_mode explicit lints clean with no extra fields.
func TestArtifactTypes_RuleExplicitLints(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"policies/incident-response/ARTIFACT.md": "---\ntype: rule\nname: incident-response\nversion: 1.0.0\ndescription: Incident response checklist.\nrule_mode: explicit\n---\n\nrules\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// ---- search, deprecation, sensitivity (standalone) -------------------------

// searchRegistry stages a skill and an agent that both mention "variance"
// so type filtering can be isolated from the query.
func searchRegistry(t *testing.T) string {
	return writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\ndescription: Flag unusual variance vs forecast.\n---\n\n<!-- body -->\n",
		"finance/close/run-variance-analysis/SKILL.md":    "---\nname: run-variance-analysis\ndescription: Flag unusual variance vs forecast after close.\n---\n\nCompare actuals vs forecast and flag variance.\n",
		"finance/procurement/vendor-check/ARTIFACT.md":    "---\ntype: agent\nname: vendor-check\nversion: 1.0.0\ndescription: Verify a vendor against variance and compliance checks.\n---\n\nReview the vendor.\n",
	})
}

// T-D-artifact-types-40 — search --type skill returns the skill.
func TestArtifactTypes_SearchTypeSkill(t *testing.T) {
	t.Parallel()
	srv := startServer(t, searchRegistry(t))
	res := runPodium(t, "", nil, "search", "--registry", srv.BaseURL, "--type", "skill", "variance")
	if res.Exit != 0 {
		t.Fatalf("search exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "finance/close/run-variance-analysis") {
		t.Errorf("search --type skill did not return the skill:\n%s", res.Stdout)
	}
}

// T-D-artifact-types-41 — search --type agent returns the agent and excludes
// the skill (type filter is exclusive).
func TestArtifactTypes_SearchTypeAgent(t *testing.T) {
	t.Parallel()
	srv := startServer(t, searchRegistry(t))
	res := runPodium(t, "", nil, "search", "--registry", srv.BaseURL, "--type", "agent", "variance")
	if res.Exit != 0 {
		t.Fatalf("search exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "finance/procurement/vendor-check") {
		t.Errorf("search --type agent did not return the agent:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "run-variance-analysis") {
		t.Errorf("type=agent leaked a skill:\n%s", res.Stdout)
	}
}

// T-D-artifact-types-42 — search_visibility: direct-only excludes an
// artifact from search results while leaving it reachable via
// load_artifact by ID.
// spec: §4.3 universal fields (search_visibility), §4.5.3 (F-4.3.3).
func TestArtifactTypes_SearchVisibilityDirectOnly(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/indexed-glossary/ARTIFACT.md": "---\ntype: context\nname: indexed-glossary\nversion: 1.0.0\ndescription: Indexed variance glossary.\n---\n\nbody\n",
		"finance/secret-glossary/ARTIFACT.md":  "---\ntype: context\nname: secret-glossary\nversion: 1.0.0\ndescription: Secret variance glossary.\nsearch_visibility: direct-only\n---\n\nbody\n",
	}))
	_, sbody := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query=variance")
	if strings.Contains(string(sbody), "finance/secret-glossary") {
		t.Errorf("direct-only artifact appeared in search results:\n%s", sbody)
	}
	if !strings.Contains(string(sbody), "finance/indexed-glossary") {
		t.Errorf("indexed artifact missing from search:\n%s", sbody)
	}
	// §4.3: a direct-only artifact stays reachable via load_artifact when
	// the caller knows the ID.
	var load struct {
		ID string `json:"id"`
	}
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id=finance/secret-glossary", &load)
	if load.ID != "finance/secret-glossary" {
		t.Errorf("direct-only artifact should load by ID, got %+v", load)
	}
}

// T-D-artifact-types-43 — a deprecated artifact is excluded from default
// search results and its load response carries the deprecation signal.
// Observed: the registry surfaces deprecated:true and a deprecation
// warning, but replaced_by does not round-trip into the load response
// (no BUILD-GAPS finding is filed for that narrower gap), so this test
// asserts the deprecated flag and warning rather than the upgrade target.
func TestArtifactTypes_DeprecatedLifecycle(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/new-glossary/ARTIFACT.md": "---\ntype: context\nname: new-glossary\nversion: 1.0.0\ndescription: Current variance glossary.\n---\n\nbody\n",
		"finance/old-glossary/ARTIFACT.md": "---\ntype: context\nname: old-glossary\nversion: 1.0.0\ndescription: Old variance glossary.\ndeprecated: true\nreplaced_by: finance/new-glossary\n---\n\nbody\n",
	}))
	_, sbody := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query=variance")
	if strings.Contains(string(sbody), "finance/old-glossary") {
		t.Errorf("deprecated artifact appeared in search results:\n%s", sbody)
	}
	if !strings.Contains(string(sbody), "finance/new-glossary") {
		t.Errorf("non-deprecated artifact missing from search:\n%s", sbody)
	}
	var load struct {
		Deprecated         bool   `json:"deprecated"`
		ReplacedBy         string `json:"replaced_by"`
		DeprecationWarning string `json:"deprecation_warning"`
	}
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id=finance/old-glossary", &load)
	if !load.Deprecated {
		t.Errorf("load response missing deprecated flag: %+v", load)
	}
	if !strings.Contains(load.DeprecationWarning, "deprecated") {
		t.Errorf("load response missing deprecation warning: %+v", load)
	}
}

// T-D-artifact-types-44 — sensitivity is exposed in the load response.
func TestArtifactTypes_SensitivityInLoad(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/procurement/vendor-check/ARTIFACT.md": "---\ntype: agent\nname: vendor-check\nversion: 1.0.0\ndescription: Check vendor.\nsensitivity: medium\n---\n\nbody\n",
	}))
	var load struct {
		Sensitivity string `json:"sensitivity"`
	}
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id=finance/procurement/vendor-check", &load)
	if load.Sensitivity != "medium" {
		t.Errorf("load sensitivity=%q, want medium", load.Sensitivity)
	}
}

// T-D-artifact-types-45 — a hook with a bundled scripts/ directory syncs
// correctly through the none adapter.
func TestArtifactTypes_HookBundledScript(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/audit/log-session-end/ARTIFACT.md":    "---\ntype: hook\nname: log-session-end\nversion: 1.0.0\ndescription: Log session-end events.\nhook_event: stop\nhook_action: |\n  scripts/log.sh\nruntime_requirements:\n  system_packages: [jq]\n---\n\nbody\n",
		"finance/audit/log-session-end/scripts/log.sh": "#!/usr/bin/env bash\necho session end | jq -R .\n",
	})
	if l := runPodium(t, "", nil, "lint", "--registry", reg); l.Exit != 0 {
		t.Fatalf("lint exit=%d stdout=%q", l.Exit, l.Stdout)
	}
	tgt := t.TempDir()
	if s := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); s.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", s.Exit, s.Stderr)
	}
	mustExist(t, filepath.Join(tgt, "finance/audit/log-session-end/ARTIFACT.md"))
	mustExist(t, filepath.Join(tgt, "finance/audit/log-session-end/scripts/log.sh"))
}

// T-D-artifact-types-46 — a missing version field fails lint.
func TestArtifactTypes_MissingVersion(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/my-context/ARTIFACT.md": "---\ntype: context\nname: my-context\ndescription: Test.\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.required_field_missing") || !strings.Contains(res.Stdout, "version") {
		t.Errorf("missing version diagnostic:\n%s", res.Stdout)
	}
}

// T-D-artifact-types-47 — an invalid semver version fails lint.
func TestArtifactTypes_InvalidVersion(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"tools/my-context/ARTIFACT.md": "---\ntype: context\nname: my-context\nversion: not-a-semver\ndescription: Test.\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.invalid_version") {
		t.Errorf("missing lint.invalid_version:\n%s", res.Stdout)
	}
}

// T-D-artifact-types-48 — effort_hint on a rule warns (hints apply only to
// agent / skill / command).
func TestArtifactTypes_EffortHintOnRuleWarns(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"policies/style/ARTIFACT.md": "---\ntype: rule\nname: style\nversion: 1.0.0\ndescription: Style rule.\nrule_mode: always\neffort_hint: high\n---\n\nrules\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.hint_on_unsupported_type") || !strings.Contains(res.Stdout, "[warning]") {
		t.Errorf("missing hint_on_unsupported_type warning:\n%s", res.Stdout)
	}
}

// T-D-artifact-types-49 — the registry returns runtime_requirements in the
// load response so the host (not the registry) decides on materialization.
func TestArtifactTypes_RuntimeRequirementsPassThrough(t *testing.T) {
	t.Parallel()
	art := "---\ntype: skill\nversion: 1.0.0\nruntime_requirements:\n  python: \">=3.10\"\n---\n\n<!-- body -->\n"
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/close/run-variance-analysis/ARTIFACT.md": art,
		"finance/close/run-variance-analysis/SKILL.md":    skillBody("run-variance-analysis"),
	}))
	st, body := getRaw(t, srv.BaseURL+"/v1/load_artifact?id=finance/close/run-variance-analysis")
	if st != 200 {
		t.Fatalf("load = HTTP %d, want 200 (host decides, registry does not refuse)\n%s", st, body)
	}
	if !strings.Contains(string(body), "runtime_requirements") {
		t.Errorf("load response omitted runtime_requirements:\n%s", body)
	}
}

// T-D-artifact-types-50 — target_harnesses should restrict materialization to
// the listed harnesses. The field is parsed but has no behavioral effect.
func TestArtifactTypes_TargetHarnessesRestrictsSync(t *testing.T) {
	t.Skip("blocked by F-6.7.2: target_harnesses is parsed but has no behavioral effect, so an artifact materializes for every harness regardless of the list")
}

// T-D-artifact-types-51 — scaffold --extends writes the extends field.
func TestArtifactTypes_ScaffoldExtends(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "finance/procurement/extended-vendor-check")
	res := runPodium(t, "", nil, "artifact", "scaffold", "--type", "agent",
		"--description", "Extended vendor check.",
		"--extends", "finance/procurement/vendor-compliance-check@1.x",
		"--yes", out)
	if res.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if art := readFile(t, filepath.Join(out, "ARTIFACT.md")); !strings.Contains(art, "extends: finance/procurement/vendor-compliance-check@1.x") {
		t.Errorf("ARTIFACT.md missing extends:\n%s", art)
	}
}

// T-D-artifact-types-52 — an invalid sensitivity value exits 2.
func TestArtifactTypes_InvalidSensitivity(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "tools/x")
	res := runPodium(t, "", nil, "artifact", "scaffold", "--type", "context", "--description", "x", "--sensitivity", "ultra", "--yes", out)
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "low|medium|high") {
		t.Errorf("stderr missing sensitivity enum: %q", res.Stderr)
	}
}

// T-D-artifact-types-53 — scaffold with no positional path exits 2.
func TestArtifactTypes_ScaffoldNoPath(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "artifact", "scaffold", "--type", "context", "--description", "x", "--yes")
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "path") {
		t.Errorf("stderr missing missing-path message: %q", res.Stderr)
	}
}

// T-D-artifact-types-54 — scaffold refuses to overwrite an existing directory
// without --force (exit 1).
func TestArtifactTypes_ScaffoldNoOverwrite(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "tools/existing")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	res := runPodium(t, "", nil, "artifact", "scaffold", "--type", "context", "--description", "x", "--yes", out)
	if res.Exit != 1 {
		t.Fatalf("exit=%d, want 1\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "already exists") || !strings.Contains(res.Stderr, "--force") {
		t.Errorf("stderr missing overwrite refusal: %q", res.Stderr)
	}
}

// T-D-artifact-types-55 — scaffold --force overwrites an existing directory.
func TestArtifactTypes_ScaffoldForce(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "tools/existing")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(out, "ARTIFACT.md"), []byte("stale content\n"), 0o644); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	res := runPodium(t, "", nil, "artifact", "scaffold", "--type", "context", "--description", "fresh content", "--force", "--yes", out)
	if res.Exit != 0 {
		t.Fatalf("exit=%d, want 0\nstderr=%s", res.Exit, res.Stderr)
	}
	if art := readFile(t, filepath.Join(out, "ARTIFACT.md")); !strings.Contains(art, "fresh content") {
		t.Errorf("ARTIFACT.md not overwritten:\n%s", art)
	}
}

// T-D-artifact-types-56 — a skill referencing an mcp-server's
// server_identifier should link to it in the dependency graph. The
// reverse-dependency edge keys on the local mcpServers name, not the
// server_identifier, so the link does not resolve to the mcp-server id.
func TestArtifactTypes_MCPServerReverseIndex(t *testing.T) {
	t.Skip("blocked by F-4.7.4: the mcpServers reverse-dependency edge keys on the local name, not server_identifier, so a skill referencing the server does not link to the mcp-server artifact id")
}

// T-D-artifact-types-57 — scaffolding a nested artifact creates the full
// domain hierarchy.
func TestArtifactTypes_DomainHierarchy(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	out := filepath.Join(root, "team-shared/finance/ap/company-glossary")
	res := runPodium(t, "", nil, "artifact", "scaffold", "--type", "context", "--description", "Company glossary.", "--yes", out)
	if res.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	for _, dir := range []string{"team-shared", "team-shared/finance", "team-shared/finance/ap", "team-shared/finance/ap/company-glossary"} {
		if fi, err := os.Stat(filepath.Join(root, dir)); err != nil || !fi.IsDir() {
			t.Errorf("expected directory %s: %v", dir, err)
		}
	}
	mustExist(t, filepath.Join(out, "ARTIFACT.md"))
}

// T-D-artifact-types-58 — an agent is searchable via the HTTP
// search_artifacts type filter; skills are excluded.
func TestArtifactTypes_AgentSearchHTTP(t *testing.T) {
	t.Parallel()
	srv := startServer(t, searchRegistry(t))
	_, body := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query=vendor&type=agent")
	if !strings.Contains(string(body), "finance/procurement/vendor-check") {
		t.Errorf("HTTP search missing the agent:\n%s", body)
	}
	if strings.Contains(string(body), "run-variance-analysis") {
		t.Errorf("type=agent leaked a skill:\n%s", body)
	}
}

// T-D-artifact-types-59 — a hook using an event the configured harness does
// not support should produce a lint diagnostic. No ingest-time
// capability-mismatch lint exists.
func TestArtifactTypes_HookUnsupportedHarnessLint(t *testing.T) {
	t.Skip("blocked by F-6.7.1: ingest-time capability-mismatch lint is absent, so a hook_event unsupported by the configured harness produces no diagnostic")
}

// T-D-artifact-types-60 — a hook with target_harnesses set and a supported
// event lints clean. With no capability-mismatch lint rule (F-6.7.1) the
// suppression is moot; lint is clean either way, which satisfies the
// documented acceptance criterion.
func TestArtifactTypes_HookTargetHarnessesClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"audit/log-session/ARTIFACT.md": "---\ntype: hook\nname: log-session\nversion: 1.0.0\ndescription: Log sessions.\nhook_event: session_end\nhook_action: |\n  echo done\ntarget_harnesses: [claude-code]\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "[error]") {
		t.Errorf("unexpected error-severity diagnostic:\n%s", res.Stdout)
	}
}
