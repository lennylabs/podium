package e2e

// End-to-end tests for docs/authoring/hints.md (D-hints).
// Covers the effort_hint and model_class_hint enums, the
// lint.hint_on_unsupported_type warning that fires when a hint is set on a type
// other than agent/skill/command, the absence of enum-value validation, and the
// verbatim pass-through of both hint fields through the standalone server, the
// MCP bridge, and filesystem sync. Tests drive the podium CLI, the standalone
// server, and the podium-mcp bridge. Doc claims that the implementation does not
// honor (with no BUILD-GAPS finding filed) are asserted against actual behavior
// with a note so a future change is detected.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// htSkillArtifact returns a minimal skill ARTIFACT.md whose frontmatter carries
// the supplied extra lines (e.g. a hint field) before the closing delimiter.
func htSkillArtifact(extra string) string {
	return "---\ntype: skill\nversion: 1.0.0\n" + extra + "---\n\n<!-- Skill body lives in SKILL.md. -->\n"
}

// htSkillMD returns a SKILL.md whose name matches the leaf directory.
func htSkillMD(name, desc string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + name + " body.\n"
}

// htLoadFrontmatter decodes the raw `frontmatter` string from a load_artifact
// response. Caller-interpreted ARTIFACT.md fields are stored verbatim there.
func htLoadFrontmatter(t *testing.T, baseURL, id string) string {
	t.Helper()
	var load struct {
		Frontmatter string `json:"frontmatter"`
	}
	getJSON(t, baseURL+"/v1/load_artifact?id="+id, &load)
	return load.Frontmatter
}

// ---- effort_hint accepted on supported types (agent/skill/command) ---------

// T-D-hints-1 — effort_hint: low on a skill lints clean with no
// hint_on_unsupported_type warning.
// spec: docs/authoring/hints.md §effort_hint table.
func TestHints_EffortLowOnSkill(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"greetings/hello/ARTIFACT.md": htSkillArtifact("effort_hint: low\n"),
		"greetings/hello/SKILL.md":    htSkillMD("hello", "Say hello to the user."),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s stderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
	if strings.Contains(res.Stdout+res.Stderr, "lint.hint_on_unsupported_type") {
		t.Errorf("unexpected hint warning on skill:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "[error]") {
		t.Errorf("unexpected error diagnostic:\n%s", res.Stdout)
	}
}

// T-D-hints-2 — effort_hint: medium on an agent lints clean.
// spec: docs/authoring/hints.md §effort_hint table.
func TestHints_EffortMediumOnAgent(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/my-agent/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: An analysis agent.\neffort_hint: medium\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout+res.Stderr, "lint.hint_on_unsupported_type") {
		t.Errorf("unexpected hint warning on agent:\n%s", res.Stdout)
	}
}

// T-D-hints-3 — effort_hint: high on a command lints clean.
// spec: docs/authoring/hints.md §effort_hint table.
func TestHints_EffortHighOnCommand(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/run-analysis/ARTIFACT.md": "---\ntype: command\nversion: 1.0.0\ndescription: Run the variance analysis.\neffort_hint: high\n---\n\n$ARGUMENTS\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout+res.Stderr, "lint.hint_on_unsupported_type") {
		t.Errorf("unexpected hint warning on command:\n%s", res.Stdout)
	}
}

// T-D-hints-4 — effort_hint: max on an agent lints clean.
// spec: docs/authoring/hints.md §effort_hint table; "Use max for agents that do
// open-ended investigation".
func TestHints_EffortMaxOnAgent(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/investigator/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: Open-ended investigation agent.\neffort_hint: max\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout+res.Stderr, "lint.hint_on_unsupported_type") {
		t.Errorf("unexpected hint warning on agent:\n%s", res.Stdout)
	}
}

// T-D-hints-5 — model_class_hint: nano on a skill lints clean.
// spec: docs/authoring/hints.md §model_class_hint table.
func TestHints_ModelNanoOnSkill(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"greetings/hello/ARTIFACT.md": htSkillArtifact("model_class_hint: nano\n"),
		"greetings/hello/SKILL.md":    htSkillMD("hello", "Simple greeting."),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout+res.Stderr, "lint.hint_on_unsupported_type") {
		t.Errorf("unexpected hint warning on skill:\n%s", res.Stdout)
	}
}

// T-D-hints-6 — model_class_hint: frontier on an agent lints clean.
// spec: docs/authoring/hints.md §model_class_hint table.
func TestHints_ModelFrontierOnAgent(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/investigator/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: Frontier-class investigator.\nmodel_class_hint: frontier\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout+res.Stderr, "lint.hint_on_unsupported_type") {
		t.Errorf("unexpected hint warning on agent:\n%s", res.Stdout)
	}
}

// T-D-hints-7 — both effort_hint and model_class_hint set together on a skill
// lint clean.
// spec: docs/authoring/hints.md §Advisory framing.
func TestHints_BothHintsOnSkill(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/run-variance-analysis/ARTIFACT.md": htSkillArtifact("effort_hint: high\nmodel_class_hint: frontier\n"),
		"personal/run-variance-analysis/SKILL.md":    htSkillMD("run-variance-analysis", "Flag unusual variance vs. forecast after month-end close."),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout+res.Stderr, "lint.hint_on_unsupported_type") {
		t.Errorf("unexpected hint warning on skill with both hints:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "[error]") {
		t.Errorf("unexpected error diagnostic:\n%s", res.Stdout)
	}
}

// ---- effort_hint / model_class_hint on unsupported types -> warning ---------

// T-D-hints-8 — effort_hint on a context warns (lint.hint_on_unsupported_type,
// severity warning) and the lint still exits 0.
// spec: docs/authoring/hints.md §Lint behavior.
func TestHints_EffortOnContextWarns(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/glossary/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Company glossary.\neffort_hint: high\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.hint_on_unsupported_type") || !strings.Contains(res.Stdout, "[warning]") {
		t.Fatalf("missing hint_on_unsupported_type warning:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "effort_hint") || !strings.Contains(res.Stdout, "context") {
		t.Errorf("warning message missing effort_hint/context:\n%s", res.Stdout)
	}
}

// T-D-hints-9 — model_class_hint on a context warns.
// spec: docs/authoring/hints.md §Lint behavior.
func TestHints_ModelOnContextWarns(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/glossary/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Company glossary.\nmodel_class_hint: large\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.hint_on_unsupported_type") || !strings.Contains(res.Stdout, "[warning]") {
		t.Fatalf("missing hint_on_unsupported_type warning:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "model_class_hint") || !strings.Contains(res.Stdout, "context") {
		t.Errorf("warning message missing model_class_hint/context:\n%s", res.Stdout)
	}
}

// T-D-hints-10 — effort_hint on a rule warns.
// spec: docs/authoring/hints.md §Lint behavior.
func TestHints_EffortOnRuleWarns(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/ts-style/ARTIFACT.md": "---\ntype: rule\nversion: 1.0.0\ndescription: TypeScript style rules.\nrule_mode: always\neffort_hint: medium\n---\n\nrules\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.hint_on_unsupported_type") || !strings.Contains(res.Stdout, "[warning]") {
		t.Errorf("missing hint_on_unsupported_type warning:\n%s", res.Stdout)
	}
}

// T-D-hints-11 — effort_hint on a hook warns.
// spec: docs/authoring/hints.md §Lint behavior.
func TestHints_EffortOnHookWarns(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/session-hook/ARTIFACT.md": "---\ntype: hook\nversion: 1.0.0\ndescription: Log session stops.\nhook_event: stop\nhook_action: |\n  echo done\neffort_hint: low\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.hint_on_unsupported_type") || !strings.Contains(res.Stdout, "[warning]") {
		t.Errorf("missing hint_on_unsupported_type warning:\n%s", res.Stdout)
	}
}

// T-D-hints-12 — effort_hint on an mcp-server warns.
// spec: docs/authoring/hints.md §Lint behavior.
func TestHints_EffortOnMcpServerWarns(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/finance-warehouse/ARTIFACT.md": "---\ntype: mcp-server\nversion: 1.0.0\ndescription: Finance data warehouse MCP server.\nserver_identifier: npx:@company/finance-warehouse-mcp\neffort_hint: high\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.hint_on_unsupported_type") || !strings.Contains(res.Stdout, "[warning]") {
		t.Errorf("missing hint_on_unsupported_type warning:\n%s", res.Stdout)
	}
}

// ---- no enum-value validation (doc claims an ingest error; none exists) -----

// T-D-hints-13 — an invalid effort_hint value (ultra) on an agent.
// The doc states an out-of-enum value is an ingest error, but EffortHint is a
// plain string and no lint rule validates enum membership, so lint exits 0 with
// no error diagnostic. No BUILD-GAPS finding is filed for this narrow gap; the
// test asserts actual behavior so a future enum check is detected as a change.
// spec: docs/authoring/hints.md §Lint behavior ("An invalid value ... ingest error").
func TestHints_InvalidEffortValueNoError(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/my-agent/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: Test agent.\neffort_hint: ultra\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (no enum validation)\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "[error]") {
		t.Errorf("doc says invalid effort_hint is an ingest error, but no enum validation exists; got error diagnostic:\n%s", res.Stdout)
	}
}

// T-D-hints-14 — an invalid model_class_hint value (gpt-4o) on an agent.
// Same gap as T-D-hints-13: ModelClassHint is a plain string with no enum
// validation, so lint exits 0 with no error diagnostic. No BUILD-GAPS finding;
// asserts actual behavior as a change-detector.
// spec: docs/authoring/hints.md §Lint behavior ("An invalid value ... ingest error").
func TestHints_InvalidModelValueNoError(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/my-agent/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: Test agent.\nmodel_class_hint: gpt-4o\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (no enum validation)\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "[error]") {
		t.Errorf("doc says invalid model_class_hint is an ingest error, but no enum validation exists; got error diagnostic:\n%s", res.Stdout)
	}
}

// T-D-hints-15 — a skill with no hint fields lints clean and emits no
// hint_on_unsupported_type diagnostic.
// spec: docs/authoring/hints.md §Lint behavior ("Both fields are optional.").
func TestHints_NoHintFieldsNoWarning(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"greetings/hello/ARTIFACT.md": htSkillArtifact(""),
		"greetings/hello/SKILL.md":    htSkillMD("hello", "Greet the user."),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout+res.Stderr, "lint.hint_on_unsupported_type") {
		t.Errorf("unexpected hint warning when no hints set:\n%s", res.Stdout)
	}
}

// ---- delivery-time pass-through (server, sync, MCP) -------------------------

// T-D-hints-16 — both hints on an agent round-trip into the load_artifact
// `frontmatter` field verbatim.
// spec: docs/authoring/hints.md §Advisory framing.
func TestHints_LoadArtifactFrontmatterCarriesHints(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"personal/my-agent/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: Frontier investigator.\neffort_hint: high\nmodel_class_hint: frontier\n---\n\nbody\n",
	}))
	fm := htLoadFrontmatter(t, srv.BaseURL, "personal/my-agent")
	if !strings.Contains(fm, "effort_hint: high") || !strings.Contains(fm, "model_class_hint: frontier") {
		t.Errorf("frontmatter missing hints:\n%s", fm)
	}
}

// T-D-hints-17 — sync --harness none writes the canonical layout with both hints
// intact in the materialized ARTIFACT.md.
// spec: docs/authoring/hints.md §Advisory framing.
func TestHints_SyncNonePreservesHints(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/investigator/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: Investigator.\neffort_hint: max\nmodel_class_hint: frontier\n---\n\nbody\n",
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, "personal/investigator/ARTIFACT.md"))
	if !strings.Contains(got, "effort_hint: max") || !strings.Contains(got, "model_class_hint: frontier") {
		t.Errorf("materialized ARTIFACT.md missing hints:\n%s", got)
	}
}

// T-D-hints-18 — sync --harness claude-code writes the agent manifest to
// .claude/agents/<leaf>.md with effort_hint carried through.
// spec: docs/authoring/hints.md §Adapter support ("No built-in adapter currently
// translates these fields").
func TestHints_SyncClaudeCodeAgentPreservesHint(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/investigator/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: Investigator.\neffort_hint: high\n---\n\nbody\n",
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, ".claude/agents/investigator.md"))
	if !strings.Contains(got, "effort_hint: high") {
		t.Errorf("materialized agent missing effort_hint:\n%s", got)
	}
}

// T-D-hints-19 — sync --harness claude-code writes a skill's SKILL.md to
// .claude/skills/<leaf>/SKILL.md. effort_hint lives in ARTIFACT.md, which the
// claude-code skill layout does not materialize, so this asserts the SKILL.md
// body is present.
// spec: docs/authoring/hints.md §Adapter support.
func TestHints_SyncClaudeCodeSkillMaterializes(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/greet/ARTIFACT.md": htSkillArtifact("effort_hint: high\n"),
		"personal/greet/SKILL.md":    htSkillMD("greet", "Greet the user."),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, ".claude/skills/greet/SKILL.md"))
	if !strings.Contains(got, "greet body.") {
		t.Errorf("materialized SKILL.md missing the skill body:\n%s", got)
	}
}

// T-D-hints-20 — model_class_hint: frontier does not cause a load failure when
// no frontier-tier model is configured. load_artifact returns 200 with the hint
// in the frontmatter.
// spec: docs/authoring/hints.md §Advisory framing.
func TestHints_FrontierHintLoadsWithoutModel(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"personal/investigator/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: Frontier investigator.\nmodel_class_hint: frontier\n---\n\nbody\n",
	}))
	if st := getStatus(t, srv.BaseURL+"/v1/load_artifact?id=personal/investigator"); st != 200 {
		t.Fatalf("load = HTTP %d, want 200", st)
	}
	fm := htLoadFrontmatter(t, srv.BaseURL, "personal/investigator")
	if !strings.Contains(fm, "model_class_hint: frontier") {
		t.Errorf("frontmatter missing model_class_hint:\n%s", fm)
	}
}

// ---- scaffold interaction ---------------------------------------------------

// T-D-hints-21 — a scaffolded skill with hints added to its ARTIFACT.md
// frontmatter lints clean. The scaffold omits hints by default; inserting them
// before the closing delimiter produces a lint-clean artifact.
// spec: docs/authoring/hints.md §Advisory framing.
func TestHints_ScaffoldSkillWithAddedHintsLintsClean(t *testing.T) {
	t.Parallel()
	reg := t.TempDir()
	out := filepath.Join(reg, "personal/run-variance-analysis")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "skill",
		"--description", "Run variance analysis.", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	mustExist(t, filepath.Join(out, "ARTIFACT.md"))
	mustExist(t, filepath.Join(out, "SKILL.md"))
	artPath := filepath.Join(out, "ARTIFACT.md")
	art := readFile(t, artPath)
	art = strings.Replace(art, "\n---\n", "\neffort_hint: high\nmodel_class_hint: frontier\n---\n", 1)
	if err := os.WriteFile(artPath, []byte(art), 0o644); err != nil {
		t.Fatalf("rewrite ARTIFACT.md: %v", err)
	}
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "[error]") {
		t.Errorf("unexpected error diagnostic after adding hints:\n%s", res.Stdout)
	}
}

// T-D-hints-22 — a scaffolded agent omits both hint fields by default and lints
// clean.
// spec: docs/authoring/hints.md §Advisory framing ("Both fields are optional.").
func TestHints_ScaffoldAgentOmitsHints(t *testing.T) {
	t.Parallel()
	reg := t.TempDir()
	out := filepath.Join(reg, "personal/my-agent")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "agent",
		"--description", "An agent.", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if strings.Contains(art, "effort_hint:") || strings.Contains(art, "model_class_hint:") {
		t.Errorf("scaffold should not emit hint fields:\n%s", art)
	}
	if res := runPodium(t, "", nil, "lint", "--registry", reg); res.Exit != 0 {
		t.Errorf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
}

// T-D-hints-23 — the MCP load_artifact path preserves both hints. The MCP result
// JSON has no frontmatter field, so the hints are checked in the materialized
// ARTIFACT.md under PODIUM_MATERIALIZE_ROOT.
// spec: docs/authoring/hints.md §Advisory framing.
func TestHints_MCPLoadArtifactPreservesHints(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"personal/analyze/ARTIFACT.md": htSkillArtifact("effort_hint: high\nmodel_class_hint: large\n"),
		"personal/analyze/SKILL.md":    htSkillMD("analyze", "Analyze data."),
	}))
	mat := t.TempDir()
	res := mcpExec(t, append(mcpServerEnv(t, srv.BaseURL), "PODIUM_HARNESS=none", "PODIUM_MATERIALIZE_ROOT="+mat),
		toolCall(1, "load_artifact", map[string]any{"id": "personal/analyze"}))
	result := rpcResult(t, res.Stdout, 1)
	if e, ok := result["error"]; ok && e != nil {
		t.Fatalf("load_artifact returned result error: %v", e)
	}
	got := readFile(t, filepath.Join(mat, "personal/analyze/ARTIFACT.md"))
	if !strings.Contains(got, "effort_hint: high") || !strings.Contains(got, "model_class_hint: large") {
		t.Errorf("materialized ARTIFACT.md missing hints:\n%s", got)
	}
}

// T-D-hints-24 — a context with effort_hint syncs successfully (the warning does
// not block sync) and the hint passes through to the materialized file.
// spec: docs/authoring/hints.md §Lint behavior (warning, not error).
func TestHints_ContextHintDoesNotBlockSync(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/glossary/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Glossary.\neffort_hint: low\n---\n\nbody\n",
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, "personal/glossary/ARTIFACT.md"))
	if !strings.Contains(got, "effort_hint: low") {
		t.Errorf("materialized context missing effort_hint:\n%s", got)
	}
}

// T-D-hints-25 — model_class_hint values small and large are both accepted on a
// command (two separate registries, each lint-clean).
// spec: docs/authoring/hints.md §model_class_hint table.
func TestHints_ModelSmallAndLargeOnCommand(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"small", "large"} {
		reg := writeRegistry(t, map[string]string{
			"personal/run-report/ARTIFACT.md": "---\ntype: command\nversion: 1.0.0\ndescription: Run the monthly report.\nmodel_class_hint: " + v + "\n---\n\n$ARGUMENTS\n",
		})
		res := runPodium(t, "", nil, "lint", "--registry", reg)
		if res.Exit != 0 {
			t.Fatalf("model_class_hint=%s lint exit=%d, want 0\nstdout=%s", v, res.Exit, res.Stdout)
		}
		if strings.Contains(res.Stdout+res.Stderr, "lint.hint_on_unsupported_type") {
			t.Errorf("model_class_hint=%s on command unexpectedly warned:\n%s", v, res.Stdout)
		}
	}
}

// T-D-hints-26 — model_class_hint: medium on a skill round-trips through sync
// --harness none into the materialized ARTIFACT.md.
// spec: docs/authoring/hints.md §model_class_hint table.
func TestHints_ModelMediumSkillRoundTrip(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/standard-skill/ARTIFACT.md": htSkillArtifact("model_class_hint: medium\n"),
		"personal/standard-skill/SKILL.md":    htSkillMD("standard-skill", "Standard skill."),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, "personal/standard-skill/ARTIFACT.md"))
	if !strings.Contains(got, "model_class_hint: medium") {
		t.Errorf("materialized ARTIFACT.md missing model_class_hint:\n%s", got)
	}
}

// T-D-hints-27 — an artifact with frontier/max hints loads and is searchable
// with no model configured. load_artifact returns 200 with both hints, and
// search_artifacts returns the artifact id.
// spec: docs/authoring/hints.md §Advisory framing.
func TestHints_FrontierHintLoadsAndSearches(t *testing.T) {
	t.Parallel()
	id := "finance/close/run-variance-analysis"
	srv := startServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": htSkillArtifact("effort_hint: max\nmodel_class_hint: frontier\n"),
		id + "/SKILL.md":    htSkillMD("run-variance-analysis", "Flag unusual variance."),
	}))
	if st := getStatus(t, srv.BaseURL+"/v1/load_artifact?id="+id); st != 200 {
		t.Fatalf("load = HTTP %d, want 200", st)
	}
	fm := htLoadFrontmatter(t, srv.BaseURL, id)
	if !strings.Contains(fm, "effort_hint: max") || !strings.Contains(fm, "model_class_hint: frontier") {
		t.Errorf("frontmatter missing hints:\n%s", fm)
	}
	_, body := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query=variance")
	if !strings.Contains(string(body), id) {
		t.Errorf("search did not return the artifact:\n%s", body)
	}
}

// T-D-hints-28 — the registry surface an SDK consumer reads carries both hints.
// The doc references an SDK consumer; this Go e2e harness has no SDK harness and
// no BUILD-GAPS finding applies, so it asserts the load_artifact frontmatter
// (the same response the SDK reads) over HTTP.
// spec: docs/authoring/hints.md §Adapter support.
func TestHints_SDKReadableHintsViaHTTP(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"personal/analyzer/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: Analyzer.\neffort_hint: high\nmodel_class_hint: large\n---\n\nbody\n",
	}))
	fm := htLoadFrontmatter(t, srv.BaseURL, "personal/analyzer")
	if !strings.Contains(fm, "effort_hint: high") || !strings.Contains(fm, "model_class_hint: large") {
		t.Errorf("frontmatter missing hints:\n%s", fm)
	}
}

// ---- enumerate all enum values cleanly -------------------------------------

// T-D-hints-29 — a registry with one agent per model_class_hint value lints
// clean with no hint_on_unsupported_type or error diagnostics.
// spec: docs/authoring/hints.md §model_class_hint table.
func TestHints_AllModelClassValuesLintClean(t *testing.T) {
	t.Parallel()
	entries := map[string]string{}
	for _, v := range []string{"nano", "small", "medium", "large", "frontier"} {
		entries["agents/tier-"+v+"/ARTIFACT.md"] = "---\ntype: agent\nversion: 1.0.0\ndescription: Tier " + v + " agent.\nmodel_class_hint: " + v + "\n---\n\nbody\n"
	}
	res := runPodium(t, "", nil, "lint", "--registry", writeRegistry(t, entries))
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout+res.Stderr, "lint.hint_on_unsupported_type") {
		t.Errorf("unexpected hint warning:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "[error]") {
		t.Errorf("unexpected error diagnostic:\n%s", res.Stdout)
	}
}

// T-D-hints-30 — a registry with one skill per effort_hint value lints clean
// with no hint_on_unsupported_type warning.
// spec: docs/authoring/hints.md §effort_hint table.
func TestHints_AllEffortValuesLintClean(t *testing.T) {
	t.Parallel()
	entries := map[string]string{}
	for _, v := range []string{"low", "medium", "high", "max"} {
		leaf := "effort-" + v
		entries["skills/"+leaf+"/ARTIFACT.md"] = htSkillArtifact("effort_hint: " + v + "\n")
		entries["skills/"+leaf+"/SKILL.md"] = htSkillMD(leaf, "Effort "+v+" skill.")
	}
	res := runPodium(t, "", nil, "lint", "--registry", writeRegistry(t, entries))
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout+res.Stderr, "lint.hint_on_unsupported_type") {
		t.Errorf("unexpected hint warning:\n%s", res.Stdout)
	}
}

// T-D-hints-31 — both hints on a rule emit exactly two separate
// lint.hint_on_unsupported_type warnings (one per field), exit 0.
// spec: docs/authoring/hints.md §Lint behavior.
func TestHints_BothHintsOnRuleTwoWarnings(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/heavy/ARTIFACT.md": "---\ntype: rule\nversion: 1.0.0\ndescription: Heavy rule.\nrule_mode: always\neffort_hint: high\nmodel_class_hint: frontier\n---\n\nrules\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if n := strings.Count(res.Stdout, "lint.hint_on_unsupported_type"); n != 2 {
		t.Errorf("hint_on_unsupported_type count=%d, want 2\n%s", n, res.Stdout)
	}
}
